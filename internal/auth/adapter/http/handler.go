// Package http adalah delivery layer REST untuk bounded context auth.
package http

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/authctx"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/platform/middleware"
	"go-auth-clean/internal/shared/pagination"
)

// maxAuthBody: body endpoint auth kecil; batasi 16 KiB (anti abuse).
const maxAuthBody int64 = 16 << 10

// AuthService adalah port yang dibutuhkan handler (didefinisikan oleh consumer).
type AuthService interface {
	Register(ctx context.Context, in app.RegisterInput) (*domain.User, error)
	VerifyEmail(ctx context.Context, in app.VerifyEmailInput) (app.VerifyEmailResult, error)
	ResendVerification(ctx context.Context, email string) error
	ForgotPassword(ctx context.Context, email string, meta app.RequestMeta) error
	ResetPassword(ctx context.Context, in app.ResetPasswordInput) error
	Login(ctx context.Context, in app.LoginInput) (app.LoginResult, error)
	Refresh(ctx context.Context, in app.RefreshInput) (app.TokenPair, error)
	Logout(ctx context.Context, in app.LogoutInput) error
	LogoutAll(ctx context.Context, in app.LogoutAllInput) error
	Me(ctx context.Context, userID uuid.UUID) (*domain.User, error)
	UpdateProfile(ctx context.Context, in app.UpdateProfileInput) (*domain.User, error)
	ChangePassword(ctx context.Context, in app.ChangePasswordInput) error
	DeleteAccount(ctx context.Context, in app.DeleteAccountInput) error
	ListSessions(ctx context.Context, userID, currentSessionID uuid.UUID) ([]app.SessionInfo, error)
	RevokeSession(ctx context.Context, in app.RevokeSessionInput) error
	ListSecurityEvents(ctx context.Context, userID uuid.UUID, after *domain.AuditKeyset, limit int) ([]domain.AuditEvent, error)
}

type Validator interface {
	Struct(s any) error
}

type Clock interface {
	app.Clock
}

// Options berisi limiter tambahan. nil = tidak dibatasi (mis. di test).
type Options struct {
	// EmailLimiter dipakai per alamat email (login, verify, resend, forgot, reset)
	// untuk menahan brute force/spam yang tersebar dari banyak IP.
	EmailLimiter middleware.RateLimiter
	// UserLimiter dipakai per user untuk endpoint sensitif terautentikasi
	// (ganti password, hapus akun).
	UserLimiter middleware.RateLimiter
}

type Handler struct {
	svc      AuthService
	validate Validator
	clock    Clock
	opt      Options
}

func NewHandler(svc AuthService, v Validator, c Clock, opt Options) *Handler {
	return &Handler{svc: svc, validate: v, clock: c, opt: opt}
}

// Routes mendaftarkan endpoint. requireAuth membungkus endpoint yang butuh login.
func (h *Handler) Routes(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/verify-email", h.verifyEmail)
	mux.HandleFunc("POST /api/v1/auth/verify-email/resend", h.resendVerification)
	mux.HandleFunc("POST /api/v1/auth/password/forgot", h.forgotPassword)
	mux.HandleFunc("POST /api/v1/auth/password/reset", h.resetPassword)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)

	auth := func(f http.HandlerFunc) http.Handler { return requireAuth(f) }
	mux.Handle("POST /api/v1/auth/logout", auth(h.logout))
	mux.Handle("POST /api/v1/auth/logout-all", auth(h.logoutAll))
	mux.Handle("GET /api/v1/users/me", auth(h.me))
	mux.Handle("PATCH /api/v1/users/me", auth(h.updateProfile))
	mux.Handle("DELETE /api/v1/users/me", auth(h.deleteAccount))
	mux.Handle("PUT /api/v1/users/me/password", auth(h.changePassword))
	mux.Handle("POST /api/v1/users/me/password", auth(h.changePassword))
	mux.Handle("GET /api/v1/users/me/sessions", auth(h.listSessions))
	mux.Handle("DELETE /api/v1/users/me/sessions/{id}", auth(h.revokeSession))
	mux.Handle("GET /api/v1/users/me/security-events", auth(h.listSecurityEvents))
}

// decodeAndValidate adalah fungsi generic: satu helper untuk semua tipe request.
func decodeAndValidate[T any](h *Handler, w http.ResponseWriter, r *http.Request) (T, error) {
	var req T
	if err := httpx.DecodeLimit(w, r, &req, maxAuthBody); err != nil {
		return req, err
	}
	return req, h.validate.Struct(req)
}

// allow menerapkan limiter opsional; menulis 429 + Retry-After bila ditolak.
func allow(w http.ResponseWriter, r *http.Request, l middleware.RateLimiter, key string) bool {
	if l == nil || key == "" {
		return true
	}
	ok, retry := l.Allow(r.Context(), key)
	if ok {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(retry.Seconds())), 1)))
	httpx.WriteError(w, r, httpx.ErrRateLimited)
	return false
}

