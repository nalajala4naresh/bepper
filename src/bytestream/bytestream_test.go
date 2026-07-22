package bytestream

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"testing"

	bytestreampb "google.golang.org/genproto/googleapis/bytestream"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// fakeServer is a minimal in-process ByteStream.Read implementation, used
// to verify Client actually speaks the real wire protocol correctly —
// there's no live remote cache available to test against here.
type fakeServer struct {
	bytestreampb.UnimplementedByteStreamServer

	blobs      map[string][]byte
	chunkSize  int
	lastHeader metadata.MD
}

func (s *fakeServer) Read(req *bytestreampb.ReadRequest, stream bytestreampb.ByteStream_ReadServer) error {
	if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
		s.lastHeader = md
	}
	data, ok := s.blobs[req.GetResourceName()]
	if !ok {
		return fmt.Errorf("not found: %s", req.GetResourceName())
	}
	chunkSize := s.chunkSize
	if chunkSize <= 0 {
		chunkSize = len(data)
		if chunkSize == 0 {
			chunkSize = 1
		}
	}
	for i := 0; i < len(data); i += chunkSize {
		end := min(i+chunkSize, len(data))
		if err := stream.Send(&bytestreampb.ReadResponse{Data: data[i:end]}); err != nil {
			return err
		}
	}
	return nil
}

func startFakeServer(t *testing.T, srv *fakeServer) (addr string, stop func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	bytestreampb.RegisterByteStreamServer(grpcServer, srv)
	go grpcServer.Serve(lis)
	return lis.Addr().String(), grpcServer.Stop
}

func TestFetch_SingleChunk(t *testing.T) {
	want := []byte("PASS\nok  \tsrc/blobstore/disk\t0.300s\n")
	addr, stop := startFakeServer(t, &fakeServer{blobs: map[string][]byte{
		"blobs/abc123/37": want,
	}})
	defer stop()

	c := &Client{Insecure: true}
	got, err := c.Fetch(context.Background(), fmt.Sprintf("bytestream://%s/blobs/abc123/37", addr))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Fetch = %q, want %q", got, want)
	}
}

func TestFetch_MultiChunk(t *testing.T) {
	want := bytes.Repeat([]byte("x"), 10_000)
	addr, stop := startFakeServer(t, &fakeServer{
		blobs:     map[string][]byte{"blobs/big/10000": want},
		chunkSize: 777, // force many chunks, and a non-divisor of len(want)
	})
	defer stop()

	c := &Client{Insecure: true}
	got, err := c.Fetch(context.Background(), fmt.Sprintf("bytestream://%s/blobs/big/10000", addr))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Fetch returned %d bytes, want %d; equal=%v", len(got), len(want), bytes.Equal(got, want))
	}
}

func TestFetch_WithInstanceName(t *testing.T) {
	want := []byte("hello")
	addr, stop := startFakeServer(t, &fakeServer{blobs: map[string][]byte{
		"my-instance/blobs/deadbeef/5": want,
	}})
	defer stop()

	c := &Client{Insecure: true}
	got, err := c.Fetch(context.Background(), fmt.Sprintf("bytestream://%s/my-instance/blobs/deadbeef/5", addr))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Fetch = %q, want %q", got, want)
	}
}

func TestFetch_AuthHeaderSent(t *testing.T) {
	srv := &fakeServer{blobs: map[string][]byte{"blobs/x/1": []byte("a")}}
	addr, stop := startFakeServer(t, srv)
	defer stop()

	c := &Client{Insecure: true, Header: "x-buildbuddy-api-key: secret123"}
	if _, err := c.Fetch(context.Background(), fmt.Sprintf("bytestream://%s/blobs/x/1", addr)); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	got := srv.lastHeader.Get("x-buildbuddy-api-key")
	if len(got) != 1 || got[0] != "secret123" {
		t.Errorf("server saw header %v, want [secret123]", got)
	}
}

func TestFetch_MaxSizeExceeded(t *testing.T) {
	addr, stop := startFakeServer(t, &fakeServer{blobs: map[string][]byte{
		"blobs/x/100": bytes.Repeat([]byte("y"), 100),
	}})
	defer stop()

	c := &Client{Insecure: true, MaxSizeBytes: 10}
	_, err := c.Fetch(context.Background(), fmt.Sprintf("bytestream://%s/blobs/x/100", addr))
	if err == nil {
		t.Fatal("Fetch: want error, got nil")
	}
}

