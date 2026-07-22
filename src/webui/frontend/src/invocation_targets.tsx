// Original code, styled in the spirit of BuildBuddy's
// invocation_target_group_card.tsx, but not ported from it: that file reads
// from InvocationModel (a 1187-line wrapper around BuildBuddy's own
// `target` proto and BuildBuddyService RPCs), which has no bepper
// equivalent. This renders index.Target summaries from bepper's own
// /api/invocations/{id}/details endpoint instead. The status-grouping
// (failing groups first) and per-target-row layout follow BuildBuddy's UX,
// reimplemented from scratch against bepper's simpler Target shape.
import { AlertCircle, Check, CheckCircle2, ChevronDown, ChevronRight, Clock, Copy, FileText, HelpCircle, MinusCircle, Search, XCircle } from "lucide-react";
import React from "react";
import { blobURL, getInvocationBlob, LogFile, Target } from "./api";
import { formatDuration, formatTime } from "./format";
import { parseJUnitXML, TestCase, TestCaseStatus, TestSuite } from "./junit_xml";
import { copyToClipboard } from "./util/clipboard";

// StatusKey buckets a target into one of the groups the Targets tab
// renders, in the fixed priority order STATUS_GROUPS lists them —
// failing/unusual outcomes first, so they're never buried below a long
// list of passing targets.
type StatusKey = "failed" | "failed_to_build" | "timeout" | "flaky" | "passed" | "built";

// classify maps a Target's (isTest, success, testStatus) onto a StatusKey.
// Note: Bazel's TestSummary.overall_status is only PASSED for a clean
// pass, so a FLAKY test (passed after retry) has success=false — without
// this classification it would render identically to a real failure.
function classify(t: Target): StatusKey {
  if (t.isTest) {
    switch (t.testStatus) {
      case "FAILED":
        return "failed";
      case "FAILED_TO_BUILD":
        return "failed_to_build";
      case "TIMEOUT":
        return "timeout";
      case "FLAKY":
        return "flaky";
      case "PASSED":
        return "passed";
      default:
        // INCOMPLETE / REMOTE_FAILURE / TOOL_HALTED_BEFORE_TESTING / NO_STATUS
        return t.success ? "passed" : "failed";
    }
  }
  return t.success ? "built" : "failed_to_build";
}

const STATUS_GROUPS: {
  key: StatusKey;
  title: string;
  icon: typeof CheckCircle2;
  className: string;
}[] = [
  { key: "failed", title: "Failed", icon: XCircle, className: "failure" },
  { key: "failed_to_build", title: "Failed to build", icon: XCircle, className: "failure" },
  { key: "timeout", title: "Timed out", icon: Clock, className: "warning" },
  { key: "flaky", title: "Flaky", icon: HelpCircle, className: "warning" },
  { key: "passed", title: "Passed", icon: CheckCircle2, className: "success" },
  { key: "built", title: "Built", icon: CheckCircle2, className: "success" },
];

function TargetIcon({ status }: { status: StatusKey }) {
  const group = STATUS_GROUPS.find((g) => g.key === status)!;
  const Icon = group.icon;
  return <Icon size={16} className={`target-icon ${group.className}`} />;
}

// isFetchable reports whether uri points somewhere bepper can actually read
// bytes from via its own API. bytestream:// refs are fetchable via
// src/bytestream (the remote cache Bazel uploaded to — not necessarily
// bepper's own server); file:// refs are local to whatever machine ran
// Bazel and never fetchable.
function isFetchable(uri: string): boolean {
  return uri.startsWith("bytestream://");
}

// isDirectlyLinkable reports whether the browser can navigate to uri on its
// own, without going through bepper's server at all.
function isDirectlyLinkable(uri: string): boolean {
  return uri.startsWith("http://") || uri.startsWith("https://");
}

const TEST_CASE_ICON: Record<TestCaseStatus, typeof CheckCircle2> = {
  passed: CheckCircle2,
  failed: XCircle,
  error: AlertCircle,
  skipped: MinusCircle,
};

const TEST_CASE_CLASS: Record<TestCaseStatus, string> = {
  passed: "success",
  failed: "failure",
  error: "failure",
  skipped: "muted-icon",
};

