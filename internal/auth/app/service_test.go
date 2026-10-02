package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

type env struct {
	st     *store
	hasher *fakeHasher
	codec  *fakeCodec
	mail   *fakeNotifier
	clock  *fakeClock
	oauth  *fakeOAuth
	svc    *Service
}

func newEnv(t *testing.T, opts ...func(*Deps, *Config)) *env {
	t.Helper()
	e := &env{
		st: newStore(), hasher: &fakeHasher{}, codec: &fakeCodec{}, mail: &fakeNotifier{},
		clock: &fakeClock{t: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)},
		oauth: &fakeOAuth{prof: domain.ExternalProfile{Provider: domain.ProviderGoogle, Subject: "g-123",
			Email: "Google@Example.com", EmailVerified: true, Name: "Gina"}},
	}
	d := Deps{
		Users: fakeUsers{e.st}, Sessions: fakeSessions{e.st}, OTPs: fakeOTPs{e.st}, Audit: fakeAudit{e.st},
		Hasher: e.hasher, Tokens: fakeIssuer{}, OTP: e.codec, Notifier: e.mail, Clock: e.clock, Tx: fakeTx{e.st},
		Roles: fakeRoles{e.st}, MFA: fakeMFA{e.st}, Identities: fakeIdentities{e.st}, LoginCodes: fakeLoginCodes{e.st},
		APIKeys: fakeAPIKeys{e.st}, MFATokens: fakeMFATokens{}, TOTP: fakeTOTP{}, Sealer: fakeSealer{},
		OAuthProviders: map[string]OAuthProvider{domain.ProviderGoogle: e.oauth},
	}
	cfg := Config{}
	for _, o := range opts {
		o(&d, &cfg)
	}
	e.svc = NewService(d, cfg)
	return e
}

var ctx = context.Background()

const pw = "Rahasia123!"

func (e *env) register(t *testing.T, email string) *domain.User {
	t.Helper()
	u, err := e.svc.Register(ctx, RegisterInput{Email: email, Password: pw, FullName: "Budi"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return u
}

// activeUser: register + verify + kembalikan user.
func (e *env) activeUser(t *testing.T, email string) *domain.User {
	t.Helper()
	u := e.register(t, email)
	m, _ := e.mail.last()
	if _, err := e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: email, Code: m.code}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	return u
}

func (e *env) login(t *testing.T, email string) LoginResult {
	t.Helper()
	r, err := e.svc.Login(ctx, LoginInput{Email: email, Password: pw, ClientIP: "10.0.0.1", UserAgent: "test"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func TestRegister(t *testing.T) {
	e := newEnv(t)
	u := e.register(t, "  Budi@Example.com ")
	if u.Email != "budi@example.com" || u.Status != domain.UserStatusPendingVerification {
		t.Fatalf("user = %+v", u)
	}
	if m, ok := e.mail.last(); !ok || m.kind != "verify" || m.to != "budi@example.com" || m.code != "123456" {
		t.Fatalf("mail = %+v", m)
	}
	if len(e.st.events(domain.EventUserRegistered)) != 1 {
		t.Fatal("audit user_registered missing")
	}
	if len(e.st.otps) != 1 {
		t.Fatal("otp not stored")
	}
	for _, o := range e.st.otps {
		if string(o.CodeHash) == "123456" || o.MaxAttempts != 5 || !o.ExpiresAt.Equal(e.clock.t.Add(10*time.Minute)) {
			t.Fatalf("otp = %+v", o)
		}
	}

	_, err := e.svc.Register(ctx, RegisterInput{Email: "budi@example.com", Password: pw, FullName: "X"})
	wantErr(t, err, domain.ErrEmailTaken)
}

func TestRegister_Validation(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		in   RegisterInput
		want error
	}{
		{"email", RegisterInput{Email: "bad", Password: pw, FullName: "A"}, domain.ErrInvalidEmail},
		{"name", RegisterInput{Email: "a@b.co", Password: pw, FullName: "  "}, domain.ErrInvalidFullName},
		{"short", RegisterInput{Email: "a@b.co", Password: "short", FullName: "A"}, domain.ErrWeakPassword},
		{"same as email", RegisterInput{Email: "abcdefgh@b.co", Password: "ABCDEFGH@b.co", FullName: "A"}, domain.ErrWeakPassword},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.Register(ctx, tt.in)
			wantErr(t, err, tt.want)
		})
	}
}

func TestRegister_FailuresRollback(t *testing.T) {
	e := newEnv(t)
	e.st.fail["otps.Create"] = errors.New("db down")
	_, err := e.svc.Register(ctx, RegisterInput{Email: "a@b.co", Password: pw, FullName: "A"})
	if err == nil || len(e.st.users) != 0 || e.mail.count("verify") != 0 {
		t.Fatalf("err=%v users=%d mails=%d", err, len(e.st.users), e.mail.count("verify"))
	}

	e = newEnv(t)
	e.hasher.hashErr = errors.New("boom")
	if _, err := e.svc.Register(ctx, RegisterInput{Email: "a@b.co", Password: pw, FullName: "A"}); err == nil {
		t.Fatal("want hash error")
	}

	e = newEnv(t)
	e.codec.genErr = errors.New("rng")
	if _, err := e.svc.Register(ctx, RegisterInput{Email: "a@b.co", Password: pw, FullName: "A"}); err == nil {
		t.Fatal("want rng error")
	}

	// Gagal kirim email tidak menggagalkan registrasi.
	e = newEnv(t)
	e.mail.err = errors.New("smtp down")
	e.register(t, "a@b.co")
}

func TestVerifyEmail(t *testing.T) {
	e := newEnv(t)
	u := e.register(t, "a@b.co")

	_, err := e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "000000"})
	wantErr(t, err, domain.ErrInvalidOTP)
	if len(e.st.events(domain.EventOTPFailed)) != 1 {
		t.Fatal("otp_failed audit missing")
	}
	_, err = e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "12ab56"})
	wantErr(t, err, domain.ErrInvalidOTP)

	res, err := e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "A@B.co", Code: "123456"})
	must(t, err)
	if res.AlreadyVerified {
		t.Fatal("first verify must not be already verified")
	}
	got, _ := e.svc.Me(ctx, u.ID)
	if got.Status != domain.UserStatusActive || got.EmailVerifiedAt == nil {
		t.Fatalf("user = %+v", got)
	}
	// Idempoten.
	res, err = e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "999999"})
	must(t, err)
	if !res.AlreadyVerified {
		t.Fatal("want already verified")
	}
}

