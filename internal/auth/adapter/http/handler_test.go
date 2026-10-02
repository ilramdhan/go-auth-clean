package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/validator"
	"go-auth-clean/internal/shared/pagination"
)

var (
	now     = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	userID  = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	sessID  = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	famID   = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	theUser = &domain.User{ID: userID, Email: "budi@example.com", FullName: "Budi", Status: domain.UserStatusActive,
		PasswordHash: fakeHash, CreatedAt: now, UpdatedAt: now}
)

// fakeHash dipakai untuk memastikan hash tidak pernah muncul di response.
const fakeHash = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNo"

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

// fakeSvc: setiap method mengembalikan err (bila diisi) dan mencatat input terakhir.
type fakeSvc struct {
	mu     sync.Mutex
	err    error
	calls  []string
	last   any
	events []domain.AuditEvent
	after  *domain.AuditKeyset
	limit  int
	authOK error
	p2     p2State
}

func (f *fakeSvc) rec(name string, in any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	f.last = in
	return f.err
}

func (f *fakeSvc) Register(_ context.Context, in app.RegisterInput) (*domain.User, error) {
	return theUser, f.rec("Register", in)
}
func (f *fakeSvc) VerifyEmail(_ context.Context, in app.VerifyEmailInput) (app.VerifyEmailResult, error) {
	return app.VerifyEmailResult{AlreadyVerified: in.Code == "000000"}, f.rec("VerifyEmail", in)
}
func (f *fakeSvc) ResendVerification(_ context.Context, email string) error {
	return f.rec("ResendVerification", email)
}
func (f *fakeSvc) ForgotPassword(_ context.Context, email string, _ app.RequestMeta) error {
	return f.rec("ForgotPassword", email)
}
func (f *fakeSvc) ResetPassword(_ context.Context, in app.ResetPasswordInput) error {
	return f.rec("ResetPassword", in)
}
func (f *fakeSvc) Login(_ context.Context, in app.LoginInput) (app.LoginResult, error) {
	return f.loginResult(), f.rec("Login", in)
}
func (f *fakeSvc) Refresh(_ context.Context, in app.RefreshInput) (app.TokenPair, error) {
	return tokens(), f.rec("Refresh", in)
}
func (f *fakeSvc) Logout(_ context.Context, in app.LogoutInput) error { return f.rec("Logout", in) }
func (f *fakeSvc) LogoutAll(_ context.Context, in app.LogoutAllInput) error {
	return f.rec("LogoutAll", in)
}
func (f *fakeSvc) Me(_ context.Context, id uuid.UUID) (*domain.User, error) {
	return theUser, f.rec("Me", id)
}
func (f *fakeSvc) UpdateProfile(_ context.Context, in app.UpdateProfileInput) (*domain.User, error) {
	return theUser, f.rec("UpdateProfile", in)
}
func (f *fakeSvc) ChangePassword(_ context.Context, in app.ChangePasswordInput) error {
	return f.rec("ChangePassword", in)
}
func (f *fakeSvc) DeleteAccount(_ context.Context, in app.DeleteAccountInput) error {
	return f.rec("DeleteAccount", in)
}
func (f *fakeSvc) ListSessions(_ context.Context, uid, cur uuid.UUID) ([]app.SessionInfo, error) {
	return []app.SessionInfo{{DeviceSession: domain.DeviceSession{FamilyID: famID, ClientIP: "10.0.0.1", CreatedAt: now, LastUsedAt: now, ExpiresAt: now}, Current: true}},
		f.rec("ListSessions", [2]uuid.UUID{uid, cur})
}
func (f *fakeSvc) RevokeSession(_ context.Context, in app.RevokeSessionInput) error {
	return f.rec("RevokeSession", in)
}
func (f *fakeSvc) ListSecurityEvents(_ context.Context, uid uuid.UUID, after *domain.AuditKeyset, limit int) ([]domain.AuditEvent, error) {
	f.after, f.limit = after, limit
	n := min(limit, len(f.events))
	return f.events[:n], f.rec("ListSecurityEvents", uid)
}
func (f *fakeSvc) AuthenticateSession(_ context.Context, _ uuid.UUID) error { return f.authOK }

