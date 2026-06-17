package web

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"github.com/defenseunicorns/keycloak-portal/internal/auth"
	"github.com/defenseunicorns/keycloak-portal/internal/config"
)

//go:embed templates/*.html
var templateFS embed.FS

const (
	stateCookie   = "oidc_state"
	nonceCookie   = "oidc_nonce"
	idTokenCookie = "id_token" // kept only as a logout hint
)

// Server holds the dependencies shared by the HTTP handlers.
type Server struct {
	auth      *auth.Authenticator
	cfg       *config.Config
	templates *template.Template
}

// NewServer parses templates and returns a Server ready to register routes.
func NewServer(authn *auth.Authenticator, cfg *config.Config) (*Server, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{auth: authn, cfg: cfg, templates: tmpl}, nil
}

// Routes wires up the HTTP routes and middleware, returning the root handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Public routes.
	mux.HandleFunc("GET /", s.handleHome)
	mux.HandleFunc("GET /auth/login", s.handleLogin)
	mux.HandleFunc("GET /auth/callback", s.handleCallback)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)

	// Protected portal page (any authenticated user).
	mux.Handle("GET /dashboard", s.auth.Authenticate(http.HandlerFunc(s.handleDashboard)))

	// Protected JSON API: returns the caller's verified claims and roles.
	mux.Handle("GET /api/me", s.auth.Authenticate(http.HandlerFunc(s.handleMe)))

	// Example role-guarded endpoint: requires the "admin" realm role.
	adminOnly := s.auth.RequireRealmRole("admin")
	mux.Handle("GET /api/admin", s.auth.Authenticate(adminOnly(http.HandlerFunc(s.handleAdmin))))

	return logging(mux)
}

// handleHome shows the landing page with a login button (or a link to the
// dashboard if the visitor already has a valid session cookie).
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	authenticated := false
	if c, err := r.Cookie(auth.AccessTokenCookie); err == nil {
		if _, err := s.auth.VerifyAccessToken(r.Context(), c.Value); err == nil {
			authenticated = true
		}
	}
	s.render(w, "home.html", map[string]any{"Authenticated": authenticated})
}

// handleLogin starts the authorization code flow: generate CSRF state + nonce,
// store them in short-lived cookies, and redirect to Keycloak.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomString()
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}
	nonce, err := randomString()
	if err != nil {
		http.Error(w, "failed to generate nonce", http.StatusInternalServerError)
		return
	}

	s.setCookie(w, stateCookie, state, 10*time.Minute)
	s.setCookie(w, nonceCookie, nonce, 10*time.Minute)

	http.Redirect(w, r, s.auth.AuthCodeURL(state, nonce), http.StatusFound)
}

// handleCallback completes the flow: validate state, exchange the code, verify
// the ID token nonce, then store the access token in a session cookie.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Error(w, "authorization error: "+errParam+" "+r.URL.Query().Get("error_description"), http.StatusBadRequest)
		return
	}

	stateCookieVal, err := r.Cookie(stateCookie)
	if err != nil || stateCookieVal.Value == "" || stateCookieVal.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	token, err := s.auth.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "code exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token in token response", http.StatusBadGateway)
		return
	}
	idToken, err := s.auth.VerifyIDToken(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "failed to verify id token: "+err.Error(), http.StatusBadGateway)
		return
	}

	nonceCookieVal, err := r.Cookie(nonceCookie)
	if err != nil || idToken.Nonce != nonceCookieVal.Value {
		http.Error(w, "invalid nonce", http.StatusBadRequest)
		return
	}

	// Persist the access token for browser navigation, and the ID token for
	// use as a logout hint. Clear the transient state/nonce cookies.
	tokenTTL := time.Until(token.Expiry)
	if tokenTTL <= 0 {
		tokenTTL = time.Hour
	}
	s.setCookie(w, auth.AccessTokenCookie, token.AccessToken, tokenTTL)
	s.setCookie(w, idTokenCookie, rawIDToken, tokenTTL)
	s.clearCookie(w, stateCookie)
	s.clearCookie(w, nonceCookie)

	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// handleLogout clears local cookies and redirects to Keycloak's RP-initiated
// logout endpoint so the SSO session is ended too.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var idHint string
	if c, err := r.Cookie(idTokenCookie); err == nil {
		idHint = c.Value
	}
	s.clearCookie(w, auth.AccessTokenCookie)
	s.clearCookie(w, idTokenCookie)
	http.Redirect(w, r, s.auth.LogoutURL(idHint), http.StatusFound)
}

// handleDashboard renders the authenticated portal page showing user identity
// and the roles Keycloak issued.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}

	clientRoles := map[string][]string{}
	for client, ra := range claims.ResourceAccess {
		if len(ra.Roles) > 0 {
			clientRoles[client] = ra.Roles
		}
	}

	s.render(w, "dashboard.html", map[string]any{
		"Username":    firstNonEmpty(claims.PreferredUsername, claims.Name, claims.Subject),
		"Email":       claims.Email,
		"RealmRoles":  claims.AllRealmRoles(),
		"ClientRoles": clientRoles,
		"IsAdmin":     claims.HasRealmRole("admin"),
	})
}

// handleMe returns the verified claims as JSON — the bearer-token API path.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	writeJSON(w, http.StatusOK, claims)
}

// handleAdmin is an example endpoint guarded by RequireRealmRole("admin").
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "welcome, admin",
		"user":    firstNonEmpty(claims.PreferredUsername, claims.Subject),
	})
}

// --- helpers ---

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
