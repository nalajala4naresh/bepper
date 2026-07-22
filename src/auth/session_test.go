package auth

import (
	"errors"
	"testing"
	"time"
)

func TestSessionCodec_RoundTrip(t *testing.T) {
	c := newSessionCodec("test-secret-at-least-16-bytes")
	want := Session{Email: "alice@example.com", Name: "Alice", Expiry: time.Now().Add(time.Hour)}

	raw, err := encodeValue(c, want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeSession(c, raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Email != want.Email || got.Name != want.Name || !got.Expiry.Equal(want.Expiry) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestSessionCodec_RejectsTampering(t *testing.T) {
	c := newSessionCodec("test-secret-at-least-16-bytes")
	raw, err := encodeValue(c, Session{Email: "alice@example.com", Expiry: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	tampered := []rune(raw)
	// Flip one character in the payload; base64.RawURLEncoding's alphabet
	// means "A" and "B" are always distinct valid characters, so this is
	// guaranteed to change the decoded bytes.
	if tampered[0] == 'A' {
		tampered[0] = 'B'
	} else {
		tampered[0] = 'A'
	}

	if _, err := decodeSession(c, string(tampered)); err == nil {
		t.Fatal("decode: expected error for tampered cookie, got nil")
	}
}

func TestSessionCodec_RejectsExpired(t *testing.T) {
	c := newSessionCodec("test-secret-at-least-16-bytes")
	raw, err := encodeValue(c, Session{Email: "alice@example.com", Expiry: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	_, err = decodeSession(c, raw)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("decode: got err %v, want ErrSessionExpired", err)
	}
}

func TestSessionCodec_DifferentKeysRejected(t *testing.T) {
	c1 := newSessionCodec("first-secret-at-least-16-bytes")
	c2 := newSessionCodec("second-secret-at-least-16-bytes")

	raw, err := encodeValue(c1, Session{Email: "alice@example.com", Expiry: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if _, err := decodeSession(c2, raw); err == nil {
		t.Fatal("decode: expected error when decoding with a different key, got nil")
	}
}

func TestLoginStateCodec_RoundTrip(t *testing.T) {
	c := newSessionCodec("test-secret-at-least-16-bytes")
	want := loginState{State: "s", Nonce: "n", ReturnPath: "/invocation?id=1"}

	raw, err := encodeValue(c, want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeValue[loginState](c, raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}
