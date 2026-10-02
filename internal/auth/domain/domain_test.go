package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeEmail(t *testing.T) {
	ok := [][2]string{
		{" Budi@Example.COM ", "budi@example.com"},
		{"a.b+tag@sub.co.id", "a.b+tag@sub.co.id"},
	}
	for _, c := range ok {
		got, err := NormalizeEmail(c[0])
		if err != nil || got != c[1] {
			t.Errorf("NormalizeEmail(%q) = %q, %v", c[0], got, err)
		}
	}
	bad := []string{"", "  ", "budi", "budi@localhost", "Budi <budi@example.com>", "bü@example.com",
		"a@b", strings.Repeat("a", 250) + "@b.co"}
	for _, in := range bad {
		if _, err := NormalizeEmail(in); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("NormalizeEmail(%q) err = %v", in, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("12345678"); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePassword("ąąąąąąąą"); err != nil {
		t.Fatal("8 rune harus lolos:", err)
	}
	for _, pw := range []string{"", "1234567", strings.Repeat("a", 73)} {
		if err := ValidatePassword(pw); !errors.Is(err, ErrWeakPassword) {
			t.Errorf("ValidatePassword(%q) = %v", pw, err)
		}
	}
	if err := ValidatePasswordFor("Budi@Example.com", "budi@example.com"); !errors.Is(err, ErrWeakPassword) {
		t.Fatal("password = email harus ditolak")
	}
	if err := ValidatePasswordFor("short", "x@y.co"); !errors.Is(err, ErrWeakPassword) {
		t.Fatal("short")
	}
	if err := ValidatePasswordFor("Rahasia123!", ""); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeFullName(t *testing.T) {
	if got, err := NormalizeFullName("  Siti Nurhaliza "); err != nil || got != "Siti Nurhaliza" {
		t.Fatalf("%q %v", got, err)
	}
	for _, in := range []string{"", "   ", strings.Repeat("a", 101), "a\x00b", "a\nb", "\xff"} {
		if _, err := NormalizeFullName(in); !errors.Is(err, ErrInvalidFullName) {
			t.Errorf("NormalizeFullName(%q) = %v", in, err)
		}
	}
	if _, err := NormalizeFullName(strings.Repeat("é", 100)); err != nil {
		t.Fatal("100 rune harus lolos")
	}
}

func TestUserStatus(t *testing.T) {
	now := time.Now()
	future, past := now.Add(time.Minute), now.Add(-time.Minute)
	u := &User{Status: UserStatusActive}
	if !u.CanLogin() || u.LoginError() != nil || u.IsLocked(now) {
		t.Fatal("active")
	}
	u.LockedUntil = &future
	if !u.IsLocked(now) {
		t.Fatal("locked")
	}
	u.LockedUntil = &past
	if u.IsLocked(now) {
		t.Fatal("lock expired")
	}
	cases := map[UserStatus]error{
		UserStatusPendingVerification: ErrEmailNotVerified,
		UserStatusSuspended:           ErrAccountSuspended,
		UserStatusDeleted:             ErrInvalidCredentials,
	}
	for st, want := range cases {
		u := &User{Status: st}
		if u.CanLogin() || !errors.Is(u.LoginError(), want) {
			t.Errorf("%s: %v", st, u.LoginError())
		}
	}
}

func TestOTPHelpers(t *testing.T) {
	now := time.Now()
	o := &OTP{ExpiresAt: now, MaxAttempts: 5, Attempts: 4}
	if !o.IsExpired(now) || o.IsExpired(now.Add(-time.Second)) {
		t.Fatal("expiry boundary")
	}
	if o.AttemptsExhausted() {
		t.Fatal("4/5")
	}
	o.Attempts = 5
	if !o.AttemptsExhausted() {
		t.Fatal("5/5")
	}
	for code, want := range map[string]bool{"123456": true, "000000": true, "12345": false, "1234567": false, "12a456": false, "١٢٣٤٥٦": false} {
		if ValidOTPFormat(code) != want {
			t.Errorf("ValidOTPFormat(%q) != %v", code, want)
		}
	}
}

func TestSessionIsActive(t *testing.T) {
	now := time.Now()
	s := &Session{ExpiresAt: now.Add(time.Hour)}
	if !s.IsActive(now) {
		t.Fatal("active")
	}
	s.RevokedAt = &now
	if s.IsActive(now) {
		t.Fatal("revoked")
	}
	s = &Session{ExpiresAt: now}
	if s.IsActive(now) {
		t.Fatal("expired")
	}
	s = &Session{ExpiresAt: now.Add(time.Hour), RotatedAt: &now}
	if s.IsActive(now) {
		t.Fatal("rotated")
	}
}
