package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/crypto"
	"go-auth-clean/internal/platform/middleware"
	"go-auth-clean/internal/platform/validator"
)

var (
	adminID  = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	targetID = uuid.MustParse("55555555-5555-5555-5555-555555555555")
	keyID    = uuid.MustParse("66666666-6666-6666-6666-666666666666")
)

// p2State mengatur perilaku fake untuk method P2.
type p2State struct {
	mfaLogin    bool // Login mengembalikan challenge 2FA
	mfaEnabled  bool
	oauth       bool
	linked      bool
	users       []domain.User
	filter      domain.UserFilter
	principal   app.Principal
	apiKeyErr   error
	oauthCbErr  error
	startResult app.OAuthStart
}

func (f *fakeSvc) loginResult() app.LoginResult {
	if f.p2.mfaLogin {
		return app.LoginResult{MFARequired: true, MFAToken: "mfa-token", MFATokenExpiresAt: now.Add(5 * time.Minute)}
	}
	return app.LoginResult{Tokens: tokens(), User: theUser}
}

func (f *fakeSvc) LoginMFA(_ context.Context, in app.LoginMFAInput) (app.LoginResult, error) {
	return app.LoginResult{Tokens: tokens(), User: theUser}, f.rec("LoginMFA", in)
}
func (f *fakeSvc) SetupMFA(_ context.Context, id uuid.UUID) (app.MFASetup, error) {
	return app.MFASetup{Secret: "SECRET", OTPAuthURI: "otpauth://totp/x"}, f.rec("SetupMFA", id)
}
func (f *fakeSvc) EnableMFA(_ context.Context, in app.MFACodeInput) ([]string, error) {
	return []string{"aaaaa-bbbbb"}, f.rec("EnableMFA", in)
}
func (f *fakeSvc) DisableMFA(_ context.Context, in app.DisableMFAInput) error {
	return f.rec("DisableMFA", in)
}
func (f *fakeSvc) RegenerateRecoveryCodes(_ context.Context, in app.MFACodeInput) ([]string, error) {
	return []string{"ccccc-ddddd"}, f.rec("RegenerateRecoveryCodes", in)
}
func (f *fakeSvc) GetMFAStatus(_ context.Context, id uuid.UUID) (app.MFAStatus, error) {
	return app.MFAStatus{Enabled: f.p2.mfaEnabled, RecoveryCodesRemaining: 10}, f.rec("GetMFAStatus", id)
}
func (f *fakeSvc) OAuthEnabled(p string) bool { return f.p2.oauth && p == domain.ProviderGoogle }
func (f *fakeSvc) StartOAuth(_ context.Context, p string) (app.OAuthStart, error) {
	st := f.p2.startResult
	if st.AuthURL == "" {
		st = app.OAuthStart{AuthURL: "https://accounts.example/auth?x=1", State: "state-1", Verifier: "verif", Nonce: "nonce"}
	}
	return st, f.rec("StartOAuth", p)
}
func (f *fakeSvc) OAuthCallback(_ context.Context, in app.OAuthCallbackInput) (app.OAuthCallbackResult, error) {
	_ = f.rec("OAuthCallback", in)
	if f.p2.oauthCbErr != nil {
		return app.OAuthCallbackResult{}, f.p2.oauthCbErr
	}
	return app.OAuthCallbackResult{LoginCode: "login-code", Linked: f.p2.linked}, nil
}
func (f *fakeSvc) ExchangeOAuthCode(_ context.Context, code string, _ app.RequestMeta) (app.LoginResult, error) {
	return f.loginResult(), f.rec("ExchangeOAuthCode", code)
}
func (f *fakeSvc) ListIdentities(_ context.Context, id uuid.UUID) ([]domain.ExternalIdentity, error) {
	return []domain.ExternalIdentity{{Provider: "google", Subject: "sub-secret", Email: "b@gmail.com", CreatedAt: now}}, f.rec("ListIdentities", id)
}
func (f *fakeSvc) CreateAPIKey(_ context.Context, in app.CreateAPIKeyInput) (app.CreatedAPIKey, error) {
	return app.CreatedAPIKey{APIKey: domain.APIKey{ID: keyID, Name: in.Name, Prefix: "ab12cd34", SecretHash: []byte("HASHBYTES"),
		Scopes: []domain.Scope{domain.ScopeRead}, CreatedAt: now}, Key: "gac_test_ab12cd34_secret"}, f.rec("CreateAPIKey", in)
}
func (f *fakeSvc) ListAPIKeys(_ context.Context, id uuid.UUID) ([]domain.APIKey, error) {
	return []domain.APIKey{{ID: keyID, Name: "ci", Prefix: "ab12cd34", SecretHash: []byte("HASHBYTES"), CreatedAt: now}}, f.rec("ListAPIKeys", id)
}
func (f *fakeSvc) RevokeAPIKey(_ context.Context, in app.RevokeAPIKeyInput) error {
	return f.rec("RevokeAPIKey", in)
}
func (f *fakeSvc) AuthenticateAPIKey(_ context.Context, raw string) (app.Principal, error) {
	if f.p2.apiKeyErr != nil {
		return app.Principal{}, f.p2.apiKeyErr
	}
	if raw != "gac_test_good" {
		return app.Principal{}, domain.ErrAPIKeyInvalid
	}
	return f.p2.principal, nil
}
func (f *fakeSvc) ListUsers(_ context.Context, actor uuid.UUID, fl domain.UserFilter) ([]domain.User, error) {
	f.p2.filter = fl
	n := min(fl.Limit, len(f.p2.users))
	return f.p2.users[:n], f.rec("ListUsers", actor)
}
func (f *fakeSvc) GetUser(_ context.Context, _, target uuid.UUID) (*domain.User, error) {
	return theUser, f.rec("GetUser", target)
}
func (f *fakeSvc) SuspendUser(_ context.Context, in app.AdminActionInput) error {
	return f.rec("SuspendUser", in)
}
func (f *fakeSvc) ActivateUser(_ context.Context, in app.AdminActionInput) error {
	return f.rec("ActivateUser", in)
}
func (f *fakeSvc) GrantRole(_ context.Context, in app.RoleChangeInput) error {
	return f.rec("GrantRole", in)
}
func (f *fakeSvc) RevokeRole(_ context.Context, in app.RoleChangeInput) error {
	return f.rec("RevokeRole", in)
}
func (f *fakeSvc) UserAuditLog(_ context.Context, _, target uuid.UUID, after *domain.AuditKeyset, limit int) ([]domain.AuditEvent, error) {
	f.after, f.limit = after, limit
	n := min(limit, len(f.events))
	return f.events[:n], f.rec("UserAuditLog", target)
}

