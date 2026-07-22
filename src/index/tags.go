package index

import "strings"

// splitAndTrimAndDedupeTags splits a comma-separated tag string, trims
// whitespace from each tag, and removes duplicate entries, adapted from
// BuildBuddy's invocation_format.SplitAndTrimAndDedupeTags (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/build_event_protocol/invocation_format/invocation_format.go
func splitAndTrimAndDedupeTags(tags string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Split(tags, ",") {
		trimmed := strings.TrimSpace(t)
		if len(trimmed) > 0 && !seen[trimmed] {
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}
	return out
}