func TestVerifyEmail_Generic(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "nobody@b.co", Code: "123456"})
	wantErr(t, err, domain.ErrInvalidOTP)
	_, err = e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "bad", Code: "123456"})
	wantErr(t, err, domain.ErrInvalidOTP)

	u := e.register(t, "a@b.co")
	e.st.users[u.ID].Status = domain.UserStatusSuspended
	_, err = e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "123456"})
	wantErr(t, err, domain.ErrInvalidOTP)

	e.st.fail["users.FindByEmail"] = errors.New("db")
	if _, err := e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "123456"}); err == nil {
		t.Fatal("want db error")
	}
}

func TestVerifyEmail_ExpiredAndAttempts(t *testing.T) {
	e := newEnv(t)
	e.register(t, "a@b.co")
	e.clock.Advance(11 * time.Minute)
	_, err := e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "123456"})
	wantErr(t, err, domain.ErrOTPExpired)

	e = newEnv(t)
	e.register(t, "a@b.co")
	for range 5 {
		_, err := e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "000000"})
		wantErr(t, err, domain.ErrInvalidOTP)
	}
	// Kode benar pun ditolak setelah attempts habis.
	_, err = e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "123456"})
	wantErr(t, err, domain.ErrOTPTooManyAttempts)
}

func TestVerifyEmail_ConsumeRace(t *testing.T) {
	e := newEnv(t)
	e.register(t, "a@b.co")
	e.st.fail["otps.Consume"] = domain.ErrInvalidOTP
	_, err := e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "123456"})
	wantErr(t, err, domain.ErrInvalidOTP)
}

