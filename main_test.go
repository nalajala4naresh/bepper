package main

import (
	"context"
	"testing"
)

func TestNewAuthenticator_RequireAuthWithoutConfigFails(t *testing.T) {
	t.Setenv("BEP_OIDC_ISSUER_URL", "")
	t.Setenv("BEP_OIDC_CLIENT_ID", "")
	t.Setenv("BEP_REQUIRE_AUTH", "true")

	if _, err := newAuthenticator(context.Background()); err == nil {
		t.Fatal("newAuthenticator: expected error when BEP_REQUIRE_AUTH=true but OIDC is unconfigured")
	}
}

func TestNewAuthenticator_DisabledWithoutRequireAuth(t *testing.T) {
	t.Setenv("BEP_OIDC_ISSUER_URL", "")
	t.Setenv("BEP_OIDC_CLIENT_ID", "")
	t.Setenv("BEP_REQUIRE_AUTH", "")

	authn, err := newAuthenticator(context.Background())
	if err != nil {
		t.Fatalf("newAuthenticator: %v", err)
	}
	if authn != nil {
		t.Fatalf("newAuthenticator: got %+v, want nil (auth disabled)", authn)
	}
}
