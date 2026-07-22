// Package store persists received Bazel build events, one blob per
// invocation, via a pluggable blobstore.Blobstore backend (local disk or
// S3).
//
// The per-invocation streaming-write pattern is adapted from BuildBuddy's
// build_event_protocol/build_event_handler (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/build_event_protocol/build_event_handler/build_event_handler.go
package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	buildeventstream "github.com/nalajala4naresh/bepper/proto/gen/build_event_stream"
	"github.com/nalajala4naresh/bepper/src/blobstore"

	"google.golang.org/protobuf/encoding/protojson"
)

// Store persists build events as one blob per invocation, one JSON line per
// event.
type Store struct {
	blobs blobstore.Blobstore

	mu      sync.Mutex
	writers map[string]io.WriteCloser
}

// New creates a Store backed by blobs.
func New(blobs blobstore.Blobstore) *Store {
	return &Store{blobs: blobs, writers: make(map[string]io.WriteCloser)}
}

func blobName(invocationID string) string {
	return invocationID + ".jsonl"
}

// writerFor returns the invocation's blob writer, opening it on first use.
// The map lock is only held for the lookup/creation, not for the write
// itself: a streaming blobstore.Writer (e.g. S3) can block on I/O, and
// holding a global lock across that would stall every other invocation's
// AppendEvent calls. Only one goroutine ever writes to a given invocation's
// stream, so no per-invocation locking is needed either.
func (s *Store) writerFor(ctx context.Context, invocationID string) (io.WriteCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if w, ok := s.writers[invocationID]; ok {
		return w, nil
	}
	w, err := s.blobs.Writer(ctx, blobName(invocationID))
	if err != nil {
		return nil, fmt.Errorf("open blob writer: %w", err)
	}
	s.writers[invocationID] = w
	return w, nil
}

// AppendEvent writes one event as a JSON line to the invocation's blob.
func (s *Store) AppendEvent(ctx context.Context, invocationID string, event *buildeventstream.BuildEvent) error {
	line, err := protojson.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	w, err := s.writerFor(ctx, invocationID)
	if err != nil {
		return err
	}

	if _, err := w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return nil
}

// ReadEvents returns the events previously stored for invocationID via
// AppendEvent, in append order. found is false if no blob exists yet — e.g.
// the invocation hasn't been finalized, or invocationID is unknown.
func (s *Store) ReadEvents(ctx context.Context, invocationID string) (events []*buildeventstream.BuildEvent, found bool, err error) {
	name := blobName(invocationID)
	exists, err := s.blobs.BlobExists(ctx, name)
	if err != nil || !exists {
		return nil, exists, err
	}

	data, err := s.blobs.ReadBlob(ctx, name)
	if err != nil {
		return nil, true, fmt.Errorf("read blob: %w", err)
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		event := &buildeventstream.BuildEvent{}
		if err := protojson.Unmarshal(line, event); err != nil {
			return nil, true, fmt.Errorf("unmarshal event: %w", err)
		}
		events = append(events, event)
	}
	return events, true, nil
}

// Finalize commits the invocation's blob. Safe to call even if no events
// were ever written for that invocation.
func (s *Store) Finalize(invocationID string) error {
	s.mu.Lock()
	w, ok := s.writers[invocationID]
	if ok {
		delete(s.writers, invocationID)
	}
	s.mu.Unlock()

	if !ok {
		return nil
	}
	return w.Close()
}
