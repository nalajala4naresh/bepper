// Package disk implements blobstore.Blobstore on the local filesystem,
// adapted from BuildBuddy's disk blobstore (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/backends/blobstore/disk/disk.go
package disk

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nalajala4naresh/bepper/src/blobstore"
)

// BlobStore stores blobs as gzip-compressed files under a root directory.
type BlobStore struct {
	rootDir string
}

// New creates a BlobStore rooted at rootDir, creating it if necessary.
func New(rootDir string) (*BlobStore, error) {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create root dir: %w", err)
	}
	return &BlobStore{rootDir: rootDir}, nil
}

func (d *BlobStore) blobPath(blobName string) (string, error) {
	if strings.Contains(blobName, "..") {
		return "", fmt.Errorf("blobName (%s) must not contain ../", blobName)
	}
	return filepath.Join(d.rootDir, blobName), nil
}

func (d *BlobStore) WriteBlob(ctx context.Context, blobName string, data []byte) (int, error) {
	path, err := d.blobPath(blobName)
	if err != nil {
		return 0, err
	}
	compressed, err := blobstore.Compress(data)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, compressed, 0o644); err != nil {
		return 0, err
	}
	return len(compressed), nil
}

func (d *BlobStore) ReadBlob(ctx context.Context, blobName string) ([]byte, error) {
	path, err := d.blobPath(blobName)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	return blobstore.Decompress(b, err)
}

func (d *BlobStore) DeleteBlob(ctx context.Context, blobName string) error {
	path, err := d.blobPath(blobName)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d *BlobStore) BlobExists(ctx context.Context, blobName string) (bool, error) {
	path, err := d.blobPath(blobName)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *BlobStore) Writer(ctx context.Context, blobName string) (io.WriteCloser, error) {
	path, err := d.blobPath(blobName)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &compressFileWriter{gz: newGzipWriter(f), f: f}, nil
}
