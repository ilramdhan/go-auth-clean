package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

var errBoom = errors.New("boom")

// curCode: kode TOTP valid untuk waktu fake clock saat ini.
func (e *env) curCode() string { return totpCode(e.clock.t.Unix() / 30) }

// enableMFA: user aktif + login + 2FA aktif. Mengembalikan login result & recovery codes.
func (e *env) enableMFA(t *testing.T, email string) (LoginResult, []string) {
	t.Helper()
	e.activeUser(t, email)
	lr := e.login(t, email)
	uid := lr.User.ID
	_, err := e.svc.SetupMFA(ctx, uid)
	must(t, err)
	codes, err := e.svc.EnableMFA(ctx, MFACodeInput{UserID: uid, SessionID: lr.Tokens.SessionID, Code: e.curCode()})
	must(t, err)
	e.clock.Advance(30 * time.Second) // step berikutnya untuk kode baru
	return lr, codes
}

func TestMFA_SetupEnableLogin(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@example.com")
	lr := e.login(t, "a@example.com")
	other := e.login(t, "a@example.com")

	st, err := e.svc.GetMFAStatus(ctx, u.ID)
	must(t, err)
	if st.Enabled || st.Pending {
		t.Fatalf("status awal = %+v", st)
	}
	setup, err := e.svc.SetupMFA(ctx, u.ID)
	must(t, err)
	if setup.Secret == "" || !strings.Contains(setup.OTPAuthURI, "a@example.com") {
		t.Fatalf("setup = %+v", setup)
	}
	if got := e.st.mfa[u.ID].SecretEncrypted; got == setup.Secret || !strings.HasPrefix(got, "enc:") {
		t.Fatal("secret harus terenkripsi")
	}
	st, _ = e.svc.GetMFAStatus(ctx, u.ID)
	if !st.Pending {
		t.Fatal("harus pending")
	}

	// kode salah / bukan angka
	_, err = e.svc.EnableMFA(ctx, MFACodeInput{UserID: u.ID, Code: "000000"})
	wantErr(t, err, domain.ErrInvalidMFACode)
	_, err = e.svc.EnableMFA(ctx, MFACodeInput{UserID: u.ID, Code: "abc"})
	wantErr(t, err, domain.ErrInvalidMFACode)
	if len(e.st.events(domain.EventMFAFailed)) != 2 {
		t.Fatal("mfa_failed harus diaudit")
	}

	codes, err := e.svc.EnableMFA(ctx, MFACodeInput{UserID: u.ID, SessionID: lr.Tokens.SessionID, Code: e.curCode()})
	must(t, err)
	if len(codes) != domain.RecoveryCodeCount || len(codes[0]) != domain.RecoveryCodeLength+1 {
		t.Fatalf("codes = %v", codes)
	}
	// sesi lain dicabut, sesi ini tetap
	wantErr(t, e.svc.AuthenticateSession(ctx, other.Tokens.SessionID), domain.ErrSessionInvalid)
	must(t, e.svc.AuthenticateSession(ctx, lr.Tokens.SessionID))

	_, err = e.svc.EnableMFA(ctx, MFACodeInput{UserID: u.ID, Code: e.curCode()})
	wantErr(t, err, domain.ErrMFAAlreadyEnabled)
	_, err = e.svc.SetupMFA(ctx, u.ID)
	wantErr(t, err, domain.ErrMFAAlreadyEnabled)

	st, _ = e.svc.GetMFAStatus(ctx, u.ID)
	if !st.Enabled || st.RecoveryCodesRemaining != 10 {
		t.Fatalf("status = %+v", st)
	}
	me, _ := e.svc.Me(ctx, u.ID)
	if !me.MFAEnabled {
		t.Fatal("Me.MFAEnabled")
	}

	// login sekarang butuh 2FA
	res, err := e.svc.Login(ctx, LoginInput{Email: "a@example.com", Password: pw})
	must(t, err)
	if !res.MFARequired || res.MFAToken == "" || res.Tokens.AccessToken != "" {
		t.Fatalf("login = %+v", res)
	}
	// kode yang sama dengan saat enable = replay (step sama)
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: res.MFAToken, Code: e.curCode()})
	wantErr(t, err, domain.ErrMFACodeReplayed)

	e.clock.Advance(30 * time.Second)
	out, err := e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: res.MFAToken, Code: e.curCode()})
	must(t, err)
	if out.Tokens.AccessToken == "" {
		t.Fatal("token kosong")
	}
	// replay kode yang sama
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: res.MFAToken, Code: e.curCode()})
	wantErr(t, err, domain.ErrMFACodeReplayed)

	// recovery code (format bebas: huruf besar, tanpa '-')
	rc := strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: res.MFAToken, Code: rc})
	must(t, err)
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: res.MFAToken, Code: codes[0]})
	wantErr(t, err, domain.ErrInvalidMFACode)
	if len(e.st.events(domain.EventMFARecoveryCodeUsed)) != 1 {
		t.Fatal("recovery code used harus diaudit")
	}
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: res.MFAToken, Code: "short"})
	wantErr(t, err, domain.ErrInvalidMFACode)
}

