// Package auth gates bepper's web UI behind SSO via generic OIDC, so it
// works against Okta, Google Workspace, Azure AD, Auth0, or any other
// OIDC-compliant identity provider through the same code path.
//
// Sessions are a stateless, encrypted cookie rather than a server-side
// store, so auth works the same way whether bepper is deployed as one
// instance or many behind a load balancer.
package auth

import (
	"fmt"
	"os"
	"strings"
)

// defaultScopes is requested when BEP_OIDC_SCOPES is unset. "openid" is
// required by the OIDC spec; profile/email give us the display name and
// email address claims the web UI shows.
var defaultScopes = []string{"openid", "profile", "email"}

// minSessionSecretLen is enforced so a short/weak BEP_SESSION_SECRET fails
// at startup instead of producing a crackable session cookie.
const minSessionSecretLen = 16

// Config configures OIDC-based SSO for the web UI.
type Config struct {
	IssuerURL     string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	Scopes        []string
	SessionSecret string
	// InsecureCookies disables the Secure cookie flag, for local HTTP dev
	// only. Never set this in production.
	InsecureCookies bool
}

// ConfigFromEnv reads BEP_OIDC_* and BEP_SESSION_SECRET/BEP_INSECURE_COOKIES.
//
// It returns (nil, nil) if BEP_OIDC_ISSUER_URL and BEP_OIDC_CLIENT_ID are
// both unset — auth is an optional feature, so the caller should treat this
// as "serve the web UI unauthenticated," matching how newBlobstore in
// main.go falls back to disk when BEP_S3_BUCKET is unset. If either is set,
// the rest of the OIDC config is required; a partially-set configuration
// returns an error rather than silently disabling auth, since that's far
// more likely to be a misconfiguration than an intentional choice.
func ConfigFromEnv() (*Config, error) {
	issuerURL := os.Getenv("BEP_OIDC_ISSUER_URL")
	clientID := os.Getenv("BEP_OIDC_CLIENT_ID")
	if issuerURL == "" && clientID == "" {
		return nil, nil
	}

	clientSecret := os.Getenv("BEP_OIDC_CLIENT_SECRET")
	redirectURL := os.Getenv("BEP_OIDC_REDIRECT_URL")
	sessionSecret := os.Getenv("BEP_SESSION_SECRET")

	var missing []string
	if issuerURL == "" {
		missing = append(missing, "BEP_OIDC_ISSUER_URL")
	}
	if clientID == "" {
		missing = append(missing, "BEP_OIDC_CLIENT_ID")
	}
	if clientSecret == "" {
		missing = append(missing, "BEP_OIDC_CLIENT_SECRET")
	}
	if redirectURL == "" {
		missing = append(missing, "BEP_OIDC_REDIRECT_URL")
	}
	if sessionSecret == "" {
		missing = append(missing, "BEP_SESSION_SECRET")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("auth: incomplete OIDC configuration, missing %s", strings.Join(missing, ", "))
	}
	if len(sessionSecret) < minSessionSecretLen {
		return nil, fmt.Errorf("auth: BEP_SESSION_SECRET must be at least %d bytes", minSessionSecretLen)
	}

	scopes := defaultScopes
	if v := os.Getenv("BEP_OIDC_SCOPES"); v != "" {
		scopes = strings.Fields(v)
	}

	return &Config{
		IssuerURL:       issuerURL,
		ClientID:        clientID,
		ClientSecret:    clientSecret,
		RedirectURL:     redirectURL,
		Scopes:          scopes,
		SessionSecret:   sessionSecret,
		InsecureCookies: os.Getenv("BEP_INSECURE_COOKIES") == "true",
	}, nil
}
