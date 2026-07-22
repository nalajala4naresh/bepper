// Original code (small enough that porting BuildBuddy's equivalent, which is
// entangled with their Invocation proto's status enum, wasn't worthwhile).
import React from "react";

export function StatusPill({ success, exitCode }: { success: boolean; exitCode?: string }) {
  return (
    <span className={`status-pill ${success ? "success" : "failure"}`}>
      <span className="dot" />
      {success ? "Success" : exitCode || "Failure"}
    </span>
  );
}