func TestLoginMFA_Errors(t *testing.T) {
	e := newEnv(t)
	lr, _ := e.enableMFA(t, "a@example.com")
	uid := lr.User.ID

	_, err := e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: "garbage", Code: "123456"})
	wantErr(t, err, domain.ErrMFATokenInvalid)
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: "mfa:" + uuid.NewString(), Code: "123456"})
	wantErr(t, err, domain.ErrMFATokenInvalid)

	tok := "mfa:" + uid.String()
	// brute force kode -> lockout
	for range domain.MaxFailedLoginAttempts {
		_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: tok, Code: "999999"})
		wantErr(t, err, domain.ErrInvalidMFACode)
	}
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: tok, Code: e.curCode()})
	wantErr(t, err, domain.ErrAccountLocked)
	e.clock.Advance(domain.LockDuration + time.Minute)

	// user suspended
	e.st.users[uid].Status = domain.UserStatusSuspended
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: tok, Code: e.curCode()})
	wantErr(t, err, domain.ErrAccountSuspended)
	e.st.users[uid].Status = domain.UserStatusActive

	// error infrastruktur
	e.st.fail["users.FindByID"] = errBoom
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: tok, Code: e.curCode()})
	wantErr(t, err, errBoom)
	delete(e.st.fail, "users.FindByID")
	e.st.fail["mfa.Get"] = errBoom
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: tok, Code: e.curCode()})
	wantErr(t, err, errBoom)
	_, err = e.svc.Login(ctx, LoginInput{Email: "a@example.com", Password: pw})
	wantErr(t, err, errBoom)
	delete(e.st.fail, "mfa.Get")
	e.st.fail["mfa.AdvanceStep"] = errBoom
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: tok, Code: e.curCode()})
	wantErr(t, err, errBoom)
	delete(e.st.fail, "mfa.AdvanceStep")
	e.st.fail["mfa.UseRecoveryCode"] = errBoom
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: tok, Code: "abcde-fghjk"})
	wantErr(t, err, errBoom)
	delete(e.st.fail, "mfa.UseRecoveryCode")

	// secret rusak (decrypt gagal) -> error internal, bukan invalid code
	e.st.mfa[uid].SecretEncrypted = "corrupt"
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: tok, Code: e.curCode()})
	if err == nil || errors.Is(err, domain.ErrInvalidMFACode) {
		t.Fatalf("err = %v", err)
	}

	// 2FA dimatikan setelah token dibuat
	delete(e.st.mfa, uid)
	_, err = e.svc.LoginMFA(ctx, LoginMFAInput{MFAToken: tok, Code: e.curCode()})
	wantErr(t, err, domain.ErrMFATokenInvalid)
}

func TestLogin_MFATokenIssueError(t *testing.T) {
	e := newEnv(t, func(d *Deps, _ *Config) { d.MFATokens = fakeMFATokens{err: errBoom} })
	e.enableMFA(t, "a@example.com")
	_, err := e.svc.Login(ctx, LoginInput{Email: "a@example.com", Password: pw})
	wantErr(t, err, errBoom)
}

func TestMFA_DisableAndRegenerate(t *testing.T) {
	e := newEnv(t)
	lr, codes := e.enableMFA(t, "a@example.com")
	uid, sid := lr.User.ID, lr.Tokens.SessionID

	// regenerate
	_, err := e.svc.RegenerateRecoveryCodes(ctx, MFACodeInput{UserID: uid, Code: "000000"})
	wantErr(t, err, domain.ErrInvalidMFACode)
	newCodes, err := e.svc.RegenerateRecoveryCodes(ctx, MFACodeInput{UserID: uid, SessionID: sid, Code: e.curCode()})
	must(t, err)
	if newCodes[0] == codes[0] {
		t.Fatal("codes harus baru")
	}
	e.clock.Advance(30 * time.Second)

	// disable: password salah, kode salah, sukses
	err = e.svc.DisableMFA(ctx, DisableMFAInput{UserID: uid, Password: "salah", Code: e.curCode()})
	wantErr(t, err, domain.ErrInvalidCredentials)
	err = e.svc.DisableMFA(ctx, DisableMFAInput{UserID: uid, Password: pw, Code: "111111"})
	wantErr(t, err, domain.ErrInvalidMFACode)
	// recovery code lama sudah tidak berlaku
	err = e.svc.DisableMFA(ctx, DisableMFAInput{UserID: uid, Password: pw, Code: codes[1]})
	wantErr(t, err, domain.ErrInvalidMFACode)
	must(t, e.svc.DisableMFA(ctx, DisableMFAInput{UserID: uid, SessionID: sid, Password: pw, Code: newCodes[0]}))
	if _, ok := e.st.mfa[uid]; ok {
		t.Fatal("mfa harus terhapus")
	}
	if len(e.st.events(domain.EventMFADisabled)) != 1 {
		t.Fatal("audit disable")
	}
	err = e.svc.DisableMFA(ctx, DisableMFAInput{UserID: uid, Password: pw, Code: "123456"})
	wantErr(t, err, domain.ErrMFANotEnabled)
	_, err = e.svc.RegenerateRecoveryCodes(ctx, MFACodeInput{UserID: uid, Code: "123456"})
	wantErr(t, err, domain.ErrMFANotEnabled)

	// login kembali tanpa 2FA
	res := e.login(t, "a@example.com")
	if res.MFARequired {
		t.Fatal("2FA harus nonaktif")
	}
	_, err = e.svc.EnableMFA(ctx, MFACodeInput{UserID: uid, Code: "123456"})
	wantErr(t, err, domain.ErrMFASetupRequired)
	err = e.svc.DisableMFA(ctx, DisableMFAInput{UserID: uuid.New(), Password: pw})
	wantErr(t, err, domain.ErrUserNotFound)
}

