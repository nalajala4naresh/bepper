package index

// EventParser and its field-extraction rules are adapted from BuildBuddy's
// event_parser.StreamingEventParser (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/build_event_protocol/event_parser/event_parser.go
//
// Ported to populate our own Record instead of BuildBuddy's Invocation
// proto. Dropped: read permissions, remote-cache/remote-execution option
// parsing, and WorkflowConfigured handling — none of that applies here,
// since this project has no auth/multi-tenancy or remote cache/execution
// integration.

import (
	"strings"
	"time"

	buildeventstream "github.com/nalajala4naresh/bepper/proto/gen/build_event_stream"
	"github.com/nalajala4naresh/bepper/proto/gen/command_line"

	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	structuredCommandLineLabelCanonical = "canonical"

	envVarOptionName = "client_env"
	envVarSeparator  = "="
)

// Priorities determine the precedence of different events as they apply to
// Record fields.
//
// For example, a RepoURL setting in BuildMetadata takes priority over a repo
// URL set via WorkspaceStatus, even if the workspace status event came after
// the build metadata event in the stream.
const (
	startedPriority         = 1
	envPriority             = 2
	workspaceStatusPriority = 3
	buildMetadataPriority   = 4
)

// EventParser consumes a stream of build events for one invocation and
// incrementally populates a Record with summary fields.
type EventParser struct {
	record    *Record
	startTime *time.Time
	priority  fieldPriorities
}

// fieldPriorities tracks which priority last set each field, so a
// lower-priority event can't overwrite a value set by a higher-priority one.
type fieldPriorities struct {
	Host, User, Role, RepoURL, BranchName, CommitSHA, Command, Pattern, Tags, ParentRunID, RunID int
}

// NewEventParser creates an EventParser that populates record as events are
// parsed.
func NewEventParser(record *Record) *EventParser {
	return &EventParser{record: record}
}

// Record returns the Record being populated.
func (p *EventParser) Record() *Record {
	return p.record
}

func (p *EventParser) ParseEvent(event *buildeventstream.BuildEvent) {
	switch payload := event.GetPayload().(type) {
	case *buildeventstream.BuildEvent_Started:
		priority := startedPriority
		startTime := timeWithFallback(payload.Started.GetStartTime(), payload.Started.GetStartTimeMillis())
		p.startTime = &startTime
		p.setCommand(payload.Started.GetCommand(), priority)
		for _, child := range event.GetChildren() {
			if pat, ok := child.GetId().(*buildeventstream.BuildEventId_Pattern); ok {
				p.setPattern(pat.Pattern.GetPattern(), priority)
			}
		}
	case *buildeventstream.BuildEvent_StructuredCommandLine:
		p.fillFromStructuredCommandLine(payload.StructuredCommandLine)
	case *buildeventstream.BuildEvent_WorkspaceStatus:
		p.fillFromWorkspaceStatus(payload.WorkspaceStatus)
	case *buildeventstream.BuildEvent_Finished:
		endTime := timeWithFallback(payload.Finished.GetFinishTime(), payload.Finished.GetFinishTimeMillis())
		if p.startTime != nil {
			p.record.DurationUsec = endTime.Sub(*p.startTime).Microseconds()
		}
		p.record.Success = payload.Finished.GetExitCode().GetCode() == 0
		p.record.BazelExitCode = payload.Finished.GetExitCode().GetName()
	case *buildeventstream.BuildEvent_BuildMetrics:
		p.record.ActionCount = payload.BuildMetrics.GetActionSummary().GetActionsExecuted()
	case *buildeventstream.BuildEvent_BuildMetadata:
		p.fillFromBuildMetadata(payload.BuildMetadata.GetMetadata())
	}
}

// timeWithFallback returns time.Time from a timestamp proto field, falling
// back to fallbackMillis when ts is nil. Adapted from BuildBuddy's
// timeutil.GetTimeWithFallback (MIT licensed).
func timeWithFallback(ts *timestamppb.Timestamp, fallbackMillis int64) time.Time {
	if ts != nil {
		return ts.AsTime()
	}
	return time.UnixMilli(fallbackMillis)
}