var _ P2Service = (*fakeSvc)(nil)

// roleVerifier: "good" = user biasa, "admin" = admin.
type roleVerifier struct{}

func (roleVerifier) VerifyRoles(tok string) (uuid.UUID, uuid.UUID, []domain.Role, error) {
	switch tok {
	case "good":
		return userID, sessID, []domain.Role{domain.RoleUser}, nil
	case "admin":
		return adminID, sessID, []domain.Role{domain.RoleUser, domain.RoleAdmin}, nil
	}
	return uuid.Nil, uuid.Nil, nil, errors.New("bad")
}

func testSealer(t *testing.T) *crypto.SecretBox {
	t.Helper()
	sb, err := crypto.NewSecretBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

func newP2Mux(t *testing.T, svc *fakeSvc, opt Options) *http.ServeMux {
	t.Helper()
	return newP2MuxWith(t, svc, opt, P2Options{})
}

func newP2MuxWith(t *testing.T, svc *fakeSvc, opt Options, p2 P2Options) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	authn := NewAuthenticator(roleVerifier{}, svc, svc, opt.UserLimiter)
	base := NewHandler(svc, validator.New(), fixedClock{}, opt)
	base.Routes(mux, authn.User)
	p2.Sealer = testSealer(t)
	p2.OAuthRedirectURL = "http://front.example/oauth/cb"
	NewP2Handler(base, svc, p2).Routes(mux, NewP2Middleware(authn))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	return mux
}

type reqOpt func(*http.Request)

func bearerTok(tok string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}
func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func doReq(t *testing.T, mux http.Handler, method, path, body string, opts ...reqOpt) (*httptest.ResponseRecorder, resp) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var r resp
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return rec, r
}

func TestP2Routes_Success(t *testing.T) {
	uid := targetID.String()
	tests := []struct {
		name, method, path, body, tok string
		status                        int
		call                          string
	}{
		{"login 2fa", "POST", "/api/v1/auth/login/2fa", `{"mfa_token":"t","code":"123456"}`, "", 200, "LoginMFA"},
		{"mfa status", "GET", "/api/v1/users/me/2fa", "", "good", 200, "GetMFAStatus"},
		{"mfa enable", "POST", "/api/v1/users/me/2fa/enable", `{"code":"123456"}`, "good", 200, "EnableMFA"},
		{"mfa disable", "POST", "/api/v1/users/me/2fa/disable", `{"password":"x","code":"123456"}`, "good", 204, "DisableMFA"},
		{"mfa regen", "POST", "/api/v1/users/me/2fa/recovery-codes", `{"code":"123456"}`, "good", 200, "RegenerateRecoveryCodes"},
		{"keys list", "GET", "/api/v1/users/me/api-keys", "", "good", 200, "ListAPIKeys"},
		{"keys create", "POST", "/api/v1/users/me/api-keys", `{"name":"ci","scopes":["read"],"expires_in_days":30}`, "good", 201, "CreateAPIKey"},
		{"keys revoke", "DELETE", "/api/v1/users/me/api-keys/" + keyID.String(), "", "good", 204, "RevokeAPIKey"},
		{"admin list", "GET", "/api/v1/admin/users?q=budi&status=active", "", "admin", 200, "ListUsers"},
		{"admin get", "GET", "/api/v1/admin/users/" + uid, "", "admin", 200, "GetUser"},
		{"admin suspend", "POST", "/api/v1/admin/users/" + uid + "/suspend", "", "admin", 204, "SuspendUser"},
		{"admin activate", "POST", "/api/v1/admin/users/" + uid + "/activate", "", "admin", 204, "ActivateUser"},
		{"admin grant", "POST", "/api/v1/admin/users/" + uid + "/roles", `{"role":"admin"}`, "admin", 204, "GrantRole"},
		{"admin revoke", "DELETE", "/api/v1/admin/users/" + uid + "/roles/admin", "", "admin", 204, "RevokeRole"},
		{"admin events", "GET", "/api/v1/admin/users/" + uid + "/security-events", "", "admin", 200, "UserAuditLog"},
		{"oauth exchange", "POST", "/api/v1/auth/oauth/exchange", `{"code":"abc"}`, "", 200, "ExchangeOAuthCode"},
		{"identities", "GET", "/api/v1/users/me/identities", "", "good", 200, "ListIdentities"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeSvc{p2: p2State{oauth: true}}
			var opts []reqOpt
			if tt.tok != "" {
				opts = append(opts, bearerTok(tt.tok))
			}
			rec, _ := doReq(t, newP2Mux(t, svc, Options{}), tt.method, tt.path, tt.body, opts...)
			if rec.Code != tt.status {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if len(svc.calls) != 1 || svc.calls[0] != tt.call {
				t.Fatalf("calls = %v", svc.calls)
			}
			if strings.Contains(rec.Body.String(), "HASHBYTES") || strings.Contains(rec.Body.String(), "sub-secret") {
				t.Fatalf("secret material leaked: %s", rec.Body)
			}
		})
	}
}