func TestMFA_InfraErrors(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@example.com")

	_, err := e.svc.SetupMFA(ctx, uuid.New())
	wantErr(t, err, domain.ErrUserNotFound)
	e.st.fail["mfa.UpsertPending"] = errBoom
	_, err = e.svc.SetupMFA(ctx, u.ID)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "mfa.UpsertPending")

	e2 := newEnv(t, func(d *Deps, _ *Config) { d.TOTP = fakeTOTP{genErr: errBoom} })
	u2 := e2.activeUser(t, "b@example.com")
	_, err = e2.svc.SetupMFA(ctx, u2.ID)
	wantErr(t, err, errBoom)
	e3 := newEnv(t, func(d *Deps, _ *Config) { d.Sealer = fakeSealer{err: errBoom} })
	u3 := e3.activeUser(t, "c@example.com")
	_, err = e3.svc.SetupMFA(ctx, u3.ID)
	wantErr(t, err, errBoom)

	_, err = e.svc.SetupMFA(ctx, u.ID)
	must(t, err)
	e.st.fail["mfa.ReplaceRecoveryCodes"] = errBoom
	_, err = e.svc.EnableMFA(ctx, MFACodeInput{UserID: u.ID, Code: e.curCode()})
	wantErr(t, err, errBoom)
	if e.st.mfa[u.ID].IsEnabled() {
		t.Fatal("harus rollback")
	}
	delete(e.st.fail, "mfa.ReplaceRecoveryCodes")
	e.st.fail["mfa.Get"] = errBoom
	_, err = e.svc.EnableMFA(ctx, MFACodeInput{UserID: u.ID, Code: e.curCode()})
	wantErr(t, err, errBoom)
	_, err = e.svc.GetMFAStatus(ctx, u.ID)
	wantErr(t, err, errBoom)
	_, err = e.svc.Me(ctx, u.ID)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "mfa.Get")
	_, err = e.svc.EnableMFA(ctx, MFACodeInput{UserID: u.ID, Code: e.curCode()})
	must(t, err)
	e.clock.Advance(30 * time.Second)

	e.st.fail["mfa.CountRecoveryCodes"] = errBoom
	_, err = e.svc.GetMFAStatus(ctx, u.ID)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "mfa.CountRecoveryCodes")
	e.st.fail["mfa.ReplaceRecoveryCodes"] = errBoom
	_, err = e.svc.RegenerateRecoveryCodes(ctx, MFACodeInput{UserID: u.ID, Code: e.curCode()})
	wantErr(t, err, errBoom)
	delete(e.st.fail, "mfa.ReplaceRecoveryCodes")
	e.st.fail["mfa.Delete"] = errBoom
	err = e.svc.DisableMFA(ctx, DisableMFAInput{UserID: u.ID, Password: pw, Code: e.curCode()})
	wantErr(t, err, errBoom)
	delete(e.st.fail, "mfa.Delete")
	e.st.fail["roles.ListByUser"] = errBoom
	_, err = e.svc.Me(ctx, u.ID)
	wantErr(t, err, errBoom)
	_, err = e.svc.Me(ctx, uuid.New())
	wantErr(t, err, domain.ErrUserNotFound)
}

func TestRandomString(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		s, err := randomString("ab", 64)
		must(t, err)
		if len(s) != 64 || strings.Trim(s, "ab") != "" || seen[s] {
			t.Fatalf("s = %q", s)
		}
		seen[s] = true
	}
}

// ===== Admin / RBAC =====

func (e *env) admin(t *testing.T, email string) *domain.User {
	t.Helper()
	u := e.activeUser(t, email)
	_, err := e.svc.PromoteByEmail(ctx, email)
	must(t, err)
	return u
}