func TestResendVerification(t *testing.T) {
	e := newEnv(t, func(_ *Deps, c *Config) { c.OTPDailyQuota = 3 })
	e.codec.next = []string{"111111", "222222", "333333", "444444"}
	e.register(t, "a@b.co")

	// Cooldown: langsung resend tidak membuat OTP baru.
	must(t, e.svc.ResendVerification(ctx, "a@b.co"))
	if e.mail.count("verify") != 1 {
		t.Fatalf("cooldown not applied: %d", e.mail.count("verify"))
	}
	e.clock.Advance(61 * time.Second)
	must(t, e.svc.ResendVerification(ctx, "a@b.co"))
	if m, _ := e.mail.last(); m.code != "222222" {
		t.Fatalf("mail = %+v", m)
	}
	// Kode lama tidak berlaku lagi.
	_, err := e.svc.VerifyEmail(ctx, VerifyEmailInput{Email: "a@b.co", Code: "111111"})
	wantErr(t, err, domain.ErrInvalidOTP)

	e.clock.Advance(61 * time.Second)
	must(t, e.svc.ResendVerification(ctx, "a@b.co"))
	e.clock.Advance(61 * time.Second)
	must(t, e.svc.ResendVerification(ctx, "a@b.co")) // kuota 3 habis
	if e.mail.count("verify") != 3 {
		t.Fatalf("quota not applied: %d", e.mail.count("verify"))
	}

	// Email tidak terdaftar / sudah aktif: sukses tanpa kirim.
	must(t, e.svc.ResendVerification(ctx, "nobody@b.co"))
	wantErr(t, e.svc.ResendVerification(ctx, "bad"), domain.ErrInvalidEmail)
	e2 := newEnv(t)
	e2.activeUser(t, "x@b.co")
	e2.clock.Advance(time.Hour)
	must(t, e2.svc.ResendVerification(ctx, "x@b.co"))
	if e2.mail.count("verify") != 1 {
		t.Fatal("active user must not receive verification")
	}
}

