// Parses Bazel's test.xml (standard JUnit XML) so invocation_targets.tsx
// can render individual test-case pass/fail/error results instead of just
// Bazel's one-line TestResult.failureMessage. Original code — no
// BuildBuddy equivalent to port from (their test.xml viewer reads from a
// server-side parsed proto, not raw XML in the browser).
//
// Uses the browser's native DOMParser rather than pulling in an XML
// parsing dependency for what's a small, well-defined format.

export type TestCaseStatus = "passed" | "failed" | "error" | "skipped";

export interface TestCase {
  name: string;
  className: string;
  time?: string;
  status: TestCaseStatus;
  message?: string;
  stackTrace?: string;
}

export interface TestSuite {
  name: string;
  tests: number;
  failures: number;
  errors: number;
  skipped: number;
  time?: string;
  testCases: TestCase[];
}

// parseJUnitXML returns null if xmlText isn't parseable JUnit XML (malformed,
// or simply not this format) — callers should fall back to a raw text view
// rather than showing nothing.
export function parseJUnitXML(xmlText: string): TestSuite[] | null {
  let doc: Document;
  try {
    doc = new DOMParser().parseFromString(xmlText, "text/xml");
  } catch {
    return null;
  }
  if (doc.querySelector("parsererror")) {
    return null;
  }

  // Matches both a bare root <testsuite> and a wrapping <testsuites>, since
  // querySelectorAll on a Document searches all descendants including the
  // document element itself.
  const suiteEls = Array.from(doc.querySelectorAll("testsuite"));
  if (suiteEls.length === 0) {
    return null;
  }

  return suiteEls.map((suiteEl) => {
    const testCaseEls = Array.from(suiteEl.querySelectorAll("testcase"));
    return {
      name: suiteEl.getAttribute("name") || "",
      tests: numAttr(suiteEl, "tests", testCaseEls.length),
      failures: numAttr(suiteEl, "failures", 0),
      errors: numAttr(suiteEl, "errors", 0),
      skipped: numAttr(suiteEl, "skipped", 0),
      time: suiteEl.getAttribute("time") || undefined,
      testCases: testCaseEls.map(parseTestCase),
    };
  });
}

function numAttr(el: Element, name: string, fallback: number): number {
  const v = el.getAttribute(name);
  if (v === null) return fallback;
  const n = Number(v);
  return Number.isFinite(n) ? n : fallback;
}

function parseTestCase(tc: Element): TestCase {
  // A testcase's direct-child failure/error/skipped elements (not ones
  // belonging to a nested element some frameworks emit) determine status;
  // querySelector on the testcase itself is scoped to its descendants,
  // which is fine here since testcase has no further nesting worth
  // distinguishing.
  const failureEl = tc.querySelector("failure");
  const errorEl = tc.querySelector("error");
  const skippedEl = tc.querySelector("skipped");

  let status: TestCaseStatus = "passed";
  let message: string | undefined;
  let stackTrace: string | undefined;

  if (failureEl) {
    status = "failed";
    message = failureEl.getAttribute("message") || undefined;
    stackTrace = failureEl.textContent?.trim() || undefined;
  } else if (errorEl) {
    status = "error";
    message = errorEl.getAttribute("message") || undefined;
    stackTrace = errorEl.textContent?.trim() || undefined;
  } else if (skippedEl) {
    status = "skipped";
  }

  return {
    name: tc.getAttribute("name") || "",
    className: tc.getAttribute("classname") || "",
    time: tc.getAttribute("time") || undefined,
    status,
    message,
    stackTrace,
  };
}