func TestAdmin_PromoteAndRoles(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.PromoteByEmail(ctx, "bad")
	wantErr(t, err, domain.ErrInvalidEmail)
	_, err = e.svc.PromoteByEmail(ctx, "none@example.com")
	wantErr(t, err, domain.ErrUserNotFound)

	a := e.admin(t, "admin@example.com")
	_, err = e.svc.PromoteByEmail(ctx, "ADMIN@example.com") // idempoten
	must(t, err)
	if r := e.st.roles[a.ID]; len(r) != 1 || r[0] != domain.RoleAdmin {
		t.Fatalf("roles = %v", r)
	}
	lr := e.login(t, "admin@example.com")
	if !domain.HasRole(lr.User.Roles, domain.RoleAdmin) || !domain.HasRole(lr.Tokens.roles, domain.RoleUser) {
		t.Fatalf("roles login = %v", lr.User.Roles)
	}
	e.st.fail["roles.Grant"] = errBoom
	_, err = e.svc.PromoteByEmail(ctx, "admin@example.com")
	wantErr(t, err, errBoom)
	delete(e.st.fail, "roles.Grant")

	u := e.activeUser(t, "u@example.com")
	in := RoleChangeInput{ActorID: a.ID, TargetID: u.ID, Role: "admin"}
	must(t, e.svc.GrantRole(ctx, in))
	if !domain.HasRole(e.st.roles[u.ID], domain.RoleAdmin) {
		t.Fatal("grant")
	}
	must(t, e.svc.GrantRole(ctx, RoleChangeInput{ActorID: a.ID, TargetID: u.ID, Role: "user"}))
	wantErr(t, e.svc.GrantRole(ctx, RoleChangeInput{ActorID: a.ID, TargetID: u.ID, Role: "root"}), domain.ErrInvalidRole)
	wantErr(t, e.svc.GrantRole(ctx, RoleChangeInput{ActorID: a.ID, TargetID: uuid.New(), Role: "admin"}), domain.ErrUserNotFound)

	wantErr(t, e.svc.RevokeRole(ctx, RoleChangeInput{ActorID: a.ID, TargetID: a.ID, Role: "admin"}), domain.ErrCannotModifySelf)
	wantErr(t, e.svc.RevokeRole(ctx, RoleChangeInput{ActorID: a.ID, TargetID: u.ID, Role: "user"}), domain.ErrInvalidRole)
	wantErr(t, e.svc.RevokeRole(ctx, RoleChangeInput{ActorID: a.ID, TargetID: u.ID, Role: "x"}), domain.ErrInvalidRole)
	wantErr(t, e.svc.RevokeRole(ctx, RoleChangeInput{ActorID: a.ID, TargetID: uuid.New(), Role: "admin"}), domain.ErrUserNotFound)
	must(t, e.svc.RevokeRole(ctx, in))
	if domain.HasRole(e.st.roles[u.ID], domain.RoleAdmin) {
		t.Fatal("revoke")
	}
	// u bukan admin lagi -> forbidden (dicek dari DB, bukan JWT)
	wantErr(t, e.svc.GrantRole(ctx, RoleChangeInput{ActorID: u.ID, TargetID: u.ID, Role: "admin"}), domain.ErrForbidden)
	wantErr(t, e.svc.RevokeRole(ctx, RoleChangeInput{ActorID: u.ID, TargetID: a.ID, Role: "admin"}), domain.ErrForbidden)
	if len(e.st.events(domain.EventRoleGranted)) < 2 || len(e.st.events(domain.EventRoleRevoked)) != 1 {
		t.Fatal("audit role")
	}
	e.st.fail["roles.Revoke"] = errBoom
	wantErr(t, e.svc.RevokeRole(ctx, in), errBoom)
	e.st.fail["roles.ListByUser"] = errBoom
	wantErr(t, e.svc.AuthorizeAdmin(ctx, a.ID), errBoom)
}

func TestAdmin_SuspendActivate(t *testing.T) {
	e := newEnv(t)
	a := e.admin(t, "admin@example.com")
	u := e.activeUser(t, "u@example.com")
	lr := e.login(t, "u@example.com")
	ck, err := e.svc.CreateAPIKey(ctx, CreateAPIKeyInput{UserID: u.ID, Name: "ci", Scopes: []string{"read"}})
	must(t, err)

	in := AdminActionInput{ActorID: a.ID, TargetID: u.ID}
	wantErr(t, e.svc.SuspendUser(ctx, AdminActionInput{ActorID: u.ID, TargetID: a.ID}), domain.ErrForbidden)
	wantErr(t, e.svc.SuspendUser(ctx, AdminActionInput{ActorID: a.ID, TargetID: a.ID}), domain.ErrCannotModifySelf)
	wantErr(t, e.svc.ActivateUser(ctx, in), domain.ErrInvalidTransition)
	must(t, e.svc.SuspendUser(ctx, in))
	wantErr(t, e.svc.SuspendUser(ctx, in), domain.ErrInvalidTransition)
	wantErr(t, e.svc.AuthenticateSession(ctx, lr.Tokens.SessionID), domain.ErrSessionInvalid)
	_, err = e.svc.AuthenticateAPIKey(ctx, ck.Key)
	wantErr(t, err, domain.ErrAPIKeyInvalid)
	_, err = e.svc.Login(ctx, LoginInput{Email: "u@example.com", Password: pw})
	wantErr(t, err, domain.ErrAccountSuspended)
	ev := e.st.events(domain.EventUserSuspended)
	if len(ev) != 1 || ev[0].UserID != u.ID || ev[0].Metadata["actor_id"] != a.ID.String() {
		t.Fatalf("audit = %+v", ev)
	}

	must(t, e.svc.ActivateUser(ctx, in))
	e.login(t, "u@example.com")
	wantErr(t, e.svc.SuspendUser(ctx, AdminActionInput{ActorID: a.ID, TargetID: uuid.New()}), domain.ErrUserNotFound)

	e.st.fail["keys.RevokeAllByUser"] = errBoom
	wantErr(t, e.svc.SuspendUser(ctx, in), errBoom)
	if e.st.users[u.ID].Status != domain.UserStatusActive {
		t.Fatal("harus rollback")
	}
	delete(e.st.fail, "keys.RevokeAllByUser")
	e.st.fail["sessions.RevokeAllByUser"] = errBoom
	wantErr(t, e.svc.SuspendUser(ctx, in), errBoom)
}

