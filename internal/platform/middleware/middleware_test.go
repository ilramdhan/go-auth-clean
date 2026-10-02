package middleware_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/platform/middleware"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestChainOrder(t *testing.T) {
	var order []string
	mk := func(name string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := middleware.Chain(okHandler, mk("a"), mk("b"), mk("c"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Join(order, ",") != "a,b,c" {
		t.Fatalf("order = %v", order)
	}
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		keep     bool
	}{
		{"generated when empty", "", false},
		{"kept when valid", "abc-123_X.y", true},
		{"replaced when too long", strings.Repeat("a", 65), false},
		{"replaced when contains newline", "abc\ninjected", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ctxID string
			h := middleware.RequestID(discardLogger())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				ctxID = middleware.RequestIDFromContext(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.incoming != "" {
				req.Header.Set("X-Request-ID", tt.incoming)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get("X-Request-ID")
			if got == "" || got != ctxID {
				t.Fatalf("header %q, ctx %q", got, ctxID)
			}
			if (got == tt.incoming) != tt.keep {
				t.Fatalf("keep = %v, got %q", tt.keep, got)
			}
		})
	}
}

func TestRecover(t *testing.T) {
	h := middleware.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		middleware.RequestID(discardLogger()), middleware.Recover)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	var body httpx.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "INTERNAL" || body.RequestID == "" {
		t.Fatalf("body = %+v", body)
	}
}

func TestRecover_AbortHandlerRepanics(t *testing.T) {
	h := middleware.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		if recover() == nil {
			t.Fatal("ErrAbortHandler harus diteruskan")
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestAccessLog_RecordsStatus(t *testing.T) {
	h := middleware.AccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("x"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		hsts     bool
		wantCSP  bool
		wantHSTS bool
	}{
		{"api path dev", "/api/v1/x", false, true, false},
		{"api path prod", "/api/v1/x", true, true, true},
		{"swagger path", "/docs/index.html", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			middleware.SecurityHeaders(tt.hsts)(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			h := rec.Header()
			if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "DENY" {
				t.Fatal("header dasar tidak ada")
			}
			if (h.Get("Content-Security-Policy") != "") != tt.wantCSP {
				t.Fatalf("CSP = %q", h.Get("Content-Security-Policy"))
			}
			if (h.Get("Strict-Transport-Security") != "") != tt.wantHSTS {
				t.Fatalf("HSTS = %q", h.Get("Strict-Transport-Security"))
			}
		})
	}
}

func TestCORS(t *testing.T) {
	mw := middleware.CORS(middleware.CORSConfig{AllowedOrigins: []string{"http://localhost:3000/", "*"}, AllowCredentials: true})
	tests := []struct {
		name       string
		method     string
		origin     string
		preflight  bool
		wantStatus int
		wantACAO   string
	}{
		{"no origin passes", http.MethodGet, "", false, 200, ""},
		{"allowed origin", http.MethodGet, "http://localhost:3000", false, 200, "http://localhost:3000"},
		{"disallowed origin no header", http.MethodGet, "http://evil.com", false, 200, ""},
		{"preflight allowed", http.MethodOptions, "http://localhost:3000", true, 204, "http://localhost:3000"},
		{"preflight disallowed", http.MethodOptions, "http://evil.com", true, 403, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/v1/x", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.preflight {
				req.Header.Set("Access-Control-Request-Method", "POST")
			}
			rec := httptest.NewRecorder()
			mw(okHandler).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantACAO {
				t.Fatalf("ACAO = %q, want %q", got, tt.wantACAO)
			}
			if tt.wantACAO != "" && rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Fatal("credentials header hilang")
			}
			if tt.preflight && tt.wantStatus == 204 && rec.Header().Get("Access-Control-Allow-Methods") == "" {
				t.Fatal("allow-methods hilang")
			}
		})
	}
}

