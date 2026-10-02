package security

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"golang.org/x/oauth2"

	"go-auth-clean/internal/auth/domain"
)

func TestVerifyAccessRoles(t *testing.T) {
	secret := bytes.Repeat([]byte("k"), 32)
	j := NewJWTIssuer(secret, "test", time.Minute)
	uid, sid := uuid.New(), uuid.New()
	tok, _, err := j.Issue(uid, sid, []domain.Role{domain.RoleUser, domain.RoleAdmin}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c, err := j.VerifyAccess(tok)
	if err != nil || c.UserID != uid || c.SessionID != sid || !domain.HasRole(c.Roles, domain.RoleAdmin) {
		t.Fatalf("%+v %v", c, err)
	}
	// Role tak dikenal diabaikan.
	cl := Claims{SessionID: sid.String(), Roles: []string{"root", "user"}, RegisteredClaims: jwt.RegisteredClaims{
		Subject: uid.String(), Issuer: "test", Audience: jwt.ClaimStrings{audience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}}
	s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, cl).SignedString(secret)
	c, err = j.VerifyAccess(s)
	if err != nil || len(c.Roles) != 1 || c.Roles[0] != domain.RoleUser {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestMFATokenIssuer(t *testing.T) {
	secret := bytes.Repeat([]byte("k"), 32)
	m := NewMFATokenIssuer(secret, "test", 5*time.Minute)
	j := NewJWTIssuer(secret, "test", time.Minute)
	uid := uuid.New()
	tok, exp, err := m.IssueMFA(uid, time.Now())
	if err != nil || time.Until(exp) < 4*time.Minute {
		t.Fatal(err)
	}
	if got, err := m.VerifyMFA(tok); err != nil || got != uid {
		t.Fatalf("%v %v", got, err)
	}
	// Audience terpisah: token MFA bukan access token, dan sebaliknya.
	if _, _, err := j.Verify(tok); err == nil {
		t.Fatal("mfa token tidak boleh diterima sebagai access token")
	}
	access, _, _ := j.Issue(uid, uuid.New(), nil, time.Now())
	if _, err := m.VerifyMFA(access); err == nil {
		t.Fatal("access token tidak boleh diterima sebagai mfa token")
	}
	old, _, _ := m.IssueMFA(uid, time.Now().Add(-time.Hour))
	if _, err := m.VerifyMFA(old); err == nil {
		t.Fatal("expired")
	}
	sign := func(c Claims) string {
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
		return s
	}
	rc := jwt.RegisteredClaims{Subject: "x", Issuer: "test", Audience: jwt.ClaimStrings{mfaAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}
	if _, err := m.VerifyMFA(sign(Claims{RegisteredClaims: rc})); err == nil {
		t.Fatal("subject invalid")
	}
	rc.Subject = uid.String()
	if _, err := m.VerifyMFA(sign(Claims{SessionID: "s", RegisteredClaims: rc})); err == nil {
		t.Fatal("sid claim harus ditolak")
	}
}

func TestMFATokenIssuer_ChallengeID(t *testing.T) {
	secret := bytes.Repeat([]byte("k"), 32)
	m := NewMFATokenIssuer(secret, "test", 5*time.Minute)
	uid := uuid.New()
	a, _, _ := m.IssueMFA(uid, time.Now())
	b, _, _ := m.IssueMFA(uid, time.Now())
	ja, err := m.ChallengeID(a)
	if err != nil || ja == "" {
		t.Fatalf("jti=%q err=%v", ja, err)
	}
	if jb, _ := m.ChallengeID(b); jb == ja {
		t.Fatal("setiap challenge harus punya jti unik")
	}
	if again, _ := m.ChallengeID(a); again != ja {
		t.Fatal("jti harus stabil untuk token yang sama")
	}
	if _, err := m.ChallengeID("garbage"); err == nil {
		t.Fatal("token rusak harus error")
	}
	access, _, _ := NewJWTIssuer(secret, "test", time.Minute).Issue(uid, uuid.New(), nil, time.Now())
	if _, err := m.ChallengeID(access); err == nil {
		t.Fatal("access token bukan challenge mfa")
	}
	noJTI, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: uid.String(), Issuer: "test",
		Audience: jwt.ClaimStrings{mfaAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}).SignedString(secret)
	if _, err := m.ChallengeID(noJTI); err == nil {
		t.Fatal("token tanpa jti harus ditolak")
	}
}

func TestTOTP(t *testing.T) {
	tp := NewTOTP("Go Auth")
	sec, err := tp.GenerateSecret()
	if err != nil || len(sec) != 32 {
		t.Fatalf("%q %v", sec, err)
	}
	u, err := url.Parse(tp.URI(sec, "budi@example.com"))
	if err != nil || u.Scheme != "otpauth" || u.Host != "totp" || u.Query().Get("secret") != sec ||
		u.Query().Get("issuer") != "Go Auth" || !strings.Contains(u.Path, "budi@example.com") {
		t.Fatalf("uri %v %v", u, err)
	}
	now := time.Unix(1_700_000_000, 0)
	code, _ := totp.GenerateCode(sec, now)
	step, ok := tp.Validate(sec, code, now)
	if !ok || step != now.Unix()/30 {
		t.Fatalf("%d %v", step, ok)
	}
	// ±1 step diterima, ±2 ditolak.
	prev, _ := totp.GenerateCode(sec, now.Add(-30*time.Second))
	if s, ok := tp.Validate(sec, prev, now); !ok || s != now.Unix()/30-1 {
		t.Fatal("skew -1")
	}
	old, _ := totp.GenerateCode(sec, now.Add(-90*time.Second))
	if _, ok := tp.Validate(sec, old, now); ok && old != code && old != prev {
		t.Fatal("skew -3 harus ditolak")
	}
	if _, ok := tp.Validate(sec, "12345", now); ok {
		t.Fatal("panjang salah")
	}
	if _, ok := tp.Validate("!!!invalid", "123456", now); ok {
		t.Fatal("secret invalid")
	}
	if _, ok := tp.Validate(sec, code, time.Unix(0, 0)); ok && code != "" {
		// step 0 jendela; hanya memastikan tidak panic pada step negatif.
		_ = ok
	}
}

// fakeIDP menyediakan token endpoint OAuth2 yang mengembalikan id_token RS256.
type fakeIDP struct {
	key      *rsa.PrivateKey
	srv      *httptest.Server
	claims   jwt.MapClaims
	noIDTok  bool
	verifier string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIDP{key: key}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.verifier = r.PostForm.Get("code_verifier")
		if r.PostForm.Get("code") != "good-code" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		body := map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600}
		if !f.noIDTok {
			s, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, f.claims).SignedString(key)
			body["id_token"] = s
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIDP) provider() *OIDCProvider {
	conf := oauth2.Config{ClientID: "cid", ClientSecret: "cs", RedirectURL: "http://app/cb",
		Endpoint: oauth2.Endpoint{AuthURL: f.srv.URL + "/auth", TokenURL: f.srv.URL + "/token"},
		Scopes:   []string{oidc.ScopeOpenID, "email"}}
	v := oidc.NewVerifier("https://idp.test", &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&f.key.PublicKey}}, &oidc.Config{ClientID: "cid"})
	return newOIDCProvider(domain.ProviderGoogle, conf, v, f.srv.Client())
}