func tokens() app.TokenPair {
	return app.TokenPair{AccessToken: "access", AccessTokenExpiresAt: now.Add(15 * time.Minute), RefreshToken: "refresh",
		RefreshTokenExpiresAt: now.Add(time.Hour), SessionID: sessID, FamilyID: famID}
}

type fakeVerifier struct{}

func (fakeVerifier) Verify(tok string) (uuid.UUID, uuid.UUID, error) {
	if tok != "good" {
		return uuid.Nil, uuid.Nil, errors.New("bad token")
	}
	return userID, sessID, nil
}

// denyLimiter menolak key yang cocok prefix.
type denyLimiter struct{ prefix string }

func (d denyLimiter) Allow(_ context.Context, key string) (bool, time.Duration) {
	if strings.HasPrefix(key, d.prefix) {
		return false, 1500 * time.Millisecond
	}
	return true, 0
}

func newMux(svc *fakeSvc, opt Options) *http.ServeMux {
	mux := http.NewServeMux()
	NewHandler(svc, validator.New(), fixedClock{}, opt).Routes(mux, RequireAuth(fakeVerifier{}, svc))
	return mux
}

type resp struct {
	Data  json.RawMessage `json:"data"`
	Meta  *pagination.PageMeta
	Error *struct {
		Code    string `json:"code"`
		Details []struct {
			Field string `json:"field"`
		} `json:"details"`
	} `json:"error"`
}

func do(t *testing.T, mux http.Handler, method, path, body string, auth bool) (*httptest.ResponseRecorder, resp) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "bearer good")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var r resp
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return rec, r
}

func code(r resp) string {
	if r.Error == nil {
		return ""
	}
	return r.Error.Code
}

func TestRoutes_Success(t *testing.T) {
	tests := []struct {
		name, method, path, body string
		auth                     bool
		status                   int
		call                     string
	}{
		{"register", "POST", "/api/v1/auth/register", `{"email":"budi@example.com","password":"Rahasia123!","full_name":"Budi"}`, false, 201, "Register"},
		{"verify", "POST", "/api/v1/auth/verify-email", `{"email":"budi@example.com","code":"123456"}`, false, 200, "VerifyEmail"},
		{"resend", "POST", "/api/v1/auth/verify-email/resend", `{"email":"budi@example.com"}`, false, 202, "ResendVerification"},
		{"forgot", "POST", "/api/v1/auth/password/forgot", `{"email":"budi@example.com"}`, false, 202, "ForgotPassword"},
		{"reset", "POST", "/api/v1/auth/password/reset", `{"email":"budi@example.com","code":"123456","new_password":"BaruSekali123"}`, false, 204, "ResetPassword"},
		{"login", "POST", "/api/v1/auth/login", `{"email":"budi@example.com","password":"Rahasia123!"}`, false, 200, "Login"},
		{"refresh", "POST", "/api/v1/auth/refresh", `{"refresh_token":"refresh"}`, false, 200, "Refresh"},
		{"logout", "POST", "/api/v1/auth/logout", "", true, 204, "Logout"},
		{"logout-all empty", "POST", "/api/v1/auth/logout-all", "", true, 204, "LogoutAll"},
		{"logout-all body", "POST", "/api/v1/auth/logout-all", `{"include_current":true}`, true, 204, "LogoutAll"},
		{"me", "GET", "/api/v1/users/me", "", true, 200, "Me"},
		{"profile", "PATCH", "/api/v1/users/me", `{"full_name":"Budi Baru"}`, true, 200, "UpdateProfile"},
		{"delete", "DELETE", "/api/v1/users/me", `{"password":"Rahasia123!"}`, true, 204, "DeleteAccount"},
		{"password put", "PUT", "/api/v1/users/me/password", `{"old_password":"Rahasia123!","new_password":"BaruSekali123"}`, true, 204, "ChangePassword"},
		{"password post", "POST", "/api/v1/users/me/password", `{"old_password":"Rahasia123!","new_password":"BaruSekali123"}`, true, 204, "ChangePassword"},
		{"sessions", "GET", "/api/v1/users/me/sessions", "", true, 200, "ListSessions"},
		{"revoke", "DELETE", "/api/v1/users/me/sessions/" + famID.String(), "", true, 204, "RevokeSession"},
		{"events", "GET", "/api/v1/users/me/security-events", "", true, 200, "ListSecurityEvents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeSvc{}
			rec, _ := do(t, newMux(svc, Options{}), tt.method, tt.path, tt.body, tt.auth)
			if rec.Code != tt.status {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if len(svc.calls) != 1 || svc.calls[0] != tt.call {
				t.Fatalf("calls = %v", svc.calls)
			}
		})
	}
}

