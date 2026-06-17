package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sampleClaims mirrors the shape of a Keycloak access token's role claims.
const sampleClaims = `{
  "sub": "abc-123",
  "preferred_username": "alice",
  "email": "alice@example.com",
  "realm_access": { "roles": ["admin", "user"] },
  "resource_access": {
    "keycloak-portal": { "roles": ["editor"] },
    "account": { "roles": ["view-profile"] }
  }
}`

func mustClaims(t *testing.T) *Claims {
	t.Helper()
	var c Claims
	if err := json.Unmarshal([]byte(sampleClaims), &c); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return &c
}

func TestHasRealmRole(t *testing.T) {
	c := mustClaims(t)
	if !c.HasRealmRole("admin") {
		t.Error("expected admin realm role")
	}
	if c.HasRealmRole("superuser") {
		t.Error("did not expect superuser realm role")
	}
}

func TestHasClientRole(t *testing.T) {
	c := mustClaims(t)
	if !c.HasClientRole("keycloak-portal", "editor") {
		t.Error("expected editor client role on keycloak-portal")
	}
	if c.HasClientRole("keycloak-portal", "admin") {
		t.Error("did not expect admin client role on keycloak-portal")
	}
	if c.HasClientRole("unknown-client", "editor") {
		t.Error("did not expect role on unknown client")
	}
}

func TestExtractTokenPrefersHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer header-token")
	r.AddCookie(&http.Cookie{Name: AccessTokenCookie, Value: "cookie-token"})
	if got := extractToken(r); got != "header-token" {
		t.Errorf("expected header token to win, got %q", got)
	}
}

func TestExtractTokenFallsBackToCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: AccessTokenCookie, Value: "cookie-token"})
	if got := extractToken(r); got != "cookie-token" {
		t.Errorf("expected cookie token, got %q", got)
	}
}

func TestExtractTokenEmpty(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := extractToken(r); got != "" {
		t.Errorf("expected empty token, got %q", got)
	}
}