func TestResend_ErrorsAndRace(t *testing.T) {
	e := newEnv(t)
	e.register(t, "a@b.co")
	e.clock.Advance(time.Hour)
	e.st.fail["otps.Stats"] = errors.New("db")
	if err := e.svc.ResendVerification(ctx, "a@b.co"); err == nil {
		t.Fatal("want stats error")
	}
	delete(e.st.fail, "otps.Stats")
	e.st.fail["otps.Create"] = domain.ErrOTPActiveExists
	must(t, e.svc.ResendVerification(ctx, "a@b.co"))
	e.st.fail["otps.Create"] = errors.New("db")
	if err := e.svc.ResendVerification(ctx, "a@b.co"); err == nil {
		t.Fatal("want create error")
	}
	e.st.fail["users.FindByEmail"] = errors.New("db")
	if err := e.svc.ResendVerification(ctx, "a@b.co"); err == nil {
		t.Fatal("want find error")
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@b.co")
	r := e.login(t, "A@b.co")
	if r.User.ID != u.ID || r.User.LastLoginAt == nil || r.Tokens.AccessToken == "" || r.Tokens.RefreshToken == "" {
		t.Fatalf("result = %+v", r)
	}
	if r.Tokens.FamilyID == uuid.Nil || r.Tokens.SessionID == uuid.Nil {
		t.Fatal("ids missing")
	}
	sess := e.st.sessions[r.Tokens.SessionID]
	if sess == nil || string(sess.RefreshTokenHash) == r.Tokens.RefreshToken || sess.ClientIP != "10.0.0.1" {
		t.Fatalf("session = %+v", sess)
	}
	if len(e.st.events(domain.EventLoginSucceeded)) != 1 {
		t.Fatal("audit login_succeeded missing")
	}
}

func TestLogin_Failures(t *testing.T) {
	e := newEnv(t)
	e.register(t, "pending@b.co")

	// Password benar tapi belum verifikasi.
	_, err := e.svc.Login(ctx, LoginInput{Email: "pending@b.co", Password: pw})
	wantErr(t, err, domain.ErrEmailNotVerified)
	// Password salah untuk pending: tetap generic.
	_, err = e.svc.Login(ctx, LoginInput{Email: "pending@b.co", Password: "wrong-password"})
	wantErr(t, err, domain.ErrInvalidCredentials)

	before := e.hasher.compares
	_, err = e.svc.Login(ctx, LoginInput{Email: "nobody@b.co", Password: pw})
	wantErr(t, err, domain.ErrInvalidCredentials)
	_, err = e.svc.Login(ctx, LoginInput{Email: "bad", Password: pw})
	wantErr(t, err, domain.ErrInvalidCredentials)
	if e.hasher.compares != before+2 {
		t.Fatal("dummy compare must run for unknown/invalid email")
	}
	evs := e.st.events(domain.EventLoginFailed)
	last := evs[len(evs)-1]
	if last.UserID != uuid.Nil || len(last.EmailHash) != 32 {
		t.Fatalf("unknown email audit = %+v", last)
	}

	u := e.activeUser(t, "s@b.co")
	e.st.users[u.ID].Status = domain.UserStatusSuspended
	_, err = e.svc.Login(ctx, LoginInput{Email: "s@b.co", Password: pw})
	wantErr(t, err, domain.ErrAccountSuspended)

	e.st.fail["users.FindByEmail"] = errors.New("db")
	if _, err := e.svc.Login(ctx, LoginInput{Email: "s@b.co", Password: pw}); err == nil {
		t.Fatal("want db error")
	}
}

func TestLogin_Lockout(t *testing.T) {
	e := newEnv(t)
	e.activeUser(t, "a@b.co")
	for range domain.MaxFailedLoginAttempts {
		_, err := e.svc.Login(ctx, LoginInput{Email: "a@b.co", Password: "wrong-password"})
		wantErr(t, err, domain.ErrInvalidCredentials)
	}
	if len(e.st.events(domain.EventAccountLocked)) != 1 {
		t.Fatal("account_locked audit missing")
	}
	_, err := e.svc.Login(ctx, LoginInput{Email: "a@b.co", Password: pw})
	wantErr(t, err, domain.ErrAccountLocked)
	e.clock.Advance(domain.LockDuration + time.Second)
	e.login(t, "a@b.co")
}

func TestLogin_TxFailureAndAuditBestEffort(t *testing.T) {
	e := newEnv(t)
	e.activeUser(t, "a@b.co")
	e.st.fail["sessions.Create"] = errors.New("db")
	if _, err := e.svc.Login(ctx, LoginInput{Email: "a@b.co", Password: pw}); err == nil {
		t.Fatal("want error")
	}
	delete(e.st.fail, "sessions.Create")
	e.st.fail["users.RegisterFailedLogin"] = errors.New("db")
	e.st.fail["audit.Record"] = errors.New("db")
	// Error audit/lockout tidak mengubah respons.
	_, err := e.svc.Login(ctx, LoginInput{Email: "a@b.co", Password: "wrong-password"})
	wantErr(t, err, domain.ErrInvalidCredentials)

	e = newEnv(t, func(d *Deps, _ *Config) { d.Tokens = fakeIssuer{err: errors.New("sign")} })
	e.activeUser(t, "a@b.co")
	if _, err := e.svc.Login(ctx, LoginInput{Email: "a@b.co", Password: pw}); err == nil {
		t.Fatal("want issuer error")
	}
}

func TestLogin_MaxSessions(t *testing.T) {
	e := newEnv(t, func(_ *Deps, c *Config) { c.MaxSessions = 2 })
	u := e.activeUser(t, "a@b.co")
	first := e.login(t, "a@b.co")
	e.clock.Advance(time.Second)
	e.login(t, "a@b.co")
	e.clock.Advance(time.Second)
	e.login(t, "a@b.co")
	list, err := e.svc.ListSessions(ctx, u.ID, uuid.Nil)
	must(t, err)
	if len(list) != 2 {
		t.Fatalf("sessions = %d", len(list))
	}
	if e.st.sessions[first.Tokens.SessionID].RevokedReason != domain.RevokeSessionLimit {
		t.Fatal("oldest must be revoked with session_limit")
	}
}

func TestRefresh(t *testing.T) {
	e := newEnv(t)
	e.activeUser(t, "a@b.co")
	r := e.login(t, "a@b.co")
	e.clock.Advance(time.Minute)

	n, err := e.svc.Refresh(ctx, RefreshInput{RefreshToken: r.Tokens.RefreshToken})
	must(t, err)
	if n.FamilyID != r.Tokens.FamilyID || n.RefreshToken == r.Tokens.RefreshToken {
		t.Fatalf("rotation = %+v", n)
	}
	// Reuse token lama -> family dicabut.
	_, err = e.svc.Refresh(ctx, RefreshInput{RefreshToken: r.Tokens.RefreshToken})
	wantErr(t, err, domain.ErrTokenReused)
	_, err = e.svc.Refresh(ctx, RefreshInput{RefreshToken: n.RefreshToken})
	wantErr(t, err, domain.ErrSessionInvalid)
	if len(e.st.events(domain.EventRefreshReuseDetected)) != 1 {
		t.Fatal("reuse audit missing")
	}

	_, err = e.svc.Refresh(ctx, RefreshInput{RefreshToken: "unknown"})
	wantErr(t, err, domain.ErrSessionInvalid)
}

func TestRefresh_Edge(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@b.co")
	r := e.login(t, "a@b.co")

	e.st.users[u.ID].Status = domain.UserStatusSuspended
	_, err := e.svc.Refresh(ctx, RefreshInput{RefreshToken: r.Tokens.RefreshToken})
	wantErr(t, err, domain.ErrAccountInactive)

	e.st.users[u.ID].Status = domain.UserStatusDeleted
	_, err = e.svc.Refresh(ctx, RefreshInput{RefreshToken: r.Tokens.RefreshToken})
	wantErr(t, err, domain.ErrSessionInvalid)

	e.st.users[u.ID].Status = domain.UserStatusActive
	e.st.fail["sessions.MarkRotated"] = domain.ErrSessionInvalid
	_, err = e.svc.Refresh(ctx, RefreshInput{RefreshToken: r.Tokens.RefreshToken})
	wantErr(t, err, domain.ErrSessionInvalid)
	delete(e.st.fail, "sessions.MarkRotated")

	e.st.fail["users.FindByID"] = errors.New("db")
	if _, err := e.svc.Refresh(ctx, RefreshInput{RefreshToken: r.Tokens.RefreshToken}); err == nil {
		t.Fatal("want db error")
	}
	delete(e.st.fail, "users.FindByID")

	e.clock.Advance(8 * 24 * time.Hour)
	_, err = e.svc.Refresh(ctx, RefreshInput{RefreshToken: r.Tokens.RefreshToken})
	wantErr(t, err, domain.ErrSessionInvalid)

	e.st.fail["sessions.FindByTokenHash"] = errors.New("db")
	if _, err := e.svc.Refresh(ctx, RefreshInput{RefreshToken: "x"}); err == nil {
		t.Fatal("want db error")
	}
}

func TestRefresh_ReuseRevokeFails(t *testing.T) {
	e := newEnv(t)
	e.activeUser(t, "a@b.co")
	r := e.login(t, "a@b.co")
	_, err := e.svc.Refresh(ctx, RefreshInput{RefreshToken: r.Tokens.RefreshToken})
	must(t, err)
	e.st.fail["sessions.RevokeFamily"] = errors.New("db")
	_, err = e.svc.Refresh(ctx, RefreshInput{RefreshToken: r.Tokens.RefreshToken})
	if err == nil || errors.Is(err, domain.ErrTokenReused) {
		t.Fatalf("err = %v", err)
	}
}

func TestLogoutAndAuthenticate(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@b.co")
	r := e.login(t, "a@b.co")
	must(t, e.svc.AuthenticateSession(ctx, r.Tokens.SessionID))
	wantErr(t, e.svc.AuthenticateSession(ctx, uuid.New()), domain.ErrSessionInvalid)

	other := uuid.New()
	wantErr(t, e.svc.Logout(ctx, LogoutInput{UserID: other, SessionID: r.Tokens.SessionID}), domain.ErrSessionNotFound)
	must(t, e.svc.Logout(ctx, LogoutInput{UserID: u.ID, SessionID: r.Tokens.SessionID}))
	wantErr(t, e.svc.AuthenticateSession(ctx, r.Tokens.SessionID), domain.ErrSessionInvalid)
	if e.st.sessions[r.Tokens.SessionID].RevokedReason != domain.RevokeLogout {
		t.Fatal("reason")
	}
	wantErr(t, e.svc.Logout(ctx, LogoutInput{UserID: u.ID, SessionID: uuid.New()}), domain.ErrSessionNotFound)

	e.st.fail["sessions.FindByID"] = errors.New("db")
	if err := e.svc.AuthenticateSession(ctx, r.Tokens.SessionID); err == nil {
		t.Fatal("want db error")
	}
}

func TestLogoutAll(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@b.co")
	a := e.login(t, "a@b.co")
	b := e.login(t, "a@b.co")

	must(t, e.svc.LogoutAll(ctx, LogoutAllInput{UserID: u.ID, SessionID: a.Tokens.SessionID}))
	must(t, e.svc.AuthenticateSession(ctx, a.Tokens.SessionID))
	wantErr(t, e.svc.AuthenticateSession(ctx, b.Tokens.SessionID), domain.ErrSessionInvalid)

	must(t, e.svc.LogoutAll(ctx, LogoutAllInput{UserID: u.ID, SessionID: a.Tokens.SessionID, IncludeCurrent: true}))
	wantErr(t, e.svc.AuthenticateSession(ctx, a.Tokens.SessionID), domain.ErrSessionInvalid)

	e.st.fail["sessions.FindByID"] = errors.New("db")
	if err := e.svc.LogoutAll(ctx, LogoutAllInput{UserID: u.ID, SessionID: a.Tokens.SessionID}); err == nil {
		t.Fatal("want error")
	}
}

func TestForgotAndResetPassword(t *testing.T) {
	e := newEnv(t)
	e.codec.next = []string{"111111", "654321"}
	u := e.activeUser(t, "a@b.co")
	r := e.login(t, "a@b.co")

	must(t, e.svc.ForgotPassword(ctx, "a@b.co", RequestMeta{ClientIP: "1.1.1.1"}))
	if m, _ := e.mail.last(); m.kind != "reset" || m.code != "654321" {
		t.Fatalf("mail = %+v", m)
	}
	if len(e.st.events(domain.EventPasswordResetRequested)) != 1 {
		t.Fatal("audit")
	}
	// Policy dicek sebelum OTP (attempts tidak berkurang).
	err := e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "a@b.co", Code: "654321", NewPassword: "short"})
	wantErr(t, err, domain.ErrWeakPassword)
	err = e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "a@b.co", Code: "000000", NewPassword: "BaruSekali123"})
	wantErr(t, err, domain.ErrInvalidOTP)

	must(t, e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "a@b.co", Code: "654321", NewPassword: "BaruSekali123"}))
	wantErr(t, e.svc.AuthenticateSession(ctx, r.Tokens.SessionID), domain.ErrSessionInvalid)
	if e.st.users[u.ID].PasswordHash != "h:BaruSekali123" || e.mail.count("changed") != 1 {
		t.Fatal("password not changed / notification missing")
	}
	// Kode sekali pakai.
	err = e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "a@b.co", Code: "654321", NewPassword: "BaruLagi12345"})
	wantErr(t, err, domain.ErrInvalidOTP)
}