func TestLogin_ResponseShape(t *testing.T) {
	svc := &fakeSvc{}
	rec, r := do(t, newMux(svc, Options{}), "POST", "/api/v1/auth/login", `{"email":"budi@example.com","password":"x"}`, false)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(r.Data, &body)
	for _, k := range []string{"access_token", "refresh_token", "session_id", "user", "token_type", "expires_in"} {
		if _, ok := body[k]; !ok {
			t.Errorf("missing %q in %s", k, r.Data)
		}
	}
	if body["session_id"] != famID.String() {
		t.Fatalf("session_id must be family id: %v", body["session_id"])
	}
	// "has_password" (boolean) adalah field publik yang sah; yang tidak boleh
	// muncul adalah nilai hash maupun field hash/password apa pun.
	if strings.Contains(rec.Body.String(), fakeHash) || strings.Contains(rec.Body.String(), "argon2") {
		t.Fatal("password hash leaked")
	}
	user, _ := body["user"].(map[string]any)
	for _, obj := range []map[string]any{body, user} {
		for _, k := range []string{"password", "password_hash", "passwordhash", "PasswordHash"} {
			if _, ok := obj[k]; ok {
				t.Fatalf("sensitive field %q exposed: %s", k, r.Data)
			}
		}
	}
	if hp, ok := user["has_password"].(bool); !ok || !hp {
		t.Fatalf("has_password must be boolean true: %v", user["has_password"])
	}
	if body["mfa_required"] != false {
		t.Fatalf("mfa_required = %v", body["mfa_required"])
	}
	in := svc.last.(app.LoginInput)
	if in.ClientIP == "" {
		t.Fatal("client ip not passed")
	}
}

func TestIdentityPassedToService(t *testing.T) {
	svc := &fakeSvc{}
	mux := newMux(svc, Options{})
	do(t, mux, "POST", "/api/v1/auth/logout-all", `{"include_current":true}`, true)
	in := svc.last.(app.LogoutAllInput)
	if in.UserID != userID || in.SessionID != sessID || !in.IncludeCurrent {
		t.Fatalf("in = %+v", in)
	}
	do(t, mux, "DELETE", "/api/v1/users/me/sessions/"+famID.String(), "", true)
	rv := svc.last.(app.RevokeSessionInput)
	if rv.UserID != userID || rv.FamilyID != famID {
		t.Fatalf("revoke = %+v", rv)
	}
}

func TestRequireAuth(t *testing.T) {
	svc := &fakeSvc{}
	mux := newMux(svc, Options{})
	for _, h := range []string{"", "Bearer", "Basic good", "Bearer   ", "Bearer bad"} {
		req := httptest.NewRequest("GET", "/api/v1/users/me", nil)
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != 401 || !strings.Contains(rec.Body.String(), "UNAUTHENTICATED") {
			t.Errorf("%q: %d %s", h, rec.Code, rec.Body.String())
		}
	}
	svc.authOK = domain.ErrSessionInvalid
	rec, r := do(t, mux, "GET", "/api/v1/users/me", "", true)
	if rec.Code != 401 || code(r) != "SESSION_INVALID" {
		t.Fatalf("revoked: %d %s", rec.Code, rec.Body.String())
	}
	svc.authOK = errors.New("db down")
	rec, _ = do(t, mux, "GET", "/api/v1/users/me", "", true)
	if rec.Code != 500 {
		t.Fatalf("db error: %d", rec.Code)
	}
	if len(svc.calls) != 0 {
		t.Fatal("service must not be called")
	}
}

