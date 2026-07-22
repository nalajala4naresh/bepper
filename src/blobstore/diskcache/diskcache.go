// Package diskcache wraps a blobstore.Blobstore with a local disk read
// cache. It exists for backends where every read is a network round trip
// (S3) — bepper's per-invocation event blobs are written once (during
// PublishBuildToolEventStream) and never modified again after Finalize, so
// once a blob has been fetched once, it's safe to keep serving that copy
// indefinitely. Disk-backed blobstores don't need this wrapper: they're
// already local.
package diskcache

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/nalajala4naresh/bepper/src/blobstore"
	"github.com/nalajala4naresh/bepper/src/blobstore/disk"

	"golang.org/x/sync/singleflight"
)

// BlobStore wraps a backing blobstore.Blobstore with a local disk cache.
// The backing store is always the source of truth: writes go there first,
// and any failure to read/write/evict the cache is logged and otherwise
// ignored rather than failing the caller's request.
type BlobStore struct {
	backing  blobstore.Blobstore
	cache    *disk.BlobStore
	dir      string
	maxBytes int64

	group singleflight.Group
}

// New creates a BlobStore caching reads from backing under dir on local
// disk. Once the cache exceeds maxBytes, the least-recently-written
// entries are evicted first; maxBytes <= 0 means no size limit.
func New(backing blobstore.Blobstore, dir string, maxBytes int64) (*BlobStore, error) {
	cache, err := disk.New(dir)
	if err != nil {
		return nil, fmt.Errorf("diskcache: %w", err)
	}
	return &BlobStore{backing: backing, cache: cache, dir: dir, maxBytes: maxBytes}, nil
}

// ReadBlob serves blobName from the local cache if present, otherwise
// fetches it from the backing store and populates the cache for next time.
// Concurrent misses for the same blobName are coalesced into a single
// backing fetch via singleflight.
func (b *BlobStore) ReadBlob(ctx context.Context, blobName string) ([]byte, error) {
	if data, err := b.cache.ReadBlob(ctx, blobName); err == nil {
		return data, nil
	} else if !os.IsNotExist(err) {
		log.Printf("diskcache: read cache entry %q: %v", blobName, err)
	}

	v, err, _ := b.group.Do(blobName, func() (any, error) {
		data, err := b.backing.ReadBlob(ctx, blobName)
		if err != nil {
			return nil, err
		}
		b.populate(ctx, blobName, data)
		return data, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

func (b *BlobStore) WriteBlob(ctx context.Context, blobName string, data []byte) (int, error) {
	n, err := b.backing.WriteBlob(ctx, blobName, data)
	if err != nil {
		return n, err
	}
	b.populate(ctx, blobName, data)
	return n, nil
}

func (b *BlobStore) DeleteBlob(ctx context.Context, blobName string) error {
	if err := b.backing.DeleteBlob(ctx, blobName); err != nil {
		return err
	}
	if err := b.cache.DeleteBlob(ctx, blobName); err != nil && !os.IsNotExist(err) {
		log.Printf("diskcache: evict deleted blob %q from cache: %v", blobName, err)
	}
	return nil
}

// BlobExists always defers to the backing store: cache presence doesn't
// prove current existence (the backing store is the source of truth for
// deletes too), and this is a cheap metadata call either way.
func (b *BlobStore) BlobExists(ctx context.Context, blobName string) (bool, error) {
	return b.backing.BlobExists(ctx, blobName)
}

// Writer tees writes to both the backing store and the local cache, so a
// freshly-written invocation is already cached the first time it's read
// (the common case: a user opens the invocation right after their build
// finishes). The backing write is authoritative — any cache write failure
// only disables caching for the rest of that write, it never fails the
// caller's write.
func (b *BlobStore) Writer(ctx context.Context, blobName string) (io.WriteCloser, error) {
	backingW, err := b.backing.Writer(ctx, blobName)
	if err != nil {
		return nil, err
	}
	cacheW, err := b.cache.Writer(ctx, blobName)
	if err != nil {
		log.Printf("diskcache: open cache writer for %q: %v", blobName, err)
		cacheW = nil
	}
	return &teeWriteCloser{backing: backingW, cache: cacheW, onClose: func() { b.evict() }}, nil
}

func (b *BlobStore) populate(ctx context.Context, blobName string, data []byte) {
	if _, err := b.cache.WriteBlob(ctx, blobName, data); err != nil {
		log.Printf("diskcache: populate %q: %v", blobName, err)
		return
	}
	b.evict()
}

// evict deletes the least-recently-written cache entries until the cache
// is back under maxBytes. It re-lists the whole cache directory on every
// call rather than maintaining an in-memory size index — deliberately
// simple, since bepper's cache holds at most a few thousand entries and
// eviction only runs once per invocation write/populate, not per read.
func (b *BlobStore) evict() {
	if b.maxBytes <= 0 {
		return
	}
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		log.Printf("diskcache: list cache dir: %v", err)
		return
	}

	type file struct {
		name    string
		size    int64
		modTime int64
	}
	var files []file
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{name: e.Name(), size: info.Size(), modTime: info.ModTime().UnixNano()})
		total += info.Size()
	}
	if total <= b.maxBytes {
		return
	}

	sort.Slice(files, func(i, j int) bool { return files[i].modTime < files[j].modTime })
	for _, f := range files {
		if total <= b.maxBytes {
			break
		}
		if err := os.Remove(filepath.Join(b.dir, f.name)); err != nil {
			log.Printf("diskcache: evict %q: %v", f.name, err)
			continue
		}
		total -= f.size
	}
}

type teeWriteCloser struct {
	backing io.WriteCloser
	cache   io.WriteCloser // set to nil once a cache write fails, disabling it for the rest of this blob
	onClose func()
}

func (t *teeWriteCloser) Write(p []byte) (int, error) {
	n, err := t.backing.Write(p)
	if err != nil {
		return n, err
	}
	if t.cache != nil {
		if _, cerr := t.cache.Write(p); cerr != nil {
			log.Printf("diskcache: cache write-through failed, disabling for this blob: %v", cerr)
			t.cache = nil
		}
	}
	return n, nil
}

func (t *teeWriteCloser) Close() error {
	err := t.backing.Close()
	if t.cache != nil {
		if cerr := t.cache.Close(); cerr != nil {
			log.Printf("diskcache: cache close failed: %v", cerr)
		} else if t.onClose != nil {
			t.onClose()
		}
	}
	return err
}
