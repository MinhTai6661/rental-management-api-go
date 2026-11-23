package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// s3OpTimeout bounds an S3 call whose context has no deadline of its own.
const s3OpTimeout = 30 * time.Second

// S3Config describes an S3-compatible bucket: Cloudflare R2, AWS S3, SeaweedFS, MinIO...
type S3Config struct {
	// Endpoint is the API address, e.g. http://localhost:8333 or https://<account>.r2.cloudflarestorage.com. The
	// scheme decides whether TLS is used; a path is not allowed.
	Endpoint        string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	// Region is the signing region ("auto" for R2, "us-east-1" for SeaweedFS).
	Region string
	// PathStyle puts the bucket in the path (host/bucket/key) instead of the host name (bucket.host/key).
	PathStyle bool
	// PublicBaseURL is the absolute prefix of every URL URL returns, without a trailing slash. With R2 it is
	// normally a custom domain in front of the bucket.
	PublicBaseURL string
}

// S3 keeps the files in one bucket of an S3-compatible service.
type S3 struct {
	client  *minio.Client
	bucket  string
	baseURL string
}

// NewS3 builds the driver. It does not contact the service: a wrong endpoint or key shows on the first call.
func NewS3(cfg S3Config) (*S3, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("s3: endpoint %q is not an http(s) address like http://localhost:8333", cfg.Endpoint)
	}
	if cfg.Bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}
	lookup := minio.BucketLookupDNS
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       u.Scheme == "https",
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: create client: %w", err)
	}
	return &S3{client: client, bucket: cfg.Bucket, baseURL: strings.TrimRight(cfg.PublicBaseURL, "/")}, nil
}

// withTimeout adds s3OpTimeout when ctx has no deadline.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s3OpTimeout)
}

// isNoSuchKey reports whether err says the object (not the bucket) does not exist.
func isNoSuchKey(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == minio.NoSuchKey || (resp.StatusCode == http.StatusNotFound && resp.Code != "NoSuchBucket")
}

// Put uploads data under key.
func (s *S3) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if !ValidKey(key) {
		return errInvalidKey
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if contentType == "" {
		contentType = contentTypeOf(key)
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

// cancelOnClose releases the context of a download when the caller closes the body.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// Open returns the object; the caller closes it.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	if !ValidKey(key) {
		return nil, Info{}, errInvalidKey
	}
	ctx, cancel := withTimeout(ctx)
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		cancel()
		return nil, Info{}, fmt.Errorf("s3 get %s: %w", key, err)
	}
	// The request is sent here, so a missing key shows now and not on the first Read.
	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		cancel()
		if isNoSuchKey(err) {
			return nil, Info{}, ErrNotFound
		}
		return nil, Info{}, fmt.Errorf("s3 stat %s: %w", key, err)
	}
	contentType := stat.ContentType
	if contentType == "" || contentType == "binary/octet-stream" || contentType == "application/octet-stream" {
		contentType = contentTypeOf(key)
	}
	return cancelOnClose{ReadCloser: obj, cancel: cancel}, Info{Size: stat.Size, ContentType: contentType}, nil
}

// Copy duplicates an object inside the bucket, on the server side.
func (s *S3) Copy(ctx context.Context, from, to string) error {
	if !ValidKey(from) || !ValidKey(to) {
		return errInvalidKey
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err := s.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: s.bucket, Object: to},
		minio.CopySrcOptions{Bucket: s.bucket, Object: from})
	if err != nil {
		if isNoSuchKey(err) {
			return ErrNotFound
		}
		return fmt.Errorf("s3 copy %s to %s: %w", from, to, err)
	}
	return nil
}

// Delete removes the objects; S3 does not report a missing key, so it is skipped.
func (s *S3) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if !ValidKey(key) {
			return errInvalidKey
		}
	}
	if len(keys) == 0 {
		return nil
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	in := make(chan minio.ObjectInfo, len(keys))
	for _, key := range keys {
		in <- minio.ObjectInfo{Key: key}
	}
	close(in)
	var failed []string
	for res := range s.client.RemoveObjects(ctx, s.bucket, in, minio.RemoveObjectsOptions{}) {
		if res.Err != nil && !isNoSuchKey(res.Err) {
			failed = append(failed, fmt.Sprintf("%s: %v", res.ObjectName, res.Err))
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf("s3 delete: %s", strings.Join(failed, "; "))
	}
	return nil
}

// URL is the public address of key.
func (s *S3) URL(key string) string { return s.baseURL + "/" + key }