function TestCaseRow({ testCase }: { testCase: TestCase }) {
  const [expanded, setExpanded] = React.useState(false);
  const Icon = TEST_CASE_ICON[testCase.status];
  const expandable = !!(testCase.message || testCase.stackTrace);
  const qualifiedName = testCase.className && testCase.className !== testCase.name ? `${testCase.className}.${testCase.name}` : testCase.name;

  return (
    <div className={`test-case-row ${expandable ? "expandable" : ""}`}>
      <div className="test-case-row-main" onClick={() => expandable && setExpanded(!expanded)}>
        {expandable ? (
          expanded ? (
            <ChevronDown size={13} className="chevron" />
          ) : (
            <ChevronRight size={13} className="chevron" />
          )
        ) : (
          <span className="chevron-spacer" />
        )}
        <Icon size={14} className={`target-icon ${TEST_CASE_CLASS[testCase.status]}`} />
        <span className="test-case-name mono">{qualifiedName}</span>
        {testCase.time && <span className="test-case-time muted">{testCase.time}s</span>}
      </div>
      {expanded && (
        <div className="test-case-detail">
          {testCase.message && <div className="test-case-message">{testCase.message}</div>}
          {testCase.stackTrace && <pre className="target-failure">{testCase.stackTrace}</pre>}
        </div>
      )}
    </div>
  );
}

function TestSuiteView({ suite }: { suite: TestSuite }) {
  return (
    <div className="test-suite">
      {suite.name && (
        <div className="test-suite-summary muted">
          {suite.name} — {suite.tests} test{suite.tests === 1 ? "" : "s"}
          {suite.failures ? `, ${suite.failures} failed` : ""}
          {suite.errors ? `, ${suite.errors} errors` : ""}
          {suite.skipped ? `, ${suite.skipped} skipped` : ""}
        </div>
      )}
      {suite.testCases.map((tc, i) => (
        <TestCaseRow key={i} testCase={tc} />
      ))}
    </div>
  );
}

type FetchState = { status: "idle" } | { status: "loading" } | { status: "error"; message: string } | { status: "loaded"; content: string };

// isTestXML reports whether name is Bazel's per-test JUnit XML result file
// (as opposed to test.log or anything else a target might report).
function isTestXML(name: string): boolean {
  return name === "test.xml" || name.endsWith("/test.xml");
}

function LogFileRef({ invocationId, file }: { invocationId: string; file: LogFile }) {
  const [state, setState] = React.useState<FetchState>({ status: "idle" });
  const [showRaw, setShowRaw] = React.useState(false);

  async function view() {
    setState({ status: "loading" });
    try {
      const content = await getInvocationBlob(invocationId, file.uri);
      setState({ status: "loaded", content });
    } catch (err) {
      setState({ status: "error", message: String(err) });
    }
  }

  const suites = state.status === "loaded" && isTestXML(file.name) ? parseJUnitXML(state.content) : null;

  return (
    <div className="log-file-ref-wrap">
      <div className="log-file-ref">
        <FileText size={13} />
        <span className="log-file-name">{file.name}</span>
        {/* bytestream:// URIs are long and not worth showing raw once
            View/Open below give a real way to actually get the content —
            the uri text only earns its place for file:// paths, where
            there's nothing else useful to show. */}
        {!isFetchable(file.uri) &&
          (isDirectlyLinkable(file.uri) ? (
            <a href={file.uri} target="_blank" rel="noreferrer" className="mono log-file-uri">
              {file.uri}
            </a>
          ) : (
            <span className="mono muted log-file-uri">{file.uri}</span>
          ))}
        {/* View (fetch + render inline) only earns its place for test.xml,
            where it's the only way to get the parsed per-test-case view —
            for everything else, Open (a plain link to the streaming
            endpoint) already covers it, and fetching a binary output file
            as text to render inline would just show garbage. */}
        {isFetchable(file.uri) && isTestXML(file.name) && state.status !== "loaded" && (
          <button className="log-file-view-btn" onClick={view} disabled={state.status === "loading"}>
            {state.status === "loading" ? "Loading…" : "View"}
          </button>
        )}
        {isFetchable(file.uri) && (
          <a className="log-file-view-btn" href={blobURL(invocationId, file.uri)} target="_blank" rel="noreferrer">
            Open
          </a>
        )}
        {suites && (
          <button className="log-file-view-btn" onClick={() => setShowRaw(!showRaw)}>
            {showRaw ? "View parsed" : "View raw XML"}
          </button>
        )}
      </div>
      {state.status === "error" && <div className="log-file-error">Failed to fetch: {state.message}</div>}
      {state.status === "loaded" &&
        (suites && !showRaw ? (
          <div className="test-suite-list">
            {suites.map((s, i) => (
              <TestSuiteView key={i} suite={s} />
            ))}
          </div>
        ) : (
          <pre className="log-file-content">{state.content || "(empty file)"}</pre>
        ))}
    </div>
  );
}

