// Package webui serves an invocation-viewer UI (invocation list + detail
// pages, backed by a small JSON API) over plain HTTP.
//
// The frontend (frontend/src) is a React/TypeScript app built with Vite.
// Its console-log viewer (frontend/src/terminal/) is ported near-verbatim
// from BuildBuddy's app/terminal (MIT licensed) — see the header comment on
// each file under frontend/src/terminal and frontend/src/util for exact
// sources. The invocation list/detail chrome around it is original code,
// since it's wired to bepper's own API rather than BuildBuddy's Invocation
// proto and BuildBuddyService.
//
// `static/dist` is Vite's build output (run `npm run build` in frontend/
// after changing frontend source), embedded here and served as-is.
package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	buildeventstream "github.com/nalajala4naresh/bepper/proto/gen/build_event_stream"
	"github.com/nalajala4naresh/bepper/src/bytestream"
	"github.com/nalajala4naresh/bepper/src/index"
	"github.com/nalajala4naresh/bepper/src/store"

	"google.golang.org/protobuf/encoding/protojson"
)

//go:embed static/dist
var staticFS embed.FS

const defaultListLimit = 50

// New returns an http.Handler serving the invocation-viewer UI and its JSON
// API, backed by idx for summaries and s for full event/console logs. blob
// fetches bytestream:// log files referenced by an invocation's targets
// (see src/bytestream); pass nil to disable that endpoint.
func New(idx index.Indexer, s *store.Store, blob *bytestream.Client) http.Handler {
	h := &handler{idx: idx, store: s, blob: blob}

	dist, err := fs.Sub(staticFS, "static/dist")
	if err != nil {
		// static/dist always exists once `npm run build` has run; see the
		// package doc comment.
		panic(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/invocations", h.listInvocations)
	mux.HandleFunc("GET /api/invocations/{id}", h.getInvocation)
	mux.HandleFunc("GET /api/invocations/{id}/log", h.getLog)
	mux.HandleFunc("GET /api/invocations/{id}/details", h.getDetails)
	mux.HandleFunc("GET /api/invocations/{id}/blob", h.getBlob)
	mux.HandleFunc("GET /api/invocations/{id}/raw", h.getRaw)
	// The SPA shell is served for all of these; frontend/src/app.tsx
	// switches between the list and detail views client-side based on the
	// pathname. /invocation/{id} is the canonical detail URL — it's also
	// what Bazel's --bes_results_url produces, since Bazel always appends
	// the invocation ID as a path segment rather than doing plain string
	// concatenation (so a "?id=" suffix on the flag value doesn't survive
	// intact). /invocation (bare, ?id= as a query param) is kept for old
	// bookmarked links.
	mux.HandleFunc("GET /{$}", spaShell(dist))
	mux.HandleFunc("GET /invocation", spaShell(dist))
	mux.HandleFunc("GET /invocation/{id}", spaShell(dist))
	mux.Handle("GET /assets/", http.FileServerFS(dist))
	mux.Handle("GET /image/", http.FileServerFS(dist))
	return mux
}

type handler struct {
	idx   index.Indexer
	store *store.Store
	blob  *bytestream.Client
}

// invocationDTO is the JSON shape of an index.Record served to the UI.
type invocationDTO struct {
	ID            string   `json:"id"`
	Command       string   `json:"command"`
	Pattern       []string `json:"pattern"`
	Tags          []string `json:"tags"`
	User          string   `json:"user"`
	Host          string   `json:"host"`
	Role          string   `json:"role"`
	RepoURL       string   `json:"repoUrl"`
	BranchName    string   `json:"branchName"`
	CommitSHA     string   `json:"commitSha"`
	ParentRunID   string   `json:"parentRunId"`
	RunID         string   `json:"runId"`
	Success       bool     `json:"success"`
	BazelExitCode string   `json:"bazelExitCode"`
	DurationUsec  int64    `json:"durationUsec"`
	ActionCount   int64    `json:"actionCount"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
}

func toDTO(rec *index.Record) invocationDTO {
	return invocationDTO{
		ID:            rec.InvocationID,
		Command:       rec.Command,
		Pattern:       rec.Pattern,
		Tags:          rec.Tags,
		User:          rec.User,
		Host:          rec.Host,
		Role:          rec.Role,
		RepoURL:       rec.RepoURL,
		BranchName:    rec.BranchName,
		CommitSHA:     rec.CommitSHA,
		ParentRunID:   rec.ParentRunID,
		RunID:         rec.RunID,
		Success:       rec.Success,
		BazelExitCode: rec.BazelExitCode,
		DurationUsec:  rec.DurationUsec,
		ActionCount:   rec.ActionCount,
		// Nanosecond precision (not just time.RFC3339) so CreatedAt can
		// round-trip as an exact pagination cursor via ?before=.
		CreatedAt: rec.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: rec.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func (h *handler) listInvocations(w http.ResponseWriter, r *http.Request) {
	opts := index.ListOptions{
		Limit:  defaultListLimit,
		Query:  r.URL.Query().Get("q"),
		Status: r.URL.Query().Get("status"),
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.Limit = n
		}
	}
	opts.Before = parseTimeParam(r, "before")
	opts.Since = parseTimeParam(r, "since")
	opts.Until = parseTimeParam(r, "until")

	recs, err := h.idx.List(r.Context(), opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	dtos := make([]invocationDTO, len(recs))
	for i, rec := range recs {
		dtos[i] = toDTO(rec)
	}
	writeJSON(w, dtos)
}

func (h *handler) getInvocation(w http.ResponseWriter, r *http.Request) {
	rec, err := h.idx.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rec == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, toDTO(rec))
}

// getLog reconstructs the invocation's console output by concatenating the
// stdout/stderr of every Progress event, in the order they were received —
// the same event field BuildBuddy's console log view is built from.
func (h *handler) getLog(w http.ResponseWriter, r *http.Request) {
	events, found, err := h.store.ReadEvents(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	var sb strings.Builder
	for _, event := range events {
		progress := event.GetProgress()
		sb.WriteString(progress.GetStdout())
		sb.WriteString(progress.GetStderr())
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(sb.String()))
}

// targetDTO is the JSON shape of an index.Target served to the UI.
type targetDTO struct {
	Label          string       `json:"label"`
	Kind           string       `json:"kind"`
	IsTest         bool         `json:"isTest"`
	Success        bool         `json:"success"`
	TestStatus     string       `json:"testStatus"`
	DurationUsec   int64        `json:"durationUsec"`
	FailureMessage string       `json:"failureMessage"`
	LogFiles       []logFile    `json:"logFiles"`
	Attempts       []attemptDTO `json:"attempts"`
	OutputFiles    []logFile    `json:"outputFiles"`
}

type logFile struct {
	Name string `json:"name"`
	URI  string `json:"uri"`
}

// attemptDTO is the JSON shape of an index.TestAttempt — one row in a test
// target's per-run history (see index.Target.Attempts).
type attemptDTO struct {
	Status        string `json:"status"`
	StartTime     string `json:"startTime"`
	DurationUsec  int64  `json:"durationUsec"`
	CachedLocally bool   `json:"cachedLocally"`
}

type flagDTO struct {
	CombinedForm string `json:"combinedForm"`
	Source       string `json:"source"`
}

// buildInfoDTO is the JSON shape of an index.BuildInfo.
type buildInfoDTO struct {
	CPU                    string `json:"cpu"`
	RemoteExecutionEnabled bool   `json:"remoteExecutionEnabled"`
	CachingEnabled         bool   `json:"cachingEnabled"`
	PackagesLoaded         int64  `json:"packagesLoaded"`
	FetchCount             int    `json:"fetchCount"`
}

type detailDTO struct {
	Targets    []targetDTO  `json:"targets"`
	Flags      []flagDTO    `json:"flags"`
	BuildError string       `json:"buildError"`
	BuildInfo  buildInfoDTO `json:"buildInfo"`
}

// getDetails returns the per-target results, effective command-line flags,
// and (if the build failed) a top-level error message — all derived by
// parsing the invocation's stored event stream on read, rather than being
// precomputed and persisted like the Record summary is.
func (h *handler) getDetails(w http.ResponseWriter, r *http.Request) {
	events, found, err := h.store.ReadEvents(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	targets := index.ParseTargets(events)
	targetDTOs := make([]targetDTO, len(targets))
	for i, t := range targets {
		logFiles := make([]logFile, len(t.LogFiles))
		for j, f := range t.LogFiles {
			logFiles[j] = logFile{Name: f.Name, URI: f.URI}
		}
		attempts := make([]attemptDTO, len(t.Attempts))
		for j, a := range t.Attempts {
			attempts[j] = attemptDTO{
				Status:        a.Status,
				StartTime:     a.StartTime.Format(time.RFC3339Nano),
				DurationUsec:  a.DurationUsec,
				CachedLocally: a.CachedLocally,
			}
		}
		outputFiles := make([]logFile, len(t.OutputFiles))
		for j, f := range t.OutputFiles {
			outputFiles[j] = logFile{Name: f.Name, URI: f.URI}
		}
		targetDTOs[i] = targetDTO{
			Label:          t.Label,
			Kind:           t.Kind,
			IsTest:         t.IsTest,
			Success:        t.Success,
			TestStatus:     t.TestStatus,
			DurationUsec:   t.DurationUsec,
			FailureMessage: t.FailureMessage,
			LogFiles:       logFiles,
			Attempts:       attempts,
			OutputFiles:    outputFiles,
		}
	}

	flags := index.ParseFlags(events)
	flagDTOs := make([]flagDTO, len(flags))
	for i, f := range flags {
		flagDTOs[i] = flagDTO{CombinedForm: f.CombinedForm, Source: f.Source}
	}

	info := index.ParseBuildInfo(events)

	writeJSON(w, detailDTO{
		Targets:    targetDTOs,
		Flags:      flagDTOs,
		BuildError: index.ParseBuildError(events),
		BuildInfo: buildInfoDTO{
			CPU:                    info.CPU,
			RemoteExecutionEnabled: info.RemoteExecutionEnabled,
			CachingEnabled:         info.CachingEnabled,
			PackagesLoaded:         info.PackagesLoaded,
			FetchCount:             info.FetchCount,
		},
	})
}

// getRaw returns the invocation's raw stored BuildEvent stream as JSON —
// the actual proto-shaped messages (via protojson, matching how they're
// serialized in src/store/store.go), not bepper's own summary DTOs. A
// debugging escape hatch for anything the summary views don't surface.
func (h *handler) getRaw(w http.ResponseWriter, r *http.Request) {
	events, found, err := h.store.ReadEvents(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	raw := make([]json.RawMessage, len(events))
	for i, event := range events {
		b, err := protojson.Marshal(event)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raw[i] = b
	}
	writeJSON(w, raw)
}

// getBlob streams a bytestream:// file's contents from whatever remote
// cache Bazel uploaded it to (see src/bytestream), straight through to the
// response as it arrives — it never buffers the whole thing in this
// process's memory, so this works for large build output artifacts
// (compiled binaries, archives, ...) the same as it does for small test
// logs. It's also a real link a browser can navigate to directly (see
// setBlobHeaders), not just something the frontend has to fetch() and
// render itself.
//
// The requested uri must be one this invocation's own event stream
// actually reported — bepper won't dial arbitrary caller-supplied hosts,
// since that would turn this endpoint into an open SSRF proxy.
func (h *handler) getBlob(w http.ResponseWriter, r *http.Request) {
	if h.blob == nil {
		http.Error(w, "remote cache fetching is not configured", http.StatusNotImplemented)
		return
	}

	uri := r.URL.Query().Get("uri")
	if uri == "" {
		http.Error(w, "missing uri", http.StatusBadRequest)
		return
	}

	events, found, err := h.store.ReadEvents(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	name, ok := fileForURI(events, uri)
	if !ok {
		http.Error(w, "uri was not reported by this invocation", http.StatusForbidden)
		return
	}

	setBlobHeaders(w, name)
	tw := &trackingWriter{w: w}
	if err := h.blob.Stream(r.Context(), uri, tw); err != nil {
		log.Printf("webui: stream blob %q: %v", uri, err)
		// If Stream already wrote some bytes before failing, net/http
		// already committed a 200 and headers on the first Write — calling
		// http.Error now wouldn't send a clean error response, it would
		// silently append the error text onto the end of the
		// already-streamed bytes, corrupting them (http.Error always
		// writes its message to the body, whether or not headers were
		// already sent). So only send it when nothing's gone out yet;
		// otherwise the client just sees a truncated/incomplete response,
		// which for chunked transfer encoding is at least a signal
		// something went wrong rather than a file with error text stitched
		// onto the end of it.
		if !tw.written {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}
}

// trackingWriter wraps an io.Writer and records whether any bytes were
// successfully written through it — see getBlob's error handling above.
type trackingWriter struct {
	w       io.Writer
	written bool
}

func (t *trackingWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		t.written = true
	}
	return n, err
}

// textLikeExtensions are rendered inline as text/plain by setBlobHeaders
// rather than triggering a download. Deliberately a small explicit list
// rather than net/mime's TypeByExtension: that consults the OS's
// mime.types database, which doesn't exist in bepper's distroless
// container image, so it would silently return "" for everything in
// production. Everything not in this list — notably extensionless
// compiled binaries, the common case for OutputFiles — downloads as
// application/octet-stream instead of rendering binary garbage inline in
// a browser tab.
var textLikeExtensions = map[string]bool{
	".log": true, ".txt": true, ".xml": true, ".json": true,
	".md": true, ".yaml": true, ".yml": true, ".csv": true,
}

// setBlobHeaders sets Content-Type (and, for non-text files, a
// Content-Disposition prompting a download with the reported filename) on
// w based on name's extension. Must be called before the first Write to
// w, same as any other response header.
func setBlobHeaders(w http.ResponseWriter, name string) {
	if textLikeExtensions[strings.ToLower(filepath.Ext(name))] {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(name)))
}

// fileForURI returns the Name reported alongside uri among events'
// targets' LogFiles/OutputFiles, confirming uri was actually reported by
// this invocation in the same pass (see getBlob's SSRF-guard comment).
func fileForURI(events []*buildeventstream.BuildEvent, uri string) (name string, ok bool) {
	for _, t := range index.ParseTargets(events) {
		for _, f := range t.LogFiles {
			if f.URI == uri {
				return f.Name, true
			}
		}
		for _, f := range t.OutputFiles {
			if f.URI == uri {
				return f.Name, true
			}
		}
	}
	return "", false
}

// parseTimeParam parses query param name as RFC3339(Nano), returning nil if
// absent or unparseable.
func parseTimeParam(r *http.Request, name string) *time.Time {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return nil
	}
	return &t
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("webui: encode json: %v", err)
	}
}

func spaShell(dist fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}
}
