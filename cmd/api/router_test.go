package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-auth-clean/internal/platform/config"
)

func testRouter(t *testing.T, swagger bool) http.Handler {
	t.Helper()
	cfg := config.Config{
		HTTP:      config.HTTPConfig{CORSAllowedOrigins: []string{"http://localhost:3000"}},
		RateLimit: config.RateLimitConfig{Enabled: true, Global: config.Rate{Limit: 100, Period: time.Minute, Burst: 100}, Auth: config.Rate{Limit: 1, Period: time.Hour, Burst: 1}, TTL: time.Minute},
		Swagger:   config.SwaggerConfig{Enabled: swagger},
	}
	h, err := newRouter(t.Context(), routerDeps{cfg: cfg, log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestRouter_HealthAndNotFound(t *testing.T) {
	h := testRouter(t, false)
	if rec := serve(h, http.MethodGet, "/healthz"); rec.Code != http.StatusOK || rec.Header().Get("X-Request-ID") == "" {
		t.Fatalf("healthz = %d", rec.Code)
	}
	rec := serve(h, http.MethodGet, "/nope")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "NOT_FOUND") {
		t.Fatalf("404 = %d %s", rec.Code, rec.Body)
	}
	if rec := serve(h, http.MethodGet, "/docs/index.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("swagger harus mati: %d", rec.Code)
	}
}

func TestRouter_Swagger(t *testing.T) {
	h := testRouter(t, true)
	if rec := serve(h, http.MethodGet, "/docs"); rec.Code != http.StatusMovedPermanently {
		t.Fatalf("/docs = %d", rec.Code)
	}
	rec := serve(h, http.MethodGet, "/docs/doc.json")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/auth/login") {
		t.Fatalf("doc.json = %d", rec.Code)
	}
}

func TestRouter_AuthRateLimit(t *testing.T) {
	h := testRouter(t, false)
	// Body kosong -> 400 dari handler; request kedua harus kena 429 sebelum handler.
	serve(h, http.MethodPost, "/api/v1/auth/login")
	if rec := serve(h, http.MethodPost, "/api/v1/auth/login"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAuthEndpointKey(t *testing.T) {
	tests := []struct {
		method, path string
		limited      bool
	}{
		{http.MethodPost, "/api/v1/auth/login", true},
		{http.MethodPost, "/api/v1/auth/register", true},
		{http.MethodPost, "/api/v1/auth/logout", false},
		{http.MethodPost, "/api/v1/auth/refresh", false},
		{http.MethodPost, "/api/v1/auth/verify-email", true},
		{http.MethodGet, "/api/v1/auth/me", false},
		{http.MethodPost, "/api/v1/transactions", false},
	}
	for _, tt := range tests {
		got := authEndpointKey(httptest.NewRequest(tt.method, tt.path, nil))
		if (got != "") != tt.limited {
			t.Errorf("%s %s key=%q", tt.method, tt.path, got)
		}
	}
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestReadyz_DrainingAndDB(t *testing.T) {
	var draining atomic.Bool
	mux := http.NewServeMux()
	registerHealth(mux, fakePinger{}, &draining)
	if rec := serve(mux, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("ready = %d", rec.Code)
	}
	draining.Store(true)
	if rec := serve(mux, http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "SHUTTING_DOWN") {
		t.Fatalf("draining = %d %s", rec.Code, rec.Body)
	}

	mux = http.NewServeMux()
	registerHealth(mux, fakePinger{err: errors.New("down")}, nil)
	if rec := serve(mux, http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("db down = %d", rec.Code)
	}
	mux = http.NewServeMux()
	registerHealth(mux, nil, nil)
	if rec := serve(mux, http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no db = %d", rec.Code)
	}
}