func TestResetPassword_PendingBecomesActive(t *testing.T) {
	e := newEnv(t)
	e.codec.next = []string{"111111", "222222"}
	u := e.register(t, "a@b.co")
	must(t, e.svc.ForgotPassword(ctx, "a@b.co", RequestMeta{}))
	must(t, e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "a@b.co", Code: "222222", NewPassword: "BaruSekali123"}))
	if e.st.users[u.ID].Status != domain.UserStatusActive {
		t.Fatal("pending must become active after reset")
	}
}

func TestResetPassword_Generic(t *testing.T) {
	e := newEnv(t)
	must(t, e.svc.ForgotPassword(ctx, "nobody@b.co", RequestMeta{}))
	if len(e.mail.sent) != 0 {
		t.Fatal("no mail for unknown email")
	}
	err := e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "nobody@b.co", Code: "123456", NewPassword: "BaruSekali123"})
	wantErr(t, err, domain.ErrInvalidOTP)
	err = e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "bad", Code: "123456", NewPassword: "BaruSekali123"})
	wantErr(t, err, domain.ErrInvalidOTP)

	u := e.activeUser(t, "s@b.co")
	e.st.users[u.ID].Status = domain.UserStatusSuspended
	err = e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "s@b.co", Code: "123456", NewPassword: "BaruSekali123"})
	wantErr(t, err, domain.ErrInvalidOTP)

	e.st.users[u.ID].Status = domain.UserStatusActive
	e.clock.Advance(time.Hour)
	must(t, e.svc.ForgotPassword(ctx, "s@b.co", RequestMeta{}))
	e.hasher.hashErr = errors.New("boom")
	if err := e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "s@b.co", Code: "123456", NewPassword: "BaruSekali123"}); err == nil {
		t.Fatal("want hash error")
	}
	e.hasher.hashErr = nil
	e.st.fail["users.FindByEmail"] = errors.New("db")
	if err := e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "s@b.co", Code: "123456", NewPassword: "BaruSekali123"}); err == nil {
		t.Fatal("want db error")
	}
}

