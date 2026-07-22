package index

import (
	"time"

	buildeventstream "github.com/nalajala4naresh/bepper/proto/gen/build_event_stream"
)

// Target is the per-target summary shown on an invocation's Targets view,
// built by parsing TargetConfigured/TargetComplete/TestSummary events.
//
// Unlike Record, this isn't persisted to Postgres — it's cheap to
// re-derive from the invocation's stored event stream on read, and there's
// no need to search/filter across targets the way there is for
// invocations.
type Target struct {
	Label   string
	Kind    string
	IsTest  bool
	Success bool
	// TestStatus is the Bazel TestStatus enum name (e.g. "PASSED",
	// "FLAKY", "TIMEOUT"), empty for non-test targets.
	TestStatus     string
	DurationUsec   int64
	FailureMessage string

	// LogFiles are file references (test.log, test.xml, undeclared
	// outputs, ...) reported for this target's test actions, in the order
	// reported. Their URI is almost always a file:// path local to the
	// machine that ran Bazel — bepper never fetches or stores their
	// contents, since doing so would require a remote cache Bazel is
	// configured to upload to (which bepper doesn't implement). Surfaced
	// as a best-effort reference, not guaranteed to resolve.
	LogFiles []LogFile

	// Attempts is one entry per TestResult event reported for this
	// target, in the order reported — for a normal single-run test this
	// is one entry, but --runs_per_test/flaky-retries report multiple
	// TestResult events for the same label, each a distinct attempt.
	// TestStatus/DurationUsec above are the overall summary across all of
	// these (from TestSummary); Attempts is what makes individual runs
	// (e.g. "attempt 2 of 3 failed, others passed") visible.
	Attempts []TestAttempt

	// OutputFiles are the target's build outputs (the actual compiled
	// binary/library/generated files, for any target — not just tests).
	// TargetComplete has a deprecated important_output field that's
	// simpler to read directly, but real Bazel output confirms it isn't
	// populated at all in current versions — the actual data lives in
	// output_group's file_sets, each a reference into a deduplicated
	// NamedSetOfFiles graph (files are shared across targets/groups, so
	// they're reported once and referenced by id rather than inlined
	// everywhere) that has to be resolved separately; see
	// resolveFileSets/parseNamedSets below.
	OutputFiles []LogFile
}

// LogFile is a file reference reported for a target's test action.
type LogFile struct {
	Name string
	URI  string
}

// TestAttempt is one TestResult event for a target — a single test run.
type TestAttempt struct {
	// Status is the Bazel TestStatus enum name for this specific attempt
	// (e.g. "PASSED", "FAILED"), independent of the target's overall
	// TestStatus.
	Status        string
	StartTime     time.Time
	DurationUsec  int64
	CachedLocally bool
}

// parseNamedSets indexes every NamedSetOfFiles event by its id, so
// resolveFileSets can look up a TargetComplete output_group's file_sets
// references regardless of where in the stream the corresponding
// NamedSetOfFiles event appears (in practice Bazel emits them before the
// TargetComplete events that reference them, but nothing in the protocol
// guarantees that, so this is a separate pass over the whole stream rather
// than something resolved inline during the main per-event loop below).
func parseNamedSets(events []*buildeventstream.BuildEvent) map[string]*buildeventstream.NamedSetOfFiles {
	sets := map[string]*buildeventstream.NamedSetOfFiles{}
	for _, event := range events {
		nsf, ok := event.GetPayload().(*buildeventstream.BuildEvent_NamedSetOfFiles)
		if !ok {
			continue
		}
		if id := event.GetId().GetNamedSet(); id != nil {
			sets[id.GetId()] = nsf.NamedSetOfFiles
		}
	}
	return sets
}

