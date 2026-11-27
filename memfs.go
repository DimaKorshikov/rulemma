package rulemma

import (
	"bytes"
	"io/fs"
	"path"
	"sync"
	"time"
)

// MemoryFS is a lightweight, concurrency-safe in-memory fs.FS for loading rulemma
// dictionaries directly from bytes (e.g. HTTP fetch in WebAssembly, S3, or tests)
// without embedding all tables into the binary.
type MemoryFS struct {
	mu    sync.RWMutex
	files map[string][]byte
}

// NewMemoryFS creates a MemoryFS initialized with an optional map of filename -> raw bytes.
func NewMemoryFS(files map[string][]byte) *MemoryFS {
	m := &MemoryFS{files: make(map[string][]byte, len(files)+4)}
	for k, v := range files {
		m.files[path.Clean(k)] = v
	}
	return m
}

// Set stores or replaces a file in MemoryFS (thread-safe).
func (m *MemoryFS) Set(name string, data []byte) {
	m.mu.Lock()
	m.files[path.Clean(name)] = data
	m.mu.Unlock()
}

// Open implements fs.FS.
func (m *MemoryFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	clean := path.Clean(name)
	m.mu.RLock()
	data, ok := m.files[clean]
	m.mu.RUnlock()
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &memFile{name: path.Base(clean), r: bytes.NewReader(data), size: int64(len(data))}, nil
}

// Stat implements fs.StatFS.
func (m *MemoryFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	clean := path.Clean(name)
	m.mu.RLock()
	data, ok := m.files[clean]
	m.mu.RUnlock()
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return memFileInfo{name: path.Base(clean), size: int64(len(data))}, nil
}

type memFile struct {
	name string
	r    *bytes.Reader
	size int64
}

func (f *memFile) Stat() (fs.FileInfo, error) {
	return memFileInfo{name: f.name, size: f.size}, nil
}
func (f *memFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *memFile) Close() error               { return nil }

type memFileInfo struct {
	name string
	size int64
}

func (fi memFileInfo) Name() string       { return fi.name }
func (fi memFileInfo) Size() int64        { return fi.size }
func (fi memFileInfo) Mode() fs.FileMode  { return 0o444 }
func (fi memFileInfo) ModTime() time.Time { return time.Time{} }
func (fi memFileInfo) IsDir() bool        { return false }
func (fi memFileInfo) Sys() any           { return nil }
