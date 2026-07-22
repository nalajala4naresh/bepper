package index

import (
	buildeventstream "github.com/nalajala4naresh/bepper/proto/gen/build_event_stream"
	"github.com/nalajala4naresh/bepper/proto/gen/command_line"
)

// Flag is one effective command-line flag, as reported in the invocation's
// canonical StructuredCommandLine event.
type Flag struct {
	CombinedForm string
	Source       string
}

// ParseFlags extracts the effective command-line flags from the canonical
// StructuredCommandLine event, excluding client_env (already surfaced via
// Record's User/RepoURL/etc. fields — see fillFromStructuredCommandLine in
// parser.go).
func ParseFlags(events []*buildeventstream.BuildEvent) []Flag {
	var flags []Flag
	for _, event := range events {
		cl, ok := event.GetPayload().(*buildeventstream.BuildEvent_StructuredCommandLine)
		if !ok || cl.StructuredCommandLine.GetCommandLineLabel() != structuredCommandLineLabelCanonical {
			continue
		}
		for _, section := range cl.StructuredCommandLine.GetSections() {
			optionList, ok := section.GetSectionType().(*command_line.CommandLineSection_OptionList)
			if !ok {
				continue
			}
			for _, option := range optionList.OptionList.GetOption() {
				if option.GetOptionName() == envVarOptionName {
					continue
				}
				flags = append(flags, Flag{
					CombinedForm: option.GetCombinedForm(),
					Source:       option.GetSource(),
				})
			}
		}
	}
	return flags
}

// ParseBuildError returns a human-readable description of why the build
// failed, preferring BuildFinished's FailureDetail and falling back to the
// last Aborted event's description. Empty if the build succeeded, or if no
// error detail was captured.
func ParseBuildError(events []*buildeventstream.BuildEvent) string {
	var lastAborted string
	for _, event := range events {
		switch payload := event.GetPayload().(type) {
		case *buildeventstream.BuildEvent_Aborted:
			if d := payload.Aborted.GetDescription(); d != "" {
				lastAborted = d
			}
		case *buildeventstream.BuildEvent_Finished:
			if fd := payload.Finished.GetFailureDetail(); fd.GetMessage() != "" {
				return fd.GetMessage()
			}
		}
	}
	return lastAborted
}

// BuildInfo holds platform/execution-strategy summary fields shown on the
// invocation Overview tab, alongside the Record fields already persisted
// to Postgres. Unlike Record, this is derived fresh from the invocation's
// stored event stream on every read (same pattern as ParseFlags/
// ParseBuildError above) — none of it needs to be searchable/indexed
// across invocations, so it doesn't need a Postgres column or migration.
type BuildInfo struct {
	CPU                    string
	RemoteExecutionEnabled bool
	CachingEnabled         bool
	PackagesLoaded         int64
	FetchCount             int
}

// ParseBuildInfo extracts BuildInfo from an invocation's event stream.
func ParseBuildInfo(events []*buildeventstream.BuildEvent) BuildInfo {
	var info BuildInfo
	for _, event := range events {
		switch payload := event.GetPayload().(type) {
		case *buildeventstream.BuildEvent_Configuration:
			if cpu := payload.Configuration.GetCpu(); cpu != "" {
				info.CPU = cpu
			}
		case *buildeventstream.BuildEvent_BuildMetrics:
			info.PackagesLoaded = payload.BuildMetrics.GetPackageMetrics().GetPackagesLoaded()
		case *buildeventstream.BuildEvent_Fetch:
			info.FetchCount++
		case *buildeventstream.BuildEvent_StructuredCommandLine:
			cl := payload.StructuredCommandLine
			if cl.GetCommandLineLabel() != structuredCommandLineLabelCanonical {
				continue
			}
			if optionValue(cl, "remote_executor") != "" {
				info.RemoteExecutionEnabled = true
			}
			if optionValue(cl, "remote_cache") != "" {
				info.CachingEnabled = true
			}
		}
	}
	return info
}

// optionValue returns the value of the first option named name found in
// cl's canonical option list, or "" if it's unset or set to an empty
// string — either way, "not configured" for the on/off checks in
// ParseBuildInfo above.
func optionValue(cl *command_line.CommandLine, name string) string {
	for _, section := range cl.GetSections() {
		optionList, ok := section.GetSectionType().(*command_line.CommandLineSection_OptionList)
		if !ok {
			continue
		}
		for _, option := range optionList.OptionList.GetOption() {
			if option.GetOptionName() == name {
				return option.GetOptionValue()
			}
		}
	}
	return ""
}
