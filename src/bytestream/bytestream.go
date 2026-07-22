// Package bytestream fetches blobs referenced by bytestream:// URIs, as
// reported in a Bazel build event's File.uri field.
//
// Bazel only reports a fetchable bytestream:// reference for outputs it
// actually uploaded to a remote cache (configured via --remote_cache,
// independent of --bes_backend) — everything else stays a local file://
// path bepper can't reach. A bytestream:// URI is self-describing: its host
// is the remote-cache endpoint Bazel uploaded to, which is not necessarily
// bepper's own server. So rather than mirroring or owning a cache, this
// package dials whatever host each URI names and reads the blob from there
// directly, via the standard Remote Execution API ByteStream service
// (google.bytestream.ByteStream, the same protocol Bazel itself uses).
package bytestream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"

	"google.golang.org/genproto/googleapis/bytestream"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// ParseURI splits a bytestream:// URI into the gRPC dial target (host:port)
// and the resource name used in a ByteStream.Read request.
func ParseURI(uri string) (target, resourceName string, err error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("parse bytestream uri: %w", err)
	}
	if u.Scheme != "bytestream" {
		return "", "", fmt.Errorf("not a bytestream:// uri: %q", uri)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("bytestream uri missing host: %q", uri)
	}
	resourceName = strings.TrimPrefix(u.Path, "/")
	if resourceName == "" {
		return "", "", fmt.Errorf("bytestream uri missing resource name: %q", uri)
	}
	return u.Host, resourceName, nil
}

// Client fetches bytestream:// blobs. The zero value dials with TLS and no
// auth; configure Insecure/Header for remote caches that need otherwise.
type Client struct {
	// Insecure, if true, dials without TLS. Most hosted remote caches
	// require TLS; this is for plaintext local/dev caches (e.g.
	// bazel-remote run without --tls).
	Insecure bool

	// Header, if set, is sent as gRPC request metadata on every Read call,
	// in "key: value" form — e.g. "x-buildbuddy-api-key: XXXX". Most
	// remote caches authenticate this way rather than via mTLS.
	Header string

	// MaxSizeBytes caps how much of a blob is read into memory before
	// Fetch aborts. Zero means no limit.
	MaxSizeBytes int64

	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

// Fetch dials uri's host and reads the full blob it names into memory.
// This buffers the entire blob server-side before returning — fine for
// small/known-bounded blobs, but Stream is what the HTTP-serving path uses
// for anything a browser might request directly, so serving one large
// build output doesn't hold the whole thing in this process's memory at
// once.
func (c *Client) Fetch(ctx context.Context, uri string) ([]byte, error) {
	var buf bytes.Buffer
	if err := c.Stream(ctx, uri, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Stream dials uri's host (reusing a connection if we've already dialed
// that host) and writes the blob it names directly to w as each chunk
// arrives over the gRPC stream, rather than buffering it. MaxSizeBytes
// here is a bandwidth/abuse safety net rather than a memory constraint,
// since at most one chunk is ever held in memory at a time.
func (c *Client) Stream(ctx context.Context, uri string, w io.Writer) error {
	target, resourceName, err := ParseURI(uri)
	if err != nil {
		return err
	}

	conn, err := c.dial(target)
	if err != nil {
		return fmt.Errorf("dial %s: %w", target, err)
	}

	if c.Header != "" {
		if k, v, ok := strings.Cut(c.Header, ":"); ok {
			ctx = metadata.AppendToOutgoingContext(ctx, strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}

	stream, err := bytestream.NewByteStreamClient(conn).Read(ctx, &bytestream.ReadRequest{ResourceName: resourceName})
	if err != nil {
		return fmt.Errorf("read %s from %s: %w", resourceName, target, err)
	}

	var total int64
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("recv %s from %s: %w", resourceName, target, err)
		}
		data := resp.GetData()
		total += int64(len(data))
		if c.MaxSizeBytes > 0 && total > c.MaxSizeBytes {
			return fmt.Errorf("blob %s exceeds max size %d bytes", resourceName, c.MaxSizeBytes)
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write %s: %w", resourceName, err)
		}
	}
}

func (c *Client) dial(target string) (*grpc.ClientConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if conn, ok := c.conns[target]; ok {
		return conn, nil
	}

	var creds credentials.TransportCredentials
	if c.Insecure {
		creds = insecure.NewCredentials()
	} else {
		creds = credentials.NewTLS(nil)
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, err
	}

	if c.conns == nil {
		c.conns = make(map[string]*grpc.ClientConn)
	}
	c.conns[target] = conn
	return conn, nil
}
