// Package index extracts queryable summary fields (command, patterns, repo,
// success, duration, ...) from a Bazel build event stream, so a UI can list
// and filter invocations without reading the full event blob.
//
// The extraction rules — which event and env var fields feed which summary
// field, and their precedence — are adapted from BuildBuddy's
// event_parser.StreamingEventParser (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/build_event_protocol/event_parser/event_parser.go
package index

import "time"

// Record is the queryable summary of one invocation, built up incrementally
// by EventParser as events for that invocation arrive.
type Record struct {
	InvocationID string

	Host        string
	User        string
	Role        string
	RepoURL     string
	BranchName  string
	CommitSHA   string
	Command     string
	Pattern     []string
	Tags        []string
	ParentRunID string
	RunID       string

	Success       bool
	BazelExitCode string
	DurationUsec  int64
	ActionCount   int64

	CreatedAt time.Time
	UpdatedAt time.Time
}
