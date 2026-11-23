package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

const (
	dirMode  = 0o750
	fileMode = 0o640
)

// LocalDisk keeps the files below one directory. It goes through os.Root, so a path can never leave the directory.
type LocalDisk struct {
	root    *os.Root
	baseURL string
}

// NewLocalDisk opens (and creates) dir. publicBaseURL is the absolute prefix of every URL, without a trailing slash.
func NewLocalDisk(dir, publicBaseURL string) (*LocalDisk, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create media dir: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open media dir: %w", err)
	}
	return &LocalDisk{root: root, baseURL: strings.TrimRight(publicBaseURL, "/")}, nil
}

// Close releases the directory handle.
func (d *LocalDisk) Close() error { return d.root.Close() }

// Put writes to a temporary file and renames it, so a reader never sees half a file.
func (d *LocalDisk) Put(ctx context.Context, key string, data []byte, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidKey(key) {
		return errInvalidKey
	}
	if err := d.root.MkdirAll(path.Dir(key), dirMode); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("random name: %w", err)
	}
	tmp := key + ".tmp-" + hex.EncodeToString(suffix)
	f, err := d.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = d.root.Remove(tmp)
		return fmt.Errorf("write file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = d.root.Remove(tmp)
		return fmt.Errorf("close file: %w", err)
	}
	if err := d.root.Rename(tmp, key); err != nil {
		_ = d.root.Remove(tmp)
		return fmt.Errorf("publish file: %w", err)
	}
	return nil
}

// Open returns the file; the caller closes it.
func (d *LocalDisk) Open(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, Info{}, err
	}
	if !ValidKey(key) {
		return nil, Info{}, errInvalidKey
	}
	f, err := d.root.Open(key)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Info{}, ErrNotFound
		}
		return nil, Info{}, fmt.Errorf("open file: %w", err)
	}
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Info{}, fmt.Errorf("stat file: %w", err)
	}
	return f, Info{Size: stat.Size(), ContentType: contentTypeOf(key)}, nil
}

// Copy duplicates a file.
func (d *LocalDisk) Copy(ctx context.Context, from, to string) error {
	src, _, err := d.Open(ctx, from)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	data, err := io.ReadAll(src)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	return d.Put(ctx, to, data, contentTypeOf(to))
}

// Delete removes the files; a missing one is skipped.
func (d *LocalDisk) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !ValidKey(key) {
			return errInvalidKey
		}
		if err := d.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("delete file: %w", err)
		}
	}
	return nil
}

// URL is the public address of key.
func (d *LocalDisk) URL(key string) string { return d.baseURL + "/" + key }
