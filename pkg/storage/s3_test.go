package storage

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

const fakeBucket = "media"

// newFakeS3 starts an in-process S3 server with one bucket and returns a driver pointed at it.
func newFakeS3(t *testing.T, pathStyle bool) (*S3, *httptest.Server) {
	t.Helper()
	backend := s3mem.New()
	if err := backend.CreateBucket(fakeBucket); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	s, err := NewS3(S3Config{
		Endpoint: srv.URL, Bucket: fakeBucket, AccessKeyID: "k", SecretAccessKey: "s",
		Region: "us-east-1", PathStyle: pathStyle, PublicBaseURL: "https://cdn.example.vn/media/",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, srv
}

func TestS3Contract(t *testing.T) {
	s, _ := newFakeS3(t, true)
	run(t, s)
}

func TestS3URL(t *testing.T) {
	s, _ := newFakeS3(t, true)
	if got := s.URL("property/x.webp"); got != "https://cdn.example.vn/media/property/x.webp" {
		t.Errorf("URL = %q", got)
	}
}

func TestS3CopyKeepsContent(t *testing.T) {
	s, _ := newFakeS3(t, true)
	ctx := context.Background()
	if err := s.Put(ctx, "property/a.jpg", []byte("abc"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if err := s.Copy(ctx, "property/a.jpg", "property/b.jpg"); err != nil {
		t.Fatal(err)
	}
	r, info, err := s.Open(ctx, "property/b.jpg")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if info.Size != 3 || info.ContentType != "image/jpeg" {
		t.Errorf("copy info: %+v", info)
	}
}

func TestS3PutDefaultsContentTypeFromKey(t *testing.T) {
	s, _ := newFakeS3(t, true)
	ctx := context.Background()
	if err := s.Put(ctx, "property/a.webp", []byte("abc"), ""); err != nil {
		t.Fatal(err)
	}
	r, info, err := s.Open(ctx, "property/a.webp")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if info.ContentType != "image/webp" {
		t.Errorf("content type = %q", info.ContentType)
	}
}

func TestS3DeleteManyIncludingMissing(t *testing.T) {
	s, _ := newFakeS3(t, true)
	ctx := context.Background()
	for _, k := range []string{"property/a.webp", "property/b.webp"} {
		if err := s.Put(ctx, k, []byte("x"), ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete(ctx, "property/a.webp", "property/missing.webp", "property/b.webp"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, k := range []string{"property/a.webp", "property/b.webp"} {
		if _, _, err := s.Open(ctx, k); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s still there: %v", k, err)
		}
	}
	if err := s.Delete(ctx); err != nil {
		t.Errorf("delete nothing: %v", err)
	}
}

func TestS3PathStyleAddressing(t *testing.T) {
	// The fake server runs on an IP address, where only path style can work; virtual-host style needs DNS for
	// <bucket>.<host>, so it must fail to reach the bucket.
	s, _ := newFakeS3(t, true)
	if err := s.Put(context.Background(), "property/a.webp", []byte("x"), ""); err != nil {
		t.Fatalf("path style: %v", err)
	}
	virtual, srv := newFakeS3(t, false)
	_ = srv
	if err := virtual.Put(context.Background(), "property/a.webp", []byte("x"), ""); err == nil {
		t.Error("virtual-host style cannot work against an IP address endpoint")
	}
}

func TestS3ContextCancelled(t *testing.T) {
	s, _ := newFakeS3(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Put(ctx, "property/a.webp", []byte("x"), ""); err == nil {
		t.Error("a cancelled context must stop the call")
	}
}

func TestNewS3RejectsBadSettings(t *testing.T) {
	good := S3Config{Endpoint: "http://localhost:8333", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"}
	cases := map[string]func(*S3Config){
		"no scheme":      func(c *S3Config) { c.Endpoint = "localhost:8333" },
		"ftp":            func(c *S3Config) { c.Endpoint = "ftp://localhost" },
		"path in url":    func(c *S3Config) { c.Endpoint = "http://localhost:8333/bucket" },
		"no bucket":      func(c *S3Config) { c.Bucket = "" },
		"empty endpoint": func(c *S3Config) { c.Endpoint = "" },
	}
	for name, mutate := range cases {
		cfg := good
		mutate(&cfg)
		if _, err := NewS3(cfg); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := NewS3(good); err != nil {
		t.Errorf("good settings: %v", err)
	}
	// The error never repeats the secret.
	cfg := good
	cfg.Endpoint = "bad"
	cfg.SecretAccessKey = "super-secret-value"
	if _, err := NewS3(cfg); err != nil && strings.Contains(err.Error(), "super-secret-value") {
		t.Error("the error leaks the secret")
	}
}