func TestAdmin_ListAndAudit(t *testing.T) {
	e := newEnv(t)
	a := e.admin(t, "admin@example.com")
	for _, em := range []string{"budi@example.com", "siti@example.com", "budiman@example.com"} {
		e.clock.Advance(time.Second)
		e.activeUser(t, em)
	}
	_, err := e.svc.ListUsers(ctx, a.ID, domain.UserFilter{})
	if err == nil {
		t.Fatal("limit 0 harus error")
	}
	list, err := e.svc.ListUsers(ctx, a.ID, domain.UserFilter{Query: "SITI", Limit: 10})
	must(t, err)
	if len(list) != 1 || list[0].Email != "siti@example.com" {
		t.Fatalf("list = %+v", list)
	}
	list, err = e.svc.ListUsers(ctx, a.ID, domain.UserFilter{Limit: 2})
	must(t, err)
	next, err := e.svc.ListUsers(ctx, a.ID, domain.UserFilter{Limit: 10,
		After: &domain.UserKeyset{CreatedAt: list[1].CreatedAt, ID: list[1].ID}})
	must(t, err)
	if len(list)+len(next) != 4 {
		t.Fatalf("paging %d+%d", len(list), len(next))
	}
	_, err = e.svc.ListUsers(ctx, list[1].ID, domain.UserFilter{Limit: 1})
	wantErr(t, err, domain.ErrForbidden)

	got, err := e.svc.GetUser(ctx, a.ID, list[0].ID)
	must(t, err)
	if got.Email != list[0].Email {
		t.Fatal("GetUser")
	}
	_, err = e.svc.GetUser(ctx, list[0].ID, a.ID)
	wantErr(t, err, domain.ErrForbidden)

	ev, err := e.svc.UserAuditLog(ctx, a.ID, list[0].ID, nil, 10)
	must(t, err)
	if len(ev) == 0 {
		t.Fatal("audit kosong")
	}
	_, err = e.svc.UserAuditLog(ctx, a.ID, uuid.New(), nil, 10)
	wantErr(t, err, domain.ErrUserNotFound)
	_, err = e.svc.UserAuditLog(ctx, list[0].ID, a.ID, nil, 10)
	wantErr(t, err, domain.ErrForbidden)
}

// ===== OAuth =====

func TestOAuth_RegisterLoginExchange(t *testing.T) {
	e := newEnv(t)
	if !e.svc.OAuthEnabled("google") || e.svc.OAuthEnabled("github") {
		t.Fatal("OAuthEnabled")
	}
	_, err := e.svc.StartOAuth(ctx, "github")
	wantErr(t, err, domain.ErrOAuthDisabled)
	st, err := e.svc.StartOAuth(ctx, "google")
	must(t, err)
	if st.State == "" || st.Verifier == "" || st.Nonce == "" || !strings.Contains(st.AuthURL, "code_challenge=") ||
		strings.Contains(st.AuthURL, st.Verifier) {
		t.Fatalf("start = %+v", st)
	}

	in := OAuthCallbackInput{Provider: "google", Code: "c", Verifier: st.Verifier, Nonce: st.Nonce}
	res, err := e.svc.OAuthCallback(ctx, in)
	must(t, err)
	if res.LoginCode == "" {
		t.Fatal("login code kosong")
	}
	u, err := fakeUsers{e.st}.FindByEmail(ctx, "google@example.com")
	must(t, err)
	if u.Status != domain.UserStatusActive || u.HasPassword() || u.FullName != "Gina" {
		t.Fatalf("user = %+v", u)
	}

	lr, err := e.svc.ExchangeOAuthCode(ctx, res.LoginCode, RequestMeta{})
	must(t, err)
	if lr.Tokens.AccessToken == "" || lr.User.ID != u.ID {
		t.Fatal("exchange")
	}
	// kode sekali pakai
	_, err = e.svc.ExchangeOAuthCode(ctx, res.LoginCode, RequestMeta{})
	wantErr(t, err, domain.ErrLoginCodeInvalid)

	// login kedua: identity sudah ada
	res, err = e.svc.OAuthCallback(ctx, in)
	must(t, err)
	// kode expired
	e.clock.Advance(2 * time.Minute)
	_, err = e.svc.ExchangeOAuthCode(ctx, res.LoginCode, RequestMeta{})
	wantErr(t, err, domain.ErrLoginCodeInvalid)
	r, err := e.svc.Cleanup(ctx)
	must(t, err)
	_ = r

	// akun password tidak bisa login dengan password kosong
	_, err = e.svc.Login(ctx, LoginInput{Email: "google@example.com", Password: ""})
	wantErr(t, err, domain.ErrInvalidCredentials)

	ids, err := e.svc.ListIdentities(ctx, u.ID)
	must(t, err)
	if len(ids) != 1 || ids[0].Subject != "g-123" {
		t.Fatalf("ids = %+v", ids)
	}

	// suspended user tidak bisa login OAuth
	e.st.users[u.ID].Status = domain.UserStatusSuspended
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrAccountSuspended)
}

