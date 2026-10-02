package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRoles(t *testing.T) {
	for _, s := range []string{"user", "admin"} {
		if r, err := ParseRole(s); err != nil || string(r) != s {
			t.Fatalf("ParseRole(%q) = %v, %v", s, r, err)
		}
	}
	if _, err := ParseRole("Admin"); !errors.Is(err, ErrInvalidRole) {
		t.Fatal("role case-sensitive")
	}
	rs := []Role{RoleUser, RoleAdmin}
	if !HasRole(rs, RoleAdmin) || HasRole(rs[:1], RoleAdmin) {
		t.Fatal("HasRole")
	}
	if got := RoleStrings(rs); len(got) != 2 || got[1] != "admin" {
		t.Fatalf("RoleStrings = %v", got)
	}
}

func TestMFAHelpers(t *testing.T) {
	var nilM *UserMFA
	if nilM.IsEnabled() || (&UserMFA{Status: MFAPending}).IsEnabled() || !(&UserMFA{Status: MFAEnabled}).IsEnabled() {
		t.Fatal("IsEnabled")
	}
	if got := NormalizeRecoveryCode(" ABCDE-fghjk "); got != "abcdefghjk" {
		t.Fatalf("got %q", got)
	}
	for code, want := range map[string]bool{"123456": true, "12345": false, "12345a": false, "1234567": false} {
		if LooksLikeTOTP(code) != want {
			t.Fatalf("LooksLikeTOTP(%q)", code)
		}
	}
}

func TestAPIKeyHelpers(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	cases := []struct {
		k    APIKey
		want bool
	}{
		{APIKey{}, true},
		{APIKey{ExpiresAt: &future}, true},
		{APIKey{ExpiresAt: &past}, false},
		{APIKey{ExpiresAt: &now}, false},
		{APIKey{RevokedAt: &past}, false},
	}
	for i, c := range cases {
		if c.k.IsActive(now) != c.want {
			t.Fatalf("case %d", i)
		}
	}
	k := APIKey{Scopes: []Scope{ScopeRead}}
	if !k.HasScope(ScopeRead) || k.HasScope(ScopeWrite) {
		t.Fatal("HasScope")
	}

	sc, err := ParseScopes([]string{" WRITE", "read", "write"})
	if err != nil || len(sc) != 2 || sc[0] != ScopeRead || sc[1] != ScopeWrite {
		t.Fatalf("ParseScopes = %v, %v", sc, err)
	}
	for _, bad := range [][]string{nil, {}, {"admin"}, {"read", ""}} {
		if _, err := ParseScopes(bad); !errors.Is(err, ErrInvalidScope) {
			t.Fatalf("ParseScopes(%v) err = %v", bad, err)
		}
	}

	if n, err := NormalizeAPIKeyName("  CI  "); err != nil || n != "CI" {
		t.Fatalf("name = %q, %v", n, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 101), "a\tb", "bad\xff"} {
		if _, err := NormalizeAPIKeyName(bad); !errors.Is(err, ErrInvalidAPIKeyName) {
			t.Fatalf("name %q err = %v", bad, err)
		}
	}
}

func TestUserP2Helpers(t *testing.T) {
	if (&User{}).HasPassword() || !(&User{PasswordHash: "x"}).HasPassword() {
		t.Fatal("HasPassword")
	}
	for _, s := range []string{"", "active", "suspended", "pending_verification"} {
		if _, err := ParseUserStatus(s); err != nil {
			t.Fatalf("status %q: %v", s, err)
		}
	}
	if _, err := ParseUserStatus("deleted"); err == nil {
		t.Fatal("deleted tidak boleh difilter")
	}
}
