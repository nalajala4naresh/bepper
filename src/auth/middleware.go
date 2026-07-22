package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// pageData is the shared shape rendered by pageTmpl — both the sign-in
// landing page and the logout confirmation are the same card layout with a
// different tagline and button.
type pageData struct {
	Tagline     string
	ButtonLabel string
	ButtonHref  string
}

// pageTmpl renders the plain HTML pages this package serves itself (the
// sign-in landing page and the logout confirmation) — self-contained and
// independent of the React app's own CSS, since these render before/
// without a session to load the SPA behind. ButtonHref carries untrusted,
// request-derived input on the login page (the return path), so this goes
// through html/template rather than string concatenation — it's
// automatically escaped for its href-attribute context, no hand-rolled
// escaping to get wrong.
//
// icon is an original isometric-block mark evoking a build artifact
// (bepper is a Bazel Build Event Service viewer) — deliberately not
// Bazel's own logo, which is trademarked and not ours to use.
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>bepper</title>
<style>
  :root {
    --bg: #0d1117;
    --panel: #161b22;
    --border: #30363d;
    --text: #c9d1d9;
    --muted: #8b949e;
    --link: #58a6ff;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg);
    color: var(--text);
    font: 14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
  }
  .card {
    width: 22rem;
    max-width: calc(100vw - 2rem);
    padding: 2.5rem 2rem;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    text-align: center;
    box-shadow: 0 10px 40px rgba(0, 0, 0, 0.35);
  }
  .icon { width: 56px; height: 56px; margin-bottom: 1.25rem; }
  h1 { margin: 0 0 0.35rem; font-size: 1.4rem; font-weight: 600; letter-spacing: -0.01em; }
  .tagline { margin: 0 0 1.75rem; color: var(--muted); font-size: 0.9rem; }
  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    padding: 0.65rem 1.5rem;
    background: var(--link);
    color: #0d1117;
    border-radius: 8px;
    text-decoration: none;
    font-weight: 600;
    font-size: 0.9rem;
    transition: filter 0.15s ease;
  }
  .btn:hover { filter: brightness(1.12); }
  .btn:active { filter: brightness(0.95); }
</style>
</head>
<body>
<div class="card">
  <svg class="icon" viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
    <polygon points="50,4 92,27 50,50 8,27" fill="#8ec2ff"/>
    <polygon points="8,27 50,50 50,96 8,73" fill="#58a6ff"/>
    <polygon points="92,27 50,50 50,96 92,73" fill="#2f6fc4"/>
  </svg>
  <h1>bepper</h1>
  <p class="tagline">{{.Tagline}}</p>
  <a class="btn" href="{{.ButtonHref}}">{{.ButtonLabel}}</a>