type cmdOptions struct {
	envVarMap map[string]string
}

func parseCommandLine(cl *command_line.CommandLine) cmdOptions {
	res := cmdOptions{envVarMap: make(map[string]string)}
	if cl == nil {
		return res
	}
	for _, section := range cl.GetSections() {
		optionList, ok := section.GetSectionType().(*command_line.CommandLineSection_OptionList)
		if !ok {
			continue
		}
		for _, option := range optionList.OptionList.GetOption() {
			if option.GetOptionName() != envVarOptionName {
				continue
			}
			if k, v, found := strings.Cut(option.GetOptionValue(), envVarSeparator); found {
				res.envVarMap[k] = v
			}
		}
	}
	return res
}

// fillFromStructuredCommandLine recognizes the CI-provider environment
// variables baked into Bazel's "client_env" structured command line option,
// covering GitHub Actions, GitLab CI, CircleCI, Buildkite, Travis, and
// Bitrise. This provider-detection knowledge is the main thing worth
// preserving from BuildBuddy's implementation.
func (p *EventParser) fillFromStructuredCommandLine(cl *command_line.CommandLine) {
	if cl.GetCommandLineLabel() != structuredCommandLineLabelCanonical {
		return
	}

	priority := envPriority
	env := parseCommandLine(cl).envVarMap

	for _, k := range []string{"USER", "GITHUB_ACTOR", "BUILDKITE_BUILD_CREATOR", "GITLAB_USER_NAME", "CIRCLE_USERNAME"} {
		if v := env[k]; v != "" {
			p.setUser(v, priority)
		}
	}
	for _, k := range []string{"TRAVIS_REPO_SLUG", "GIT_REPOSITORY_URL", "GIT_URL", "BUILDKITE_REPO", "REPO_URL", "CIRCLE_REPOSITORY_URL", "GITHUB_REPOSITORY", "CI_REPOSITORY_URL"} {
		if v := env[k]; v != "" {
			p.setRepoURL(v, priority)
		}
	}
	for _, k := range []string{"TRAVIS_BRANCH", "BITRISE_GIT_BRANCH", "GIT_BRANCH", "BUILDKITE_BRANCH", "CIRCLE_BRANCH", "GITHUB_HEAD_REF", "CI_COMMIT_BRANCH", "CI_MERGE_REQUEST_SOURCE_BRANCH_NAME"} {
		if v := env[k]; v != "" {
			p.setBranchName(v, priority)
		}
	}
	if v := env["GITHUB_REF"]; strings.HasPrefix(v, "refs/heads/") {
		p.setBranchName(strings.TrimPrefix(v, "refs/heads/"), priority)
	}
	for _, k := range []string{"TRAVIS_COMMIT", "BITRISE_GIT_COMMIT", "GIT_COMMIT", "BUILDKITE_COMMIT", "CIRCLE_SHA1", "GITHUB_SHA", "COMMIT_SHA", "VOLATILE_GIT_COMMIT", "CI_COMMIT_SHA"} {
		if v := env[k]; v != "" {
			p.setCommitSHA(v, priority)
		}
	}
	if v := env["CI"]; v != "" {
		p.setRole("CI", priority)
	}
	if v := env["CI_RUNNER"]; v != "" {
		p.setRole("CI_RUNNER", priority)
	}
}

func (p *EventParser) fillFromWorkspaceStatus(ws *buildeventstream.WorkspaceStatus) {
	priority := workspaceStatusPriority
	for _, item := range ws.GetItem() {
		if item.GetValue() == "" {
			continue
		}
		switch item.GetKey() {
		case "BUILD_USER", "USER":
			p.setUser(item.GetValue(), priority)
		case "BUILD_HOST", "HOST":
			p.setHost(item.GetValue(), priority)
		case "PATTERN":
			p.setPattern(strings.Split(item.GetValue(), " "), priority)
		case "ROLE":
			p.setRole(item.GetValue(), priority)
		case "REPO_URL":
			p.setRepoURL(item.GetValue(), priority)
		case "GIT_BRANCH":
			p.setBranchName(item.GetValue(), priority)
		case "COMMIT_SHA":
			p.setCommitSHA(item.GetValue(), priority)
		case "TAGS":
			p.setTags(item.GetValue(), priority)
		}
	}
}