func TestOAuth_NoAutoLinkAndLinkFlow(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "google@example.com")
	in := OAuthCallbackInput{Provider: "google", Code: "c", Verifier: "v", Nonce: "n"}
	_, err := e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrOAuthAccountExists)

	// link saat login
	in.LinkUserID = u.ID
	res, err := e.svc.OAuthCallback(ctx, in)
	must(t, err)
	if !res.Linked {
		t.Fatal("linked")
	}
	res, err = e.svc.OAuthCallback(ctx, in) // idempoten
	must(t, err)
	if !res.Linked || len(e.st.events(domain.EventOAuthLinked)) != 1 {
		t.Fatal("link idempoten")
	}
	// user lain mencoba link identity yang sama
	o := e.activeUser(t, "other@example.com")
	in.LinkUserID = o.ID
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrIdentityTaken)
	// sekarang login biasa via google -> user u
	in.LinkUserID = uuid.Nil
	res, err = e.svc.OAuthCallback(ctx, in)
	must(t, err)
	lr, err := e.svc.ExchangeOAuthCode(ctx, res.LoginCode, RequestMeta{})
	must(t, err)
	if lr.User.ID != u.ID {
		t.Fatal("harus login sebagai u")
	}
	// link ke user tidak ada
	e.oauth.prof.Subject = "g-999"
	in.LinkUserID = uuid.New()
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrUserNotFound)
	// user o sudah punya? belum: link sukses, lalu provider sama subject lain -> taken (unique user,provider)
	in.LinkUserID = o.ID
	_, err = e.svc.OAuthCallback(ctx, in)
	must(t, err)
	e.oauth.prof.Subject = "g-1000"
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrIdentityTaken)
}

func TestOAuth_WithMFA(t *testing.T) {
	e := newEnv(t)
	lr, _ := e.enableMFA(t, "google@example.com")
	in := OAuthCallbackInput{Provider: "google", Code: "c", Verifier: "v", Nonce: "n", LinkUserID: lr.User.ID}
	_, err := e.svc.OAuthCallback(ctx, in)
	must(t, err)
	in.LinkUserID = uuid.Nil
	res, err := e.svc.OAuthCallback(ctx, in)
	must(t, err)
	out, err := e.svc.ExchangeOAuthCode(ctx, res.LoginCode, RequestMeta{})
	must(t, err)
	if !out.MFARequired || out.Tokens.AccessToken != "" {
		t.Fatal("OAuth login tetap butuh 2FA")
	}
}

func TestOAuth_Errors(t *testing.T) {
	e := newEnv(t)
	in := OAuthCallbackInput{Provider: "google", Code: "c", Verifier: "v", Nonce: "n"}

	_, err := e.svc.OAuthCallback(ctx, OAuthCallbackInput{Provider: "github"})
	wantErr(t, err, domain.ErrOAuthDisabled)
	_, err = e.svc.OAuthCallback(ctx, OAuthCallbackInput{Provider: "google"})
	wantErr(t, err, domain.ErrOAuthFailed)

	e.oauth.err = errBoom
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrOAuthFailed)
	e.oauth.err = nil

	good := e.oauth.prof
	e.oauth.prof.EmailVerified = false
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrOAuthEmailNotVerif)
	e.oauth.prof = good
	e.oauth.prof.Provider = "github"
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrOAuthFailed)
	e.oauth.prof = good
	e.oauth.prof.Email = "not-an-email"
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrOAuthFailed)
	e.oauth.prof = good

	e.st.fail["identities.FindBySubject"] = errBoom
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "identities.FindBySubject")
	e.st.fail["users.FindByEmail"] = errBoom
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "users.FindByEmail")
	e.st.fail["codes.Create"] = errBoom
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, errBoom)
	if len(e.st.users) != 0 || len(e.st.idents) != 0 {
		t.Fatal("register OAuth harus rollback")
	}
	delete(e.st.fail, "codes.Create")
	e.st.fail["users.Create"] = domain.ErrEmailTaken
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, domain.ErrOAuthAccountExists)
	delete(e.st.fail, "users.Create")
	e.st.fail["identities.Create"] = errBoom
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "identities.Create")

	// nama tidak valid -> fallback dari email
	e.oauth.prof.Name = "   "
	res, err := e.svc.OAuthCallback(ctx, in)
	must(t, err)
	u, _ := fakeUsers{e.st}.FindByEmail(ctx, "google@example.com")
	if u.FullName != "google" {
		t.Fatalf("name = %q", u.FullName)
	}

	// exchange errors
	e.st.fail["codes.Consume"] = errBoom
	_, err = e.svc.ExchangeOAuthCode(ctx, res.LoginCode, RequestMeta{})
	wantErr(t, err, errBoom)
	delete(e.st.fail, "codes.Consume")
	e.st.fail["users.FindByID"] = errBoom
	_, err = e.svc.ExchangeOAuthCode(ctx, res.LoginCode, RequestMeta{})
	wantErr(t, err, errBoom)
	_, err = e.svc.OAuthCallback(ctx, in)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "users.FindByID")

	res, _ = e.svc.OAuthCallback(ctx, in)
	e.st.users[u.ID].Status = domain.UserStatusSuspended
	_, err = e.svc.ExchangeOAuthCode(ctx, res.LoginCode, RequestMeta{})
	wantErr(t, err, domain.ErrAccountSuspended)
	e.st.users[u.ID].Status = domain.UserStatusActive
	res, _ = e.svc.OAuthCallback(ctx, in)
	delete(e.st.users, u.ID)
	_, err = e.svc.ExchangeOAuthCode(ctx, res.LoginCode, RequestMeta{})
	wantErr(t, err, domain.ErrLoginCodeInvalid)
}

