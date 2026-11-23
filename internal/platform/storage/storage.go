// Package storage keeps binary files (pictures) behind a small port, so the business code does not know whether
// they live on a local disk or in an S3-compatible bucket.
package storage

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
)

// ErrNotFound is returned when a key holds no object.
var ErrNotFound = errors.New("storage: object not found")

// maxKeyBytes is the longest key accepted.
const maxKeyBytes = 200

// Info describes a stored object.
type Info struct {
	Size        int64
	ContentType string
}

// Storage is where files are kept. Keys are relative, slash separated and checked by ValidKey; a key is written
// once and never overwritten by the callers, so a URL built from it can be cached for ever.
type Storage interface {
	// Put stores data under key, replacing what was there.
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Open returns the content of key (ErrNotFound when missing). The caller closes it.
	Open(ctx context.Context, key string) (io.ReadCloser, Info, error)
	// Copy duplicates one object under another key (ErrNotFound when the source is missing).
	Copy(ctx context.Context, from, to string) error
	// Delete removes the objects; keys that do not exist are not an error.
	Delete(ctx context.Context, keys ...string) error
	// URL is the absolute public address of key.
	URL(key string) string
}

// keyPattern: lowercase path segments of letters, digits and underscores, ending in a known extension.
var keyPattern = regexp.MustCompile(`^[a-z0-9_]+(/[a-z0-9_]+)*\.(webp|jpg)$`)

// ValidKey reports whether key is safe to use as a path: lowercase, relative, no "..", no empty segment, a
// .webp or .jpg extension and at most 200 bytes.
func ValidKey(key string) bool {
	return len(key) <= maxKeyBytes && keyPattern.MatchString(key)
}

// contentTypeOf derives the content type from the extension of a valid key.
func contentTypeOf(key string) string {
	if strings.HasSuffix(key, ".webp") {
		return "image/webp"
	}
	return "image/jpeg"
}

// errInvalidKey is returned for a key ValidKey refuses.
var errInvalidKey = errors.New("storage: invalid key")
