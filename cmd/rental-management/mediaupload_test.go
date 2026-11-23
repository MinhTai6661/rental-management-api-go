package main

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"rental-management/pkg/storage"
)

func sampleTree() fstest.MapFS {
	return fstest.MapFS{
		"property/2026/11/aaa.webp":       {Data: []byte("one")},
		"property/2026/11/aaa_thumb.webp": {Data: []byte("t")},
		"property/2026/11/bbb.jpg":        {Data: []byte("two")},
		"property/2026/11/ccc.webp.tmp-1": {Data: []byte("half")}, // temporary file: ignored
		"property/readme.txt":             {Data: []byte("x")},    // not a picture: ignored
	}
}

func TestUploadMediaCopiesPicturesAndSkipsWhatIsThere(t *testing.T) {
	dst := storage.NewMemory("http://x")
	var out strings.Builder
	rep, err := uploadMedia(t.Context(), sampleTree(), dst, &out)
	if err != nil {
		t.Fatal(err)
	}
	if rep != (uploadReport{Uploaded: 3, Ignored: 2}) {
		t.Errorf("first run: %+v", rep)
	}
	if keys := dst.Keys(); len(keys) != 3 {
		t.Errorf("destination keys: %v", keys)
	}

	rep, err = uploadMedia(t.Context(), sampleTree(), dst, &out)
	if err != nil || rep != (uploadReport{Existing: 3, Ignored: 2}) {
		t.Errorf("second run must upload nothing: %+v %v", rep, err)
	}

	// A different size means the object is not the same file: it is uploaded again.
	tree := sampleTree()
	tree["property/2026/11/bbb.jpg"] = &fstest.MapFile{Data: []byte("longer content")}
	rep, err = uploadMedia(t.Context(), tree, dst, &out)
	if err != nil || rep.Uploaded != 1 || rep.Existing != 2 {
		t.Errorf("changed file: %+v %v", rep, err)
	}
	r, _, _ := dst.Open(t.Context(), "property/2026/11/bbb.jpg")
	got, _ := io.ReadAll(r)
	if string(got) != "longer content" {
		t.Errorf("content = %q", got)
	}
}

// failing refuses to store one key.
type failing struct {
	*storage.Memory
	bad string
}

func (f failing) Put(ctx context.Context, key string, data []byte, ct string) error {
	if key == f.bad {
		return errors.New("disk full")
	}
	return f.Memory.Put(ctx, key, data, ct)
}

func TestUploadMediaReportsFailuresAndKeepsGoing(t *testing.T) {
	dst := failing{Memory: storage.NewMemory("http://x"), bad: "property/2026/11/bbb.jpg"}
	var out strings.Builder
	rep, err := uploadMedia(t.Context(), sampleTree(), dst, &out)
	if err == nil {
		t.Fatal("a failed file must make the command fail")
	}
	if rep.Failed != 1 || rep.Uploaded != 2 || !strings.Contains(out.String(), "FAIL property/2026/11/bbb.jpg") {
		t.Errorf("report: %+v\n%s", rep, out.String())
	}
}

func TestUploadMediaToFakeS3FromDisk(t *testing.T) {
	backend := s3mem.New()
	if err := backend.CreateBucket("media"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	defer srv.Close()
	dst, err := storage.NewS3(storage.S3Config{Endpoint: srv.URL, Bucket: "media", AccessKeyID: "k",
		SecretAccessKey: "s", Region: "us-east-1", PathStyle: true, PublicBaseURL: "http://cdn.test"})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "property", "2026", "11"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "property", "2026", "11", "aaa.webp"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	rep, err := uploadMedia(t.Context(), os.DirFS(dir), dst, &out)
	if err != nil || rep.Uploaded != 1 {
		t.Fatalf("upload: %+v %v", rep, err)
	}
	r, info, err := dst.Open(t.Context(), "property/2026/11/aaa.webp")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if string(got) != "hello" || info.ContentType != "image/webp" {
		t.Errorf("stored %q %+v", got, info)
	}
	if _, err := os.Stat(filepath.Join(dir, "property", "2026", "11", "aaa.webp")); err != nil {
		t.Error("the local file must stay")
	}
	rep, err = uploadMedia(t.Context(), os.DirFS(dir), dst, &out)
	if err != nil || rep.Uploaded != 0 || rep.Existing != 1 {
		t.Errorf("second run: %+v %v", rep, err)
	}
}

func TestRunMediaUploadRefusesWithoutTheS3Driver(t *testing.T) {
	t.Setenv("STORAGE_DRIVER", "local")
	err := runMediaUpload(t.Context(), nil, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "STORAGE_DRIVER=s3") {
		t.Fatalf("must refuse with the local driver: %v", err)
	}
}
