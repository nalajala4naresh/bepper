# bepper webui frontend

React/TypeScript SPA for the invocation viewer, built with Vite.

`src/terminal/` and parts of `src/util/`, `src/components/` are ported
near-verbatim from BuildBuddy's `app/terminal` (MIT licensed) — see
`NOTICE.md` at the repo root and each file's header comment. Everything else
here is original, written against bepper's own JSON API
(`src/webui/webui.go`).

## Building

```
npm install
npm run build
```

This writes to `../static/dist`, which `src/webui/webui.go` embeds via
`go:embed`. **Run this after any frontend change and commit the result** —
there's no CI step that does it for you, and `go build` embeds whatever is
in `static/dist` at the time.

## Dev server

`npm run dev` runs Vite's dev server with `/api` proxied to `localhost:8080`
(the Go server) — start the Go server separately first.