func TestResetPassword_TxRollback(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@b.co")
	e.clock.Advance(time.Hour)
	must(t, e.svc.ForgotPassword(ctx, "a@b.co", RequestMeta{}))
	e.st.fail["sessions.RevokeAllByUser"] = errors.New("db")
	if err := e.svc.ResetPassword(ctx, ResetPasswordInput{Email: "a@b.co", Code: "123456", NewPassword: "BaruSekali123"}); err == nil {
		t.Fatal("want error")
	}
	if e.st.users[u.ID].PasswordHash != "h:"+pw {
		t.Fatal("password must be rolled back")
	}
}

func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@b.co")
	a := e.login(t, "a@b.co")
	b := e.login(t, "a@b.co")

	in := ChangePasswordInput{UserID: u.ID, SessionID: a.Tokens.SessionID, OldPassword: "wrong-password", NewPassword: "BaruSekali123"}
	wantErr(t, e.svc.ChangePassword(ctx, in), domain.ErrInvalidCredentials)
	if e.st.users[u.ID].FailedLoginAttempts != 1 {
		t.Fatal("failed attempt must be counted")
	}
	in.OldPassword = pw
	in.NewPassword = pw
	wantErr(t, e.svc.ChangePassword(ctx, in), domain.ErrPasswordReused)
	in.NewPassword = "short"
	wantErr(t, e.svc.ChangePassword(ctx, in), domain.ErrWeakPassword)

	in.NewPassword = "BaruSekali123"
	must(t, e.svc.ChangePassword(ctx, in))
	must(t, e.svc.AuthenticateSession(ctx, a.Tokens.SessionID))
	wantErr(t, e.svc.AuthenticateSession(ctx, b.Tokens.SessionID), domain.ErrSessionInvalid)
	if len(e.st.events(domain.EventPasswordChanged)) != 1 || e.mail.count("changed") != 1 {
		t.Fatal("audit/notify missing")
	}

	wantErr(t, e.svc.ChangePassword(ctx, ChangePasswordInput{UserID: uuid.New()}), domain.ErrUserNotFound)

	e.hasher.hashErr = errors.New("boom")
	in.OldPassword, in.NewPassword = "BaruSekali123", "BaruLagi12345"
	if err := e.svc.ChangePassword(ctx, in); err == nil {
		t.Fatal("want hash error")
	}
	e.hasher.hashErr = nil
	e.st.fail["users.UpdatePassword"] = errors.New("db")
	if err := e.svc.ChangePassword(ctx, in); err == nil {
		t.Fatal("want db error")
	}
}

