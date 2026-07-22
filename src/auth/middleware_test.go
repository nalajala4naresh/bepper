package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

// fakeIDP is an in-process stand-in for an OIDC provider: a JWKS endpoint
// and a token endpoint that returns whatever ID token the test configures.
// go-oidc needs real discovery/JWKS/token endpoints to exercise its
// verification logic, so this is a fake rather than a mock of go-oidc
// itself, matching the existing in-process-fake test style used for the
// gRPC ByteStream server (see src/bytestream/bytestream_test.go).
type fakeIDP struct {
	key         *rsa.PrivateKey
	token       *httptest.Server
	provider    *oidc.Provider
	nextIDToken string
}

func newFakeIDP(t *testing.T, issuer string) *fakeIDP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	f := &fakeIDP{key: key}

	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key:       &key.PublicKey,
		KeyID:     "test-key",
		Algorithm: "RS256",
		Use:       "sig",
	}}}
	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(jwksSrv.Close)

	f.token = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"id_token":     f.nextIDToken,
			"expires_in":   3600,
		})
	}))
	t.Cleanup(f.token.Close)

	cfg := &oidc.ProviderConfig{
		IssuerURL: issuer,
		AuthURL:   issuer + "/auth",
		TokenURL:  f.token.URL,
		JWKSURL:   jwksSrv.URL,
	}
	f.provider = cfg.NewProvider(context.Background())
	return f
}

