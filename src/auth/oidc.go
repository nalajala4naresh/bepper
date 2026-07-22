package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Authenticator verifies users against an OIDC identity provider and
// issues bepper's own session cookies once they've signed in. See Wrap for
// the HTTP surface built on top of it.
type Authenticator struct {
	oauth2Config  oauth2.Config
	verifier      *oidc.IDTokenVerifier
	codec         sessionCodec
	secureCookies bool
}

// New builds an Authenticator from cfg. It performs OIDC discovery against
// cfg.IssuerURL (an HTTP request to its .well-known/openid-configuration
// document), so it can fail if the issuer is unreachable or misconfigured
// — callers should treat that as fatal at startup rather than retrying,
// since serving the web UI half-configured for auth would be worse than
// not starting.
func New(ctx context.Context, cfg Config) (*Authenticator, error) {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("auth: OIDC discovery against %q failed: %w", cfg.IssuerURL, err)
	}

	return &Authenticator{
		oauth2Config: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       cfg.Scopes,
		},
		verifier:      provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		codec:         newSessionCodec(cfg.SessionSecret),
		secureCookies: !cfg.InsecureCookies,
	}, nil
}
