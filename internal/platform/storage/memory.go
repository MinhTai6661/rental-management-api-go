package storage

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strings"
	"sync"
)

// Memory keeps the files in memory; it is for tests.
type Memory struct {
	mu      sync.Mutex
	files   map[string][]byte
	baseURL string
}

// NewMemory returns an empty store whose URLs start with publicBaseURL.
func NewMemory(publicBaseURL string) *Memory {
	return &Memory{files: map[string][]byte{}, baseURL: strings.TrimRight(publicBaseURL, "/")}
}

// Put stores a copy of data.
func (m *Memory) Put(_ context.Context, key string, data []byte, _ string) error {
	if !ValidKey(key) {
		return errInvalidKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[key] = append([]byte(nil), data...)
	return nil
}

// Open returns the stored bytes.
func (m *Memory) Open(_ context.Context, key string) (io.ReadCloser, Info, error) {
	if !ValidKey(key) {
		return nil, Info{}, errInvalidKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[key]
	if !ok {
		return nil, Info{}, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), Info{Size: int64(len(data)), ContentType: contentTypeOf(key)}, nil
}

// Copy duplicates an object.
func (m *Memory) Copy(_ context.Context, from, to string) error {
	if !ValidKey(from) || !ValidKey(to) {
		return errInvalidKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[from]
	if !ok {
		return ErrNotFound
	}
	m.files[to] = append([]byte(nil), data...)
	return nil
}

// Delete removes objects; missing keys are skipped.
func (m *Memory) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		if !ValidKey(key) {
			return errInvalidKey
		}
		delete(m.files, key)
	}
	return nil
}

// URL is the public address of key.
func (m *Memory) URL(key string) string { return m.baseURL + "/" + key }

// Keys lists the stored keys in order (for assertions).
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.files))
	for k := range m.files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
