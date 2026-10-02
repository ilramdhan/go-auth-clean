package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/authctx"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/platform/middleware"
)

// RoleTokenVerifier memverifikasi access token sekaligus mengembalikan roles.
type RoleTokenVerifier interface {
	VerifyRoles(token string) (userID, sessionID uuid.UUID, roles []domain.Role, err error)
}

type APIKeyAuthenticator interface {
	AuthenticateAPIKey(ctx context.Context, raw string) (app.Principal, error)
}

type principalKey struct{}

// PrincipalFromContext mengembalikan principal (user/roles/API key) milik request.
func PrincipalFromContext(ctx context.Context) (app.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(app.Principal)
	return p, ok
}

func withPrincipal(r *http.Request, p app.Principal) *http.Request {
	ctx := context.WithValue(r.Context(), principalKey{}, p)
	ctx = authctx.WithIdentity(ctx, authctx.Identity{UserID: p.UserID, SessionID: p.SessionID})
	return r.WithContext(ctx)
}

var (
	errForbidden     = &httpx.Error{Status: http.StatusForbidden, Code: "FORBIDDEN", Message: "tidak memiliki izin"}
	errInsufficScope = &httpx.Error{Status: http.StatusForbidden, Code: "INSUFFICIENT_SCOPE", Message: "scope API key tidak mencukupi"}
	errAPIKeyInvalid = &httpx.Error{Status: http.StatusUnauthorized, Code: "API_KEY_INVALID", Message: "API key tidak valid, kedaluwarsa, atau dicabut"}
)

// Authenticator menyediakan middleware autentikasi/otorisasi modul auth.
type Authenticator struct {
	tokens   RoleTokenVerifier
	sessions SessionChecker
	apiKeys  APIKeyAuthenticator
	// keyLimiter membatasi request per API key (nil = tanpa batas).
	keyLimiter middleware.RateLimiter
}

func NewAuthenticator(tokens RoleTokenVerifier, sessions SessionChecker, apiKeys APIKeyAuthenticator, keyLimiter middleware.RateLimiter) *Authenticator {
	return &Authenticator{tokens: tokens, sessions: sessions, apiKeys: apiKeys, keyLimiter: keyLimiter}
}

// User hanya menerima access token (Bearer). Dipakai untuk endpoint pengelolaan
// akun (password, 2FA, sesi, API key, admin) yang tidak boleh diakses API key.
func (a *Authenticator) User(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearer(r)
		if !ok {
			httpx.WriteError(w, r, errUnauthenticated)
			return
		}
		a.serveBearer(w, r, raw, next)
	})
}

// Any menerima access token ATAU API key (header `Authorization: ApiKey <key>`
// atau `X-API-Key`). Scope API key: GET/HEAD/OPTIONS butuh read, lainnya write.
func (a *Authenticator) Any(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw, ok := bearer(r); ok {
			a.serveBearer(w, r, raw, next)
			return
		}
		key, ok := apiKeyFromRequest(r)
		if !ok || a.apiKeys == nil {
			httpx.WriteError(w, r, errUnauthenticated)
			return
		}
		p, err := a.apiKeys.AuthenticateAPIKey(r.Context(), key)
		if err != nil {
			if errors.Is(err, domain.ErrAPIKeyInvalid) {
				httpx.WriteError(w, r, errAPIKeyInvalid)
				return
			}
			httpx.WriteError(w, r, mapError(err))
			return
		}
		if !allow(w, r, a.keyLimiter, "apikey:"+p.APIKeyID.String()) {
			return
		}
		need := domain.ScopeWrite
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			need = domain.ScopeRead
		}
		if !hasScope(p.Scopes, need) {
			httpx.WriteError(w, r, errInsufficScope)
			return
		}
		next.ServeHTTP(w, withPrincipal(r, p))
	})
}

func hasScope(scopes []domain.Scope, s domain.Scope) bool {
	for _, x := range scopes {
		if x == s {
			return true
		}
	}
	return false
}

func (a *Authenticator) serveBearer(w http.ResponseWriter, r *http.Request, raw string, next http.Handler) {
	userID, sessionID, roles, err := a.tokens.VerifyRoles(raw)
	if err != nil {
		httpx.WriteError(w, r, errUnauthenticated)
		return
	}
	if err := a.sessions.AuthenticateSession(r.Context(), sessionID); err != nil {
		if errors.Is(err, domain.ErrSessionInvalid) || errors.Is(err, domain.ErrSessionNotFound) {
			httpx.WriteError(w, r, errSessionRevoked)
			return
		}
		httpx.WriteError(w, r, mapError(err))
		return
	}
	next.ServeHTTP(w, withPrincipal(r, app.Principal{UserID: userID, SessionID: sessionID, Roles: roles}))
}

// RequireRole menolak request bila roles di access token tidak memuat role.
// Use case admin tetap memeriksa ulang role dari DB (token bisa basi s.d. TTL).
func RequireRole(role domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFromContext(r.Context())
			if !ok {
				httpx.WriteError(w, r, errUnauthenticated)
				return
			}
			if p.APIKeyID != uuid.Nil || !domain.HasRole(p.Roles, role) {
				httpx.WriteError(w, r, errForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearer(r *http.Request) (string, bool) {
	scheme, raw, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	raw = strings.TrimSpace(raw)
	if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" {
		return "", false
	}
	return raw, true
}

func apiKeyFromRequest(r *http.Request) (string, bool) {
	if scheme, raw, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "ApiKey") {
		raw = strings.TrimSpace(raw)
		return raw, raw != ""
	}
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k, true
	}
	return "", false
}

// tokenOnlyVerifier mengadaptasi TokenVerifier lama (tanpa roles).
type tokenOnlyVerifier struct{ v TokenVerifier }

func (t tokenOnlyVerifier) VerifyRoles(token string) (uuid.UUID, uuid.UUID, []domain.Role, error) {
	u, s, err := t.v.Verify(token)
	return u, s, nil, err
}
