// Package filestore adapts the byte store of pkg/storage to the file port of the property use cases: it speaks the
// domain's types (file info, "file not found") instead of the storage package's.
package filestore

import (
	"context"
	"errors"
	"io"

	domain "rental-management/internal/domain/property"
	"rental-management/pkg/storage"
)

// Store keeps the picture files on a storage.Storage.
type Store struct{ inner storage.Storage }

// New returns a file store on the byte store.
func New(inner storage.Storage) *Store { return &Store{inner: inner} }

// Put stores data under key, replacing what was there.
func (s *Store) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return s.inner.Put(ctx, key, data, contentType)
}

// Open returns the content of key. A key that is not a valid storage key holds nothing, so it is not found.
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, domain.FileInfo, error) {
	if !storage.ValidKey(key) {
		return nil, domain.FileInfo{}, domain.ErrFileNotFound
	}
	rc, info, err := s.inner.Open(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, domain.FileInfo{}, domain.ErrFileNotFound
	}
	return rc, domain.FileInfo{Size: info.Size, ContentType: info.ContentType}, err
}

// Copy duplicates one file under another key.
func (s *Store) Copy(ctx context.Context, from, to string) error {
	err := s.inner.Copy(ctx, from, to)
	if errors.Is(err, storage.ErrNotFound) {
		return domain.ErrFileNotFound
	}
	return err
}

// Delete removes the files; keys that do not exist are not an error.
func (s *Store) Delete(ctx context.Context, keys ...string) error {
	return s.inner.Delete(ctx, keys...)
}
