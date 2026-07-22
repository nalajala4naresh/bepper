package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/nalajala4naresh/bepper/src/auth"
	"github.com/nalajala4naresh/bepper/src/blobstore"
	"github.com/nalajala4naresh/bepper/src/blobstore/disk"
	"github.com/nalajala4naresh/bepper/src/blobstore/diskcache"
	"github.com/nalajala4naresh/bepper/src/blobstore/s3"
	"github.com/nalajala4naresh/bepper/src/bytestream"
	"github.com/nalajala4naresh/bepper/src/index"
	"github.com/nalajala4naresh/bepper/src/index/postgres"
	"github.com/nalajala4naresh/bepper/src/server"
	"github.com/nalajala4naresh/bepper/src/store"
	"github.com/nalajala4naresh/bepper/src/webui"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// blobCacheDir/defaultBlobCacheMaxBytes configure the local disk read
// cache newBlobstore puts in front of S3 (see src/blobstore/diskcache).
// Disk-backed blobstore doesn't get one: it's already local, so caching it
// would just be a second copy of the same file.
const (
	blobCacheDir             = "data/blobcache"
	defaultBlobCacheMaxBytes = 1 << 30 // 1 GiB
)

// newBlobstore picks an S3-backed store if BEP_S3_BUCKET is set (for HA,
// multi-instance deployments), otherwise falls back to local disk. The S3
// case is wrapped in a local disk cache by default — every invocation view
// otherwise re-fetches and re-decompresses the same immutable blob from S3
// on every click — disable with BEP_BLOB_CACHE_DISABLE=true, or resize it
// with BEP_BLOB_CACHE_MAX_BYTES (defaults to 1 GiB).
func newBlobstore(ctx context.Context) (blobstore.Blobstore, error) {
	bucket := os.Getenv("BEP_S3_BUCKET")
	if bucket == "" {
		return disk.New("data/events")
	}

	store, err := s3.New(ctx, s3.Config{
		Bucket:   bucket,
		Region:   os.Getenv("BEP_S3_REGION"),
		Endpoint: os.Getenv("BEP_S3_ENDPOINT"),
	})
	if err != nil {
		return nil, err
	}
	if os.Getenv("BEP_BLOB_CACHE_DISABLE") == "true" {
		return store, nil
	}

	maxBytes := int64(defaultBlobCacheMaxBytes)
	if v := os.Getenv("BEP_BLOB_CACHE_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid BEP_BLOB_CACHE_MAX_BYTES %q: %w", v, err)
		}
		maxBytes = n
	}
	return diskcache.New(store, blobCacheDir, maxBytes)
}

// defaultPostgresDSN is used when BEP_POSTGRES_DSN isn't set, pointing at a
// local Postgres instance. Invocation indexing is not optional, so unlike
// BEP_S3_BUCKET this has a default rather than disabling the feature.
const defaultPostgresDSN = "postgres://postgres:postgres@localhost:5432/bepper?sslmode=disable"

// newIndexer connects to Postgres via BEP_POSTGRES_DSN, falling back to
// defaultPostgresDSN if unset.
func newIndexer(ctx context.Context) (index.Indexer, error) {
	dsn := os.Getenv("BEP_POSTGRES_DSN")
	if dsn == "" {
		dsn = defaultPostgresDSN
		log.Printf("BEP_POSTGRES_DSN not set; defaulting to local Postgres at %s", dsn)
	}
	return postgres.New(ctx, dsn)
}

// defaultMaxRemoteCacheBlobBytes caps how much of a bytestream:// file (a
// test log, or now an output artifact — see src/index/targets.go's
// OutputFiles) the Targets tab will fetch into memory in one request, if
// BEP_REMOTE_CACHE_MAX_BLOB_BYTES isn't set. 20MB comfortably covers log
// files; output artifacts (compiled binaries, etc.) can be much larger, so
// this is overridable per-deployment rather than raised as a new blanket
// default that'd apply even to installs that never look at output files.
const defaultMaxRemoteCacheBlobBytes = 20 * 1024 * 1024

// newBytestreamClient configures fetching of bytestream:// log files
// referenced by build events. Bazel reports these when its build is
// configured with --remote_cache — a server independent of this one, whose
// host is encoded directly in each bytestream:// URI, so no endpoint
// config is needed here beyond auth. See src/bytestream for why.
func newBytestreamClient() (*bytestream.Client, error) {
	maxSizeBytes := int64(defaultMaxRemoteCacheBlobBytes)
	if v := os.Getenv("BEP_REMOTE_CACHE_MAX_BLOB_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid BEP_REMOTE_CACHE_MAX_BLOB_BYTES %q: %w", v, err)
		}
		maxSizeBytes = n
	}
	return &bytestream.Client{
		Insecure:     os.Getenv("BEP_REMOTE_CACHE_INSECURE") == "true",
		Header:       os.Getenv("BEP_REMOTE_CACHE_HEADER"),
		MaxSizeBytes: maxSizeBytes,
	}, nil
}

// newAuthenticator builds an OIDC authenticator for the web UI from
// BEP_OIDC_*/BEP_SESSION_SECRET, or returns nil if BEP_OIDC_ISSUER_URL and
// BEP_OIDC_CLIENT_ID are both unset — like newBlobstore's disk fallback,
// auth is an optional feature so the web UI serves unauthenticated in that
// case (e.g. for local dev). Unlike that fallback, a partially-set OIDC
// config is treated as a startup error rather than silently disabling
// auth, since that's far more likely to be a mistake than intentional.
//
// If BEP_REQUIRE_AUTH=true, an unconfigured OIDC setup is also a startup
// error instead of falling back to unauthenticated — this is the knob for
// deployments that never want the no-auth path to be reachable at all,
// e.g. so a forgotten env var in production can't silently expose the UI.
func newAuthenticator(ctx context.Context) (*auth.Authenticator, error) {
	cfg, err := auth.ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		if os.Getenv("BEP_REQUIRE_AUTH") == "true" {
			return nil, fmt.Errorf("BEP_REQUIRE_AUTH=true but BEP_OIDC_ISSUER_URL/BEP_OIDC_CLIENT_ID are not set")
		}
		log.Println("BEP_OIDC_ISSUER_URL not set; web UI auth is disabled")
		return nil, nil
	}
	return auth.New(ctx, *cfg)
}

func main() {
	ctx := context.Background()

	blobs, err := newBlobstore(ctx)
	if err != nil {
		log.Fatalf("failed to create blobstore: %v", err)
	}
	eventStore := store.New(blobs)

	idx, err := newIndexer(ctx)
	if err != nil {
		log.Fatalf("failed to create indexer: %v", err)
	}

	authn, err := newAuthenticator(ctx)
	if err != nil {
		log.Fatalf("failed to configure auth: %v", err)
	}

	lis, err := net.Listen("tcp", ":1985")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	server.NewBuildEventServer(eventStore, idx).Register(grpcServer)
	reflection.Register(grpcServer)

	bytestreamClient, err := newBytestreamClient()
	if err != nil {
		log.Fatalf("failed to configure remote cache client: %v", err)
	}
	var uiHandler http.Handler = webui.New(idx, eventStore, bytestreamClient)
	if authn != nil {
		uiHandler = authn.Wrap(uiHandler)
		log.Println("web UI auth enabled")
	}

	go func() {
		log.Println("invocation viewer UI listening on :8080")
		if err := http.ListenAndServe(":8080", uiHandler); err != nil {
			log.Fatalf("failed to serve UI: %v", err)
		}
	}()

	log.Println("BES server listening on :1985")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