</div>
</body>
</html>`))

// Wrap mounts /auth/login, /auth/callback, /auth/logout, and /auth/me on
// top of next, and gates every other request behind a valid session
// cookie: unauthenticated requests under /api/ get a plain 401 (a redirect
// would break callers doing fetch()/JSON parsing); other unauthenticated
// page loads get a sign-in landing page with an explicit "Log in" button,
// not an automatic redirect into the IdP — nobody should be bounced into a
// third-party login flow without a page in between that they can see (and
// that a click, not a page load, is what triggers it).
func (a *Authenticator) Wrap(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", a.handleLogin)
	mux.HandleFunc("GET /auth/callback", a.handleCallback)
	mux.HandleFunc("GET /auth/logout", a.handleLogout)
	mux.HandleFunc("GET /auth/me", a.handleMe)
	mux.Handle("/", a.requireSession(next))
	return mux
}

func (a *Authenticator) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := sessionFromRequest(r, a.codec); ok {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		a.renderLoginPage(w, r)
	})
}

// renderLoginPage shows the sign-in landing page. Its "Log in" button
// links to /auth/login with the originally-requested path preserved as
// ?return=, so a deep link (e.g. shared /invocation/<id> URL) still lands
// the user on the right page once they've signed in, same as before this
// was a direct redirect.
func (a *Authenticator) renderLoginPage(w http.ResponseWriter, r *http.Request) {
	loginURL := "/auth/login?return=" + url.QueryEscape(r.URL.RequestURI())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.Execute(w, pageData{
		Tagline:     "Sign in to view build invocations.",
		ButtonLabel: "Log in",
		ButtonHref:  loginURL,
	}); err != nil {
		log.Printf("auth: render login page: %v", err)
	}
}

// handleLogin starts the OIDC authorization-code flow: it stashes a random
// state (CSRF protection for the callback) and nonce (replay protection
// for the ID token) in a short-lived cookie, then redirects to the
// provider's login page.
func (a *Authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
	returnPath := validateReturnPath(r.URL.Query().Get("return"))

	state, err := randomToken(32)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken(32)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := setLoginStateCookie(w, a.codec, a.secureCookies, loginState{
		State:      state,
		Nonce:      nonce,
		ReturnPath: returnPath,
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, a.oauth2Config.AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
}

// handleCallback completes the flow started by handleLogin: it verifies
// the state and nonce stashed there, exchanges the authorization code,
// verifies the returned ID token, and — only once all of that checks out —
// issues bepper's own session cookie.
func (a *Authenticator) handleCallback(w http.ResponseWriter, r *http.Request) {
	ls, ok := loginStateFromRequest(r, a.codec)
	// The state cookie is single-use: clear it up front regardless of how
	// this request turns out, so a leaked callback URL can't be replayed.
	clearLoginStateCookie(w, a.secureCookies)
	if !ok {
		http.Error(w, "missing or expired login state", http.StatusBadRequest)
		return
	}

	if subtle.ConstantTimeCompare([]byte(ls.State), []byte(r.URL.Query().Get("state"))) != 1 {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	token, err := a.oauth2Config.Exchange(ctx, code)
	if err != nil {
		log.Printf("auth: code exchange failed: %v", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		log.Printf("auth: token response missing id_token")
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	idToken, err := a.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		log.Printf("auth: id token verification failed: %v", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	// go-oidc verifies signature/issuer/audience/expiry, but does NOT check
	// the nonce automatically — that's on us, and skipping it would allow a
	// previously-issued ID token to be replayed into a new session.
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(ls.Nonce)) != 1 {
		log.Printf("auth: id token nonce mismatch")
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		log.Printf("auth: failed to parse id token claims: %v", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	session := Session{
		Email:  claims.Email,
		Name:   claims.Name,
		Expiry: time.Now().Add(sessionLifetime),
	}
	if err := setSessionCookie(w, a.codec, a.secureCookies, session); err != nil {
		log.Printf("auth: failed to set session cookie: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, ls.ReturnPath, http.StatusFound)
}

// handleLogout clears the local session cookie and shows an explicit
// confirmation page rather than redirecting back into the app. Redirecting
// to "/" would immediately bounce through the unauthenticated-page
// redirect straight into /auth/login and on into the IdP's login page — if
// the user's IdP session (e.g. Google) is still live, that can silently
// re-approve and hand back a fresh bepper session before the user sees
// anything, making logout look like it did nothing. This also doesn't sign
// the user out of the identity provider itself — RP-initiated logout
// varies too much across providers to support generically here — so a
// live IdP session will still silently re-authenticate on the next
// deliberate login click, just not automatically.
func (a *Authenticator) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w, a.secureCookies)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.Execute(w, pageData{
		Tagline:     "You have been logged out.",
		ButtonLabel: "Log back in",
		ButtonHref:  "/auth/login",
	}); err != nil {
		log.Printf("auth: render logout page: %v", err)
	}
}

func (a *Authenticator) handleMe(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromRequest(r, a.codec)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}{Email: session.Email, Name: session.Name})
}

// validateReturnPath rejects anything but a same-origin relative path, to
// prevent /auth/login?return= from being used as an open redirect.
func validateReturnPath(p string) string {
	const fallback = "/"
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return fallback
	}
	u, err := url.Parse(p)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return fallback
	}
	return p
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
