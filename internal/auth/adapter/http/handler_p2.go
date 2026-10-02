package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/platform/logger"
	"go-auth-clean/internal/platform/middleware"
	"go-auth-clean/internal/shared/pagination"
)

// P2Service adalah port use case fitur P2 (2FA, OAuth, API key, admin).
type P2Service interface {
	LoginMFA(ctx context.Context, in app.LoginMFAInput) (app.LoginResult, error)
	SetupMFA(ctx context.Context, userID uuid.UUID) (app.MFASetup, error)
	EnableMFA(ctx context.Context, in app.MFACodeInput) ([]string, error)
	DisableMFA(ctx context.Context, in app.DisableMFAInput) error
	RegenerateRecoveryCodes(ctx context.Context, in app.MFACodeInput) ([]string, error)
	GetMFAStatus(ctx context.Context, userID uuid.UUID) (app.MFAStatus, error)

	OAuthEnabled(provider string) bool
	StartOAuth(ctx context.Context, provider string) (app.OAuthStart, error)
	OAuthCallback(ctx context.Context, in app.OAuthCallbackInput) (app.OAuthCallbackResult, error)
	ExchangeOAuthCode(ctx context.Context, code string, meta app.RequestMeta) (app.LoginResult, error)
	ListIdentities(ctx context.Context, userID uuid.UUID) ([]domain.ExternalIdentity, error)

	CreateAPIKey(ctx context.Context, in app.CreateAPIKeyInput) (app.CreatedAPIKey, error)
	ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, in app.RevokeAPIKeyInput) error

	ListUsers(ctx context.Context, actorID uuid.UUID, f domain.UserFilter) ([]domain.User, error)
	GetUser(ctx context.Context, actorID, targetID uuid.UUID) (*domain.User, error)
	SuspendUser(ctx context.Context, in app.AdminActionInput) error
	ActivateUser(ctx context.Context, in app.AdminActionInput) error
	GrantRole(ctx context.Context, in app.RoleChangeInput) error
	RevokeRole(ctx context.Context, in app.RoleChangeInput) error
	UserAuditLog(ctx context.Context, actorID, targetID uuid.UUID, after *domain.AuditKeyset, limit int) ([]domain.AuditEvent, error)
}

// CookieSealer mengenkripsi cookie state OAuth (AEAD). Dipenuhi crypto.SecretBox.
type CookieSealer interface {
	EncryptString(plaintext string, aad []byte) (string, error)
	DecryptString(ciphertext string, aad []byte) (string, error)
}

// P2Options berisi konfigurasi delivery fitur P2.
type P2Options struct {
	// Sealer wajib bila OAuth aktif (cookie state/PKCE/nonce terenkripsi).
	Sealer CookieSealer
	// OAuthRedirectURL adalah halaman frontend tujuan setelah callback OAuth.
	// Sukses login: <url>#code=...; link: <url>?linked=google; gagal: <url>?error=CODE.
	OAuthRedirectURL string
	// SecureCookie menandai cookie OAuth Secure (wajib di HTTPS/produksi).
	SecureCookie bool
	// MFAAttempts membatasi jumlah percobaan kode per mfa_token (per challenge).
	// Limiter atau ChallengeID nil = tidak dibatasi (mis. di test).
	MFAAttempts MFAAttemptGuard
}

// MFAAttemptGuard: limiter percobaan POST /auth/login/2fa per challenge.
// Challenge (mfa_token) berupa JWT stateless, jadi key-nya adalah jti token
// yang sudah diverifikasi; setelah kuota habis challenge dianggap hangus.
type MFAAttemptGuard struct {
	// Limiter sebaiknya token bucket dengan Burst = jumlah percobaan maksimum
	// dan refill yang lebih lambat dari umur mfa_token.
	Limiter middleware.RateLimiter
	// ChallengeID mengembalikan jti dari mfa_token yang valid (dipenuhi
	// security.MFATokenIssuer). Token tidak valid tidak dihitung; service
	// yang menolaknya dengan MFA_TOKEN_INVALID.
	ChallengeID func(token string) (string, error)
}

// errMFAChallengeExpired: kuota percobaan challenge habis -> login ulang.
var errMFAChallengeExpired = &httpx.Error{
	Status: http.StatusUnauthorized, Code: "MFA_CHALLENGE_EXPIRED",
	Message: "terlalu banyak percobaan kode 2FA, silakan login ulang",
}