func TestP2_ServiceErrorsMapped(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrMFANotEnabled, 409, "MFA_NOT_ENABLED"},
		{domain.ErrMFASetupRequired, 409, "MFA_SETUP_REQUIRED"},
		{domain.ErrMFACodeReplayed, 401, "INVALID_MFA_CODE"},
		{domain.ErrInvalidMFACode, 401, "INVALID_MFA_CODE"},
		{domain.ErrMFATokenInvalid, 401, "MFA_TOKEN_INVALID"},
		{domain.ErrForbidden, 403, "FORBIDDEN"},
		{domain.ErrInvalidRole, 422, "INVALID_ROLE"},
		{domain.ErrCannotModifySelf, 409, "CANNOT_MODIFY_SELF"},
		{domain.ErrInvalidTransition, 409, "INVALID_STATUS_TRANSITION"},
		{domain.ErrInvalidStatus, 400, "INVALID_PARAMETER"},
		{domain.ErrOAuthDisabled, 404, "NOT_FOUND"},
		{domain.ErrOAuthEmailNotVerif, 403, "OAUTH_EMAIL_NOT_VERIFIED"},
		{domain.ErrOAuthAccountExists, 409, "OAUTH_ACCOUNT_EXISTS"},
		{domain.ErrIdentityTaken, 409, "IDENTITY_TAKEN"},
		{domain.ErrOAuthFailed, 400, "OAUTH_FAILED"},
		{domain.ErrLoginCodeInvalid, 400, "LOGIN_CODE_INVALID"},
		{domain.ErrAPIKeyNotFound, 404, "API_KEY_NOT_FOUND"},
		{domain.ErrAPIKeyInvalid, 401, "API_KEY_INVALID"},
		{domain.ErrAPIKeyLimit, 409, "API_KEY_LIMIT"},
		{domain.ErrInvalidAPIKeyName, 422, "INVALID_API_KEY_NAME"},
		{domain.ErrInvalidScope, 422, "INVALID_SCOPE"},
		{domain.ErrInvalidExpiry, 422, "INVALID_EXPIRY"},
		{domain.ErrMFAAlreadyEnabled, 409, "MFA_ALREADY_ENABLED"},
	}
	// Setiap route yang memanggil service harus memetakan error domain dengan benar.
	routes := []struct{ method, path, body, tok string }{
		{"POST", "/api/v1/auth/login/2fa", `{"mfa_token":"t","code":"123456"}`, ""},
		{"GET", "/api/v1/users/me/2fa", "", "good"},
		{"POST", "/api/v1/users/me/2fa/enable", `{"code":"123456"}`, "good"},
		{"POST", "/api/v1/users/me/2fa/disable", `{"password":"x","code":"123456"}`, "good"},
		{"POST", "/api/v1/users/me/2fa/recovery-codes", `{"code":"123456"}`, "good"},
		{"GET", "/api/v1/users/me/api-keys", "", "good"},
		{"POST", "/api/v1/users/me/api-keys", `{"name":"ci","scopes":["read"]}`, "good"},
		{"DELETE", "/api/v1/users/me/api-keys/" + keyID.String(), "", "good"},
		{"GET", "/api/v1/admin/users", "", "admin"},
		{"GET", "/api/v1/admin/users/" + targetID.String(), "", "admin"},
		{"POST", "/api/v1/admin/users/" + targetID.String() + "/suspend", "", "admin"},
		{"POST", "/api/v1/admin/users/" + targetID.String() + "/roles", `{"role":"admin"}`, "admin"},
		{"DELETE", "/api/v1/admin/users/" + targetID.String() + "/roles/admin", "", "admin"},
		{"GET", "/api/v1/admin/users/" + targetID.String() + "/security-events", "", "admin"},
		{"POST", "/api/v1/auth/oauth/exchange", `{"code":"abc"}`, ""},
		{"GET", "/api/v1/users/me/identities", "", "good"},
		{"POST", "/api/v1/users/me/identities/google/link", "", "good"},
	}
	for _, c := range cases {
		for _, rt := range routes {
			svc := &fakeSvc{err: c.err, p2: p2State{oauth: true}}
			var opts []reqOpt
			if rt.tok != "" {
				opts = append(opts, bearerTok(rt.tok))
			}
			rec, r := doReq(t, newP2Mux(t, svc, Options{}), rt.method, rt.path, rt.body, opts...)
			if rec.Code != c.status || code(r) != c.code {
				t.Fatalf("%v %s %s: got %d %s", c.err, rt.method, rt.path, rec.Code, code(r))
			}
		}
	}
}

