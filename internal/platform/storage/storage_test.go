package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidKey(t *testing.T) {
	cases := map[string]bool{
		"property/2026/11/abc123_thumb.jpg": true,
		"property/2026/11/abc123.webp":      true,
		"a.webp":                            true,
		"../x.webp":                         false,
		"/abs/x.webp":                       false,
		"a/../b.webp":                       false,
		"a/./b.webp":                        false,
		"a//b.webp":                         false,
		"Property/x.webp":                   false,
		"property/x.png":                    false,
		"property/x":                        false,
		"property/x.webp.tmp":               false,
		"property/x y.webp":                 false,
		"property\\x.webp":                  false,
		"":                                  false,
		strings.Repeat("a", 201) + ".webp":  false,
	}
	for key, want := range cases {
		if got := ValidKey(key); got != want {
			t.Errorf("ValidKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// run exercises the Storage contract on any implementation.
func run(t *testing.T, s Storage) {
	t.Helper()
	ctx := context.Background()
	key, other := "property/2026/11/aaa.webp", "property/2026/11/bbb.webp"

	if _, _, err := s.Open(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Errorf("open missing: %v", err)
	}
	if err := s.Put(ctx, key, []byte("hello"), "image/webp"); err != nil {
		t.Fatal(err)
	}
	r, info, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r)
	_ = r.Close()
	if string(data) != "hello" || info.Size != 5 || info.ContentType != "image/webp" {
		t.Errorf("round trip: %q %+v", data, info)
	}

	if err := s.Copy(ctx, key, other); err != nil {
		t.Fatal(err)
	}
	if err := s.Copy(ctx, "property/2026/11/none.webp", other); !errors.Is(err, ErrNotFound) {
		t.Errorf("copy missing: %v", err)
	}
	if err := s.Delete(ctx, key, other, "property/2026/11/never.webp"); err != nil {
		t.Errorf("delete (one missing): %v", err)
	}
	if _, _, err := s.Open(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted file still there: %v", err)
	}

	for _, bad := range []string{"../escape.webp", "/abs.webp", "a/../b.webp"} {
		if err := s.Put(ctx, bad, []byte("x"), ""); err == nil {
			t.Errorf("Put(%q) must be refused", bad)
		}
		if _, _, err := s.Open(ctx, bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) must be refused, got %v", bad, err)
		}
		if err := s.Delete(ctx, bad); err == nil {
			t.Errorf("Delete(%q) must be refused", bad)
		}
	}
}

func TestLocalDisk(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalDisk(dir, "http://localhost:8090/v1/media/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	run(t, s)

	if got := s.URL("property/x.webp"); got != "http://localhost:8090/v1/media/property/x.webp" {
		t.Errorf("URL = %q", got)
	}
	// A write leaves no temp file behind and nothing outside the directory.
	if err := s.Put(context.Background(), "property/2026/11/ccc.jpg", []byte("x"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 1 || !strings.HasSuffix(files[0], "ccc.jpg") {
		t.Errorf("files on disk: %v", files)
	}
	if st, err := os.Stat(files[0]); err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("file mode: %v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.webp")); err == nil {
		t.Error("a file escaped the directory")
	}
}

func TestMemory(t *testing.T) {
	m := NewMemory("http://x/v1/media")
	run(t, m)
	_ = m.Put(context.Background(), "property/b.webp", []byte("1"), "")
	_ = m.Put(context.Background(), "property/a.webp", []byte("1"), "")
	if keys := m.Keys(); len(keys) != 2 || keys[0] != "property/a.webp" {
		t.Errorf("keys: %v", keys)
	}
}