func TestFetch_NotFound(t *testing.T) {
	addr, stop := startFakeServer(t, &fakeServer{blobs: map[string][]byte{}})
	defer stop()

	c := &Client{Insecure: true}
	_, err := c.Fetch(context.Background(), fmt.Sprintf("bytestream://%s/blobs/missing/1", addr))
	if err == nil {
		t.Fatal("Fetch: want error, got nil")
	}
}

func TestFetch_ConnectionReuse(t *testing.T) {
	addr, stop := startFakeServer(t, &fakeServer{blobs: map[string][]byte{
		"blobs/a/1": []byte("a"),
		"blobs/b/1": []byte("b"),
	}})
	defer stop()

	c := &Client{Insecure: true}
	ctx := context.Background()
	if _, err := c.Fetch(ctx, fmt.Sprintf("bytestream://%s/blobs/a/1", addr)); err != nil {
		t.Fatalf("Fetch a: %v", err)
	}
	if _, err := c.Fetch(ctx, fmt.Sprintf("bytestream://%s/blobs/b/1", addr)); err != nil {
		t.Fatalf("Fetch b: %v", err)
	}
	c.mu.Lock()
	n := len(c.conns)
	c.mu.Unlock()
	if n != 1 {
		t.Errorf("dialed %d connections for the same host, want 1", n)
	}
}

func TestStream_MultiChunk(t *testing.T) {
	want := bytes.Repeat([]byte("z"), 10_000)
	addr, stop := startFakeServer(t, &fakeServer{
		blobs:     map[string][]byte{"blobs/big/10000": want},
		chunkSize: 777,
	})
	defer stop()

	c := &Client{Insecure: true}
	var buf bytes.Buffer
	if err := c.Stream(context.Background(), fmt.Sprintf("bytestream://%s/blobs/big/10000", addr), &buf); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("Stream wrote %d bytes, want %d; equal=%v", buf.Len(), len(want), bytes.Equal(buf.Bytes(), want))
	}
}

// TestStream_MaxSizeExceededPartialWrite verifies that Stream writes
// whatever chunks arrived before the size limit was hit, rather than
// buffering everything and discarding it on failure — that's the whole
// point of streaming instead of calling Fetch, so a partial write on error
// is expected/correct, not a bug.
func TestStream_MaxSizeExceededPartialWrite(t *testing.T) {
	addr, stop := startFakeServer(t, &fakeServer{
		blobs:     map[string][]byte{"blobs/x/100": bytes.Repeat([]byte("y"), 100)},
		chunkSize: 10,
	})
	defer stop()

	c := &Client{Insecure: true, MaxSizeBytes: 25}
	var buf bytes.Buffer
	err := c.Stream(context.Background(), fmt.Sprintf("bytestream://%s/blobs/x/100", addr), &buf)
	if err == nil {
		t.Fatal("Stream: want error, got nil")
	}
	if buf.Len() == 0 {
		t.Error("Stream: want some bytes written before the limit was hit, got none")
	}
	if buf.Len() >= 100 {
		t.Errorf("Stream: wrote all %d bytes despite a 25-byte limit", buf.Len())
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("boom") }

func TestStream_WriterErrorPropagates(t *testing.T) {
	addr, stop := startFakeServer(t, &fakeServer{blobs: map[string][]byte{"blobs/x/1": []byte("a")}})
	defer stop()

	c := &Client{Insecure: true}
	err := c.Stream(context.Background(), fmt.Sprintf("bytestream://%s/blobs/x/1", addr), errWriter{})
	if err == nil {
		t.Fatal("Stream: want error from failing writer, got nil")
	}
}

func TestParseURI(t *testing.T) {
	cases := []struct {
		uri              string
		wantTarget       string
		wantResourceName string
		wantErr          bool
	}{
		{"bytestream://cache.example.com/blobs/abc/123", "cache.example.com", "blobs/abc/123", false},
		{"bytestream://cache.example.com:1985/my-instance/blobs/abc/123", "cache.example.com:1985", "my-instance/blobs/abc/123", false},
		{"file:///tmp/test.log", "", "", true},
		{"bytestream://", "", "", true},
		{"bytestream://cache.example.com/", "", "", true},
		{"not a uri at all \x7f", "", "", true},
	}
	for _, tc := range cases {
		target, resourceName, err := ParseURI(tc.uri)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseURI(%q): want error, got target=%q resourceName=%q", tc.uri, target, resourceName)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseURI(%q): unexpected error: %v", tc.uri, err)
			continue
		}
		if target != tc.wantTarget || resourceName != tc.wantResourceName {
			t.Errorf("ParseURI(%q) = (%q, %q), want (%q, %q)", tc.uri, target, resourceName, tc.wantTarget, tc.wantResourceName)
		}
	}
}