func TestLogin_MFAChallenge(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/oauth/exchange"} {
		svc := &fakeSvc{p2: p2State{mfaLogin: true, oauth: true}}
		body := `{"email":"budi@example.com","password":"x"}`
		if strings.Contains(path, "exchange") {
			body = `{"code":"abc"}`
		}
		rec, r := doReq(t, newP2Mux(t, svc, Options{}), "POST", path, body)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		var m map[string]any
		_ = json.Unmarshal(r.Data, &m)
		if m["mfa_required"] != true || m["mfa_token"] != "mfa-token" || m["access_token"] != nil {
			t.Fatalf("%s: challenge = %s", path, r.Data)
		}
	}
}

func TestMFASetup(t *testing.T) {
	svc := &fakeSvc{}
	rec, r := doReq(t, newP2Mux(t, svc, Options{}), "POST", "/api/v1/users/me/2fa/setup", "", bearerTok("good"))
	if rec.Code != 200 || !strings.Contains(string(r.Data), "otpauth://") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	svc = &fakeSvc{p2: p2State{mfaEnabled: true}}
	rec, r = doReq(t, newP2Mux(t, svc, Options{}), "POST", "/api/v1/users/me/2fa/setup", "", bearerTok("good"))
	if rec.Code != 409 || code(r) != "MFA_ALREADY_ENABLED" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	svc = &fakeSvc{err: errors.New("db")}
	if rec, _ := doReq(t, newP2Mux(t, svc, Options{}), "POST", "/api/v1/users/me/2fa/setup", "", bearerTok("good")); rec.Code != 500 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestP2_InputPassedToService(t *testing.T) {
	svc := &fakeSvc{}
	mux := newP2Mux(t, svc, Options{})
	doReq(t, mux, "POST", "/api/v1/users/me/api-keys", `{"name":"ci","scopes":["read","write"],"expires_in_days":2}`, bearerTok("good"))
	in := svc.last.(app.CreateAPIKeyInput)
	if in.UserID != userID || in.SessionID != sessID || in.ExpiresIn != 48*time.Hour || len(in.Scopes) != 2 {
		t.Fatalf("%+v", in)
	}
	doReq(t, mux, "DELETE", "/api/v1/admin/users/"+targetID.String()+"/roles/admin", "", bearerTok("admin"))
	rc := svc.last.(app.RoleChangeInput)
	if rc.ActorID != adminID || rc.TargetID != targetID || rc.Role != "admin" {
		t.Fatalf("%+v", rc)
	}
	doReq(t, mux, "POST", "/api/v1/users/me/2fa/disable", `{"password":"pw","code":"abcde-fghij"}`, bearerTok("good"))
	if d := svc.last.(app.DisableMFAInput); d.Password != "pw" || d.Code != "abcde-fghij" || d.UserID != userID {
		t.Fatalf("%+v", d)
	}
}

func TestP2_CreatedKeyShownOnce(t *testing.T) {
	svc := &fakeSvc{}
	rec, r := doReq(t, newP2Mux(t, svc, Options{}), "POST", "/api/v1/users/me/api-keys", `{"name":"ci","scopes":["read"]}`, bearerTok("good"))
	var m map[string]any
	_ = json.Unmarshal(r.Data, &m)
	if rec.Code != 201 || m["key"] != "gac_test_ab12cd34_secret" || m["prefix"] != "ab12cd34" || m["secret_hash"] != nil {
		t.Fatalf("%s", rec.Body)
	}
	rec, _ = doReq(t, newP2Mux(t, svc, Options{}), "GET", "/api/v1/users/me/api-keys", "", bearerTok("good"))
	if strings.Contains(rec.Body.String(), `"key"`) {
		t.Fatalf("list must not include key: %s", rec.Body)
	}
}

func TestP2_Validation(t *testing.T) {
	tests := []struct {
		method, path, body, tok string
		status                  int
		code                    string
	}{
		{"POST", "/api/v1/auth/login/2fa", `{"mfa_token":"t"}`, "", 422, "VALIDATION_FAILED"},
		{"POST", "/api/v1/auth/login/2fa", `{`, "", 400, "INVALID_JSON"},
		{"POST", "/api/v1/users/me/2fa/enable", `{"code":"12"}`, "good", 422, "VALIDATION_FAILED"},
		{"POST", "/api/v1/users/me/2fa/recovery-codes", `{`, "good", 400, "INVALID_JSON"},
		{"POST", "/api/v1/users/me/2fa/disable", `{"code":"123456"}`, "good", 422, "VALIDATION_FAILED"},
		{"POST", "/api/v1/users/me/api-keys", `{"name":"ci","scopes":["admin"]}`, "good", 422, "VALIDATION_FAILED"},
		{"POST", "/api/v1/users/me/api-keys", `{"name":"ci","scopes":["read"],"expires_in_days":999}`, "good", 422, "VALIDATION_FAILED"},
		{"DELETE", "/api/v1/users/me/api-keys/nope", "", "good", 400, "INVALID_PARAMETER"},
		{"GET", "/api/v1/admin/users?limit=0", "", "admin", 400, "INVALID_PARAMETER"},
		{"GET", "/api/v1/admin/users?status=weird", "", "admin", 400, "INVALID_PARAMETER"},
		{"GET", "/api/v1/admin/users?q=" + strings.Repeat("a", 255), "", "admin", 400, "INVALID_PARAMETER"},
		{"GET", "/api/v1/admin/users?cursor=garbage", "", "admin", 400, "INVALID_CURSOR"},
		{"GET", "/api/v1/admin/users/nope", "", "admin", 400, "INVALID_PARAMETER"},
		{"POST", "/api/v1/admin/users/nope/suspend", "", "admin", 400, "INVALID_PARAMETER"},
		{"POST", "/api/v1/admin/users/nope/roles", `{"role":"admin"}`, "admin", 400, "INVALID_PARAMETER"},
		{"POST", "/api/v1/admin/users/" + targetID.String() + "/roles", `{"role":"root"}`, "admin", 422, "VALIDATION_FAILED"},
		{"DELETE", "/api/v1/admin/users/nope/roles/admin", "", "admin", 400, "INVALID_PARAMETER"},
		{"GET", "/api/v1/admin/users/nope/security-events", "", "admin", 400, "INVALID_PARAMETER"},
		{"GET", "/api/v1/admin/users/" + targetID.String() + "/security-events?limit=abc", "", "admin", 400, "INVALID_PARAMETER"},
		{"GET", "/api/v1/admin/users/" + targetID.String() + "/security-events?cursor=x", "", "admin", 400, "INVALID_CURSOR"},
		{"POST", "/api/v1/auth/oauth/exchange", `{}`, "", 422, "VALIDATION_FAILED"},
	}
	for _, tt := range tests {
		svc := &fakeSvc{p2: p2State{oauth: true}}
		var opts []reqOpt
		if tt.tok != "" {
			opts = append(opts, bearerTok(tt.tok))
		}
		rec, r := doReq(t, newP2Mux(t, svc, Options{}), tt.method, tt.path, tt.body, opts...)
		if rec.Code != tt.status || code(r) != tt.code {
			t.Errorf("%s %s: %d %s", tt.method, tt.path, rec.Code, rec.Body)
		}
		if len(svc.calls) != 0 {
			t.Errorf("%s %s: service must not be called: %v", tt.method, tt.path, svc.calls)
		}
	}
}

func TestP2_UserRateLimit(t *testing.T) {
	for _, p := range []struct{ path, body string }{
		{"/api/v1/users/me/2fa/enable", `{"code":"123456"}`},
		{"/api/v1/users/me/2fa/disable", `{"password":"x","code":"123456"}`},
	} {
		svc := &fakeSvc{}
		rec, r := doReq(t, newP2Mux(t, svc, Options{UserLimiter: denyLimiter{prefix: "auth:mfa:"}}), "POST", p.path, p.body, bearerTok("good"))
		if rec.Code != 429 || code(r) != "RATE_LIMITED" || len(svc.calls) != 0 {
			t.Fatalf("%s: %d %s", p.path, rec.Code, rec.Body)
		}
	}
}

func TestAdminUsersPagination(t *testing.T) {
	users := make([]domain.User, 3)
	for i := range users {
		users[i] = domain.User{ID: uuid.New(), Email: "u@x.io", Status: domain.UserStatusActive, CreatedAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	svc := &fakeSvc{p2: p2State{users: users}}
	mux := newP2Mux(t, svc, Options{})
	rec, r := doReq(t, mux, "GET", "/api/v1/admin/users?limit=2&q=%20u%20&status=active", "", bearerTok("admin"))
	if rec.Code != 200 || r.Meta == nil || !r.Meta.HasMore || r.Meta.NextCursor == "" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f := svc.p2.filter; f.Limit != 3 || f.Query != "u" || f.Status != domain.UserStatusActive || f.After != nil {
		t.Fatalf("filter = %+v", f)
	}
	var page []map[string]any
	_ = json.Unmarshal(r.Data, &page)
	if len(page) != 2 {
		t.Fatalf("len = %d", len(page))
	}
	next := url.QueryEscape(r.Meta.NextCursor)
	rec, _ = doReq(t, mux, "GET", "/api/v1/admin/users?limit=2&q=u&status=active&cursor="+next, "", bearerTok("admin"))
	if rec.Code != 200 || svc.p2.filter.After == nil || svc.p2.filter.After.ID != users[1].ID {
		t.Fatalf("%d %+v", rec.Code, svc.p2.filter.After)
	}
	// Cursor terikat ke filter: ganti q -> INVALID_CURSOR.
	_, r = doReq(t, mux, "GET", "/api/v1/admin/users?limit=2&q=zzz&cursor="+next, "", bearerTok("admin"))
	if code(r) != "INVALID_CURSOR" {
		t.Fatalf("code = %s", code(r))
	}
}

func TestAdminSecurityEventsPagination(t *testing.T) {
	evs := make([]domain.AuditEvent, 2)
	for i := range evs {
		evs[i] = domain.AuditEvent{ID: uuid.New(), EventType: "login_succeeded", Outcome: "success", OccurredAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	svc := &fakeSvc{events: evs}
	mux := newP2Mux(t, svc, Options{})
	path := "/api/v1/admin/users/" + targetID.String() + "/security-events?limit=1"
	_, r := doReq(t, mux, "GET", path, "", bearerTok("admin"))
	if r.Meta == nil || !r.Meta.HasMore || svc.limit != 2 {
		t.Fatalf("meta = %+v limit=%d", r.Meta, svc.limit)
	}
	rec, _ := doReq(t, mux, "GET", path+"&cursor="+url.QueryEscape(r.Meta.NextCursor), "", bearerTok("admin"))
	if rec.Code != 200 || svc.after == nil || svc.after.ID != evs[0].ID {
		t.Fatalf("%d after=%+v", rec.Code, svc.after)
	}
}

func TestRBAC(t *testing.T) {
	svc := &fakeSvc{p2: p2State{principal: app.Principal{UserID: adminID, APIKeyID: keyID,
		Roles: []domain.Role{domain.RoleAdmin}, Scopes: []domain.Scope{domain.ScopeRead, domain.ScopeWrite}}}}
	mux := newP2Mux(t, svc, Options{})
	cases := []struct {
		name   string
		opts   []reqOpt
		status int
		code   string
	}{
		{"no token", nil, 401, "UNAUTHENTICATED"},
		{"bad token", []reqOpt{bearerTok("nope")}, 401, "UNAUTHENTICATED"},
		{"plain user", []reqOpt{bearerTok("good")}, 403, "FORBIDDEN"},
		// Admin route hanya menerima access token; API key (meski milik admin) ditolak.
		{"api key", []reqOpt{header("X-API-Key", "gac_test_good")}, 401, "UNAUTHENTICATED"},
	}
	for _, c := range cases {
		rec, r := doReq(t, mux, "GET", "/api/v1/admin/users", "", c.opts...)
		if rec.Code != c.status || code(r) != c.code {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	if len(svc.calls) != 0 {
		t.Fatalf("calls = %v", svc.calls)
	}
	// RequireRole tanpa principal (salah rakit) -> 401; principal API key -> 403.
	h := RequireRole(domain.RoleAdmin)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("must not run") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("no principal = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, withPrincipal(httptest.NewRequest("GET", "/", nil), svc.p2.principal))
	if rec.Code != 403 {
		t.Fatalf("api key principal = %d", rec.Code)
	}
}

func TestWhoAmI_APIKeyAndScopes(t *testing.T) {
	readOnly := app.Principal{UserID: userID, APIKeyID: keyID, Roles: []domain.Role{domain.RoleUser}, Scopes: []domain.Scope{domain.ScopeRead}}
	svc := &fakeSvc{p2: p2State{principal: readOnly}}
	mux := newP2Mux(t, svc, Options{})

	for _, o := range []reqOpt{header("X-API-Key", "gac_test_good"), header("Authorization", "ApiKey gac_test_good")} {
		rec, r := doReq(t, mux, "GET", "/api/v1/auth/whoami", "", o)
		var m map[string]any
		_ = json.Unmarshal(r.Data, &m)
		if rec.Code != 200 || m["auth_method"] != "api_key" || m["api_key_id"] != keyID.String() || m["session_id"] != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	rec, r := doReq(t, mux, "GET", "/api/v1/auth/whoami", "", bearerTok("good"))
	var m map[string]any
	_ = json.Unmarshal(r.Data, &m)
	if rec.Code != 200 || m["auth_method"] != "access_token" || m["session_id"] != sessID.String() {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// Scope read tidak cukup untuk method non-GET.
	inner := NewAuthenticator(roleVerifier{}, svc, svc, nil).Any(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-API-Key", "gac_test_good")
	inner.ServeHTTP(rr, req)
	if rr.Code != 403 || !strings.Contains(rr.Body.String(), "INSUFFICIENT_SCOPE") {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}

	for _, c := range []struct {
		name   string
		svc    *fakeSvc
		opt    Options
		key    string
		status int
		code   string
	}{
		{"bad key", &fakeSvc{}, Options{}, "gac_test_bad", 401, "API_KEY_INVALID"},
		{"no key", &fakeSvc{}, Options{}, "", 401, "UNAUTHENTICATED"},
		{"suspended", &fakeSvc{p2: p2State{apiKeyErr: domain.ErrAccountSuspended}}, Options{}, "gac_test_good", 403, "ACCOUNT_SUSPENDED"},
		{"rate", &fakeSvc{p2: p2State{principal: readOnly}}, Options{UserLimiter: denyLimiter{prefix: "apikey:"}}, "gac_test_good", 429, "RATE_LIMITED"},
	} {
		var opts []reqOpt
		if c.key != "" {
			opts = append(opts, header("X-API-Key", c.key))
		}
		rec, r := doReq(t, newP2Mux(t, c.svc, c.opt), "GET", "/api/v1/auth/whoami", "", opts...)
		if rec.Code != c.status || code(r) != c.code {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	// Endpoint pengelolaan akun tidak menerima API key.
	rec, _ = doReq(t, mux, "GET", "/api/v1/users/me/api-keys", "", header("X-API-Key", "gac_test_good"))
	if rec.Code != 401 {
		t.Fatalf("api key on account mgmt = %d", rec.Code)
	}
}

func TestOAuthRoutesDisabled(t *testing.T) {
	svc := &fakeSvc{}
	mux := newP2Mux(t, svc, Options{})
	for _, p := range []struct{ m, path string }{
		{"GET", "/api/v1/auth/oauth/google/start"}, {"GET", "/api/v1/auth/oauth/google/callback"},
		{"POST", "/api/v1/auth/oauth/exchange"}, {"GET", "/api/v1/users/me/identities"},
	} {
		if rec, _ := doReq(t, mux, p.m, p.path, "", bearerTok("good")); rec.Code != http.StatusTeapot {
			t.Fatalf("%s must not be registered: %d", p.path, rec.Code)
		}
	}
}

// oauthFlowCookie menjalankan /start dan mengembalikan cookie flow.
func oauthFlowCookie(t *testing.T, mux http.Handler, opts ...reqOpt) *http.Cookie {
	t.Helper()
	rec, _ := doReq(t, mux, "GET", "/api/v1/auth/oauth/google/start", "", opts...)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "https://accounts.example/") {
		t.Fatalf("start = %d %v", rec.Code, rec.Header())
	}
	cs := rec.Result().Cookies()
	if len(cs) != 1 || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteLaxMode || strings.Contains(cs[0].Value, "verif") {
		t.Fatalf("cookie = %+v", cs)
	}
	return cs[0]
}

func TestOAuthFlow(t *testing.T) {
	svc := &fakeSvc{p2: p2State{oauth: true}}
	mux := newP2Mux(t, svc, Options{})
	c := oauthFlowCookie(t, mux)
	withCookie := func(c *http.Cookie) reqOpt { return func(r *http.Request) { r.AddCookie(c) } }

	rec, _ := doReq(t, mux, "GET", "/api/v1/auth/oauth/google/callback?state=state-1&code=xyz", "", withCookie(c))
	loc := rec.Header().Get("Location")
	if rec.Code != 302 || loc != "http://front.example/oauth/cb#code=login-code" {
		t.Fatalf("%d %s", rec.Code, loc)
	}
	in := svc.last.(app.OAuthCallbackInput)
	if in.Verifier != "verif" || in.Nonce != "nonce" || in.Code != "xyz" || in.LinkUserID != uuid.Nil {
		t.Fatalf("%+v", in)
	}
	// Cookie selalu dihapus setelah callback.
	if cs := rec.Result().Cookies(); len(cs) != 1 || cs[0].MaxAge != -1 {
		t.Fatalf("cookie not cleared: %+v", cs)
	}

	bad := []struct{ name, query, want string }{
		{"state mismatch", "state=evil&code=x", "OAUTH_STATE_INVALID"},
		{"no state", "code=x", "OAUTH_STATE_INVALID"},
		{"canceled", "state=state-1&error=access_denied", "OAUTH_CANCELED"},
	}
	for _, b := range bad {
		rec, _ := doReq(t, mux, "GET", "/api/v1/auth/oauth/google/callback?"+b.query, "", withCookie(c))
		if rec.Code != 302 || rec.Header().Get("Location") != "http://front.example/oauth/cb?error="+b.want {
			t.Errorf("%s: %d %s", b.name, rec.Code, rec.Header().Get("Location"))
		}
	}
	// Tanpa cookie / cookie dirusak.
	rec, _ = doReq(t, mux, "GET", "/api/v1/auth/oauth/google/callback?state=state-1&code=x", "")
	if !strings.HasSuffix(rec.Header().Get("Location"), "error=OAUTH_STATE_INVALID") {
		t.Fatal(rec.Header().Get("Location"))
	}
	tampered := *c
	tampered.Value = c.Value[:len(c.Value)-4] + "AAAA"
	rec, _ = doReq(t, mux, "GET", "/api/v1/auth/oauth/google/callback?state=state-1&code=x", "", withCookie(&tampered))
	if !strings.HasSuffix(rec.Header().Get("Location"), "error=OAUTH_STATE_INVALID") {
		t.Fatal(rec.Header().Get("Location"))
	}

	// Error domain diteruskan sebagai kode; error tak dikenal -> INTERNAL.
	for _, e := range []struct {
		err  error
		want string
	}{{domain.ErrOAuthAccountExists, "OAUTH_ACCOUNT_EXISTS"}, {errors.New("boom"), "INTERNAL"}} {
		svc.p2.oauthCbErr = e.err
		rec, _ = doReq(t, mux, "GET", "/api/v1/auth/oauth/google/callback?state=state-1&code=x", "", withCookie(c))
		if rec.Header().Get("Location") != "http://front.example/oauth/cb?error="+e.want {
			t.Errorf("%v: %s", e.err, rec.Header().Get("Location"))
		}
	}
}

func TestOAuthLinkFlow(t *testing.T) {
	svc := &fakeSvc{p2: p2State{oauth: true, linked: true}}
	mux := newP2Mux(t, svc, Options{})
	rec, r := doReq(t, mux, "POST", "/api/v1/users/me/identities/google/link", "", bearerTok("good"))
	if rec.Code != 200 || !strings.Contains(string(r.Data), "accounts.example") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c := rec.Result().Cookies()[0]
	rec, _ = doReq(t, mux, "GET", "/api/v1/auth/oauth/google/callback?state=state-1&code=x", "", func(r *http.Request) { r.AddCookie(c) })
	if rec.Header().Get("Location") != "http://front.example/oauth/cb?linked=google" {
		t.Fatal(rec.Header().Get("Location"))
	}
	if in := svc.last.(app.OAuthCallbackInput); in.LinkUserID != userID {
		t.Fatalf("link user = %v", in.LinkUserID)
	}
}

func TestOAuthCookieExpired(t *testing.T) {
	svc := &fakeSvc{p2: p2State{oauth: true}}
	h := NewP2Handler(NewHandler(svc, validator.New(), fixedClock{}, Options{}), svc, P2Options{Sealer: testSealer(t), OAuthRedirectURL: "http://f/cb"})
	raw, _ := json.Marshal(oauthFlow{State: "s", Exp: now.Add(-time.Second).Unix()})
	sealed, _ := h.opt.Sealer.EncryptString(string(raw), oauthCookieAAD)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: oauthCookieName, Value: sealed})
	if _, ok := h.readOAuthCookie(req); ok {
		t.Fatal("expired flow accepted")
	}
	// Redirect URL kosong -> 500 (salah konfigurasi), bukan open redirect.
	h.opt.OAuthRedirectURL = ""
	rec := httptest.NewRecorder()
	h.redirectError(rec, httptest.NewRequest("GET", "/", nil), "X")
	if rec.Code != 500 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestOAuthStartError(t *testing.T) {
	svc := &fakeSvc{err: domain.ErrOAuthDisabled, p2: p2State{oauth: true}}
	rec, r := doReq(t, newP2Mux(t, svc, Options{}), "GET", "/api/v1/auth/oauth/google/start", "")
	if rec.Code != 404 || code(r) != "NOT_FOUND" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// fakeChallengeID: token "bad" dianggap tidak valid; selain itu jti = token.
func fakeChallengeID(tok string) (string, error) {
	if tok == "bad" {
		return "", errors.New("invalid")
	}
	return "jti-" + tok, nil
}

func TestLoginMFA_PerChallengeAttemptLimit(t *testing.T) {
	const maxAttempts = 5
	svc := &fakeSvc{err: domain.ErrInvalidMFACode}
	lim := middleware.NewMemoryLimiter(t.Context(), middleware.RateConfig{Limit: 1, Period: 24 * time.Hour, Burst: maxAttempts}, time.Hour)
	mux := newP2MuxWith(t, svc, Options{}, P2Options{MFAAttempts: MFAAttemptGuard{Limiter: lim, ChallengeID: fakeChallengeID}})
	body := func(tok string) string { return `{"mfa_token":"` + tok + `","code":"123456"}` }

	for i := range maxAttempts {
		rec, r := doReq(t, mux, "POST", "/api/v1/auth/login/2fa", body("A"))
		if rec.Code != 401 || code(r) != "INVALID_MFA_CODE" {
			t.Fatalf("attempt %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	calls := len(svc.calls)
	// Percobaan ke-6 pada challenge yang sama: challenge hangus, service tidak dipanggil.
	rec, r := doReq(t, mux, "POST", "/api/v1/auth/login/2fa", body("A"))
	if rec.Code != 401 || code(r) != "MFA_CHALLENGE_EXPIRED" || len(svc.calls) != calls {
		t.Fatalf("attempt 6: %d %s calls=%d", rec.Code, rec.Body, len(svc.calls)-calls)
	}
	// Bahkan kode yang benar ditolak setelah challenge hangus.
	svc.err = nil
	if rec, r := doReq(t, mux, "POST", "/api/v1/auth/login/2fa", body("A")); rec.Code != 401 || code(r) != "MFA_CHALLENGE_EXPIRED" {
		t.Fatalf("challenge hangus harus tetap ditolak: %d %s", rec.Code, rec.Body)
	}
	// Challenge baru (login ulang) punya kuota sendiri.
	if rec, _ := doReq(t, mux, "POST", "/api/v1/auth/login/2fa", body("B")); rec.Code != 200 {
		t.Fatalf("challenge baru: %d %s", rec.Code, rec.Body)
	}
	// Token tidak valid tidak dihitung; service tetap menolak dengan MFA_TOKEN_INVALID.
	svc.err = domain.ErrMFATokenInvalid
	for range maxAttempts + 2 {
		if rec, r := doReq(t, mux, "POST", "/api/v1/auth/login/2fa", body("bad")); rec.Code != 401 || code(r) != "MFA_TOKEN_INVALID" {
			t.Fatalf("bad token: %d %s", rec.Code, rec.Body)
		}
	}
	if lim.Len() != 2 {
		t.Fatalf("limiter keys = %d; want 2 (A, B)", lim.Len())
	}
}

func TestLoginMFA_NoGuardIsUnlimited(t *testing.T) {
	svc := &fakeSvc{err: domain.ErrInvalidMFACode}
	mux := newP2MuxWith(t, svc, Options{}, P2Options{MFAAttempts: MFAAttemptGuard{Limiter: denyLimiter{prefix: ""}}})
	// ChallengeID nil -> guard nonaktif meski Limiter ada.
	if rec, r := doReq(t, mux, "POST", "/api/v1/auth/login/2fa", `{"mfa_token":"A","code":"123456"}`); rec.Code != 401 || code(r) != "INVALID_MFA_CODE" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
