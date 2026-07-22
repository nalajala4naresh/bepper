// Original code for the metadata header/grid/tabs (tied to bepper's own
// index.Record/Target fields, which have no BuildBuddy Invocation-proto
// equivalent). The console log panel embeds the ported TerminalComponent,
// which is the actual "events viewer" widget; InvocationTargets and the
// flags list are original, reading bepper's own /details endpoint.
import { AlertTriangle } from "lucide-react";
import React from "react";
import { getInvocation, getInvocationDetails, getInvocationLog, getInvocationRaw, Invocation, InvocationDetails } from "./api";
import { formatDuration, formatTime } from "./format";
import { InvocationTargets } from "./invocation_targets";
import { StatusPill } from "./status_pill";
import TerminalComponent from "./terminal/terminal";

function InfoCell({ label, value }: { label: string; value?: string | number }) {
  return (
    <div className="info-cell">
      <span className="label">{label}</span>
      <span className="value">{value || "—"}</span>
    </div>
  );
}

type Tab = "overview" | "targets" | "log" | "config" | "raw";

export function InvocationDetail({ id }: { id: string }) {
  const [inv, setInv] = React.useState<Invocation | null | undefined>(undefined);
  const [error, setError] = React.useState<string | null>(null);
  const [log, setLog] = React.useState<string | undefined>(undefined);
  const [details, setDetails] = React.useState<InvocationDetails | undefined>(undefined);
  const [raw, setRaw] = React.useState<string | undefined>(undefined);
  const [tab, setTab] = React.useState<Tab>("overview");

  React.useEffect(() => {
    getInvocation(id)
      .then(setInv)
      .catch((err) => setError(String(err)));
    getInvocationLog(id).then(setLog);
    getInvocationDetails(id).then(setDetails);
    setRaw(undefined);
  }, [id]);

  // Raw events can be large, so this is only fetched once the Raw tab is
  // actually opened, not eagerly alongside the other tabs' data above.
  React.useEffect(() => {
    if (tab === "raw" && raw === undefined) {
      getInvocationRaw(id).then(setRaw);
    }
  }, [tab, id, raw]);

  if (error) {
    return <p className="empty-state">Failed to load invocation: {error}</p>;
  }
  if (inv === undefined) {
    return <p className="muted">Loading invocation…</p>;
  }
  if (inv === null) {
    return (
      <p className="empty-state">
        No invocation found for ID <code>{id}</code>.
      </p>
    );
  }

  const targetCount = details?.targets.length ?? 0;

  return (
    <>
      <div className="detail-header">
        <StatusPill success={inv.success} exitCode={inv.bazelExitCode} />
        <h1>{inv.id}</h1>
      </div>

      {details?.buildError && (
        <div className="error-banner">
          <AlertTriangle size={16} />
          <span>{details.buildError}</span>
        </div>
      )}

      <div className="tab-bar">
        <button className={tab === "overview" ? "active" : ""} onClick={() => setTab("overview")}>
          Overview
        </button>
        <button className={tab === "targets" ? "active" : ""} onClick={() => setTab("targets")}>
          Targets{details ? ` (${targetCount})` : ""}
        </button>
        <button className={tab === "log" ? "active" : ""} onClick={() => setTab("log")}>
          Console
        </button>
        <button className={tab === "config" ? "active" : ""} onClick={() => setTab("config")}>
          Config
        </button>
        <button className={tab === "raw" ? "active" : ""} onClick={() => setTab("raw")}>
          Raw
        </button>
      </div>

      {tab === "overview" && (
        <>
          <div className="info-grid">
            <InfoCell label="Command" value={inv.command ? `bazel ${inv.command}` : undefined} />
            <InfoCell label="Pattern" value={inv.pattern?.join(" ")} />
            <InfoCell label="Duration" value={formatDuration(inv.durationUsec)} />
            <InfoCell label="Actions executed" value={inv.actionCount} />
            <InfoCell label="User" value={inv.user} />
            <InfoCell label="Host" value={inv.host} />
            <InfoCell label="Role" value={inv.role} />
            <InfoCell label="Repo" value={inv.repoUrl} />
            <InfoCell label="Branch" value={inv.branchName} />
            <InfoCell label="Commit" value={inv.commitSha} />
            <InfoCell label="Started" value={formatTime(inv.createdAt)} />
            <InfoCell label="Updated" value={formatTime(inv.updatedAt)} />
            <InfoCell label="CPU" value={details?.buildInfo.cpu} />
            <InfoCell label="Remote execution" value={details ? (details.buildInfo.remoteExecutionEnabled ? "Enabled" : "Disabled") : undefined} />
            <InfoCell label="Caching" value={details ? (details.buildInfo.cachingEnabled ? "Enabled" : "Disabled") : undefined} />
            <InfoCell label="Packages loaded" value={details?.buildInfo.packagesLoaded} />
            <InfoCell label="Fetches" value={details?.buildInfo.fetchCount} />
          </div>

          {inv.tags?.length ? (
            <div style={{ marginBottom: "1.5rem" }}>
              {inv.tags.map((tag) => (
                <span className="tag" key={tag}>
                  {tag}
                </span>
              ))}
            </div>
          ) : null}
        </>
      )}

      {tab === "targets" &&
        (details ? (
          <InvocationTargets invocationId={id} targets={details.targets} />
        ) : (
          <p className="muted">Loading targets…</p>
        ))}

      {tab === "log" && (
        <div className="log-panel">
          <div className="terminal-wrapper">
            <TerminalComponent
              value={log ?? ""}
              loading={log === undefined}
              title={<div className="title">Console output</div>}
              lightTheme={false}
              bottomControls
              debugId="build-logs"
            />
          </div>
        </div>
      )}

      {tab === "config" &&
        (details ? (
          details.flags.length ? (
            <table className="invocations">
              <thead>
                <tr>
                  <th>Flag</th>
                  <th>Source</th>
                </tr>
              </thead>
              <tbody>
                {details.flags.map((f, i) => (
                  <tr key={i}>
                    <td className="mono">{f.combinedForm}</td>
                    <td className="muted">{f.source || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p className="muted">No non-default flags were reported for this invocation.</p>
          )
        ) : (
          <p className="muted">Loading details…</p>
        ))}

      {tab === "raw" &&
        (raw !== undefined ? (
          raw ? (
            <pre className="raw-events">{raw}</pre>
          ) : (
            <p className="muted">No events were stored for this invocation.</p>
          )
        ) : (
          <p className="muted">Loading raw events…</p>
        ))}
    </>
  );
}
