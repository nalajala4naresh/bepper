package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrSessionExpired is returned by decode when a session cookie's Expiry
// has passed. It's checked separately from cipher/tamper errors so callers
// (or tests) can distinguish "expired" from "invalid".
var ErrSessionExpired = errors.New("auth: session expired")

// sessionLifetime is how long a session cookie is valid for after login.
// Sessions aren't refreshed on activity in v1 — a user is signed back out
// after this long regardless of how recently they used the UI, and bepper
// doesn't re-check the identity provider before then even if the user's
// IdP session was revoked earlier.
const sessionLifetime = 24 * time.Hour

// loginStateLifetime bounds how long a user has to complete the
// login->IdP->callback round trip before the state cookie set at /auth/login
// expires.
const loginStateLifetime = 10 * time.Minute

const (
	sessionCookieName    = "bepper_session"
	loginStateCookieName = "bepper_oidc_state"
)

// Session is the identity bepper trusts for a request, encoded into the
// bepper_session cookie.
type Session struct {
	Email  string    `json:"email"`
	Name   string    `json:"name,omitempty"`
	Expiry time.Time `json:"expiry"`
}

// loginState is stashed in a short-lived cookie between /auth/login and
// /auth/callback: state guards against CSRF on the callback, nonce guards
// against ID token replay, and returnPath is where to send the user once
// login completes.
type loginState struct {
	State      string `json:"state"`
	Nonce      string `json:"nonce"`
	ReturnPath string `json:"returnPath"`
}

// sessionCodec encrypts and authenticates cookie payloads with AES-GCM, so
// a cookie's contents can't be forged or tampered with without knowing
// BEP_SESSION_SECRET. This is what makes bepper's sessions stateless: no
// server-side session store is needed, which matters for multi-instance
// deployments.
type sessionCodec struct {
	key [32]byte
}

func newSessionCodec(secret string) sessionCodec {
	return sessionCodec{key: sha256.Sum256([]byte(secret))}
}

func (c sessionCodec) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encodeValue[T any](c sessionCodec, v T) (string, error) {
	plaintext, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	gcm, err := c.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func decodeValue[T any](c sessionCodec, raw string) (T, error) {
	var zero T
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return zero, err
	}
	gcm, err := c.gcm()
	if err != nil {
		return zero, err
	}
	if len(data) < gcm.NonceSize() {
		return zero, errors.New("auth: cookie payload too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(plaintext, &v); err != nil {
		return zero, err
	}
	return v, nil
}

func decodeSession(c sessionCodec, raw string) (Session, error) {
	s, err := decodeValue[Session](c, raw)
	if err != nil {
		return Session{}, err
	}
	if time.Now().After(s.Expiry) {
		return Session{}, ErrSessionExpired
	}
	return s, nil
}

func setSessionCookie(w http.ResponseWriter, c sessionCodec, secure bool, s Session) error {
	raw, err := encodeValue(c, s)
	if err != nil {
		return fmt.Errorf("auth: encode session: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  s.Expiry,
	})
	return nil
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func sessionFromRequest(r *http.Request, c sessionCodec) (Session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return Session{}, false
	}
	s, err := decodeSession(c, cookie.Value)
	if err != nil {
		return Session{}, false
	}
	return s, true
}

func setLoginStateCookie(w http.ResponseWriter, c sessionCodec, secure bool, ls loginState) error {
	raw, err := encodeValue(c, ls)
	if err != nil {
		return fmt.Errorf("auth: encode login state: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     loginStateCookieName,
		Value:    raw,
		Path:     "/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(loginStateLifetime.Seconds()),
	})
	return nil
}

func clearLoginStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     loginStateCookieName,
		Value:    "",
		Path:     "/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func loginStateFromRequest(r *http.Request, c sessionCodec) (loginState, bool) {
	cookie, err := r.Cookie(loginStateCookieName)
	if err != nil {
		return loginState{}, false
	}
	ls, err := decodeValue[loginState](c, cookie.Value)
	if err != nil {
		return loginState{}, false
	}
	return ls, true
}