function CopyLabelButton({ label }: { label: string }) {
  const [copied, setCopied] = React.useState(false);

  function onClick(e: React.MouseEvent) {
    e.stopPropagation(); // don't also toggle the row's expand/collapse
    copyToClipboard(label);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  }

  return (
    <button className="target-copy-btn" onClick={onClick} title="Copy target label" aria-label="Copy target label">
      {copied ? <Check size={13} /> : <Copy size={13} />}
    </button>
  );
}

// TargetListItem is a single row in the sidebar: compact, single-line
// (truncated with an ellipsis — the full label is a title tooltip and one
// click away in the detail pane's copy button), no inline expand. Unlike
// the old accordion rows, clicking just selects the target rather than
// toggling anything in place.
function TargetListItem({
  target,
  status,
  selected,
  onClick,
}: {
  target: Target;
  status: StatusKey;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <div className={`target-list-item ${selected ? "selected" : ""}`} onClick={onClick} title={target.label}>
      <TargetIcon status={status} />
      <span className="target-list-label mono">{target.label}</span>
      <span className="target-list-duration muted">{target.isTest ? formatDuration(target.durationUsec) : ""}</span>
    </div>
  );
}

// TargetDetailPane is the right-hand pane showing the currently-selected
// target's full label, status, failure message, and log files (including
// the parsed test.xml view from LogFileRef) — the content that used to
// live inline under each accordion row.
function TargetDetailPane({ invocationId, target, status }: { invocationId: string; target: Target; status: StatusKey }) {
  const hasFailureDetail = !!target.failureMessage;
  const hasOutputFiles = target.outputFiles.length > 0;
  const hasLogFiles = target.logFiles.length > 0;
  // A single attempt is already covered by the summary line above
  // (testStatus + duration) — the table only earns its space once there's
  // actual per-run history to show (--runs_per_test, flaky retries).
  const hasAttempts = target.attempts.length > 1;

  return (
    <div>
      <div className="target-detail-header">
        <TargetIcon status={status} />
        <h3 className="mono">{target.label}</h3>
        <CopyLabelButton label={target.label} />
      </div>
      <div className="target-detail-meta muted">
        {target.kind}
        {target.isTest && target.testStatus ? ` · ${target.testStatus}` : ""}
        {target.isTest && target.durationUsec ? ` · ${formatDuration(target.durationUsec)}` : ""}
      </div>
      {hasFailureDetail && <pre className="target-failure">{target.failureMessage}</pre>}
      {hasOutputFiles && (
        <div className="log-file-list">
          <h4>Output files ({target.outputFiles.length})</h4>
          {target.outputFiles.map((f, i) => (
            <LogFileRef key={i} invocationId={invocationId} file={f} />
          ))}
        </div>
      )}
      {hasLogFiles && (
        <div className="log-file-list">
          {target.logFiles.map((f, i) => (
            <LogFileRef key={i} invocationId={invocationId} file={f} />
          ))}
        </div>
      )}
      {hasAttempts && (
        <div className="attempts-table">
          <h4>Run history ({target.attempts.length} attempts)</h4>
          <table className="invocations">
            <thead>
              <tr>
                <th>Run</th>
                <th>Status</th>
                <th>Started</th>
                <th>Duration</th>
              </tr>
            </thead>
            <tbody>
              {target.attempts.map((a, i) => (
                <tr key={i}>
                  <td>{i + 1}</td>
                  <td>
                    {a.status}
                    {a.cachedLocally && <span className="cached-badge">cached</span>}
                  </td>
                  <td className="muted">{formatTime(a.startTime)}</td>
                  <td className="muted">{formatDuration(a.durationUsec)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {!hasFailureDetail && !hasOutputFiles && !hasLogFiles && !hasAttempts && <p className="muted">No additional details for this target.</p>}
    </div>
  );
}

// firstInteresting picks the target most likely worth looking at first:
// the first non-passing one, or just the first target if everything
// passed/built cleanly.
function firstInteresting(targets: Target[]): Target | undefined {
  return targets.find((t) => classify(t) !== "passed" && classify(t) !== "built") ?? targets[0];
}

// TargetListGroup is one collapsible status section in the sidebar.
// Passed/Built groups start collapsed by default — they're the least
// likely thing you opened the tab to look at, especially once there are
// a handful of failures mixed in with hundreds of passing targets — unless
// the initially-selected target happens to live there (e.g. everything
// passed, so there's nothing else to auto-select), in which case starting
// collapsed would hide the very row that's selected.
function TargetListGroup({
  group,
  targets,
  selectedLabel,
  onSelect,
}: {
  group: (typeof STATUS_GROUPS)[number];
  targets: Target[];
  selectedLabel: string | null;
  onSelect: (label: string) => void;
}) {
  const [collapsed, setCollapsed] = React.useState(
    () => (group.key === "passed" || group.key === "built") && !targets.some((t) => t.label === selectedLabel)
  );

  return (
    <div className="target-list-group">
      <div className={`target-list-group-header targets-summary-${group.className}`} onClick={() => setCollapsed(!collapsed)}>
        {collapsed ? <ChevronRight size={13} className="chevron" /> : <ChevronDown size={13} className="chevron" />}
        {group.title} ({targets.length})
      </div>
      {!collapsed &&
        targets.map((t) => (
          <TargetListItem key={t.label} target={t} status={group.key} selected={t.label === selectedLabel} onClick={() => onSelect(t.label)} />
        ))}
    </div>
  );
}

export function InvocationTargets({ invocationId, targets }: { invocationId: string; targets: Target[] }) {
  const [filter, setFilter] = React.useState("");
  // Lazy initializer: InvocationTargets only ever mounts once `targets` is
  // already loaded (see invocation_detail.tsx's tab === "targets" branch),
  // so this only needs to run once, not react to `targets` changing later.
  const [selectedLabel, setSelectedLabel] = React.useState<string | null>(() => firstInteresting(targets)?.label ?? null);

  if (targets.length === 0) {
    return <p className="muted">No target results were reported for this invocation.</p>;
  }

  const filtered = filter.trim() ? targets.filter((t) => t.label.toLowerCase().includes(filter.trim().toLowerCase())) : targets;
  const groups = STATUS_GROUPS.map((g) => ({ group: g, targets: filtered.filter((t) => classify(t) === g.key) })).filter(
    (g) => g.targets.length > 0
  );
  const selectedTarget = targets.find((t) => t.label === selectedLabel) ?? null;

  return (
    <div className="targets-tab">
      <div className="filter-input targets-filter">
        <Search size={14} />
        <input type="text" placeholder="Filter targets by label…" value={filter} onChange={(e) => setFilter(e.target.value)} />
      </div>

      <div className="targets-layout">
        <div className="targets-sidebar">
          {groups.length === 0 ? (
            <p className="muted targets-sidebar-empty">No targets match "{filter}".</p>
          ) : (
            groups.map(({ group, targets: groupTargets }) => (
              <TargetListGroup key={group.key} group={group} targets={groupTargets} selectedLabel={selectedLabel} onSelect={setSelectedLabel} />
            ))
          )}
        </div>
        <div className="targets-detail-pane">
          {selectedTarget ? (
            <TargetDetailPane invocationId={invocationId} target={selectedTarget} status={classify(selectedTarget)} />
          ) : (
            <p className="muted">Select a target to view details.</p>
          )}
        </div>
      </div>
    </div>
  );
}
