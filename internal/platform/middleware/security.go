package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SecurityHeaders menambahkan header keamanan untuk API JSON.
// hsts=true hanya untuk production di belakang HTTPS.
func SecurityHeaders(hsts bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			// Swagger UI butuh script/style; CSP ketat hanya untuk endpoint API.
			if !strings.HasPrefix(r.URL.Path, "/docs/") {
				h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
				h.Set("Cache-Control", "no-store")
			}
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORSConfig mengatur kebijakan CORS.
type CORSConfig struct {
	// AllowedOrigins: daftar origin eksplisit (mis. http://localhost:3000).
	// "*" hanya diizinkan bila AllowCredentials=false.
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           time.Duration
}

// CORS menangani preflight OPTIONS dan header CORS. Pasang di LUAR mux supaya
// OPTIONS tidak berakhir 405 di http.ServeMux.
func CORS(cfg CORSConfig) Middleware {
	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	wildcard := false
	for _, o := range cfg.AllowedOrigins {
		if o == "*" {
			// Wildcard + credentials dilarang spesifikasi; abaikan wildcard.
			wildcard = !cfg.AllowCredentials
			continue
		}
		allowed[strings.TrimRight(o, "/")] = struct{}{}
	}
	methods := strings.Join(orDefault(cfg.AllowedMethods, []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}), ", ")
	headers := strings.Join(orDefault(cfg.AllowedHeaders, []string{"Authorization", "Content-Type", "Idempotency-Key", "If-Match", "X-API-Key", "X-Request-ID"}), ", ")
	exposed := strings.Join(orDefault(cfg.ExposedHeaders, []string{"X-Request-ID", "Retry-After", "RateLimit-Limit", "RateLimit-Remaining", "ETag", "Idempotent-Replayed", "Content-Disposition"}), ", ")
	maxAge := strconv.Itoa(int(cfg.MaxAge.Seconds()))
	if cfg.MaxAge == 0 {
		maxAge = "600"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			h := w.Header()
			h.Add("Vary", "Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			_, ok := allowed[origin]
			if !ok && !wildcard {
				// Origin tidak diizinkan: jangan tambahkan header CORS. Preflight ditolak.
				if isPreflight(r) {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if ok {
				h.Set("Access-Control-Allow-Origin", origin)
				if cfg.AllowCredentials {
					h.Set("Access-Control-Allow-Credentials", "true")
				}
			} else {
				h.Set("Access-Control-Allow-Origin", "*")
			}

			if isPreflight(r) {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				h.Set("Access-Control-Allow-Methods", methods)
				h.Set("Access-Control-Allow-Headers", headers)
				h.Set("Access-Control-Max-Age", maxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			h.Set("Access-Control-Expose-Headers", exposed)
			next.ServeHTTP(w, r)
		})
	}
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

func orDefault(v, def []string) []string {
	if len(v) == 0 {
		return def
	}
	return v
}
