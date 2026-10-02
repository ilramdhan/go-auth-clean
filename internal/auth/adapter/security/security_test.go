package security

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"go-auth-clean/internal/auth/domain"
)

func TestBcryptHasher(t *testing.T) {
	h, err := NewBcryptHasher(bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := h.Hash("Rahasia123!")
	if err != nil || hash == "Rahasia123!" {
		t.Fatal(err)
	}
	if err := h.Compare(hash, "Rahasia123!"); err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(hash, "salah"); err == nil {
		t.Fatal("mismatch harus error")
	}
	if err := h.Compare("", "apa saja"); err == nil {
		t.Fatal("hash kosong (dummy) harus error")
	}
	if _, err := NewBcryptHasher(100); err == nil {
		t.Fatal("cost invalid harus error")
	}
}

func TestJWTIssuer(t *testing.T) {
	secret := bytes.Repeat([]byte("k"), 32)
	j := NewJWTIssuer(secret, "test", time.Minute)
	uid, sid := uuid.New(), uuid.New()
	tok, exp, err := j.Issue(uid, sid, []domain.Role{domain.RoleUser, domain.RoleAdmin}, time.Now())
	if err != nil || exp.IsZero() {
		t.Fatal(err)
	}
	gu, gs, err := j.Verify(tok)
	if err != nil || gu != uid || gs != sid {
		t.Fatalf("%v %v %v", gu, gs, err)
	}

	if _, _, err := NewJWTIssuer([]byte("other-secret-other-secret-other!"), "test", time.Minute).Verify(tok); err == nil {
		t.Fatal("secret lain harus ditolak")
	}
	if _, _, err := NewJWTIssuer(secret, "other", time.Minute).Verify(tok); err == nil {
		t.Fatal("issuer lain harus ditolak")
	}
	old, _, _ := j.Issue(uid, sid, nil, time.Now().Add(-time.Hour))
	if _, _, err := j.Verify(old); err == nil {
		t.Fatal("expired harus ditolak")
	}
	if _, _, err := j.Verify("not.a.jwt"); err == nil {
		t.Fatal("garbage")
	}

	// alg none / subject & sid tidak valid.
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": uid.String()}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, _, err := j.Verify(none); err == nil {
		t.Fatal("alg none harus ditolak")
	}
	sign := func(sub, sidv string) string {
		c := Claims{SessionID: sidv, RegisteredClaims: jwt.RegisteredClaims{
			Subject: sub, Issuer: "test", Audience: jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		}}
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
		return s
	}
	if _, _, err := j.Verify(sign("x", sid.String())); err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatalf("subject: %v", err)
	}
	if _, _, err := j.Verify(sign(uid.String(), "x")); err == nil || !strings.Contains(err.Error(), "session") {
		t.Fatalf("sid: %v", err)
	}
}

func TestHMACOTPCodec(t *testing.T) {
	if _, err := NewHMACOTPCodec(make([]byte, 31)); err == nil {
		t.Fatal("key pendek harus ditolak")
	}
	key := bytes.Repeat([]byte{7}, 32)
	c, err := NewHMACOTPCodec(key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(c.pepper, key) {
		t.Fatal("pepper harus diturunkan, bukan key mentah")
	}
	seen := map[string]bool{}
	for range 200 {
		code, err := c.Generate()
		if err != nil || len(code) != 6 || strings.Trim(code, "0123456789") != "" {
			t.Fatalf("code %q %v", code, err)
		}
		seen[code] = true
	}
	if len(seen) < 150 {
		t.Fatal("kode tidak cukup acak")
	}
	h := c.Hash("123456")
	if len(h) != 32 || !c.Verify(h, "123456") || c.Verify(h, "123457") || c.Verify(nil, "123456") {
		t.Fatal("verify")
	}
	c2, _ := NewHMACOTPCodec(bytes.Repeat([]byte{8}, 32))
	if c2.Verify(h, "123456") {
		t.Fatal("pepper berbeda harus menghasilkan hash berbeda")
	}
}