func (f *fakeIDP) signIDToken(t *testing.T, issuer, clientID, email, name, nonce string, expiry time.Time) string {
	t.Helper()

	claims := map[string]any{
		"iss":   issuer,
		"sub":   "test-subject",
		"aud":   clientID,
		"exp":   expiry.Unix(),
		"iat":   time.Now().Unix(),
		"nonce": nonce,
		"email": email,
		"name":  name,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, &jose.SignerOptions{
		ExtraHeaders: map[jose.HeaderKey]any{"kid": "test-key"},
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	compact, err := jws.CompactSerialize()
	if err != nil {
		t.Fatalf("compact serialize: %v", err)
	}
	return compact
}

const (
	testIssuer   = "https://idp.example.com"
	testClientID = "test-client-id"
)

func newTestAuthenticator(t *testing.T) (*Authenticator, *fakeIDP) {
	t.Helper()
	fidp := newFakeIDP(t, testIssuer)
	return &Authenticator{
		oauth2Config: oauth2.Config{
			ClientID:     testClientID,
			ClientSecret: "test-client-secret",
			RedirectURL:  "https://bepper.example.com/auth/callback",
			Endpoint:     fidp.provider.Endpoint(),
			Scopes:       []string{"openid", "email", "profile"},
		},
		verifier:      fidp.provider.Verifier(&oidc.Config{ClientID: testClientID}),
		codec:         newSessionCodec("test-secret-at-least-16-bytes"),
		secureCookies: false,
	}, fidp
}

func stubNext(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestValidateReturnPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty defaults to root", "", "/"},
		{"simple relative path allowed", "/invocation?id=1", "/invocation?id=1"},
		{"protocol-relative rejected", "//evil.com", "/"},
		{"absolute url rejected", "http://evil.com", "/"},
		{"javascript scheme rejected", "javascript:alert(1)", "/"},
		{"missing leading slash rejected", "invocation", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateReturnPath(tt.input); got != tt.want {
				t.Errorf("validateReturnPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWrap_UnauthenticatedAPIGets401(t *testing.T) {
	authn, _ := newTestAuthenticator(t)
	var nextCalled bool
	handler := authn.Wrap(stubNext(&nextCalled))

	req := httptest.NewRequest("GET", "/api/invocations", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("unexpected Location header on API 401: %q", loc)
	}
	if nextCalled {
		t.Fatal("next handler should not have been called")
	}
}

func TestWrap_UnauthenticatedPageShowsLoginPage(t *testing.T) {
	authn, _ := newTestAuthenticator(t)
	var nextCalled bool
	handler := authn.Wrap(stubNext(&nextCalled))

	req := httptest.NewRequest("GET", "/invocation?id=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// An unauthenticated page load renders a sign-in landing page directly
	// (200, with a "Log in" link) rather than auto-redirecting into the
	// IdP — nobody should be bounced into a third-party login flow by a
	// plain page load with nothing in between for them to see or decide on.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no redirect)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("unexpected Location header: %q", loc)
	}
	body := rec.Body.String()
	wantHref := `href="/auth/login?return=%2Finvocation%3Fid%3D1"`
	if !strings.Contains(body, wantHref) {
		t.Fatalf("login page body missing %q; got:\n%s", wantHref, body)
	}
	if nextCalled {
		t.Fatal("next handler should not have been called")
	}
}

func TestWrap_ValidSessionPassesThrough(t *testing.T) {
	authn, _ := newTestAuthenticator(t)
	var nextCalled bool
	handler := authn.Wrap(stubNext(&nextCalled))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	if err := setSessionCookie(rec, authn.codec, false, Session{
		Email:  "alice@example.com",
		Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("setSessionCookie: %v", err)
	}
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !nextCalled {
		t.Fatal("next handler should have been called for a valid session")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// startLogin drives /auth/login and returns the state cookie set by it plus
// the state/nonce the (fake) IdP would see in the authorization request.
func startLogin(t *testing.T, handler http.Handler, returnPath string) (stateCookie *http.Cookie, state, nonce string) {
	t.Helper()

	q := url.Values{}
	if returnPath != "" {
		q.Set("return", returnPath)
	}
	req := httptest.NewRequest("GET", "/auth/login?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("/auth/login status = %d, want 302", rec.Code)
	}
	authURL, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse auth redirect: %v", err)
	}
	state = authURL.Query().Get("state")
	nonce = authURL.Query().Get("nonce")
	if state == "" || nonce == "" {
		t.Fatalf("expected state and nonce in auth redirect, got %q", authURL)
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == loginStateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("expected bepper_oidc_state cookie to be set")
	}
	return stateCookie, state, nonce
}

func TestLoginCallback_Success(t *testing.T) {
	authn, fidp := newTestAuthenticator(t)
	var nextCalled bool
	handler := authn.Wrap(stubNext(&nextCalled))

	stateCookie, state, nonce := startLogin(t, handler, "/invocation?id=1")

	fidp.nextIDToken = fidp.signIDToken(t, testIssuer, testClientID, "alice@example.com", "Alice", nonce, time.Now().Add(time.Hour))

	req := httptest.NewRequest("GET", "/auth/callback?state="+state+"&code=test-code", nil)
	req.AddCookie(stateCookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("/auth/callback status = %d, want 302, body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/invocation?id=1" {
		t.Fatalf("Location = %q, want /invocation?id=1", loc)
	}

	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected bepper_session cookie to be set")
	}
	session, err := decodeSession(authn.codec, sessionCookie.Value)
	if err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if session.Email != "alice@example.com" || session.Name != "Alice" {
		t.Fatalf("session = %+v, want alice@example.com/Alice", session)
	}
}

func TestCallback_StateMismatch(t *testing.T) {
	authn, _ := newTestAuthenticator(t)
	handler := authn.Wrap(stubNext(new(bool)))

	stateCookie, _, _ := startLogin(t, handler, "")

	req := httptest.NewRequest("GET", "/auth/callback?state=wrong-state&code=test-code", nil)
	req.AddCookie(stateCookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	assertNoSessionCookieSet(t, rec)
}

func TestCallback_MissingStateCookie(t *testing.T) {
	authn, _ := newTestAuthenticator(t)
	handler := authn.Wrap(stubNext(new(bool)))

	req := httptest.NewRequest("GET", "/auth/callback?state=whatever&code=test-code", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	assertNoSessionCookieSet(t, rec)
}

func TestCallback_NonceMismatch(t *testing.T) {
	authn, fidp := newTestAuthenticator(t)
	handler := authn.Wrap(stubNext(new(bool)))

	stateCookie, state, _ := startLogin(t, handler, "")

	// Sign a token with a nonce that doesn't match the one stashed at login.
	fidp.nextIDToken = fidp.signIDToken(t, testIssuer, testClientID, "alice@example.com", "Alice", "wrong-nonce", time.Now().Add(time.Hour))

	req := httptest.NewRequest("GET", "/auth/callback?state="+state+"&code=test-code", nil)
	req.AddCookie(stateCookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertNoSessionCookieSet(t, rec)
}

func TestLogout_ClearsSessionAndDoesNotRedirect(t *testing.T) {
	authn, _ := newTestAuthenticator(t)
	handler := authn.Wrap(stubNext(new(bool)))

	req := httptest.NewRequest("GET", "/auth/logout", nil)
	setupRec := httptest.NewRecorder()
	if err := setSessionCookie(setupRec, authn.codec, false, Session{
		Email:  "alice@example.com",
		Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("setSessionCookie: %v", err)
	}
	for _, c := range setupRec.Result().Cookies() {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Logout renders a confirmation page directly instead of redirecting —
	// redirecting to "/" would immediately bounce through /auth/login and
	// on into the IdP, which can silently re-authenticate a user whose IdP
	// session is still live, making logout look like a no-op.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no redirect)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("unexpected Location header on logout: %q", loc)
	}

	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.MaxAge >= 0 {
		t.Fatalf("expected logout to clear the session cookie, got %+v", sessionCookie)
	}
}

func assertNoSessionCookieSet(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			t.Fatalf("session cookie should not be set, got %q", c.Value)
		}
	}
}
