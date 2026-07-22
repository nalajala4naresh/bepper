// Original code — simple pathname-based switch between the two pages.
// BuildBuddy's app/router is a much bigger singleton wired to their whole
// app (auth redirects, many routes); not worth pulling in for two pages —
// bepper's own /auth/* handlers on the server own all auth redirects
// instead, so the frontend only needs to render who's logged in.
import React, { useEffect, useState } from "react";
import { getCurrentUser, type CurrentUser } from "./api";
import { InvocationDetail } from "./invocation_detail";
import { InvocationList } from "./invocation_list";

export function App() {
  const path = window.location.pathname;
  const [user, setUser] = useState<CurrentUser | null>(null);

  useEffect(() => {
    getCurrentUser().then(setUser);
  }, []);

  return (
    <>
      <header className="topbar">
        <a className="logo" href="/">
          bepper
        </a>
        {user && (
          <div className="auth-status">
            <span>{user.email}</span>
            <a href="/auth/logout">Log out</a>
          </div>
        )}
      </header>
      <main>{path.startsWith("/invocation") ? <DetailRoute /> : <InvocationList />}</main>
    </>
  );
}

// DetailRoute accepts the invocation ID as either a /invocation/<id> path
// segment (bepper's own links, and what Bazel's --bes_results_url produces
// — it always appends the invocation ID as a path segment, not a query
// value, even if the flag's value ends in something like "?id=") or a
// legacy /invocation?id=<id> query param, for old bookmarked links.
function DetailRoute() {
  const path = window.location.pathname;
  const pathID = path.startsWith("/invocation/") ? decodeURIComponent(path.slice("/invocation/".length)) : "";
  const id = pathID || new URLSearchParams(window.location.search).get("id");
  if (!id) {
    return <p className="empty-state">Missing invocation ID.</p>;
  }
  return <InvocationDetail id={id} />;
}