func emailKey(scope, email string) string {
	return "auth:" + scope + ":email:" + strings.ToLower(strings.TrimSpace(email))
}

func (h *Handler) allowEmail(w http.ResponseWriter, r *http.Request, scope, email string) bool {
	return allow(w, r, h.opt.EmailLimiter, emailKey(scope, email))
}

func (h *Handler) allowUser(w http.ResponseWriter, r *http.Request, scope string, userID uuid.UUID) bool {
	return allow(w, r, h.opt.UserLimiter, "auth:"+scope+":user:"+userID.String())
}

func meta(r *http.Request) app.RequestMeta {
	return app.RequestMeta{ClientIP: clientIP(r), UserAgent: r.UserAgent()}
}

func identity(r *http.Request) authctx.Identity {
	id, _ := authctx.FromContext(r.Context())
	return id
}

var acceptedOTP = AcceptedResponse{Message: "jika email terdaftar dan memenuhi syarat, kode telah dikirim"}

// register godoc
//
//	@Summary		Register a new user
//	@Description	Creates an account with status pending_verification and emails a 6-digit verification code. The user must verify before logging in.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		RegisterRequest	true	"Registration payload"
//	@Success		201		{object}	UserEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		409		{object}	httpx.ErrorResponse	"EMAIL_TAKEN"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_EMAIL / WEAK_PASSWORD / INVALID_FULL_NAME"
//	@Failure		429		{object}	httpx.ErrorResponse	"RATE_LIMITED"
//	@Router			/auth/register [post]
func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[RegisterRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user, err := h.svc.Register(r.Context(), app.RegisterInput{
		Email: req.Email, Password: req.Password, FullName: req.FullName, Meta: meta(r),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusCreated, toUserResponse(user))
}

// verifyEmail godoc
//
//	@Summary		Verify email with OTP
//	@Description	Activates a pending account using the 6-digit code sent by email. Idempotent: an already active account returns already_verified=true.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		VerifyEmailRequest	true	"Email and code"
//	@Success		200		{object}	VerifyEmailEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_OTP / OTP_EXPIRED / INVALID_JSON"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429		{object}	httpx.ErrorResponse	"OTP_TOO_MANY_ATTEMPTS / RATE_LIMITED"
//	@Router			/auth/verify-email [post]
func (h *Handler) verifyEmail(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[VerifyEmailRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.allowEmail(w, r, "otp", req.Email) {
		return
	}
	res, err := h.svc.VerifyEmail(r.Context(), app.VerifyEmailInput{Email: req.Email, Code: req.Code, Meta: meta(r)})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, VerifyEmailResponse{Verified: true, AlreadyVerified: res.AlreadyVerified})
}

// resendVerification godoc
//
//	@Summary		Resend email verification code
//	@Description	Always returns 202 (does not reveal whether the email exists). A new code is sent only for pending accounts, subject to a cooldown and a daily quota; the previous code is invalidated.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		EmailRequest	true	"Email"
//	@Success		202		{object}	AcceptedEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429		{object}	httpx.ErrorResponse	"RATE_LIMITED"
//	@Router			/auth/verify-email/resend [post]
func (h *Handler) resendVerification(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[EmailRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.allowEmail(w, r, "send", req.Email) {
		return
	}
	if err := h.svc.ResendVerification(r.Context(), req.Email); err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusAccepted, acceptedOTP)
}

// forgotPassword godoc
//
//	@Summary		Request password reset code
//	@Description	Always returns 202 (does not reveal whether the email exists). Sends a 6-digit reset code to active accounts.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		EmailRequest	true	"Email"
//	@Success		202		{object}	AcceptedEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429		{object}	httpx.ErrorResponse	"RATE_LIMITED"
//	@Router			/auth/password/forgot [post]
func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[EmailRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.allowEmail(w, r, "send", req.Email) {
		return
	}
	if err := h.svc.ForgotPassword(r.Context(), req.Email, meta(r)); err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusAccepted, acceptedOTP)
}