// allowMFAAttempt mengonsumsi satu percobaan untuk challenge token.
func (h *P2Handler) allowMFAAttempt(r *http.Request, token string) bool {
	g := h.opt.MFAAttempts
	if g.Limiter == nil || g.ChallengeID == nil {
		return true
	}
	jti, err := g.ChallengeID(token)
	if err != nil || jti == "" {
		return true
	}
	ok, _ := g.Limiter.Allow(r.Context(), "auth:mfa_challenge:"+jti)
	return ok
}

const (
	oauthCookieName = "gac_oauth"
	oauthFlowTTL    = 10 * time.Minute
)

// P2Handler menangani endpoint 2FA, OAuth, API key dan admin.
type P2Handler struct {
	*Handler
	p2  P2Service
	opt P2Options
}

func NewP2Handler(base *Handler, svc P2Service, opt P2Options) *P2Handler {
	return &P2Handler{Handler: base, p2: svc, opt: opt}
}

// P2Middleware adalah middleware autentikasi untuk route P2.
type P2Middleware struct {
	// User: access token saja (pengelolaan akun tidak boleh lewat API key).
	User func(http.Handler) http.Handler
	// Any: access token ATAU API key.
	Any func(http.Handler) http.Handler
	// Admin: User + RequireRole(admin).
	Admin func(http.Handler) http.Handler
}

// NewP2Middleware merakit P2Middleware dari Authenticator.
func NewP2Middleware(a *Authenticator) P2Middleware {
	admin := RequireRole(domain.RoleAdmin)
	return P2Middleware{User: a.User, Any: a.Any, Admin: func(next http.Handler) http.Handler { return a.User(admin(next)) }}
}

// Routes mendaftarkan endpoint P2. Route OAuth hanya didaftarkan bila provider
// dikonfigurasi (selain itu jatuh ke fallback 404).
func (h *P2Handler) Routes(mux *http.ServeMux, mw P2Middleware) {
	u := func(f http.HandlerFunc) http.Handler { return mw.User(f) }
	a := func(f http.HandlerFunc) http.Handler { return mw.Admin(f) }

	mux.Handle("GET /api/v1/auth/whoami", mw.Any(http.HandlerFunc(h.whoami)))

	mux.HandleFunc("POST /api/v1/auth/login/2fa", h.loginMFA)
	mux.Handle("GET /api/v1/users/me/2fa", u(h.mfaStatus))
	mux.Handle("POST /api/v1/users/me/2fa/setup", u(h.setupMFA))
	mux.Handle("POST /api/v1/users/me/2fa/enable", u(h.enableMFA))
	mux.Handle("POST /api/v1/users/me/2fa/disable", u(h.disableMFA))
	mux.Handle("POST /api/v1/users/me/2fa/recovery-codes", u(h.regenerateRecoveryCodes))

	mux.Handle("GET /api/v1/users/me/api-keys", u(h.listAPIKeys))
	mux.Handle("POST /api/v1/users/me/api-keys", u(h.createAPIKey))
	mux.Handle("DELETE /api/v1/users/me/api-keys/{id}", u(h.revokeAPIKey))

	mux.Handle("GET /api/v1/admin/users", a(h.adminListUsers))
	mux.Handle("GET /api/v1/admin/users/{id}", a(h.adminGetUser))
	mux.Handle("POST /api/v1/admin/users/{id}/suspend", a(h.adminSuspend))
	mux.Handle("POST /api/v1/admin/users/{id}/activate", a(h.adminActivate))
	mux.Handle("POST /api/v1/admin/users/{id}/roles", a(h.adminGrantRole))
	mux.Handle("DELETE /api/v1/admin/users/{id}/roles/{role}", a(h.adminRevokeRole))
	mux.Handle("GET /api/v1/admin/users/{id}/security-events", a(h.adminSecurityEvents))

	if h.p2.OAuthEnabled(domain.ProviderGoogle) && h.opt.Sealer != nil {
		mux.HandleFunc("GET /api/v1/auth/oauth/google/start", h.oauthStart)
		mux.HandleFunc("GET /api/v1/auth/oauth/google/callback", h.oauthCallback)
		mux.HandleFunc("POST /api/v1/auth/oauth/exchange", h.oauthExchange)
		mux.Handle("POST /api/v1/users/me/identities/google/link", u(h.oauthLinkStart))
		mux.Handle("GET /api/v1/users/me/identities", u(h.listIdentities))
	}
}

