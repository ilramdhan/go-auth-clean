package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/adapter/security"
	authapp "go-auth-clean/internal/auth/app"
	authdomain "go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/finance"
	finapp "go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/platform/clock"
	"go-auth-clean/internal/platform/config"
	"go-auth-clean/internal/platform/crypto"
)

var testSecret = bytes.Repeat([]byte("s"), 32)

func testConfig(swagger, rateLimit bool) config.Config {
	return config.Config{
		AppEnv:      "development",
		FrontendURL: "http://localhost:3000",
		HTTP:        config.HTTPConfig{CORSAllowedOrigins: []string{"http://localhost:3000"}},
		JWT:         config.JWTConfig{Secret: testSecret, Issuer: "test", AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour},
		RateLimit: config.RateLimitConfig{
			Enabled: rateLimit, TTL: time.Minute,
			Global: config.Rate{Limit: 1000, Period: time.Minute, Burst: 1000},
			Auth:   config.Rate{Limit: 1000, Period: time.Minute, Burst: 1000},
		},
		Security: config.SecurityConfig{EncryptionKey: make([]byte, 32), TOTPIssuer: "test"},
		Swagger:  config.SwaggerConfig{Enabled: swagger},
	}
}

// fullRouter merakit router lengkap (auth + P2 + finance) tanpa database:
// constructor repository hanya menyimpan pool (nil), jadi aman selama test
// tidak sampai ke query (mis. request tanpa token ditolak middleware).
func fullRouter(t *testing.T, cfg config.Config) http.Handler {
	t.Helper()
	env, err := loadAuthEnv(cfg)
	if err != nil {
		t.Fatal(err)
	}
	deps := authapp.Deps{Clock: clock.System{}}
	sealer, err := wireAuthP2(t.Context(), cfg, env, nil, &deps)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := newRouter(t.Context(), routerDeps{
		cfg: cfg, log: log,
		authSvc: authapp.NewService(deps, env.Service),
		tokens:  security.NewJWTIssuer(cfg.JWT.Secret, cfg.JWT.Issuer, cfg.JWT.AccessTokenTTL),
		clock:   clock.System{}, auth: env, sealer: sealer,
		finance: finance.NewModule(finance.Deps{Logger: log}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestFullRouter_RoutesRegistered(t *testing.T) {
	h := fullRouter(t, testConfig(false, true))
	// Route terautentikasi: terdaftar -> 401 dari middleware auth (bukan 404 fallback).
	protected := []struct{ method, path string }{
		{"GET", "/api/v1/users/me"},
		{"GET", "/api/v1/users/me/sessions"},
		{"GET", "/api/v1/users/me/2fa"},
		{"GET", "/api/v1/users/me/api-keys"},
		{"GET", "/api/v1/auth/whoami"},
		{"GET", "/api/v1/admin/users"},
		{"GET", "/api/v1/accounts"},
		{"POST", "/api/v1/accounts"},
		{"GET", "/api/v1/categories"},
	}
	for _, p := range protected {
		rec := do(h, p.method, p.path, "", nil)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "UNAUTHENTICATED") {
			t.Errorf("%s %s = %d %s", p.method, p.path, rec.Code, rec.Body)
		}
	}
	// Route publik: body tidak valid -> 400 dari handler (route terdaftar).
	public := []string{
		"/api/v1/auth/register", "/api/v1/auth/login", "/api/v1/auth/login/2fa",
		"/api/v1/auth/refresh", "/api/v1/auth/password/forgot",
	}
	for _, p := range public {
		rec := do(h, "POST", p, "{", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s = %d %s", p, rec.Code, rec.Body)
		}
	}
	// OAuth tidak dikonfigurasi -> route tidak didaftarkan -> fallback 404 JSON.
	if rec := do(h, "GET", "/api/v1/auth/oauth/google/start", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("oauth start = %d", rec.Code)
	}
}

func TestFullRouter_NotFoundJSONAndMethod(t *testing.T) {
	h := fullRouter(t, testConfig(false, false))
	rec := do(h, "GET", "/api/v1/does-not-exist", "", nil)
	if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") ||
		!strings.Contains(rec.Body.String(), `"NOT_FOUND"`) || !strings.Contains(rec.Body.String(), "request_id") {
		t.Fatalf("404 = %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if rec := do(h, "GET", "/healthz", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	// Tanpa pool, /readyz melaporkan DB_UNAVAILABLE.
	if rec := do(h, "GET", "/readyz", "", nil); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "DB_UNAVAILABLE") {
		t.Fatalf("readyz = %d %s", rec.Code, rec.Body)
	}
}

func TestFullRouter_SwaggerSwitch(t *testing.T) {
	on := fullRouter(t, testConfig(true, false))
	if rec := do(on, "GET", "/docs/doc.json", "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/auth/login/2fa") {
		t.Fatalf("swagger on: %d", rec.Code)
	}
	if rec := do(on, "GET", "/docs", "", nil); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/docs/index.html" {
		t.Fatalf("/docs redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	off := fullRouter(t, testConfig(false, false))
	for _, p := range []string{"/docs", "/docs/doc.json", "/docs/index.html"} {
		if rec := do(off, "GET", p, "", nil); rec.Code != http.StatusNotFound {
			t.Fatalf("swagger off %s: %d", p, rec.Code)
		}
	}
}

func TestFullRouter_CORSAndSecurityHeaders(t *testing.T) {
	h := fullRouter(t, testConfig(false, false))
	allowed := map[string]string{"Origin": "http://localhost:3000"}
	rec := do(h, "GET", "/healthz", "", allowed)
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("CORS headers: %v", rec.Header())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers: %v", rec.Header())
	}
	pre := do(h, "OPTIONS", "/api/v1/auth/login", "", map[string]string{
		"Origin": "http://localhost:3000", "Access-Control-Request-Method": "POST",
	})
	if pre.Code != http.StatusNoContent || !strings.Contains(pre.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Fatalf("preflight = %d %v", pre.Code, pre.Header())
	}
	evil := do(h, "GET", "/healthz", "", map[string]string{"Origin": "http://evil.example"})
	if evil.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("origin asing tidak boleh mendapat header CORS")
	}
}

func TestFullRouter_AuthEndpointRateLimit(t *testing.T) {
	cfg := testConfig(false, true)
	cfg.RateLimit.Auth = config.Rate{Limit: 1, Period: time.Hour, Burst: 1}
	h := fullRouter(t, cfg)
	do(h, "POST", "/api/v1/auth/login/2fa", "{", nil)
	rec := do(h, "POST", "/api/v1/auth/login/2fa", "{", nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("auth limit = %d", rec.Code)
	}
	// Refresh dikecualikan dari limit auth (hanya kena limit global).
	for range 3 {
		if rec := do(h, "POST", "/api/v1/auth/refresh", "{", nil); rec.Code == http.StatusTooManyRequests {
			t.Fatal("refresh tidak boleh kena limit auth")
		}
	}
}

func TestAuthEndpointKey_IPScoped(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	if k := authEndpointKey(r); !strings.HasPrefix(k, "auth:ip:") {
		t.Fatalf("key = %q", k)
	}
	if k := authEndpointKey(httptest.NewRequest("POST", "/api/v1/auth/logout-all", nil)); k != "" {
		t.Fatalf("logout-all key = %q", k)
	}
	if k := authEndpointKey(httptest.NewRequest("POST", "/api/v1/authx", nil)); k != "" {
		t.Fatalf("prefix mirip tidak boleh kena: %q", k)
	}
}

func TestNewMFAAttemptGuard(t *testing.T) {
	cfg := testConfig(false, false)
	env, err := loadAuthEnv(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if env.MFAMaxAttempts != 5 {
		t.Fatalf("default max attempts = %d", env.MFAMaxAttempts)
	}
	env.MFAMaxAttempts = 0 // fallback ke default
	g := newMFAAttemptGuard(t.Context(), cfg, env)
	tok, _, err := security.NewMFATokenIssuer(cfg.JWT.Secret, cfg.JWT.Issuer, env.MFATokenTTL).IssueMFA(uuid.New(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	jti, err := g.ChallengeID(tok)
	if err != nil || jti == "" {
		t.Fatalf("jti=%q err=%v", jti, err)
	}
	if _, err := g.ChallengeID("not-a-token"); err == nil {
		t.Fatal("token rusak harus error")
	}
	for i := range defaultMFAMaxAttempts {
		if ok, _ := g.Limiter.Allow(t.Context(), jti); !ok {
			t.Fatalf("attempt %d ditolak", i+1)
		}
	}
	if ok, _ := g.Limiter.Allow(t.Context(), jti); ok {
		t.Fatal("attempt ke-6 harus ditolak")
	}
}

func TestLoadAuthEnv(t *testing.T) {
	cfg := testConfig(false, false)
	t.Setenv("AUTH_MAX_SESSIONS", "3")
	t.Setenv("AUTH_MFA_TOKEN_TTL", "2m")
	t.Setenv("AUTH_MFA_MAX_ATTEMPTS", "7")
	e, err := loadAuthEnv(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if e.Service.MaxSessions != 3 || e.MFATokenTTL != 2*time.Minute || e.MFAMaxAttempts != 7 ||
		e.Service.APIKeyEnv != "test" || e.CleanupInterval != time.Hour {
		t.Fatalf("env = %+v", e)
	}

	t.Setenv("AUTH_MAX_SESSIONS", "-1")
	t.Setenv("AUTH_MFA_TOKEN_TTL", "soon")
	_, err = loadAuthEnv(cfg)
	if err == nil || !strings.Contains(err.Error(), "AUTH_MAX_SESSIONS") || !strings.Contains(err.Error(), "AUTH_MFA_TOKEN_TTL") {
		t.Fatalf("err = %v", err)
	}
}

func TestAPIKeyEnv(t *testing.T) {
	if got := apiKeyEnv(config.Config{AppEnv: config.EnvProduction}); got != "live" {
		t.Fatalf("prod = %q", got)
	}
	if got := apiKeyEnv(config.Config{AppEnv: "development"}); got != "test" {
		t.Fatalf("dev = %q", got)
	}
}

func TestWireAuthP2(t *testing.T) {
	cfg := testConfig(false, false)
	var d authapp.Deps
	sealer, err := wireAuthP2(t.Context(), cfg, authEnv{MFATokenTTL: time.Minute}, nil, &d)
	if err != nil || sealer == nil {
		t.Fatal(err)
	}
	if d.Roles == nil || d.MFA == nil || d.Identities == nil || d.LoginCodes == nil || d.APIKeys == nil ||
		d.MFATokens == nil || d.TOTP == nil || d.Sealer == nil {
		t.Fatalf("deps belum lengkap: %+v", d)
	}
	if len(d.OAuthProviders) != 0 {
		t.Fatal("OAuth harus nonaktif tanpa kredensial")
	}

	cfg.Security.EncryptionKey = []byte("short")
	if _, err := wireAuthP2(t.Context(), cfg, authEnv{}, nil, &authapp.Deps{}); !errors.Is(err, crypto.ErrInvalidKey) {
		t.Fatalf("err = %v", err)
	}
}

type fakeFinder struct {
	user *authdomain.User
	err  error
	got  string
}

func (f *fakeFinder) FindByEmail(_ context.Context, email string) (*authdomain.User, error) {
	f.got = email
	return f.user, f.err
}

func TestUserDirectory_LookupByEmail(t *testing.T) {
	id := uuid.New()
	f := &fakeFinder{user: &authdomain.User{ID: id}}
	got, err := userDirectory{users: f}.LookupByEmail(t.Context(), "a@b.io")
	if err != nil || got != id || f.got != "a@b.io" {
		t.Fatalf("got %v %v (email %q)", got, err, f.got)
	}

	_, err = userDirectory{users: &fakeFinder{err: authdomain.ErrUserNotFound}}.LookupByEmail(t.Context(), "x@y.io")
	if !errors.Is(err, finapp.ErrUserNotFound) {
		t.Fatalf("not found -> %v", err)
	}

	boom := errors.New("db down")
	got, err = userDirectory{users: &fakeFinder{err: boom}}.LookupByEmail(t.Context(), "x@y.io")
	if !errors.Is(err, boom) || got != uuid.Nil {
		t.Fatalf("db error -> %v %v", got, err)
	}
}
