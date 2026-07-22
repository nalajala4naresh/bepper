package index

import (
	"context"
	"time"
)

// Indexer persists invocation Records for querying by a UI.
type Indexer interface {
	Upsert(ctx context.Context, rec *Record) error

	// Get returns the record for invocationID, or nil if it isn't indexed.
	Get(ctx context.Context, invocationID string) (*Record, error)

	// List returns the most recent invocations matching opts, newest first.
	List(ctx context.Context, opts ListOptions) ([]*Record, error)
}

// ListOptions filters and bounds a List call. The zero value matches
// everything, most-recent-first.
type ListOptions struct {
	Limit int

	// Query, if set, matches invocations whose command, pattern, repo URL,
	// branch, commit SHA, or user contain it (case-insensitive substring
	// match).
	Query string

	// Status, if set, filters to "success" or "failure".
	Status string

	// Before, if set, restricts the results to invocations created strictly
	// before this time — pass the CreatedAt of the last row from a previous
	// List call to fetch the next page (keyset pagination).
	Before *time.Time

	// Since and Until, if set, restrict results to invocations created in
	// [Since, Until) — a user-facing time-range filter, independent of
	// Before's pagination role (the two combine fine: Before only ever
	// narrows further within whatever [Since, Until) already selected).
	Since *time.Time
	Until *time.Time
}