// resolveFileSets collects every File transitively reachable from ids,
// which is itself a DAG (a NamedSetOfFiles can reference further named
// sets via its own file_sets field, e.g. a binary's output group
// referencing its dependencies' output groups) — seen guards against
// revisiting the same set twice, since the same set is commonly shared
// across multiple targets/groups by design (that's the point of
// deduplicating files into named sets rather than inlining them
// everywhere).
func resolveFileSets(sets map[string]*buildeventstream.NamedSetOfFiles, ids []*buildeventstream.BuildEventId_NamedSetOfFilesId, seen map[string]bool) []LogFile {
	var files []LogFile
	for _, id := range ids {
		key := id.GetId()
		if seen[key] {
			continue
		}
		seen[key] = true

		set, ok := sets[key]
		if !ok {
			continue
		}
		for _, f := range set.GetFiles() {
			if uri := f.GetUri(); uri != "" {
				files = append(files, LogFile{Name: f.GetName(), URI: uri})
			}
		}
		files = append(files, resolveFileSets(sets, set.GetFileSets(), seen)...)
	}
	return files
}

// ParseTargets extracts a Target summary for every target referenced in
// events, in first-seen order.
func ParseTargets(events []*buildeventstream.BuildEvent) []*Target {
	namedSets := parseNamedSets(events)

	byLabel := map[string]*Target{}
	var order []string

	get := func(label string) *Target {
		t, ok := byLabel[label]
		if !ok {
			t = &Target{Label: label}
			byLabel[label] = t
			order = append(order, label)
		}
		return t
	}

	for _, event := range events {
		id := event.GetId()
		switch payload := event.GetPayload().(type) {
		case *buildeventstream.BuildEvent_Configured:
			if tc := id.GetTargetConfigured(); tc != nil && tc.GetAspect() == "" {
				t := get(tc.GetLabel())
				t.Kind = payload.Configured.GetTargetKind()
				t.IsTest = payload.Configured.GetTestSize() != buildeventstream.TestSize_UNKNOWN
			}
		case *buildeventstream.BuildEvent_Completed:
			if tc := id.GetTargetCompleted(); tc != nil && tc.GetAspect() == "" {
				t := get(tc.GetLabel())
				t.Success = payload.Completed.GetSuccess()
				if fd := payload.Completed.GetFailureDetail(); fd != nil {
					t.FailureMessage = fd.GetMessage()
				}
				for _, og := range payload.Completed.GetOutputGroup() {
					t.OutputFiles = append(t.OutputFiles, resolveFileSets(namedSets, og.GetFileSets(), map[string]bool{})...)
				}
			}
		case *buildeventstream.BuildEvent_TestSummary:
			if ts := id.GetTestSummary(); ts != nil {
				t := get(ts.GetLabel())
				t.IsTest = true
				t.TestStatus = payload.TestSummary.GetOverallStatus().String()
				t.Success = payload.TestSummary.GetOverallStatus() == buildeventstream.TestStatus_PASSED
				t.DurationUsec = payload.TestSummary.GetTotalRunDuration().AsDuration().Microseconds()
			}
		case *buildeventstream.BuildEvent_TestResult:
			if tr := id.GetTestResult(); tr != nil {
				t := get(tr.GetLabel())
				t.IsTest = true
				for _, f := range payload.TestResult.GetTestActionOutput() {
					if uri := f.GetUri(); uri != "" {
						t.LogFiles = append(t.LogFiles, LogFile{Name: f.GetName(), URI: uri})
					}
				}
				t.Attempts = append(t.Attempts, TestAttempt{
					Status:        payload.TestResult.GetStatus().String(),
					StartTime:     payload.TestResult.GetTestAttemptStart().AsTime(),
					DurationUsec:  payload.TestResult.GetTestAttemptDuration().AsDuration().Microseconds(),
					CachedLocally: payload.TestResult.GetCachedLocally(),
				})
			}
		}
	}

	targets := make([]*Target, 0, len(order))
	for _, label := range order {
		targets = append(targets, byLabel[label])
	}
	return targets
}