// ===== API keys =====

func TestAPIKey_Lifecycle(t *testing.T) {
	e := newEnv(t, func(_ *Deps, c *Config) { c.APIKeyMax = 2 })
	u := e.activeUser(t, "a@example.com")

	ck, err := e.svc.CreateAPIKey(ctx, CreateAPIKeyInput{UserID: u.ID, Name: " CI deploy ", Scopes: []string{"write", "read", "read"}})
	must(t, err)
	parts := strings.Split(ck.Key, "_")
	if len(parts) != 4 || parts[0] != "gac" || parts[1] != "live" || parts[2] != ck.APIKey.Prefix || len(parts[3]) != 32 {
		t.Fatalf("key = %q", ck.Key)
	}
	if ck.APIKey.Name != "CI deploy" || len(ck.APIKey.Scopes) != 2 || ck.APIKey.ExpiresAt == nil {
		t.Fatalf("apikey = %+v", ck.APIKey)
	}
	if strings.Contains(string(e.st.keys[ck.APIKey.ID].SecretHash), parts[3]) {
		t.Fatal("secret tidak boleh disimpan plaintext")
	}

	p, err := e.svc.AuthenticateAPIKey(ctx, ck.Key)
	must(t, err)
	if p.UserID != u.ID || p.APIKeyID != ck.APIKey.ID || p.SessionID != uuid.Nil || len(p.Scopes) != 2 {
		t.Fatalf("principal = %+v", p)
	}
	if e.st.keys[ck.APIKey.ID].LastUsedAt == nil {
		t.Fatal("last_used_at")
	}

	for _, bad := range []string{"", "gac_live_x", ck.Key + "x", strings.Replace(ck.Key, "live", "prod", 1),
		"gac_live_" + ck.APIKey.Prefix + "_" + strings.Repeat("A", 32), "gac_live_ABCDEFGH_" + strings.Repeat("a", 32),
		"gac_live_zzzzzzzz_" + strings.Repeat("a", 32), "gac_live_" + ck.APIKey.Prefix + "_" + strings.Repeat("!", 32)} {
		if _, err := e.svc.AuthenticateAPIKey(ctx, bad); !errors.Is(err, domain.ErrAPIKeyInvalid) {
			t.Fatalf("%q: err = %v", bad, err)
		}
	}

	_, err = e.svc.CreateAPIKey(ctx, CreateAPIKeyInput{UserID: u.ID, Name: "two", Scopes: []string{"read"}, ExpiresIn: 24 * time.Hour})
	must(t, err)
	_, err = e.svc.CreateAPIKey(ctx, CreateAPIKeyInput{UserID: u.ID, Name: "three", Scopes: []string{"read"}})
	wantErr(t, err, domain.ErrAPIKeyLimit)

	list, err := e.svc.ListAPIKeys(ctx, u.ID)
	must(t, err)
	if len(list) != 2 {
		t.Fatal("list")
	}

	other := e.activeUser(t, "b@example.com")
	wantErr(t, e.svc.RevokeAPIKey(ctx, RevokeAPIKeyInput{UserID: other.ID, KeyID: ck.APIKey.ID}), domain.ErrAPIKeyNotFound)
	must(t, e.svc.RevokeAPIKey(ctx, RevokeAPIKeyInput{UserID: u.ID, KeyID: ck.APIKey.ID}))
	wantErr(t, e.svc.RevokeAPIKey(ctx, RevokeAPIKeyInput{UserID: u.ID, KeyID: ck.APIKey.ID}), domain.ErrAPIKeyNotFound)
	_, err = e.svc.AuthenticateAPIKey(ctx, ck.Key)
	wantErr(t, err, domain.ErrAPIKeyInvalid)
	if len(e.st.events(domain.EventAPIKeyCreated)) != 2 || len(e.st.events(domain.EventAPIKeyRevoked)) != 1 {
		t.Fatal("audit api key")
	}

	// slot kosong lagi setelah revoke
	ck3, err := e.svc.CreateAPIKey(ctx, CreateAPIKeyInput{UserID: u.ID, Name: "three", Scopes: []string{"read"}, ExpiresIn: time.Hour})
	must(t, err)
	e.clock.Advance(2 * time.Hour)
	_, err = e.svc.AuthenticateAPIKey(ctx, ck3.Key)
	wantErr(t, err, domain.ErrAPIKeyInvalid)
}