func TestCORS_WildcardWithoutCredentials(t *testing.T) {
	mw := middleware.CORS(middleware.CORSConfig{AllowedOrigins: []string{"*"}, MaxAge: time.Minute})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://any.com")
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("ACAO = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestIPResolver(t *testing.T) {
	tests := []struct {
		name    string
		trusted []string
		remote  string
		xff     string
		want    string
	}{
		{"no trusted ignores xff", nil, "1.2.3.4:5555", "9.9.9.9", "1.2.3.4"},
		{"untrusted remote ignores xff", []string{"10.0.0.0/8"}, "1.2.3.4:5555", "9.9.9.9", "1.2.3.4"},
		{"trusted remote uses xff", []string{"10.0.0.0/8"}, "10.0.0.1:5555", "9.9.9.9", "9.9.9.9"},
		{"spoofed left entry ignored", []string{"10.0.0.0/8"}, "10.0.0.1:5555", "6.6.6.6, 9.9.9.9", "9.9.9.9"},
		{"multiple trusted hops", []string{"10.0.0.0/8", "172.16.0.1"}, "10.0.0.1:1", "9.9.9.9, 172.16.0.1", "9.9.9.9"},
		{"garbage xff falls back", []string{"10.0.0.1"}, "10.0.0.1:1", "not-an-ip", "10.0.0.1"},
		{"ipv6 remote", nil, "[::1]:80", "", "::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := middleware.NewIPResolver(tt.trusted)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := res.ClientIP(req); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}

			var fromCtx string
			middleware.ClientIP(res)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				fromCtx = middleware.ClientIPFromRequest(r)
			})).ServeHTTP(httptest.NewRecorder(), req)
			if fromCtx != tt.want {
				t.Fatalf("ctx ip %q, want %q", fromCtx, tt.want)
			}
		})
	}
}

func TestNewIPResolver_Invalid(t *testing.T) {
	for _, in := range []string{"nope", "10.0.0.0/99"} {
		if _, err := middleware.NewIPResolver([]string{in}); err == nil {
			t.Errorf("%q harus error", in)
		}
	}
}

func TestClientIPFromRequest_Fallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "5.6.7.8:1"
	if got := middleware.ClientIPFromRequest(req); got != "5.6.7.8" {
		t.Fatalf("got %q", got)
	}
}

func TestMemoryLimiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := middleware.NewMemoryLimiter(ctx, middleware.RateConfig{Limit: 1, Period: time.Hour, Burst: 2}, time.Minute)

	for i := range 2 {
		if ok, _ := l.Allow(ctx, "k"); !ok {
			t.Fatalf("request %d harus lolos (burst)", i)
		}
	}
	ok, retry := l.Allow(ctx, "k")
	if ok || retry <= 0 {
		t.Fatalf("request ke-3 harus ditolak, retry=%v", retry)
	}
	if ok, _ := l.Allow(ctx, "other"); !ok {
		t.Fatal("key lain punya bucket sendiri")
	}
	if l.Len() != 2 {
		t.Fatalf("len = %d", l.Len())
	}
}

func TestMemoryLimiter_Unlimited(t *testing.T) {
	ctx := t.Context()
	l := middleware.NewMemoryLimiter(ctx, middleware.RateConfig{}, 0)
	for range 100 {
		if ok, _ := l.Allow(ctx, "k"); !ok {
			t.Fatal("limit 0 berarti tanpa batas")
		}
	}
}

func TestMemoryLimiter_Concurrent(t *testing.T) {
	ctx := t.Context()
	l := middleware.NewMemoryLimiter(ctx, middleware.RateConfig{Limit: 1000, Period: time.Second, Burst: 1000}, time.Minute)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			for range 20 {
				l.Allow(ctx, string(rune('a'+i%5)))
			}
		})
	}
	wg.Wait()
	l.Cleanup()
}

func TestRateLimitMiddleware(t *testing.T) {
	ctx := t.Context()
	l := middleware.NewMemoryLimiter(ctx, middleware.RateConfig{Limit: 1, Period: time.Minute, Burst: 1}, time.Minute)
	h := middleware.RateLimit(l, middleware.KeyByIP("t"))(okHandler)

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.1.1.1:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := do(); rec.Code != http.StatusOK {
		t.Fatalf("first status %d", rec.Code)
	}
	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After hilang")
	}
	var body httpx.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Error.Code != "RATE_LIMITED" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestRateLimitMiddleware_EmptyKeySkips(t *testing.T) {
	l := middleware.NewMemoryLimiter(t.Context(), middleware.RateConfig{Limit: 1, Period: time.Hour, Burst: 1}, time.Minute)
	h := middleware.RateLimit(l, func(*http.Request) string { return "" })(okHandler)
	for range 5 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	}
}
