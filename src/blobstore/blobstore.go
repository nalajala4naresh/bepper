// Package blobstore defines a minimal pluggable object-storage abstraction
// (disk, S3, ...), adapted from BuildBuddy's server/interfaces.Blobstore and
// server/backends/blobstore/util (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/interfaces/interfaces.go
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/backends/blobstore/util/util.go
package blobstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
)

// Blobstore stores and retrieves opaque, named blobs.
type Blobstore interface {
	BlobExists(ctx context.Context, blobName string) (bool, error)
	ReadBlob(ctx context.Context, blobName string) ([]byte, error)
	WriteBlob(ctx context.Context, blobName string, data []byte) (int, error)
	DeleteBlob(ctx context.Context, blobName string) error

	// Writer returns a streaming writer for blobName. The blob is only
	// committed to the backing store once Close is called; callers must
	// call Close to finish the write.
	Writer(ctx context.Context, blobName string) (io.WriteCloser, error)
}

// Compress gzip-compresses data.
func Compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decompress gzip-decompresses data. If err is non-nil it is returned
// unchanged, so callers can write `return Decompress(b, err)` directly
// after a read.
func Decompress(data []byte, err error) ([]byte, error) {
	if err != nil {
		return data, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	return io.ReadAll(gz)
}
