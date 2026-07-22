// Original code — small formatting helpers, not worth pulling in
// BuildBuddy's full app/format/format.tsx (tied to their moment.js usage and
// broader unit set).

export function formatDuration(usec: number): string {
  if (!usec) return "—";
  const sec = usec / 1e6;
  if (sec < 60) return `${sec.toFixed(1)}s`;
  const min = Math.floor(sec / 60);
  const rem = Math.round(sec % 60);
  return `${min}m ${rem}s`;
}

export function formatTime(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}
