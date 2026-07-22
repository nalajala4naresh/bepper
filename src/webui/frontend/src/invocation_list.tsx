// Original code — new list view calling bepper's own API (BuildBuddy's
// invocation_card.tsx is wired to their Invocation proto and dashboard
// filter/search state, neither of which exists here).
import React from "react";
import { Invocation, ListFilter, listInvocations, PAGE_SIZE } from "./api";
import { formatDuration, formatTime } from "./format";
import { InvocationFilterBar } from "./invocation_filter";
import { StatusPill } from "./status_pill";

export function InvocationList() {
  const [invocations, setInvocations] = React.useState<Invocation[] | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [filter, setFilter] = React.useState<ListFilter>({});
  const [hasMore, setHasMore] = React.useState(false);
  const [loadingMore, setLoadingMore] = React.useState(false);

  // Reset and load the first page whenever the filter changes.
  React.useEffect(() => {
    setInvocations(null);
    listInvocations(filter)
      .then((page) => {
        setInvocations(page);
        setHasMore(page.length === (filter.limit ?? PAGE_SIZE));
      })
      .catch((err) => setError(String(err)));
  }, [filter]);

  async function loadMore() {
    if (!invocations || invocations.length === 0) return;
    setLoadingMore(true);
    try {
      const before = invocations[invocations.length - 1].createdAt;
      const page = await listInvocations({ ...filter, before });
      setInvocations([...invocations, ...page]);
      setHasMore(page.length === (filter.limit ?? PAGE_SIZE));
    } catch (err) {
      setError(String(err));
    } finally {
      setLoadingMore(false);
    }
  }

  return (
    <>
      <InvocationFilterBar onChange={setFilter} />
      <InvocationTable
        invocations={invocations}
        error={error}
        filtered={!!(filter.q || filter.status || filter.since || filter.until)}
      />
      {invocations && invocations.length > 0 && hasMore && (
        <div className="load-more">
          <button onClick={loadMore} disabled={loadingMore}>
            {loadingMore ? "Loading…" : "Load more"}
          </button>
        </div>
      )}
    </>
  );
}

function InvocationTable({
  invocations,
  error,
  filtered,
}: {
  invocations: Invocation[] | null;
  error: string | null;
  filtered: boolean;
}) {
  if (error) {
    return <p className="empty-state">Failed to load invocations: {error}</p>;
  }
  if (invocations === null) {
    return <p className="muted">Loading invocations…</p>;
  }
  if (invocations.length === 0) {
    return (
      <p className="empty-state">
        {filtered ? (
          "No invocations match this filter."
        ) : (
          <>
            No invocations yet. Point <code>--bes_backend</code> at this server and run a build.
          </>
        )}
      </p>
    );
  }

  return (
    <table className="invocations">
      <thead>
        <tr>
          <th>Status</th>
          <th>Target / command</th>
          <th>User</th>
          <th>Branch</th>
          <th>Duration</th>
          <th>Started</th>
        </tr>
      </thead>
      <tbody>
        {invocations.map((inv) => (
          <tr key={inv.id}>
            <td>
              <StatusPill success={inv.success} exitCode={inv.bazelExitCode} />
            </td>
            <td>
              <a className="mono" href={`/invocation/${encodeURIComponent(inv.id)}`}>
                {inv.pattern?.length ? inv.pattern.join(" ") : inv.id}
              </a>
              {inv.command && <div className="muted mono">bazel {inv.command}</div>}
            </td>
            <td>{[inv.user, inv.host].filter(Boolean).join("@") || "—"}</td>
            <td className="mono">{inv.branchName || (inv.commitSha ? inv.commitSha.slice(0, 8) : "—")}</td>
            <td>{formatDuration(inv.durationUsec)}</td>
            <td className="muted">{formatTime(inv.createdAt)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
