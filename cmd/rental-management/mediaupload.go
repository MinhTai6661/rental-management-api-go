package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"rental-management/config"
	"rental-management/pkg/storage"
)

// uploadReport counts what a media upload did.
type uploadReport struct {
	Uploaded int // copied to the destination
	Existing int // already there with the same size
	Ignored  int // not a picture key (temporary file, other extension...)
	Failed   int // could not be read or written
}

// runMediaUpload runs `rental-management media-upload`: it copies the pictures of MEDIA_DIR into the S3 bucket under
// the same keys. It only reads the local files and never deletes anything, and it is safe to run again.
func runMediaUpload(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 {
		return errors.New("media-upload takes no arguments (usage: rental-management media-upload)")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.StorageDriver != "s3" {
		return errors.New("media-upload needs STORAGE_DRIVER=s3 (and the S3_* settings): there is nowhere to upload to")
	}
	dst, err := newS3Storage(cfg)
	if err != nil {
		return err
	}
	info, err := os.Stat(cfg.MediaDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("MEDIA_DIR %q is not a readable directory", cfg.MediaDir)
	}
	_, _ = fmt.Fprintf(out, "uploading %s to bucket %s\n", cfg.MediaDir, cfg.S3Bucket)
	_, err = uploadMedia(ctx, os.DirFS(cfg.MediaDir), dst, out)
	return err
}

// uploadMedia walks src and puts every picture into dst under its relative path. A key that is already in dst with
// the same size is skipped, so a second run uploads nothing. Pictures are written once and never overwritten by
// the API, so a size match is enough. It reports the counts and returns an error when any file failed.
func uploadMedia(ctx context.Context, src fs.FS, dst storage.Storage, out io.Writer) (uploadReport, error) {
	var rep uploadReport
	walkErr := fs.WalkDir(src, ".", func(key string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			rep.Failed++
			_, _ = fmt.Fprintf(out, "FAIL %s: %v\n", key, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() || !storage.ValidKey(key) {
			rep.Ignored++
			return nil
		}
		status, err := uploadOne(ctx, src, dst, key)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rep.Failed++
			_, _ = fmt.Fprintf(out, "FAIL %s: %v\n", key, err)
			return nil
		}
		if status == uploaded {
			rep.Uploaded++
		} else {
			rep.Existing++
		}
		return nil
	})
	_, _ = fmt.Fprintf(out, "uploaded %d, already there %d, ignored %d, failed %d\n",
		rep.Uploaded, rep.Existing, rep.Ignored, rep.Failed)
	if walkErr != nil {
		return rep, fmt.Errorf("media-upload interrupted: %w", walkErr)
	}
	if rep.Failed > 0 {
		return rep, fmt.Errorf("media-upload: %d file(s) failed", rep.Failed)
	}
	return rep, nil
}

type uploadStatus int

const (
	uploaded uploadStatus = iota
	alreadyThere
)

// uploadOne puts one file unless dst already holds it with the same size.
func uploadOne(ctx context.Context, src fs.FS, dst storage.Storage, key string) (uploadStatus, error) {
	data, err := fs.ReadFile(src, key)
	if err != nil {
		return 0, fmt.Errorf("read: %w", err)
	}
	body, info, err := dst.Open(ctx, key)
	switch {
	case err == nil:
		_ = body.Close()
		if info.Size == int64(len(data)) {
			return alreadyThere, nil
		}
	case !errors.Is(err, storage.ErrNotFound):
		return 0, fmt.Errorf("check: %w", err)
	}
	contentType := "image/jpeg"
	if strings.HasSuffix(key, ".webp") {
		contentType = "image/webp"
	}
	if err := dst.Put(ctx, key, data, contentType); err != nil {
		return 0, fmt.Errorf("upload: %w", err)
	}
	return uploaded, nil
}
