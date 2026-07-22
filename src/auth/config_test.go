package auth

import "testing"

func TestConfigFromEnv_Unset(t *testing.T) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg != nil {
		t.Fatalf("ConfigFromEnv: got %+v, want nil (auth disabled)", cfg)
	}
}

func TestConfigFromEnv_Partial(t *testing.T) {
	t.Setenv("BEP_OIDC_ISSUER_URL", "https://idp.example.com")
	t.Setenv("BEP_OIDC_CLIENT_ID", "client-id")
	// BEP_OIDC_CLIENT_SECRET, BEP_OIDC_REDIRECT_URL, BEP_SESSION_SECRET left unset.

	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("ConfigFromEnv: expected error for partial config, got nil")
	}
}

func TestConfigFromEnv_ShortSecret(t *testing.T) {
	t.Setenv("BEP_OIDC_ISSUER_URL", "https://idp.example.com")
	t.Setenv("BEP_OIDC_CLIENT_ID", "client-id")
	t.Setenv("BEP_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("BEP_OIDC_REDIRECT_URL", "https://bepper.example.com/auth/callback")
	t.Setenv("BEP_SESSION_SECRET", "short")

	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("ConfigFromEnv: expected error for short session secret, got nil")
	}
}

func TestConfigFromEnv_Full(t *testing.T) {
	t.Setenv("BEP_OIDC_ISSUER_URL", "https://idp.example.com")
	t.Setenv("BEP_OIDC_CLIENT_ID", "client-id")
	t.Setenv("BEP_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("BEP_OIDC_REDIRECT_URL", "https://bepper.example.com/auth/callback")
	t.Setenv("BEP_SESSION_SECRET", "a-sufficiently-long-session-secret")
	t.Setenv("BEP_OIDC_SCOPES", "openid email")
	t.Setenv("BEP_INSECURE_COOKIES", "true")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg == nil {
		t.Fatal("ConfigFromEnv: got nil, want populated config")
	}
	if cfg.IssuerURL != "https://idp.example.com" || cfg.ClientID != "client-id" {
		t.Fatalf("ConfigFromEnv: unexpected config %+v", cfg)
	}
	if len(cfg.Scopes) != 2 || cfg.Scopes[0] != "openid" || cfg.Scopes[1] != "email" {
		t.Fatalf("ConfigFromEnv: unexpected scopes %v", cfg.Scopes)
	}
	if !cfg.InsecureCookies {
		t.Fatal("ConfigFromEnv: expected InsecureCookies=true")
	}
}

func TestConfigFromEnv_DefaultScopes(t *testing.T) {
	t.Setenv("BEP_OIDC_ISSUER_URL", "https://idp.example.com")
	t.Setenv("BEP_OIDC_CLIENT_ID", "client-id")
	t.Setenv("BEP_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("BEP_OIDC_REDIRECT_URL", "https://bepper.example.com/auth/callback")
	t.Setenv("BEP_SESSION_SECRET", "a-sufficiently-long-session-secret")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if len(cfg.Scopes) != 3 {
		t.Fatalf("ConfigFromEnv: got scopes %v, want default 3 scopes", cfg.Scopes)
	}
}