// resetPassword godoc
//
//	@Summary		Reset password with OTP
//	@Description	Sets a new password using the reset code and revokes ALL sessions of the user.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body	ResetPasswordRequest	true	"Email, code and new password"
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_OTP / OTP_EXPIRED / INVALID_JSON"
//	@Failure		422	{object}	httpx.ErrorResponse	"VALIDATION_FAILED / WEAK_PASSWORD"
//	@Failure		429	{object}	httpx.ErrorResponse	"OTP_TOO_MANY_ATTEMPTS / RATE_LIMITED"
//	@Router			/auth/password/reset [post]
func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[ResetPasswordRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.allowEmail(w, r, "otp", req.Email) {
		return
	}
	err = h.svc.ResetPassword(r.Context(), app.ResetPasswordInput{
		Email: req.Email, Code: req.Code, NewPassword: req.NewPassword, Meta: meta(r),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// login godoc
//
//	@Summary		Log in with email and password
//	@Description	Returns an access token (JWT), a rotating refresh token, the device session id and the user profile. Unverified accounts get 403 EMAIL_NOT_VERIFIED (only after a correct password). If the account has 2FA enabled the response is instead {"data":{"mfa_required":true,"mfa_token":"...","expires_at":"..."}} (MFAChallengeEnvelope); continue with POST /auth/login/2fa.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LoginRequest	true	"Credentials"
//	@Success		200		{object}	LoginEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401		{object}	httpx.ErrorResponse	"INVALID_CREDENTIALS"
//	@Failure		403		{object}	httpx.ErrorResponse	"EMAIL_NOT_VERIFIED / ACCOUNT_SUSPENDED / ACCOUNT_INACTIVE"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429		{object}	httpx.ErrorResponse	"ACCOUNT_LOCKED / RATE_LIMITED"
//	@Router			/auth/login [post]
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[LoginRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !h.allowEmail(w, r, "login", req.Email) {
		return
	}
	res, err := h.svc.Login(r.Context(), app.LoginInput{
		Email: req.Email, Password: req.Password,
		ClientIP: clientIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	writeLogin(w, res, h.clock.Now())
}

// refresh godoc
//
//	@Summary		Rotate refresh token
//	@Description	Exchanges a refresh token for a new token pair. Reusing an already rotated refresh token revokes the whole device session (401 TOKEN_REUSED).
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		RefreshRequest	true	"Refresh token"
//	@Success		200		{object}	TokenEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401		{object}	httpx.ErrorResponse	"SESSION_INVALID / TOKEN_REUSED"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429		{object}	httpx.ErrorResponse	"RATE_LIMITED"
//	@Router			/auth/refresh [post]
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[RefreshRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	tokens, err := h.svc.Refresh(r.Context(), app.RefreshInput{
		RefreshToken: req.RefreshToken, ClientIP: clientIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toTokenResponse(tokens, h.clock.Now()))
}

// logout godoc
//
//	@Summary	Log out current session
//	@Tags		auth
//	@Produce	json
//	@Security	BearerAuth
//	@Success	204
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Router		/auth/logout [post]
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if err := h.svc.Logout(r.Context(), app.LogoutInput{UserID: id.UserID, SessionID: id.SessionID, Meta: meta(r)}); err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// logoutAll godoc
//
//	@Summary		Log out other sessions
//	@Description	Revokes every session of the current user except the current one. Send {"include_current": true} to also revoke the current session. Body is optional.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body	LogoutAllRequest	false	"Options"
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Router			/auth/logout-all [post]
func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request) {
	var req LogoutAllRequest
	if r.ContentLength != 0 {
		if err := httpx.DecodeLimit(w, r, &req, maxAuthBody); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
	}
	id := identity(r)
	err := h.svc.LogoutAll(r.Context(), app.LogoutAllInput{
		UserID: id.UserID, SessionID: id.SessionID, IncludeCurrent: req.IncludeCurrent, Meta: meta(r),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// me godoc
//
//	@Summary	Get current user profile
//	@Tags		users
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	UserEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure	404	{object}	httpx.ErrorResponse	"USER_NOT_FOUND"
//	@Router		/users/me [get]
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.svc.Me(r.Context(), identity(r).UserID)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toUserResponse(user))
}

// updateProfile godoc
//
//	@Summary		Update current user profile
//	@Description	Partial update. Only fields present in the body are changed. Email cannot be changed here.
//	@Tags			users
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		UpdateProfileRequest	true	"Fields to update"
//	@Success		200		{object}	UserEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON / NO_FIELDS_TO_UPDATE"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_FULL_NAME"
//	@Router			/users/me [patch]
func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[UpdateProfileRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user, err := h.svc.UpdateProfile(r.Context(), app.UpdateProfileInput{
		UserID: identity(r).UserID, FullName: req.FullName, Meta: meta(r),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toUserResponse(user))
}

// changePassword godoc
//
//	@Summary		Change password
//	@Description	Changes the password (requires the current one). Other sessions are revoked; the current session stays logged in. Also available as POST.
//	@Tags			users
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body	ChangePasswordRequest	true	"Old and new password"
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401	{object}	httpx.ErrorResponse	"INVALID_CREDENTIALS / UNAUTHENTICATED"
//	@Failure		422	{object}	httpx.ErrorResponse	"VALIDATION_FAILED / WEAK_PASSWORD / PASSWORD_REUSED"
//	@Failure		429	{object}	httpx.ErrorResponse	"ACCOUNT_LOCKED / RATE_LIMITED"
//	@Router			/users/me/password [put]
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[ChangePasswordRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id := identity(r)
	if !h.allowUser(w, r, "password", id.UserID) {
		return
	}
	err = h.svc.ChangePassword(r.Context(), app.ChangePasswordInput{
		UserID: id.UserID, SessionID: id.SessionID,
		OldPassword: req.OldPassword, NewPassword: req.NewPassword, Meta: meta(r),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteAccount godoc
//
//	@Summary		Delete current account
//	@Description	Soft-deletes the account after password re-authentication: personal data is anonymized, all sessions are revoked, and the email can be registered again.
//	@Tags			users
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body	DeleteAccountRequest	true	"Current password"
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401	{object}	httpx.ErrorResponse	"INVALID_CREDENTIALS / UNAUTHENTICATED"
//	@Failure		422	{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Failure		429	{object}	httpx.ErrorResponse	"ACCOUNT_LOCKED / RATE_LIMITED"
//	@Router			/users/me [delete]
func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAndValidate[DeleteAccountRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id := identity(r)
	if !h.allowUser(w, r, "delete", id.UserID) {
		return
	}
	err = h.svc.DeleteAccount(r.Context(), app.DeleteAccountInput{
		UserID: id.UserID, SessionID: id.SessionID, Password: req.Password, Meta: meta(r),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listSessions godoc
//
//	@Summary		List active sessions (devices)
//	@Description	Returns the active device sessions of the current user, newest first. current=true marks the session making this request.
//	@Tags			users
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	SessionListEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Router			/users/me/sessions [get]
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	list, err := h.svc.ListSessions(r.Context(), id.UserID, id.SessionID)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	out := make([]SessionResponse, 0, len(list))
	for i := range list {
		out = append(out, toSessionResponse(list[i]))
	}
	httpx.Data(w, http.StatusOK, out)
}

// revokeSession godoc
//
//	@Summary		Revoke a session (device)
//	@Description	Revokes one of the current user's sessions. Sessions of other users return 404.
//	@Tags			users
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Session id"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_PARAMETER"
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Failure		404	{object}	httpx.ErrorResponse	"SESSION_NOT_FOUND"
//	@Router			/users/me/sessions/{id} [delete]
func (h *Handler) revokeSession(w http.ResponseWriter, r *http.Request) {
	familyID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, r, invalidParam("id", "id harus UUID"))
		return
	}
	id := identity(r)
	err = h.svc.RevokeSession(r.Context(), app.RevokeSessionInput{
		UserID: id.UserID, SessionID: id.SessionID, FamilyID: familyID, Meta: meta(r),
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// securityEventsFilter: tidak ada filter selain user (cursor tetap diikat ke endpoint).
var securityEventsFilter = pagination.FilterHash("security-events")

// listSecurityEvents godoc
//
//	@Summary		List security events
//	@Description	Audit trail of the current user's security events (logins, password changes, ...), newest first, cursor paginated.
//	@Tags			users
//	@Produce		json
//	@Security		BearerAuth
//	@Param			limit	query		int		false	"Page size (1-100, default 20)"
//	@Param			cursor	query		string	false	"Opaque cursor from meta.next_cursor"
//	@Success		200		{object}	SecurityEventListEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_CURSOR / INVALID_PARAMETER"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED / SESSION_INVALID"
//	@Router			/users/me/security-events [get]
func (h *Handler) listSecurityEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := pagination.ParseLimit(q.Get("limit"))
	if err != nil {
		httpx.WriteError(w, r, invalidParam("limit", err.Error()))
		return
	}
	var after *domain.AuditKeyset
	if c := q.Get("cursor"); c != "" {
		cur, err := pagination.Decode(c, securityEventsFilter)
		if err != nil {
			httpx.WriteError(w, r, mapError(err))
			return
		}
		after = &domain.AuditKeyset{OccurredAt: cur.Time, ID: cur.ID}
	}
	events, err := h.svc.ListSecurityEvents(r.Context(), identity(r).UserID, after, limit+1)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	page, pm := pagination.Page(events, limit, securityEventsFilter, func(e domain.AuditEvent) (time.Time, uuid.UUID) {
		return e.OccurredAt, e.ID
	})
	out := make([]SecurityEventResponse, 0, len(page))
	for i := range page {
		out = append(out, toSecurityEventResponse(page[i]))
	}
	httpx.List(w, out, pm)
}

// clientIP memakai IP hasil middleware.ClientIP (X-Forwarded-For hanya dipercaya
// bila TRUSTED_PROXIES dikonfigurasi), fallback ke RemoteAddr.
func clientIP(r *http.Request) string { return middleware.ClientIPFromRequest(r) }