func TestUpdateProfile(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@b.co")
	_, err := e.svc.UpdateProfile(ctx, UpdateProfileInput{UserID: u.ID})
	wantErr(t, err, domain.ErrNoFieldsToUpdate)
	bad := " "
	_, err = e.svc.UpdateProfile(ctx, UpdateProfileInput{UserID: u.ID, FullName: &bad})
	wantErr(t, err, domain.ErrInvalidFullName)
	name := "  Budi Baru "
	got, err := e.svc.UpdateProfile(ctx, UpdateProfileInput{UserID: u.ID, FullName: &name})
	must(t, err)
	if got.FullName != "Budi Baru" || len(e.st.events(domain.EventProfileUpdated)) != 1 {
		t.Fatalf("got = %+v", got)
	}
	_, err = e.svc.UpdateProfile(ctx, UpdateProfileInput{UserID: uuid.New(), FullName: &name})
	wantErr(t, err, domain.ErrUserNotFound)
}

func TestDeleteAccount(t *testing.T) {
	var hooked uuid.UUID
	e := newEnv(t, func(d *Deps, _ *Config) {
		d.DeletionHooks = []UserDeletionHook{hookFunc(func(_ context.Context, id uuid.UUID, _ time.Time) error {
			hooked = id
			return nil
		})}
	})
	u := e.activeUser(t, "a@b.co")
	r := e.login(t, "a@b.co")

	wantErr(t, e.svc.DeleteAccount(ctx, DeleteAccountInput{UserID: u.ID, Password: "wrong-password"}), domain.ErrInvalidCredentials)
	must(t, e.svc.DeleteAccount(ctx, DeleteAccountInput{UserID: u.ID, SessionID: r.Tokens.SessionID, Password: pw}))
	if hooked != u.ID {
		t.Fatal("hook not called")
	}
	wantErr(t, e.svc.AuthenticateSession(ctx, r.Tokens.SessionID), domain.ErrSessionInvalid)
	_, err := e.svc.Me(ctx, u.ID)
	wantErr(t, err, domain.ErrUserNotFound)
	if e.st.users[u.ID].Email == "a@b.co" {
		t.Fatal("email must be anonymized")
	}
	// Email bisa register ulang.
	e.register(t, "a@b.co")

	wantErr(t, e.svc.DeleteAccount(ctx, DeleteAccountInput{UserID: uuid.New(), Password: pw}), domain.ErrUserNotFound)
}

