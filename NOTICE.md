This project incorporates code from other open source projects.

## bazelbuild/bazel (Apache License 2.0)

`proto/bazel/` vendors `build_event_stream.proto` and its required
dependencies from https://github.com/bazelbuild/bazel, licensed under the
Apache License 2.0.

## buildbuddy-io/buildbuddy (MIT)

The following are adapted from https://github.com/buildbuddy-io/buildbuddy,
licensed under the MIT ("MIT Expat") license for all content outside its
`enterprise/` directory:

- `src/blobstore/` — the `Blobstore` interface and disk/S3 backends, adapted
  from `server/interfaces.Blobstore` and `server/backends/blobstore/`.
- `src/store/` and the ack-ordering logic in `src/server/server.go` — the
  per-invocation streaming-write and sequence-number consistency check,
  adapted from `server/build_event_protocol/build_event_handler` and
  `server/build_event_protocol/build_event_server`.
- `src/index/parser.go` — the event-to-summary field extraction rules
  (including the multi-CI-provider environment variable detection), adapted
  from `server/build_event_protocol/event_parser`.
- `src/index/giturl.go` — repo URL normalization, adapted from
  `server/util/git`.
- `src/index/tags.go` — tag list parsing, adapted from
  `server/build_event_protocol/invocation_format`.
- `src/index/postgres/` — the invocation summary schema and upsert pattern,
  loosely modeled on the `Invocation` row `server/build_event_protocol`
  writes on finalize (reimplemented with a much smaller field set and plain
  `database/sql` instead of GORM, since this project has no multi-tenancy,
  auth, or remote-cache/execution features to carry fields for).
- `src/webui/frontend/src/terminal/`, `src/webui/frontend/src/util/`
  (`math.ts`, `scroller.ts`, `clipboard.ts`, `animated_value.ts`,
  `animation_loop.ts`, `time_delta.ts`), and
  `src/webui/frontend/src/components/{input,spinner}.{tsx,css}` — the
  virtualized, ANSI-aware console log viewer, ported near-verbatim from
  `app/terminal/`, the same-named files under `app/util/`, and
  `app/components/{input,spinner}/`. `src/webui/frontend/src/router.ts` and
  `src/webui/frontend/src/capabilities.ts` are minimal local stand-ins for
  `app/router/router.ts` and `app/capabilities/capabilities.ts` (the only
  two methods/fields the ported code reads from them), since this project
  has no router or capabilities system of its own.

Each adapted file carries a header comment linking to its source. The rest
of `src/webui/` (the JSON API, the invocation list/detail pages, and their
styling) is original code written against bepper's own data model — it isn't
adapted from BuildBuddy's `app/invocation/`, which is tied to their
`Invocation` protobuf and `BuildBuddyService` RPC API.
