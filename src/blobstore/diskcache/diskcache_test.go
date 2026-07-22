package diskcache

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeBacking is an in-memory blobstore.Blobstore, standing in for S3 in
// tests — it counts calls so tests can assert on cache hit/miss behavior
// without a real network round trip.
type fakeBacking struct {
	mu                     sync.Mutex
	blobs                  map[string][]byte
	reads, writes, deletes int
	blockReads             chan struct{} // if non-nil, ReadBlob waits on it before proceeding
}

func newFakeBacking() *fakeBacking {
	return &fakeBacking{blobs: map[string][]byte{}}
}

func (f *fakeBacking) BlobExists(ctx context.Context, blobName string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.blobs[blobName]
	return ok, nil
}

func (f *fakeBacking) ReadBlob(ctx context.Context, blobName string) ([]byte, error) {
	if f.blockReads != nil {
		<-f.blockReads
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	data, ok := f.blobs[blobName]
	if !ok {
		return nil, fmt.Errorf("blob %q not found", blobName)
	}
	return data, nil
}

func (f *fakeBacking) WriteBlob(ctx context.Context, blobName string, data []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	cp := append([]byte(nil), data...)
	f.blobs[blobName] = cp
	return len(cp), nil
}

func (f *fakeBacking) DeleteBlob(ctx context.Context, blobName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	delete(f.blobs, blobName)
	return nil
}

func (f *fakeBacking) Writer(ctx context.Context, blobName string) (io.WriteCloser, error) {
	return &fakeWriter{backing: f, blobName: blobName}, nil
}

type fakeWriter struct {
	backing  *fakeBacking
	blobName string
	buf      bytes.Buffer
}

func (w *fakeWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *fakeWriter) Close() error {
	_, err := w.backing.WriteBlob(context.Background(), w.blobName, w.buf.Bytes())
	return err
}

func (f *fakeBacking) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func TestReadBlob_CachesAfterFirstFetch(t *testing.T) {
	backing := newFakeBacking()
	backing.blobs["a"] = []byte("hello")
	c, err := New(backing, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 3; i++ {
		data, err := c.ReadBlob(context.Background(), "a")
		if err != nil {
			t.Fatalf("ReadBlob: %v", err)
		}
		if string(data) != "hello" {
			t.Fatalf("ReadBlob = %q, want %q", data, "hello")
		}
	}
	if got := backing.readCount(); got != 1 {
		t.Fatalf("backing.reads = %d, want 1 (later reads should hit the disk cache)", got)
	}
}

func TestReadBlob_NotFoundPropagates(t *testing.T) {
	c, err := New(newFakeBacking(), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.ReadBlob(context.Background(), "missing"); err == nil {
		t.Fatal("ReadBlob: expected error for missing blob, got nil")
	}
}

func TestReadBlob_ConcurrentMissesCoalesced(t *testing.T) {
	backing := newFakeBacking()
	backing.blobs["a"] = []byte("hello")
	backing.blockReads = make(chan struct{})
	c, err := New(backing, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.ReadBlob(context.Background(), "a")
		}(i)
	}
	// Give every goroutine a chance to reach the (blocked) backing fetch
	// before releasing it, so they actually overlap rather than running
	// serially.
	time.Sleep(50 * time.Millisecond)
	close(backing.blockReads)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: ReadBlob: %v", i, err)
		}
	}
	if got := backing.readCount(); got != 1 {
		t.Fatalf("backing.reads = %d, want 1 (concurrent misses should be coalesced)", got)
	}
}

func TestWriteBlob_WritesThroughAndPopulatesCache(t *testing.T) {
	backing := newFakeBacking()
	c, err := New(backing, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.WriteBlob(context.Background(), "a", []byte("hello")); err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}
	if backing.writes != 1 {
		t.Fatalf("backing.writes = %d, want 1", backing.writes)
	}

	data, err := c.ReadBlob(context.Background(), "a")
	if err != nil {
		t.Fatalf("ReadBlob: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("ReadBlob = %q, want %q", data, "hello")
	}
	if got := backing.readCount(); got != 0 {
		t.Fatalf("backing.reads = %d, want 0 (WriteBlob should have already populated the cache)", got)
	}
}

func TestWriter_TeesToBackingAndCache(t *testing.T) {
	backing := newFakeBacking()
	c, err := New(backing, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w, err := c.Writer(context.Background(), "a")
	if err != nil {
		t.Fatalf("Writer: %v", err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if backing.blobs["a"] == nil || string(backing.blobs["a"]) != "hello" {
		t.Fatalf("backing blob = %q, want %q", backing.blobs["a"], "hello")
	}

	data, err := c.ReadBlob(context.Background(), "a")
	if err != nil {
		t.Fatalf("ReadBlob: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("ReadBlob = %q, want %q", data, "hello")
	}
	if got := backing.readCount(); got != 0 {
		t.Fatalf("backing.reads = %d, want 0 (Writer should have already populated the cache)", got)
	}
}

func TestDeleteBlob_RemovesFromBackingAndCache(t *testing.T) {
	backing := newFakeBacking()
	c, err := New(backing, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.WriteBlob(context.Background(), "a", []byte("hello")); err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}

	if err := c.DeleteBlob(context.Background(), "a"); err != nil {
		t.Fatalf("DeleteBlob: %v", err)
	}
	if backing.deletes != 1 {
		t.Fatalf("backing.deletes = %d, want 1", backing.deletes)
	}
	if _, err := c.ReadBlob(context.Background(), "a"); err == nil {
		t.Fatal("ReadBlob: expected error after delete, got nil")
	}
}

func TestEvict_RemovesOldestWhenOverCap(t *testing.T) {
	blob := func(n byte) []byte { return bytes.Repeat([]byte{n}, 10) }

	// disk.BlobStore gzip-compresses everything it writes, so the on-disk
	// size of even a tiny blob isn't just len(data) — measure it for real
	// via a throwaway store instead of guessing, so the cap below reliably
	// fits exactly two blobs and not three.
	probe, err := New(newFakeBacking(), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New (probe): %v", err)
	}
	if _, err := probe.WriteBlob(context.Background(), "probe", blob('x')); err != nil {
		t.Fatalf("WriteBlob (probe): %v", err)
	}
	probeInfo, err := os.Stat(filepath.Join(probe.dir, "probe"))
	if err != nil {
		t.Fatalf("Stat (probe): %v", err)
	}
	maxBytes := 2*probeInfo.Size() + 1

	backing := newFakeBacking()
	dir := t.TempDir()
	c, err := New(backing, dir, maxBytes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i, name := range []string{"a", "b", "c"} {
		if _, err := c.WriteBlob(context.Background(), name, blob(byte('a'+i))); err != nil {
			t.Fatalf("WriteBlob(%s): %v", name, err)
		}
		// Give each write a distinct mtime the eviction inside the *next*
		// WriteBlob call will see, so eviction order is deterministic
		// regardless of filesystem mtime resolution.
		time.Sleep(10 * time.Millisecond)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := map[string]bool{}
	var total int64
	for _, e := range entries {
		names[e.Name()] = true
		info, err := e.Info()
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		total += info.Size()
	}
	if total > maxBytes {
		t.Fatalf("cache dir size = %d bytes, want <= %d", total, maxBytes)
	}
	if names["a"] {
		t.Fatal("expected oldest entry \"a\" to have been evicted")
	}
	if !names["c"] {
		t.Fatal("expected newest entry \"c\" to still be cached")
	}
}