// writeLogin menulis hasil login: token + profil, atau challenge 2FA.
func writeLogin(w http.ResponseWriter, res app.LoginResult, now time.Time) {
	if res.MFARequired {
		httpx.Data(w, http.StatusOK, MFAChallengeResponse{MFARequired: true, MFAToken: res.MFAToken, ExpiresAt: res.MFATokenExpiresAt})
		return
	}
	httpx.Data(w, http.StatusOK, LoginResponse{
		TokenResponse: toTokenResponse(res.Tokens, now),
		User:          toUserResponse(res.User),
	})
}

func pathUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, invalidParam(name, name+" harus UUID")
	}
	return id, nil
}

// whoami godoc
//
//	@Summary		Identify the caller
//	@Description	Returns the authenticated principal. Accepts an access token OR an API key (`X-API-Key: <key>` or `Authorization: ApiKey <key>`); useful to test an API key. API keys need the read scope here.
//	@Tags			auth
//	@Produce		json
//	@Security		BearerAuth
//	@Security		ApiKeyAuth
//	@Success		200	{object}	WhoAmIEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID / API_KEY_INVALID"
//	@Failure		403	{object}	httpx.ErrorResponse	"INSUFFICIENT_SCOPE"
//	@Failure		429	{object}	httpx.ErrorResponse	"RATE_LIMITED"
//	@Router			/auth/whoami [get]
func (h *P2Handler) whoami(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, errUnauthenticated)
		return
	}
	out := WhoAmIResponse{UserID: p.UserID, AuthMethod: "access_token", Roles: roleStrings(p.Roles), Scopes: []string{}}
	if p.APIKeyID != uuid.Nil {
		id := p.APIKeyID
		out.AuthMethod, out.APIKeyID = "api_key", &id
		for _, sc := range p.Scopes {
			out.Scopes = append(out.Scopes, string(sc))
		}
	} else {
		id := p.SessionID
		out.SessionID = &id
	}
	httpx.Data(w, http.StatusOK, out)
}

// ---------- 2FA ----------

// loginMFA godoc
//
//	@Summary		Complete login with a 2FA code
//	@Description	Second login step when /auth/login answered mfa_required=true. Accepts a 6-digit TOTP code or a recovery code. Failures count toward the account lockout. Each mfa_token (challenge) allows at most 5 attempts; after that it is invalidated (401 MFA_CHALLENGE_EXPIRED) and the client must log in again.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LoginMFARequest	true	"mfa_token and code"
//	@Success		200		{object}	LoginEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401		{object}	httpx.ErrorResponse	"MFA_TOKEN_INVALID / INVALID_MFA_CODE / MFA_CHALLENGE_EXPIRED"
//	@Failure		403		{object}	httpx.ErrorResponse	"ACCOUNT_SUSPENDED / ACCOUNT_INACTIVE"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429		{object}	httpx.ErrorResponse	"ACCOUNT_LOCKED / RATE_LIMITED"
//	@Router			/auth/login/2fa [post]
func (h *P2Handler) loginMFA(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[LoginMFARequest](h.Handler, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.allowMFAAttempt(r, req.MFAToken) {
		logger.FromContext(r.Context()).WarnContext(r.Context(), "mfa challenge attempts exhausted")
		httpx.WriteError(w, r, errMFAChallengeExpired)
		return
	}
	res, err := h.p2.LoginMFA(r.Context(), app.LoginMFAInput{MFAToken: req.MFAToken, Code: req.Code, Meta: meta(r)})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	writeLogin(w, res, h.clock.Now())
}

// mfaStatus godoc
//
//	@Summary	Get 2FA status
//	@Tags		2fa
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	MFAStatusEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Router		/users/me/2fa [get]
func (h *P2Handler) mfaStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.p2.GetMFAStatus(r.Context(), identity(r).UserID)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toMFAStatusResponse(st))
}