func (p *EventParser) fillFromBuildMetadata(metadata map[string]string) {
	priority := buildMetadataPriority
	if v := metadata["COMMIT_SHA"]; v != "" {
		p.setCommitSHA(v, priority)
	}
	if v := metadata["BRANCH_NAME"]; v != "" {
		p.setBranchName(v, priority)
	}
	if v := metadata["REPO_URL"]; v != "" {
		p.setRepoURL(v, priority)
	}
	if v := metadata["USER"]; v != "" {
		p.setUser(v, priority)
	}
	if v := metadata["HOST"]; v != "" {
		p.setHost(v, priority)
	}
	if v := metadata["PATTERN"]; v != "" {
		p.setPattern(strings.Split(v, " "), priority)
	}
	if v := metadata["ROLE"]; v != "" {
		p.setRole(v, priority)
	}
	if v := metadata["PARENT_RUN_ID"]; v != "" {
		p.setParentRunID(v, priority)
	}
	if v := metadata["RUN_ID"]; v != "" {
		p.setRunID(v, priority)
	}

	var tagValues []string
	if v := metadata["TAGS"]; v != "" {
		tagValues = append(tagValues, v)
	}
	for key, value := range metadata {
		if tagKey, ok := strings.CutPrefix(key, "TAG_"); ok {
			if value != "" {
				tagValues = append(tagValues, tagKey+"="+value)
			} else {
				tagValues = append(tagValues, tagKey)
			}
		}
	}
	if len(tagValues) > 0 {
		p.setTags(strings.Join(tagValues, ","), priority)
	}
}

// All setX methods below only apply value if it hasn't already been set by
// an event with strictly higher priority.

func (p *EventParser) setHost(value string, priority int) {
	if p.priority.Host <= priority {
		p.priority.Host = priority
		p.record.Host = value
	}
}

func (p *EventParser) setUser(value string, priority int) {
	if p.priority.User <= priority {
		p.priority.User = priority
		p.record.User = value
	}
}

func (p *EventParser) setRole(value string, priority int) {
	if p.priority.Role <= priority {
		p.priority.Role = priority
		p.record.Role = value
	}
}

func (p *EventParser) setRepoURL(value string, priority int) {
	if norm, err := normalizeRepoURL(value); err == nil && norm != nil {
		value = norm.String()
	}
	if p.priority.RepoURL <= priority {
		p.priority.RepoURL = priority
		p.record.RepoURL = value
	}
}

func (p *EventParser) setBranchName(value string, priority int) {
	if p.priority.BranchName <= priority {
		p.priority.BranchName = priority
		p.record.BranchName = value
	}
}

func (p *EventParser) setCommitSHA(value string, priority int) {
	if p.priority.CommitSHA <= priority {
		p.priority.CommitSHA = priority
		p.record.CommitSHA = value
	}
}

func (p *EventParser) setCommand(value string, priority int) {
	if p.priority.Command <= priority {
		p.priority.Command = priority
		p.record.Command = value
	}
}

func (p *EventParser) setPattern(value []string, priority int) {
	if p.priority.Pattern <= priority {
		p.priority.Pattern = priority
		p.record.Pattern = value
	}
}

func (p *EventParser) setTags(value string, priority int) {
	tags := splitAndTrimAndDedupeTags(value)
	if p.priority.Tags <= priority {
		p.record.Tags = append(p.record.Tags, tags...)
	} else {
		p.record.Tags = append(tags, p.record.Tags...)
	}
	p.priority.Tags = priority
}

func (p *EventParser) setParentRunID(value string, priority int) {
	if p.priority.ParentRunID <= priority {
		p.priority.ParentRunID = priority
		p.record.ParentRunID = value
	}
}

func (p *EventParser) setRunID(value string, priority int) {
	if p.priority.RunID <= priority {
		p.priority.RunID = priority
		p.record.RunID = value
	}
}