func TestValidationAndDecodeErrors(t *testing.T) {
	svc := &fakeSvc{}
	mux := newMux(svc, Options{})
	tests := []struct {
		name, method, path, body string
		auth                     bool
		status                   int
		code                     string
	}{
		{"bad json", "POST", "/api/v1/auth/login", `{`, false, 400, ""},
		{"unknown field", "POST", "/api/v1/auth/login", `{"email":"a@b.co","password":"x","admin":true}`, false, 400, ""},
		{"missing", "POST", "/api/v1/auth/register", `{}`, false, 422, "VALIDATION_FAILED"},
		{"otp format", "POST", "/api/v1/auth/verify-email", `{"email":"a@b.co","code":"12ab56"}`, false, 422, "VALIDATION_FAILED"},
		{"too large", "POST", "/api/v1/auth/login", `{"email":"a@b.co","password":"` + strings.Repeat("x", 20<<10) + `"}`, false, 413, ""},
		{"bad uuid", "DELETE", "/api/v1/users/me/sessions/not-uuid", "", true, 400, "INVALID_PARAMETER"},
		{"bad limit", "GET", "/api/v1/users/me/security-events?limit=abc", "", true, 400, "INVALID_PARAMETER"},
		{"bad cursor", "GET", "/api/v1/users/me/security-events?cursor=zzz", "", true, 400, "INVALID_CURSOR"},
		{"logout-all bad body", "POST", "/api/v1/auth/logout-all", `{`, true, 400, ""},
		{"profile bad", "PATCH", "/api/v1/users/me", `[]`, true, 400, ""},
		{"password missing", "PUT", "/api/v1/users/me/password", `{}`, true, 422, "VALIDATION_FAILED"},
		{"delete missing", "DELETE", "/api/v1/users/me", `{}`, true, 422, "VALIDATION_FAILED"},
		{"resend missing", "POST", "/api/v1/auth/verify-email/resend", `{}`, false, 422, "VALIDATION_FAILED"},
		{"forgot missing", "POST", "/api/v1/auth/password/forgot", `{}`, false, 422, "VALIDATION_FAILED"},
		{"reset missing", "POST", "/api/v1/auth/password/reset", `{}`, false, 422, "VALIDATION_FAILED"},
		{"refresh missing", "POST", "/api/v1/auth/refresh", `{}`, false, 422, "VALIDATION_FAILED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, r := do(t, mux, tt.method, tt.path, tt.body, tt.auth)
			if rec.Code != tt.status || (tt.code != "" && code(r) != tt.code) {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
	if len(svc.calls) != 0 {
		t.Fatalf("service must not be called: %v", svc.calls)
	}
}

func TestMapError(t *testing.T) {
	cases := map[error]struct {
		status int
		code   string
	}{
		domain.ErrEmailTaken:         {409, "EMAIL_TAKEN"},
		domain.ErrInvalidEmail:       {422, "INVALID_EMAIL"},
		domain.ErrWeakPassword:       {422, "WEAK_PASSWORD"},
		domain.ErrPasswordReused:     {422, "PASSWORD_REUSED"},
		domain.ErrInvalidFullName:    {422, "INVALID_FULL_NAME"},
		domain.ErrNoFieldsToUpdate:   {400, "NO_FIELDS_TO_UPDATE"},
		domain.ErrInvalidCredentials: {401, "INVALID_CREDENTIALS"},
		domain.ErrAccountLocked:      {429, "ACCOUNT_LOCKED"},
		domain.ErrEmailNotVerified:   {403, "EMAIL_NOT_VERIFIED"},
		domain.ErrAccountSuspended:   {403, "ACCOUNT_SUSPENDED"},
		domain.ErrAccountInactive:    {403, "ACCOUNT_INACTIVE"},
		domain.ErrInvalidOTP:         {400, "INVALID_OTP"},
		domain.ErrOTPNotFound:        {400, "INVALID_OTP"},
		domain.ErrOTPExpired:         {400, "OTP_EXPIRED"},
		domain.ErrOTPTooManyAttempts: {429, "OTP_TOO_MANY_ATTEMPTS"},
		domain.ErrTokenReused:        {401, "TOKEN_REUSED"},
		domain.ErrSessionInvalid:     {401, "SESSION_INVALID"},
		domain.ErrSessionNotFound:    {404, "SESSION_NOT_FOUND"},
		domain.ErrUserNotFound:       {404, "USER_NOT_FOUND"},
		errors.New("boom"):           {500, "INTERNAL"},
	}
	for err, want := range cases {
		svc := &fakeSvc{err: err}
		rec, r := do(t, newMux(svc, Options{}), "GET", "/api/v1/users/me", "", true)
		if rec.Code != want.status || code(r) != want.code {
			t.Errorf("%v: got %d %s", err, rec.Code, rec.Body.String())
		}
		if want.status == 500 && strings.Contains(rec.Body.String(), "boom") {
			t.Error("internal error leaked")
		}
	}
}

func TestServiceErrorsOnEveryRoute(t *testing.T) {
	svc := &fakeSvc{err: domain.ErrInvalidCredentials}
	mux := newMux(svc, Options{})
	reqs := [][4]string{
		{"POST", "/api/v1/auth/register", `{"email":"budi@example.com","password":"Rahasia123!","full_name":"Budi"}`, ""},
		{"POST", "/api/v1/auth/verify-email", `{"email":"budi@example.com","code":"123456"}`, ""},
		{"POST", "/api/v1/auth/verify-email/resend", `{"email":"budi@example.com"}`, ""},
		{"POST", "/api/v1/auth/password/forgot", `{"email":"budi@example.com"}`, ""},
		{"POST", "/api/v1/auth/password/reset", `{"email":"budi@example.com","code":"123456","new_password":"BaruSekali123"}`, ""},
		{"POST", "/api/v1/auth/login", `{"email":"budi@example.com","password":"x"}`, ""},
		{"POST", "/api/v1/auth/refresh", `{"refresh_token":"r"}`, ""},
		{"POST", "/api/v1/auth/logout", "", "a"},
		{"POST", "/api/v1/auth/logout-all", "", "a"},
		{"PATCH", "/api/v1/users/me", `{"full_name":"X"}`, "a"},
		{"DELETE", "/api/v1/users/me", `{"password":"x"}`, "a"},
		{"PUT", "/api/v1/users/me/password", `{"old_password":"x","new_password":"BaruSekali123"}`, "a"},
		{"GET", "/api/v1/users/me/sessions", "", "a"},
		{"DELETE", "/api/v1/users/me/sessions/" + famID.String(), "", "a"},
		{"GET", "/api/v1/users/me/security-events", "", "a"},
	}
	for _, q := range reqs {
		rec, r := do(t, mux, q[0], q[1], q[2], q[3] != "")
		if rec.Code != 401 || code(r) != "INVALID_CREDENTIALS" {
			t.Errorf("%s %s: %d %s", q[0], q[1], rec.Code, rec.Body.String())
		}
	}
}

func TestRateLimits(t *testing.T) {
	email := denyLimiter{prefix: "auth:"}
	cases := [][3]string{
		{"/api/v1/auth/login", `{"email":"Budi@Example.com","password":"x"}`, "auth:login:email:budi@example.com"},
		{"/api/v1/auth/verify-email", `{"email":"budi@example.com","code":"123456"}`, ""},
		{"/api/v1/auth/verify-email/resend", `{"email":"budi@example.com"}`, ""},
		{"/api/v1/auth/password/forgot", `{"email":"budi@example.com"}`, ""},
		{"/api/v1/auth/password/reset", `{"email":"budi@example.com","code":"123456","new_password":"BaruSekali123"}`, ""},
	}
	for _, c := range cases {
		svc := &fakeSvc{}
		rec, r := do(t, newMux(svc, Options{EmailLimiter: email}), "POST", c[0], c[1], false)
		if rec.Code != 429 || code(r) != "RATE_LIMITED" || rec.Header().Get("Retry-After") != "2" || len(svc.calls) != 0 {
			t.Errorf("%s: %d %s retry=%s", c[0], rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
		}
	}
	if got := emailKey("login", " Budi@Example.com "); got != "auth:login:email:budi@example.com" {
		t.Fatal(got)
	}

	user := denyLimiter{prefix: "auth:password:user:" + userID.String()}
	svc := &fakeSvc{}
	mux := newMux(svc, Options{UserLimiter: user})
	rec, _ := do(t, mux, "PUT", "/api/v1/users/me/password", `{"old_password":"x","new_password":"BaruSekali123"}`, true)
	if rec.Code != 429 {
		t.Fatalf("password: %d", rec.Code)
	}
	rec, _ = do(t, mux, "DELETE", "/api/v1/users/me", `{"password":"x"}`, true)
	if rec.Code != 204 {
		t.Fatalf("delete limited by other scope: %d", rec.Code)
	}
	mux = newMux(&fakeSvc{}, Options{UserLimiter: denyLimiter{prefix: "auth:delete:"}})
	rec, _ = do(t, mux, "DELETE", "/api/v1/users/me", `{"password":"x"}`, true)
	if rec.Code != 429 {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestSecurityEventsPagination(t *testing.T) {
	var evs []domain.AuditEvent
	for i := range 5 {
		evs = append(evs, domain.AuditEvent{ID: uuid.New(), UserID: userID, EventType: domain.EventLoginSucceeded,
			Outcome: domain.OutcomeSuccess, ClientIP: "10.0.0.1", Metadata: map[string]string{"k": "v"},
			OccurredAt: now.Add(-time.Duration(i) * time.Minute)})
	}
	svc := &fakeSvc{events: evs}
	mux := newMux(svc, Options{})
	rec, r := do(t, mux, "GET", "/api/v1/users/me/security-events?limit=2", "", true)
	if rec.Code != 200 || r.Meta == nil || !r.Meta.HasMore || r.Meta.NextCursor == "" || r.Meta.Limit != 2 {
		t.Fatalf("page1: %s", rec.Body.String())
	}
	if svc.limit != 3 || svc.after != nil {
		t.Fatalf("service limit=%d after=%v", svc.limit, svc.after)
	}
	var items []map[string]any
	_ = json.Unmarshal(r.Data, &items)
	if len(items) != 2 || items[0]["event_type"] != "login_succeeded" {
		t.Fatalf("items %s", r.Data)
	}

	rec, _ = do(t, mux, "GET", "/api/v1/users/me/security-events?limit=2&cursor="+r.Meta.NextCursor, "", true)
	if rec.Code != 200 || svc.after == nil || svc.after.ID != evs[1].ID || !svc.after.OccurredAt.Equal(evs[1].OccurredAt) {
		t.Fatalf("cursor not decoded: %d %+v", rec.Code, svc.after)
	}

	// Cursor dari endpoint lain ditolak.
	other := pagination.Cursor{Time: now, ID: uuid.New(), Filter: pagination.FilterHash("other")}.Encode()
	rec, r = do(t, mux, "GET", "/api/v1/users/me/security-events?cursor="+other, "", true)
	if rec.Code != 400 || code(r) != "INVALID_CURSOR" {
		t.Fatalf("foreign cursor: %d", rec.Code)
	}

	svc.events = nil
	rec, r = do(t, mux, "GET", "/api/v1/users/me/security-events", "", true)
	if rec.Code != 200 || string(r.Data) != "[]" || r.Meta.HasMore {
		t.Fatalf("empty: %s", rec.Body.String())
	}
}

func TestVerifyEmail_AlreadyVerified(t *testing.T) {
	_, r := do(t, newMux(&fakeSvc{}, Options{}), "POST", "/api/v1/auth/verify-email", `{"email":"a@b.co","code":"000000"}`, false)
	if !strings.Contains(string(r.Data), `"already_verified":true`) {
		t.Fatal(string(r.Data))
	}
}