func TestDeleteAccount_HookFailureRollsBack(t *testing.T) {
	e := newEnv(t, func(d *Deps, _ *Config) {
		d.DeletionHooks = []UserDeletionHook{hookFunc(func(context.Context, uuid.UUID, time.Time) error {
			return errors.New("finance down")
		})}
	})
	u := e.activeUser(t, "a@b.co")
	if err := e.svc.DeleteAccount(ctx, DeleteAccountInput{UserID: u.ID, Password: pw}); err == nil {
		t.Fatal("want error")
	}
	if e.st.users[u.ID].Status != domain.UserStatusActive {
		t.Fatal("must roll back")
	}
}

func TestSessionsListAndRevoke(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@b.co")
	a := e.login(t, "a@b.co")
	e.clock.Advance(time.Second)
	b := e.login(t, "a@b.co")

	list, err := e.svc.ListSessions(ctx, u.ID, a.Tokens.SessionID)
	must(t, err)
	if len(list) != 2 || list[0].FamilyID != b.Tokens.FamilyID || list[0].Current || !list[1].Current {
		t.Fatalf("list = %+v", list)
	}

	// IDOR: user lain -> not found.
	wantErr(t, e.svc.RevokeSession(ctx, RevokeSessionInput{UserID: uuid.New(), FamilyID: b.Tokens.FamilyID}), domain.ErrSessionNotFound)
	must(t, e.svc.RevokeSession(ctx, RevokeSessionInput{UserID: u.ID, SessionID: a.Tokens.SessionID, FamilyID: b.Tokens.FamilyID}))
	wantErr(t, e.svc.AuthenticateSession(ctx, b.Tokens.SessionID), domain.ErrSessionInvalid)
	wantErr(t, e.svc.RevokeSession(ctx, RevokeSessionInput{UserID: u.ID, FamilyID: b.Tokens.FamilyID}), domain.ErrSessionNotFound)

	// currentFamily: session milik user lain diabaikan.
	other := e.activeUser(t, "o@b.co")
	list, err = e.svc.ListSessions(ctx, other.ID, a.Tokens.SessionID)
	must(t, err)
	if len(list) != 0 {
		t.Fatal("other user must see none")
	}
	e.st.fail["sessions.ListActiveByUser"] = errors.New("db")
	if _, err := e.svc.ListSessions(ctx, u.ID, uuid.Nil); err == nil {
		t.Fatal("want db error")
	}
	e.st.fail["sessions.FindByID"] = errors.New("db")
	if _, err := e.svc.ListSessions(ctx, u.ID, a.Tokens.SessionID); err == nil {
		t.Fatal("want db error")
	}
}

func TestListSecurityEvents(t *testing.T) {
	e := newEnv(t)
	u := e.activeUser(t, "a@b.co")
	e.login(t, "a@b.co")
	evs, err := e.svc.ListSecurityEvents(ctx, u.ID, nil, 10)
	must(t, err)
	if len(evs) != 3 || evs[0].EventType != domain.EventLoginSucceeded {
		t.Fatalf("events = %+v", evs)
	}
	if _, err := e.svc.ListSecurityEvents(ctx, u.ID, nil, 0); err == nil {
		t.Fatal("limit 0 must fail")
	}
}

func TestCleanup(t *testing.T) {
	e := newEnv(t)
	e.register(t, "pending@b.co")
	e.activeUser(t, "a@b.co")
	e.login(t, "a@b.co")
	e.clock.Advance(91 * 24 * time.Hour)
	res, err := e.svc.Cleanup(ctx)
	must(t, err)
	if res.Unverified != 1 || res.Sessions != 1 || res.OTPs != 2 || res.AuditAnonymized == 0 {
		t.Fatalf("res = %+v", res)
	}
	for _, op := range []string{"audit.AnonymizeBefore", "users.PurgeUnverified", "otps.DeleteExpired", "sessions.DeleteExpired"} {
		e.st.fail[op] = errors.New("db")
		if _, err := e.svc.Cleanup(ctx); err == nil {
			t.Fatalf("%s: want error", op)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abc", 5); got != "abc" {
		t.Fatal(got)
	}
	if got := truncate("aé", 2); got != "a" {
		t.Fatalf("got %q", got)
	}
}

func TestConfigDefaults(t *testing.T) {
	c := Config{}.withDefaults()
	if c.OTPTTL != 10*time.Minute || c.MaxSessions != 10 || c.OTPMaxAttempts != 5 || c.PasswordResetTTL != 30*time.Minute {
		t.Fatalf("%+v", c)
	}
}
