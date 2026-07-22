// Original code — a small debounced search + status + time-range filter, in
// the spirit of BuildBuddy's invocation_filter.tsx but reimplemented against
// bepper's single query/status/range filter set rather than their per-tab
// filter set (artifactFilter/targetFilter/executionFilter/...), which
// doesn't apply here.
import { Search } from "lucide-react";
import React from "react";
import { ListFilter } from "./api";

const DEBOUNCE_MS = 200;

// toISO converts a <input type="datetime-local"> value (local time, no
// timezone, e.g. "2026-07-19T09:30") to an ISO string for the API. Empty
// input yields undefined rather than an invalid-date string.
function toISO(localDateTime: string): string | undefined {
  if (!localDateTime) return undefined;
  const d = new Date(localDateTime);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

export function InvocationFilterBar({ onChange }: { onChange: (filter: ListFilter) => void }) {
  const [q, setQ] = React.useState("");
  const [status, setStatus] = React.useState<ListFilter["status"]>("");
  const [since, setSince] = React.useState("");
  const [until, setUntil] = React.useState("");
  const debounceRef = React.useRef<number | undefined>(undefined);

  React.useEffect(() => {
    window.clearTimeout(debounceRef.current);
    debounceRef.current = window.setTimeout(
      () => onChange({ q, status, since: toISO(since), until: toISO(until) }),
      DEBOUNCE_MS
    );
    return () => window.clearTimeout(debounceRef.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, status, since, until]);

  return (
    <div className="filter-bar">
      <div className="filter-input">
        <Search size={16} />
        <input
          type="text"
          placeholder="Filter by command, pattern, repo, branch, commit, or user…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
      </div>
      <select value={status} onChange={(e) => setStatus(e.target.value as ListFilter["status"])}>
        <option value="">All statuses</option>
        <option value="success">Success</option>
        <option value="failure">Failure</option>
      </select>
      <input
        type="datetime-local"
        className="filter-date"
        aria-label="Created after"
        title="Created after"
        value={since}
        onChange={(e) => setSince(e.target.value)}
      />
      <span className="filter-date-sep muted">–</span>
      <input
        type="datetime-local"
        className="filter-date"
        aria-label="Created before"
        title="Created before"
        value={until}
        onChange={(e) => setUntil(e.target.value)}
      />
    </div>
  );
}
