// Typed client for bepper's invocation-viewer JSON API (src/webui/webui.go).
// Original code — bepper's API has no BuildBuddy equivalent to port from.

// authedFetch wraps fetch() for API calls: if the session has expired
// server-side (e.g. a long-lived tab), the server returns 401 instead of
// the HTML login redirect it'd send for a page load, so this sends the
// browser to /auth/login itself rather than letting callers try to parse
// JSON out of a 401 body.
async function authedFetch(url: string): Promise<Response> {
  const resp = await fetch(url);
  if (resp.status === 401) {
    const returnPath = window.location.pathname + window.location.search;
    window.location.href = `/auth/login?return=${encodeURIComponent(returnPath)}`;
    return new Promise(() => {});
  }
  return resp;
}

export interface CurrentUser {
  email: string;
  name?: string;
}

// getCurrentUser returns the logged-in user, or null if SSO isn't
// configured on this server or the request otherwise fails. Uses a plain
// fetch, not authedFetch — a 401 here just means "not logged in yet",
// which is expected on the login page itself and shouldn't redirect.
export async function getCurrentUser(): Promise<CurrentUser | null> {
  try {
    const resp = await fetch("/auth/me");
    if (!resp.ok) return null;
    return await resp.json();
  } catch {
    return null;
  }
}

export interface Invocation {
  id: string;
  command: string;
  pattern: string[];
  tags: string[];
  user: string;
  host: string;
  role: string;
  repoUrl: string;
  branchName: string;
  commitSha: string;
  parentRunId: string;
  runId: string;
  success: boolean;
  bazelExitCode: string;
  durationUsec: number;
  actionCount: number;
  createdAt: string;
  updatedAt: string;
}

export interface LogFile {
  name: string;
  uri: string;
}

// TestAttempt is one TestResult event for a test target — a single run.
// A normal single-run test has one attempt; --runs_per_test/flaky-retries
// report multiple, each independently timed and statused.
export interface TestAttempt {
  status: string;
  startTime: string;
  durationUsec: number;
  cachedLocally: boolean;
}

export interface Target {
  label: string;
  kind: string;
  isTest: boolean;
  success: boolean;
  testStatus: string;
  durationUsec: number;
  failureMessage: string;
  // File references (test.log, test.xml, ...) reported for this target's
  // test actions. uri is almost always a file:// path local to whatever
  // machine ran Bazel, not something this server fetched or can serve —
  // see the comment on index.Target.LogFiles in the Go code.
  logFiles: LogFile[];
  attempts: TestAttempt[];
  // The target's actual build outputs (binary/library/generated files),
  // for any target — not just tests. Same fetchability caveats as
  // logFiles (uri is usually a local file:// path).
  outputFiles: LogFile[];
}

export interface Flag {
  combinedForm: string;
  source: string;
}

// BuildInfo is platform/execution-strategy summary data — see
// index.BuildInfo in the Go code for how each field is derived.
export interface BuildInfo {
  cpu: string;
  remoteExecutionEnabled: boolean;
  cachingEnabled: boolean;
  packagesLoaded: number;
  fetchCount: number;
}

export interface InvocationDetails {
  targets: Target[];
  flags: Flag[];
  buildError: string;
  buildInfo: BuildInfo;
}

export const PAGE_SIZE = 50;

export interface ListFilter {
  limit?: number;
  q?: string;
  status?: "" | "success" | "failure";
  // Before, if set, requests the page of invocations created strictly
  // before this timestamp (an Invocation.createdAt value from a previous
  // page) — keyset pagination, see index.ListOptions.Before in the Go code.
  before?: string;
  // Since/until, if set, restrict results to invocations created in
  // [since, until) — ISO timestamps, user-facing time-range filter.
  since?: string;
  until?: string;
}

async function getJSON<T>(url: string): Promise<T> {
  const resp = await authedFetch(url);
  if (!resp.ok) {
    throw new Error(`${url}: HTTP ${resp.status}`);
  }
  return resp.json();
}

export function listInvocations(filter: ListFilter = {}): Promise<Invocation[]> {
  const params = new URLSearchParams();
  params.set("limit", String(filter.limit ?? PAGE_SIZE));
  if (filter.q) params.set("q", filter.q);
  if (filter.status) params.set("status", filter.status);
  if (filter.before) params.set("before", filter.before);
  if (filter.since) params.set("since", filter.since);
  if (filter.until) params.set("until", filter.until);
  return getJSON(`/api/invocations?${params}`);
}

export function getInvocation(id: string): Promise<Invocation | null> {
  return getJSON<Invocation>(`/api/invocations/${encodeURIComponent(id)}`).catch((err): Invocation | null => {
    if (String(err).includes("404")) return null;
    throw err;
  });
}

export async function getInvocationLog(id: string): Promise<string> {
  const resp = await authedFetch(`/api/invocations/${encodeURIComponent(id)}/log`);
  if (!resp.ok) return "";
  return resp.text();
}

const emptyBuildInfo: BuildInfo = { cpu: "", remoteExecutionEnabled: false, cachingEnabled: false, packagesLoaded: 0, fetchCount: 0 };
const emptyDetails: InvocationDetails = { targets: [], flags: [], buildError: "", buildInfo: emptyBuildInfo };

export async function getInvocationDetails(id: string): Promise<InvocationDetails> {
  const resp = await authedFetch(`/api/invocations/${encodeURIComponent(id)}/details`);
  if (!resp.ok) return emptyDetails;
  return resp.json();
}

// getInvocationRaw fetches the invocation's raw stored BuildEvent stream —
// the actual proto-shaped JSON (see webui.go's getRaw), pretty-printed
// here rather than server-side since that's cheap and keeps the response
// itself compact over the wire.
export async function getInvocationRaw(id: string): Promise<string> {
  const resp = await authedFetch(`/api/invocations/${encodeURIComponent(id)}/raw`);
  if (!resp.ok) return "";
  const data = await resp.json();
  return JSON.stringify(data, null, 2);
}

// blobURL builds the URL for a bytestream:// file's contents — whatever
// remote cache Bazel uploaded it to, see src/bytestream/. The server only
// allows uris it already reported for this invocation (see fileForURI in
// webui.go), so this can't be used to probe arbitrary hosts. Exported so
// callers can use it as a real <a href> download/open link, not just via
// getInvocationBlob's fetch-and-render below — the server now streams the
// response directly (see getBlob in webui.go), so a plain browser
// navigation to this URL works for arbitrarily large files without
// needing JS to buffer anything client-side either.
export function blobURL(id: string, uri: string): string {
  return `/api/invocations/${encodeURIComponent(id)}/blob?uri=${encodeURIComponent(uri)}`;
}

export async function getInvocationBlob(id: string, uri: string): Promise<string> {
  const resp = await authedFetch(blobURL(id, uri));
  if (!resp.ok) {
    throw new Error((await resp.text().catch(() => "")) || `HTTP ${resp.status}`);
  }
  return resp.text();
}