// setupMFA godoc
//
//	@Summary		Start 2FA enrollment
//	@Description	Generates a new TOTP secret (stored encrypted, status pending) and returns it with an otpauth:// URI to render as a QR code. Calling again replaces a pending secret.
//	@Tags			2fa
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	MFASetupEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		409	{object}	httpx.ErrorResponse	"MFA_ALREADY_ENABLED"
//	@Router			/users/me/2fa/setup [post]
func (h *P2Handler) setupMFA(w http.ResponseWriter, r *http.Request) {
	userID := identity(r).UserID
	st, err := h.p2.GetMFAStatus(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	if st.Enabled {
		httpx.WriteError(w, r, mapError(domain.ErrMFAAlreadyEnabled))
		return
	}
	setup, err := h.p2.SetupMFA(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.Data(w, http.StatusOK, MFASetupResponse{Secret: setup.Secret, OTPAuthURI: setup.OTPAuthURI})
}

// enableMFA godoc
//
//	@Summary		Confirm and enable 2FA
//	@Description	Verifies the first code from the authenticator app, enables 2FA, and returns 10 single-use recovery codes (shown only once; stored hashed). Other sessions are revoked.
//	@Tags			2fa
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		MFACodeRequest	true	"TOTP code"
//	@Success		200		{object}	RecoveryCodesEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401		{object}	httpx.ErrorResponse	"INVALID_MFA_CODE / UNAUTHENTICATED"
//	@Failure		409		{object}	httpx.ErrorResponse	"MFA_SETUP_REQUIRED / MFA_ALREADY_ENABLED"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429		{object}	httpx.ErrorResponse	"RATE_LIMITED"
//	@Router			/users/me/2fa/enable [post]
func (h *P2Handler) enableMFA(w http.ResponseWriter, r *http.Request) {
	h.withCode(w, r, "mfa", h.p2.EnableMFA)
}

// regenerateRecoveryCodes godoc
//
//	@Summary		Regenerate recovery codes
//	@Description	Replaces all recovery codes (requires a current TOTP code). The new codes are shown only once.
//	@Tags			2fa
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		MFACodeRequest	true	"TOTP code"
//	@Success		200		{object}	RecoveryCodesEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401		{object}	httpx.ErrorResponse	"INVALID_MFA_CODE / UNAUTHENTICATED"
//	@Failure		409		{object}	httpx.ErrorResponse	"MFA_NOT_ENABLED"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429		{object}	httpx.ErrorResponse	"RATE_LIMITED"
//	@Router			/users/me/2fa/recovery-codes [post]
func (h *P2Handler) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	h.withCode(w, r, "mfa", h.p2.RegenerateRecoveryCodes)
}

func (h *P2Handler) withCode(w http.ResponseWriter, r *http.Request, scope string, fn func(context.Context, app.MFACodeInput) ([]string, error)) {
	req, err := decodeAndValidate[MFACodeRequest](h.Handler, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id := identity(r)
	if !h.allowUser(w, r, scope, id.UserID) {
		return
	}
	codes, err := fn(r.Context(), app.MFACodeInput{UserID: id.UserID, SessionID: id.SessionID, Code: req.Code, Meta: meta(r)})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.Data(w, http.StatusOK, RecoveryCodesResponse{RecoveryCodes: codes})
}

// disableMFA godoc
//
//	@Summary		Disable 2FA
//	@Description	Requires the current password AND a TOTP or recovery code. Other sessions are revoked.
//	@Tags			2fa
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body	DisableMFARequest	true	"Password and code"
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401	{object}	httpx.ErrorResponse	"INVALID_CREDENTIALS / INVALID_MFA_CODE / UNAUTHENTICATED"
//	@Failure		409	{object}	httpx.ErrorResponse	"MFA_NOT_ENABLED"
//	@Failure		422	{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429	{object}	httpx.ErrorResponse	"RATE_LIMITED"
//	@Router			/users/me/2fa/disable [post]
func (h *P2Handler) disableMFA(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[DisableMFARequest](h.Handler, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id := identity(r)
	if !h.allowUser(w, r, "mfa", id.UserID) {
		return
	}
	err = h.p2.DisableMFA(r.Context(), app.DisableMFAInput{
		UserID: id.UserID, SessionID: id.SessionID, Password: req.Password, Code: req.Code, Meta: meta(r),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- API keys ----------

// listAPIKeys godoc
//
//	@Summary		List API keys
//	@Description	Returns metadata of the current user's API keys (never the secret).
//	@Tags			api-keys
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	APIKeyListEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Router			/users/me/api-keys [get]
func (h *P2Handler) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.p2.ListAPIKeys(r.Context(), identity(r).UserID)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	now := h.clock.Now()
	out := make([]APIKeyResponse, 0, len(keys))
	for i := range keys {
		out = append(out, toAPIKeyResponse(keys[i], now))
	}
	httpx.Data(w, http.StatusOK, out)
}

// createAPIKey godoc
//
//	@Summary		Create an API key
//	@Description	Creates a key in the format gac_<env>_<prefix>_<secret>. The full key is returned ONLY in this response; the server stores just the prefix and a SHA-256 hash. Send it as `X-API-Key: <key>` or `Authorization: ApiKey <key>`.
//	@Tags			api-keys
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		CreateAPIKeyRequest	true	"Name, scopes, expiry"
//	@Success		201		{object}	CreatedAPIKeyEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		409		{object}	httpx.ErrorResponse	"API_KEY_LIMIT"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_API_KEY_NAME / INVALID_SCOPE / INVALID_EXPIRY"
//	@Router			/users/me/api-keys [post]
func (h *P2Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[CreateAPIKeyRequest](h.Handler, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id := identity(r)
	created, err := h.p2.CreateAPIKey(r.Context(), app.CreateAPIKeyInput{
		UserID: id.UserID, SessionID: id.SessionID, Name: req.Name, Scopes: req.Scopes,
		ExpiresIn: time.Duration(req.ExpiresInDays) * 24 * time.Hour, Meta: meta(r),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.Data(w, http.StatusCreated, CreatedAPIKeyResponse{
		APIKeyResponse: toAPIKeyResponse(created.APIKey, h.clock.Now()), Key: created.Key,
	})
}

// revokeAPIKey godoc
//
//	@Summary		Revoke an API key
//	@Description	Revokes one of the current user's API keys immediately. Keys of other users return 404.
//	@Tags			api-keys
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path	string	true	"API key id"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_PARAMETER"
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		404	{object}	httpx.ErrorResponse	"API_KEY_NOT_FOUND"
//	@Router			/users/me/api-keys/{id} [delete]
func (h *P2Handler) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	keyID, err := pathUUID(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id := identity(r)
	err = h.p2.RevokeAPIKey(r.Context(), app.RevokeAPIKeyInput{UserID: id.UserID, SessionID: id.SessionID, KeyID: keyID, Meta: meta(r)})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Admin ----------

// adminListUsers godoc
//
//	@Summary		List users (admin)
//	@Description	Keyset-paginated user list, newest first. q searches email/name (substring), status filters by lifecycle status.
//	@Tags			admin
//	@Produce		json
//	@Security		BearerAuth
//	@Param			q		query		string	false	"Search email or name"
//	@Param			status	query		string	false	"Status filter"	Enums(pending_verification, active, suspended)
//	@Param			limit	query		int		false	"Page size (1-100, default 20)"
//	@Param			cursor	query		string	false	"Opaque cursor from meta.next_cursor"
//	@Success		200		{object}	UserListEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_CURSOR / INVALID_PARAMETER"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		403		{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Router			/admin/users [get]
func (h *P2Handler) adminListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := pagination.ParseLimit(q.Get("limit"))
	if err != nil {
		httpx.WriteError(w, r, invalidParam("limit", err.Error()))
		return
	}
	status, err := domain.ParseUserStatus(q.Get("status"))
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	search := strings.TrimSpace(q.Get("q"))
	if len(search) > 254 {
		httpx.WriteError(w, r, invalidParam("q", "q maksimal 254 karakter"))
		return
	}
	filter := pagination.FilterHash("admin-users", "q="+search, "status="+string(status))
	f := domain.UserFilter{Query: search, Status: status, Limit: limit + 1}
	if c := q.Get("cursor"); c != "" {
		cur, err := pagination.Decode(c, filter)
		if err != nil {
			httpx.WriteError(w, r, mapError(err))
			return
		}
		f.After = &domain.UserKeyset{CreatedAt: cur.Time, ID: cur.ID}
	}
	users, err := h.p2.ListUsers(r.Context(), identity(r).UserID, f)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	page, pm := pagination.Page(users, limit, filter, func(u domain.User) (time.Time, uuid.UUID) { return u.CreatedAt, u.ID })
	out := make([]UserResponse, 0, len(page))
	for i := range page {
		out = append(out, toUserResponse(&page[i]))
	}
	httpx.List(w, out, pm)
}

// adminGetUser godoc
//
//	@Summary	Get a user (admin)
//	@Tags		admin
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"User id"	format(uuid)
//	@Success	200	{object}	UserEnvelope
//	@Failure	400	{object}	httpx.ErrorResponse	"INVALID_PARAMETER"
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure	403	{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Failure	404	{object}	httpx.ErrorResponse	"USER_NOT_FOUND"
//	@Router		/admin/users/{id} [get]
func (h *P2Handler) adminGetUser(w http.ResponseWriter, r *http.Request) {
	target, err := pathUUID(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, err := h.p2.GetUser(r.Context(), identity(r).UserID, target)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toUserResponse(u))
}

// adminSuspend godoc
//
//	@Summary		Suspend (lock) a user (admin)
//	@Description	Sets status to suspended and revokes all of the user's sessions and API keys. Admins cannot suspend themselves.
//	@Tags			admin
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path	string	true	"User id"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_PARAMETER"
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		403	{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Failure		404	{object}	httpx.ErrorResponse	"USER_NOT_FOUND"
//	@Failure		409	{object}	httpx.ErrorResponse	"CANNOT_MODIFY_SELF / INVALID_STATUS_TRANSITION"
//	@Router			/admin/users/{id}/suspend [post]
func (h *P2Handler) adminSuspend(w http.ResponseWriter, r *http.Request) {
	h.adminAction(w, r, h.p2.SuspendUser)
}

// adminActivate godoc
//
//	@Summary	Reactivate (unlock) a suspended user (admin)
//	@Tags		admin
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path	string	true	"User id"	format(uuid)
//	@Success	204
//	@Failure	400	{object}	httpx.ErrorResponse	"INVALID_PARAMETER"
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure	403	{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Failure	404	{object}	httpx.ErrorResponse	"USER_NOT_FOUND"
//	@Failure	409	{object}	httpx.ErrorResponse	"CANNOT_MODIFY_SELF / INVALID_STATUS_TRANSITION"
//	@Router		/admin/users/{id}/activate [post]
func (h *P2Handler) adminActivate(w http.ResponseWriter, r *http.Request) {
	h.adminAction(w, r, h.p2.ActivateUser)
}

func (h *P2Handler) adminAction(w http.ResponseWriter, r *http.Request, fn func(context.Context, app.AdminActionInput) error) {
	target, err := pathUUID(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := fn(r.Context(), app.AdminActionInput{ActorID: identity(r).UserID, TargetID: target, Meta: meta(r)}); err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminGrantRole godoc
//
//	@Summary		Grant a role (admin)
//	@Description	Idempotent. Granting "user" is a no-op (every user has it implicitly). The new role appears in the target's access token after its next refresh.
//	@Tags			admin
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path	string		true	"User id"	format(uuid)
//	@Param			body	body	RoleRequest	true	"Role"
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_JSON / INVALID_PARAMETER"
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		403	{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Failure		404	{object}	httpx.ErrorResponse	"USER_NOT_FOUND"
//	@Failure		422	{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_ROLE"
//	@Router			/admin/users/{id}/roles [post]
func (h *P2Handler) adminGrantRole(w http.ResponseWriter, r *http.Request) {
	target, err := pathUUID(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	req, err := decodeAndValidate[RoleRequest](h.Handler, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	err = h.p2.GrantRole(r.Context(), app.RoleChangeInput{ActorID: identity(r).UserID, TargetID: target, Role: req.Role, Meta: meta(r)})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminRevokeRole godoc
//
//	@Summary		Revoke a role (admin)
//	@Description	Idempotent. The implicit "user" role cannot be revoked; admins cannot revoke their own admin role.
//	@Tags			admin
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path	string	true	"User id"	format(uuid)
//	@Param			role	path	string	true	"Role"		Enums(admin)
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_PARAMETER"
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		403	{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Failure		404	{object}	httpx.ErrorResponse	"USER_NOT_FOUND"
//	@Failure		409	{object}	httpx.ErrorResponse	"CANNOT_MODIFY_SELF"
//	@Failure		422	{object}	httpx.ErrorResponse	"INVALID_ROLE"
//	@Router			/admin/users/{id}/roles/{role} [delete]
func (h *P2Handler) adminRevokeRole(w http.ResponseWriter, r *http.Request) {
	target, err := pathUUID(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	err = h.p2.RevokeRole(r.Context(), app.RoleChangeInput{ActorID: identity(r).UserID, TargetID: target, Role: r.PathValue("role"), Meta: meta(r)})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminSecurityEvents godoc
//
//	@Summary	List a user's security events (admin)
//	@Tags		admin
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id		path		string	true	"User id"	format(uuid)
//	@Param		limit	query		int		false	"Page size (1-100, default 20)"
//	@Param		cursor	query		string	false	"Opaque cursor from meta.next_cursor"
//	@Success	200		{object}	SecurityEventListEnvelope
//	@Failure	400		{object}	httpx.ErrorResponse	"INVALID_CURSOR / INVALID_PARAMETER"
//	@Failure	401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure	403		{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Failure	404		{object}	httpx.ErrorResponse	"USER_NOT_FOUND"
//	@Router		/admin/users/{id}/security-events [get]
func (h *P2Handler) adminSecurityEvents(w http.ResponseWriter, r *http.Request) {
	target, err := pathUUID(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	q := r.URL.Query()
	limit, err := pagination.ParseLimit(q.Get("limit"))
	if err != nil {
		httpx.WriteError(w, r, invalidParam("limit", err.Error()))
		return
	}
	filter := pagination.FilterHash("admin-security-events", target.String())
	var after *domain.AuditKeyset
	if c := q.Get("cursor"); c != "" {
		cur, err := pagination.Decode(c, filter)
		if err != nil {
			httpx.WriteError(w, r, mapError(err))
			return
		}
		after = &domain.AuditKeyset{OccurredAt: cur.Time, ID: cur.ID}
	}
	events, err := h.p2.UserAuditLog(r.Context(), identity(r).UserID, target, after, limit+1)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	page, pm := pagination.Page(events, limit, filter, func(e domain.AuditEvent) (time.Time, uuid.UUID) { return e.OccurredAt, e.ID })
	out := make([]SecurityEventResponse, 0, len(page))
	for i := range page {
		out = append(out, toSecurityEventResponse(page[i]))
	}
	httpx.List(w, out, pm)
}

// ---------- OAuth (Google OIDC + PKCE) ----------

// oauthFlow disimpan di cookie terenkripsi antara start dan callback.
type oauthFlow struct {
	State    string    `json:"s"`
	Verifier string    `json:"v"`
	Nonce    string    `json:"n"`
	Link     uuid.UUID `json:"l,omitempty"`
	Exp      int64     `json:"e"`
}

var oauthCookieAAD = []byte("oauth-flow:" + domain.ProviderGoogle)

const oauthCookiePath = "/api/v1/auth/oauth/google"

func (h *P2Handler) beginOAuth(w http.ResponseWriter, r *http.Request, link uuid.UUID) (string, error) {
	st, err := h.p2.StartOAuth(r.Context(), domain.ProviderGoogle)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(oauthFlow{State: st.State, Verifier: st.Verifier, Nonce: st.Nonce, Link: link,
		Exp: h.clock.Now().Add(oauthFlowTTL).Unix()})
	if err != nil {
		return "", err
	}
	sealed, err := h.opt.Sealer.EncryptString(string(raw), oauthCookieAAD)
	if err != nil {
		return "", err
	}
	// SameSite=Lax wajib: callback adalah navigasi top-level dari accounts.google.com.
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookieName, Value: sealed, Path: oauthCookiePath, MaxAge: int(oauthFlowTTL.Seconds()),
		HttpOnly: true, Secure: h.opt.SecureCookie, SameSite: http.SameSiteLaxMode,
	})
	return st.AuthURL, nil
}

func (h *P2Handler) clearOAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookieName, Value: "", Path: oauthCookiePath, MaxAge: -1,
		HttpOnly: true, Secure: h.opt.SecureCookie, SameSite: http.SameSiteLaxMode,
	})
}

// oauthStart godoc
//
//	@Summary		Start Google sign-in
//	@Description	Browser endpoint. Redirects (302) to Google with state, PKCE S256 code_challenge and nonce; these are kept in an encrypted HttpOnly cookie. Returns 404 when Google OAuth is not configured.
//	@Tags			oauth
//	@Success		302
//	@Failure		404	{object}	httpx.ErrorResponse	"NOT_FOUND"
//	@Router			/auth/oauth/google/start [get]
func (h *P2Handler) oauthStart(w http.ResponseWriter, r *http.Request) {
	authURL, err := h.beginOAuth(w, r, uuid.Nil)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, authURL, http.StatusFound)
}

// oauthLinkStart godoc
//
//	@Summary		Start linking a Google account
//	@Description	Returns the Google consent URL for the logged-in user and sets the encrypted flow cookie (call with credentials). After consent the callback links the identity and redirects to the frontend with ?linked=google.
//	@Tags			oauth
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	OAuthStartEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		404	{object}	httpx.ErrorResponse	"NOT_FOUND"
//	@Router			/users/me/identities/google/link [post]
func (h *P2Handler) oauthLinkStart(w http.ResponseWriter, r *http.Request) {
	authURL, err := h.beginOAuth(w, r, identity(r).UserID)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.Data(w, http.StatusOK, OAuthStartResponse{AuthURL: authURL})
}

// oauthCallback godoc
//
//	@Summary		Google OAuth callback
//	@Description	Browser endpoint (redirect URI registered at Google). Verifies state against the cookie, exchanges the code with the PKCE verifier and validates the id_token (signature, iss, aud, exp, nonce). Always redirects (302) to the frontend: login success -> <redirect>#code=<one-time code> (exchange via POST /auth/oauth/exchange), link success -> ?linked=google, failure -> ?error=<CODE>. An existing password account with the same email is never auto-linked (OAUTH_ACCOUNT_EXISTS).
//	@Tags			oauth
//	@Param			code	query	string	false	"Authorization code"
//	@Param			state	query	string	true	"State"
//	@Param			error	query	string	false	"Error from provider"
//	@Success		302
//	@Failure		404	{object}	httpx.ErrorResponse	"NOT_FOUND"
//	@Router			/auth/oauth/google/callback [get]
func (h *P2Handler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	flow, ok := h.readOAuthCookie(r)
	h.clearOAuthCookie(w)
	q := r.URL.Query()
	state := q.Get("state")
	if !ok || state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(flow.State)) != 1 {
		h.redirectError(w, r, "OAUTH_STATE_INVALID")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		h.redirectError(w, r, "OAUTH_CANCELED")
		return
	}
	res, err := h.p2.OAuthCallback(r.Context(), app.OAuthCallbackInput{
		Provider: domain.ProviderGoogle, Code: q.Get("code"), Verifier: flow.Verifier, Nonce: flow.Nonce,
		LinkUserID: flow.Link, Meta: meta(r),
	})
	if err != nil {
		code := "INTERNAL"
		if he, ok := errors.AsType[*httpx.Error](mapError(err)); ok {
			code = he.Code
		} else {
			logger.FromContext(r.Context()).ErrorContext(r.Context(), "oauth callback", slog.Any("error", err))
		}
		h.redirectError(w, r, code)
		return
	}
	if res.Linked {
		h.redirect(w, r, url.Values{"linked": {domain.ProviderGoogle}}, "")
		return
	}
	// Fragment (#) tidak dikirim ke server/log dan tidak bocor lewat Referer.
	h.redirect(w, r, nil, "code="+url.QueryEscape(res.LoginCode))
}

func (h *P2Handler) readOAuthCookie(r *http.Request) (oauthFlow, bool) {
	c, err := r.Cookie(oauthCookieName)
	if err != nil || c.Value == "" {
		return oauthFlow{}, false
	}
	raw, err := h.opt.Sealer.DecryptString(c.Value, oauthCookieAAD)
	if err != nil {
		return oauthFlow{}, false
	}
	var f oauthFlow
	if err := json.Unmarshal([]byte(raw), &f); err != nil || f.State == "" || h.clock.Now().Unix() > f.Exp {
		return oauthFlow{}, false
	}
	return f, true
}

func (h *P2Handler) redirectError(w http.ResponseWriter, r *http.Request, code string) {
	h.redirect(w, r, url.Values{"error": {code}}, "")
}

func (h *P2Handler) redirect(w http.ResponseWriter, r *http.Request, q url.Values, fragment string) {
	u, err := url.Parse(h.opt.OAuthRedirectURL)
	if err != nil || h.opt.OAuthRedirectURL == "" {
		httpx.WriteError(w, r, errors.New("oauth redirect url not configured"))
		return
	}
	if q != nil {
		u.RawQuery = q.Encode()
	}
	u.Fragment = fragment
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// oauthExchange godoc
//
//	@Summary		Exchange OAuth one-time code for tokens
//	@Description	Exchanges the single-use code (valid ~60s) delivered to the frontend by the callback redirect. If the account has 2FA, responds with mfa_required=true and an mfa_token (continue with /auth/login/2fa).
//	@Tags			oauth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		OAuthExchangeRequest	true	"One-time code"
//	@Success		200		{object}	LoginEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON / LOGIN_CODE_INVALID"
//	@Failure		403		{object}	httpx.ErrorResponse	"ACCOUNT_SUSPENDED / ACCOUNT_INACTIVE"
//	@Failure		404		{object}	httpx.ErrorResponse	"NOT_FOUND"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429		{object}	httpx.ErrorResponse	"RATE_LIMITED"
//	@Router			/auth/oauth/exchange [post]
func (h *P2Handler) oauthExchange(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[OAuthExchangeRequest](h.Handler, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.p2.ExchangeOAuthCode(r.Context(), req.Code, meta(r))
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	writeLogin(w, res, h.clock.Now())
}

// listIdentities godoc
//
//	@Summary	List linked external accounts
//	@Tags		oauth
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	IdentityListEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Router		/users/me/identities [get]
func (h *P2Handler) listIdentities(w http.ResponseWriter, r *http.Request) {
	ids, err := h.p2.ListIdentities(r.Context(), identity(r).UserID)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	out := make([]IdentityResponse, 0, len(ids))
	for _, i := range ids {
		out = append(out, IdentityResponse{Provider: i.Provider, Email: i.Email, CreatedAt: i.CreatedAt})
	}
	httpx.Data(w, http.StatusOK, out)
}
