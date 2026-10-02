package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	httpswagger "github.com/swaggo/http-swagger/v2"

	_ "go-auth-clean/docs/swagger" // registrasi spec hasil `make swagger`
	authhttp "go-auth-clean/internal/auth/adapter/http"
	"go-auth-clean/internal/auth/adapter/security"
	authapp "go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/finance"
	"go-auth-clean/internal/platform/clock"
	"go-auth-clean/internal/platform/config"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/platform/middleware"
	"go-auth-clean/internal/platform/validator"
)

type routerDeps struct {
	cfg     config.Config
	log     *slog.Logger
	pool    *pgxpool.Pool
	authSvc *authapp.Service
	tokens  *security.JWTIssuer
	clock   clock.Clock
	auth    authEnv
	// sealer mengenkripsi cookie state OAuth (nil = route OAuth tidak didaftarkan).
	sealer authhttp.CookieSealer
	// finance nil = route finance tidak didaftarkan (dipakai di test router).
	finance *finance.Module
	// draining di-set true saat shutdown dimulai supaya /readyz langsung 503
	// dan load balancer berhenti mengirim traffic baru (nil = tidak dipakai).
	draining *atomic.Bool
}

// newRouter merakit mux + middleware global. ctx mengontrol umur goroutine
// janitor rate limiter (berhenti saat server shutdown).
func newRouter(ctx context.Context, d routerDeps) (http.Handler, error) {
	cfg := d.cfg
	ipResolver, err := middleware.NewIPResolver(cfg.HTTP.TrustedProxies)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	var db pinger
	if d.pool != nil {
		db = d.pool
	}
	registerHealth(mux, db, d.draining)
	if cfg.Swagger.Enabled {
		registerSwagger(mux)
	}

	var authOpt authhttp.Options
	if cfg.RateLimit.Enabled {
		authOpt.EmailLimiter = middleware.NewMemoryLimiter(ctx, toRate(d.auth.EmailRate), cfg.RateLimit.TTL)
		authOpt.UserLimiter = middleware.NewMemoryLimiter(ctx, toRate(d.auth.UserRate), cfg.RateLimit.TTL)
	}
	if d.authSvc != nil {
		var keyLimiter middleware.RateLimiter
		if cfg.RateLimit.Enabled {
			keyLimiter = middleware.NewMemoryLimiter(ctx, toRate(d.auth.APIKeyRate), cfg.RateLimit.TTL)
		}
		authn := authhttp.NewAuthenticator(d.tokens, d.authSvc, d.authSvc, keyLimiter)
		base := authhttp.NewHandler(d.authSvc, validator.New(), d.clock, authOpt)
		base.Routes(mux, authn.User)
		authhttp.NewP2Handler(base, d.authSvc, authhttp.P2Options{
			Sealer: d.sealer, OAuthRedirectURL: cfg.FrontendURL + "/auth/oauth/callback", SecureCookie: cfg.IsProduction(),
			MFAAttempts: newMFAAttemptGuard(ctx, cfg, d.auth),
		}).Routes(mux, authhttp.NewP2Middleware(authn))
		if d.finance != nil {
			d.finance.RegisterRoutes(mux, authn.User)
		}
	}

	// Fallback JSON 404 untuk path yang tidak terdaftar (format error konsisten).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.ErrNotFound)
	})

	var handler http.Handler = mux
	mws := []middleware.Middleware{
		middleware.RequestID(d.log),
		middleware.AccessLog,
		middleware.Recover,
		middleware.SecurityHeaders(cfg.IsProduction()),
		middleware.CORS(middleware.CORSConfig{AllowedOrigins: cfg.HTTP.CORSAllowedOrigins, AllowCredentials: true}),
		middleware.ClientIP(ipResolver),
	}
	if cfg.RateLimit.Enabled {
		rl := cfg.RateLimit
		global := middleware.NewMemoryLimiter(ctx, toRate(rl.Global), rl.TTL)
		auth := middleware.NewMemoryLimiter(ctx, toRate(rl.Auth), rl.TTL)
		mws = append(mws,
			middleware.RateLimit(global, middleware.KeyByIP("global")),
			// Limit lebih ketat untuk endpoint auth publik (brute force / spam OTP).
			middleware.RateLimit(auth, authEndpointKey),
		)
	}
	return middleware.Chain(handler, mws...), nil
}

// authEndpointKey hanya mengembalikan key untuk POST /api/v1/auth/* (kecuali
// logout yang sudah butuh token, dan refresh yang memakai token acak 256-bit
// sehingga tidak bisa di-brute force; refresh tetap kena limit global).
func authEndpointKey(r *http.Request) string {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
		return ""
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/auth/logout") || r.URL.Path == "/api/v1/auth/refresh" {
		return ""
	}
	return middleware.KeyByIP("auth")(r)
}

// newMFAAttemptGuard membatasi percobaan kode per mfa_token. Selalu aktif
// (kontrol keamanan, bukan throttling biasa) terlepas dari RATE_LIMIT_ENABLED.
// Bucket: burst = max attempts, refill 1 per 24 jam (jauh melebihi umur token),
// TTL janitor > umur token supaya hitungan tidak hilang selama token masih valid.
func newMFAAttemptGuard(ctx context.Context, cfg config.Config, e authEnv) authhttp.MFAAttemptGuard {
	attempts := e.MFAMaxAttempts
	if attempts <= 0 {
		attempts = defaultMFAMaxAttempts
	}
	ttl := max(2*e.MFATokenTTL, cfg.RateLimit.TTL, 10*time.Minute)
	verifier := security.NewMFATokenIssuer(cfg.JWT.Secret, cfg.JWT.Issuer, e.MFATokenTTL)
	return authhttp.MFAAttemptGuard{
		Limiter:     middleware.NewMemoryLimiter(ctx, middleware.RateConfig{Limit: 1, Period: 24 * time.Hour, Burst: attempts}, ttl),
		ChallengeID: verifier.ChallengeID,
	}
}

const defaultMFAMaxAttempts = 5

func toRate(r config.Rate) middleware.RateConfig {
	return middleware.RateConfig{Limit: r.Limit, Period: r.Period, Burst: r.Burst}
}

func registerSwagger(mux *http.ServeMux) {
	mux.Handle("GET /docs/", httpswagger.Handler(httpswagger.URL("/docs/doc.json")))
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/index.html", http.StatusMovedPermanently)
	})
}

// pinger dipenuhi *pgxpool.Pool; interface supaya /readyz bisa dites tanpa DB.
type pinger interface {
	Ping(ctx context.Context) error
}

func registerHealth(mux *http.ServeMux, db pinger, draining *atomic.Bool) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if draining != nil && draining.Load() {
			httpx.WriteError(w, r, &httpx.Error{Status: http.StatusServiceUnavailable, Code: "SHUTTING_DOWN", Message: "server sedang shutdown"})
			return
		}
		if db == nil {
			httpx.WriteError(w, r, &httpx.Error{Status: http.StatusServiceUnavailable, Code: "DB_UNAVAILABLE", Message: "database tidak tersedia"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			httpx.WriteError(w, r, &httpx.Error{Status: http.StatusServiceUnavailable, Code: "DB_UNAVAILABLE", Message: "database tidak tersedia"})
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