func TestOIDCProvider(t *testing.T) {
	f := newFakeIDP(t)
	p := f.provider()
	sum := sha256.Sum256([]byte("verifier"))
	ch := base64.RawURLEncoding.EncodeToString(sum[:])
	au, _ := url.Parse(p.AuthCodeURL("st", ch, "nn"))
	q := au.Query()
	if q.Get("state") != "st" || q.Get("code_challenge") != ch || q.Get("code_challenge_method") != "S256" ||
		q.Get("nonce") != "nn" || q.Get("client_id") != "cid" {
		t.Fatalf("auth url %v", au)
	}

	base := func() jwt.MapClaims {
		return jwt.MapClaims{"iss": "https://idp.test", "aud": "cid", "sub": "g-1", "nonce": "nn",
			"email": "a@b.com", "email_verified": true, "name": "A",
			"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
	}
	ctx := context.Background()
	f.claims = base()
	prof, err := p.Exchange(ctx, "good-code", "verifier", "nn")
	if err != nil || prof.Subject != "g-1" || prof.Email != "a@b.com" || !prof.EmailVerified || prof.Name != "A" || prof.Provider != "google" {
		t.Fatalf("%+v %v", prof, err)
	}
	if f.verifier != "verifier" {
		t.Fatal("PKCE verifier harus dikirim")
	}
	if _, err := p.Exchange(ctx, "bad", "verifier", "nn"); err == nil {
		t.Fatal("exchange gagal")
	}
	if _, err := p.Exchange(ctx, "good-code", "verifier", "other"); err == nil {
		t.Fatal("nonce mismatch")
	}
	f.claims = base()
	f.claims["aud"] = "other"
	if _, err := p.Exchange(ctx, "good-code", "verifier", "nn"); err == nil {
		t.Fatal("audience salah")
	}
	f.claims = base()
	f.claims["email_verified"] = "yes"
	if _, err := p.Exchange(ctx, "good-code", "verifier", "nn"); err == nil {
		t.Fatal("claims invalid")
	}
	f.noIDTok = true
	if _, err := p.Exchange(ctx, "good-code", "verifier", "nn"); err == nil {
		t.Fatal("tanpa id_token")
	}
	if _, err := NewGoogleProvider(canceledCtx(), "a", "b", "c"); err == nil {
		t.Fatal("discovery dengan ctx batal harus gagal")
	}
}

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
