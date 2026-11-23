package filestore

import (
	"context"
	"errors"
	"io"
	"testing"

	domain "rental-management/internal/domain/property"
	"rental-management/pkg/storage"
)

func TestStoreSpeaksTheDomainsTypes(t *testing.T) {
	ctx := context.Background()
	s := New(storage.NewMemory("http://files.test/v1/media"))
	if err := s.Put(ctx, "property/2026/11/a.webp", []byte("img"), "image/webp"); err != nil {
		t.Fatal(err)
	}
	rc, info, err := s.Open(ctx, "property/2026/11/a.webp")
	if err != nil || info.ContentType != "image/webp" || info.Size != 3 {
		t.Fatalf("info %+v, err %v", info, err)
	}
	if body, _ := io.ReadAll(rc); string(body) != "img" {
		t.Errorf("body %q", body)
	}
	_ = rc.Close()

	if err := s.Copy(ctx, "property/2026/11/a.webp", "property/2026/11/b.webp"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "property/2026/11/a.webp", "property/2026/11/missing.webp"); err != nil {
		t.Fatal(err)
	}
}

func TestMissingAndMalformedKeysAreNotFound(t *testing.T) {
	ctx := context.Background()
	s := New(storage.NewMemory("http://files.test/v1/media"))
	if _, _, err := s.Open(ctx, "property/2026/11/none.webp"); !errors.Is(err, domain.ErrFileNotFound) {
		t.Errorf("a missing file: %v", err)
	}
	if _, _, err := s.Open(ctx, "property/../etc/passwd"); !errors.Is(err, domain.ErrFileNotFound) {
		t.Errorf("a malformed key: %v", err)
	}
	if err := s.Copy(ctx, "property/2026/11/none.webp", "property/2026/11/x.webp"); !errors.Is(err, domain.ErrFileNotFound) {
		t.Errorf("copying a missing file: %v", err)
	}
}
