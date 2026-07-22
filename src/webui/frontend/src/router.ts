// Minimal stand-in for BuildBuddy's app/router/router.ts. The ported
// app/terminal components only use two methods from it (deep-linking to a
// log line via the URL hash, "#log@123"), so that's all this provides —
// bepper has no other router-driven navigation yet.
const TAB = "log";

function parseHash(): { tab: string; line: number } | null {
  const hash = location.hash.replace(/^#/, "");
  const [tab, lineStr] = hash.split("@");
  const line = Number(lineStr);
  if (!tab || !Number.isFinite(line)) return null;
  return { tab, line };
}

export default {
  getTab(): string {
    return TAB;
  },
  getLineNumber(): number | undefined {
    const parsed = parseHash();
    return parsed && parsed.tab === TAB ? parsed.line : undefined;
  },
};