func TestAPIKey_Validation(t *testing.T) {
	e := newEnv(t, func(_ *Deps, c *Config) { c.APIKeyEnv = "test" })
	u := e.activeUser(t, "a@example.com")
	cases := []struct {
		in   CreateAPIKeyInput
		want error
	}{
		{CreateAPIKeyInput{Name: "", Scopes: []string{"read"}}, domain.ErrInvalidAPIKeyName},
		{CreateAPIKeyInput{Name: "x\x00y", Scopes: []string{"read"}}, domain.ErrInvalidAPIKeyName},
		{CreateAPIKeyInput{Name: "x", Scopes: nil}, domain.ErrInvalidScope},
		{CreateAPIKeyInput{Name: "x", Scopes: []string{"admin"}}, domain.ErrInvalidScope},
		{CreateAPIKeyInput{Name: "x", Scopes: []string{"read"}, ExpiresIn: time.Minute}, domain.ErrInvalidExpiry},
		{CreateAPIKeyInput{Name: "x", Scopes: []string{"read"}, ExpiresIn: 400 * 24 * time.Hour}, domain.ErrInvalidExpiry},
	}
	for _, c := range cases {
		c.in.UserID = u.ID
		_, err := e.svc.CreateAPIKey(ctx, c.in)
		wantErr(t, err, c.want)
	}
	ck, err := e.svc.CreateAPIKey(ctx, CreateAPIKeyInput{UserID: u.ID, Name: "x", Scopes: []string{"read"}})
	must(t, err)
	if !strings.HasPrefix(ck.Key, "gac_test_") {
		t.Fatal("env test")
	}

	e.st.fail["keys.CountActiveByUser"] = errBoom
	_, err = e.svc.CreateAPIKey(ctx, CreateAPIKeyInput{UserID: u.ID, Name: "x", Scopes: []string{"read"}})
	wantErr(t, err, errBoom)
	delete(e.st.fail, "keys.CountActiveByUser")
	e.st.fail["audit.Record"] = errBoom
	_, err = e.svc.CreateAPIKey(ctx, CreateAPIKeyInput{UserID: u.ID, Name: "y", Scopes: []string{"read"}})
	wantErr(t, err, errBoom)
	delete(e.st.fail, "audit.Record")

	// auth errors
	e.st.fail["keys.FindByPrefix"] = errBoom
	_, err = e.svc.AuthenticateAPIKey(ctx, ck.Key)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "keys.FindByPrefix")
	e.st.fail["keys.TouchLastUsed"] = errBoom // best effort
	_, err = e.svc.AuthenticateAPIKey(ctx, ck.Key)
	must(t, err)
	delete(e.st.fail, "keys.TouchLastUsed")
	e.st.fail["roles.ListByUser"] = errBoom
	_, err = e.svc.AuthenticateAPIKey(ctx, ck.Key)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "roles.ListByUser")
	e.st.fail["users.FindByID"] = errBoom
	_, err = e.svc.AuthenticateAPIKey(ctx, ck.Key)
	wantErr(t, err, errBoom)
	delete(e.st.fail, "users.FindByID")
	delete(e.st.users, u.ID)
	_, err = e.svc.AuthenticateAPIKey(ctx, ck.Key)
	wantErr(t, err, domain.ErrAPIKeyInvalid)
}

func TestDeleteAccount_CleansP2(t *testing.T) {
	e := newEnv(t)
	lr, _ := e.enableMFA(t, "google@example.com")
	uid := lr.User.ID
	_, err := e.svc.OAuthCallback(ctx, OAuthCallbackInput{Provider: "google", Code: "c", Verifier: "v", Nonce: "n", LinkUserID: uid})
	must(t, err)
	_, err = e.svc.CreateAPIKey(ctx, CreateAPIKeyInput{UserID: uid, Name: "k", Scopes: []string{"read"}})
	must(t, err)

	for _, op := range []string{"keys.RevokeAllByUser", "mfa.Delete", "identities.DeleteByUser"} {
		e.st.fail[op] = errBoom
		wantErr(t, e.svc.DeleteAccount(ctx, DeleteAccountInput{UserID: uid, Password: pw}), errBoom)
		delete(e.st.fail, op)
	}
	must(t, e.svc.DeleteAccount(ctx, DeleteAccountInput{UserID: uid, Password: pw}))
	if len(e.st.mfa) != 0 || len(e.st.idents) != 0 {
		t.Fatal("mfa & identity harus terhapus")
	}
	for _, k := range e.st.keys {
		if k.RevokedAt == nil {
			t.Fatal("api key harus dicabut")
		}
	}
	// Google yang sama bisa register ulang sebagai akun baru
	res, err := e.svc.OAuthCallback(ctx, OAuthCallbackInput{Provider: "google", Code: "c", Verifier: "v", Nonce: "n"})
	must(t, err)
	if res.LoginCode == "" {
		t.Fatal("register ulang")
	}
}

func TestCleanup_LoginCodesError(t *testing.T) {
	e := newEnv(t)
	e.st.fail["codes.DeleteExpired"] = errBoom
	_, err := e.svc.Cleanup(ctx)
	wantErr(t, err, errBoom)
}
