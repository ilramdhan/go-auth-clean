package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

func h32(s string) []byte {
	b := sha256.Sum256([]byte(s))
	return b[:]
}

func TestUserRepository_ListAndStatus(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	r := NewUserRepository(p)
	var users []*domain.User
	for i := range 5 {
		u := &domain.User{ID: uuid.New(), Email: fmt.Sprintf("u%d@example.com", i), PasswordHash: "h",
			FullName: fmt.Sprintf("Nama %d", i), Status: domain.UserStatusActive,
			CreatedAt: t0.Add(time.Duration(i) * time.Minute), UpdatedAt: t0}
		ok(t, r.Create(ctx, u))
		users = append(users, u)
	}
	special := &domain.User{ID: uuid.New(), Email: "persen_100%@example.com", PasswordHash: "h", FullName: "Siti Aminah",
		Status: domain.UserStatusPendingVerification, CreatedAt: t0.Add(time.Hour), UpdatedAt: t0}
	ok(t, r.Create(ctx, special))
	ok(t, r.SoftDelete(ctx, users[0].ID, t0))

	all, err := r.List(ctx, domain.UserFilter{Limit: 10})
	ok(t, err)
	if len(all) != 5 || all[0].ID != special.ID {
		t.Fatalf("list %d", len(all))
	}
	page1, err := r.List(ctx, domain.UserFilter{Limit: 2})
	ok(t, err)
	page2, err := r.List(ctx, domain.UserFilter{Limit: 10, After: &domain.UserKeyset{CreatedAt: page1[1].CreatedAt, ID: page1[1].ID}})
	ok(t, err)
	if len(page1) != 2 || len(page2) != 3 || page2[0].ID == page1[1].ID {
		t.Fatalf("keyset %d %d", len(page1), len(page2))
	}
	got, err := r.List(ctx, domain.UserFilter{Query: "siti", Limit: 10})
	ok(t, err)
	if len(got) != 1 || got[0].ID != special.ID {
		t.Fatalf("search name %d", len(got))
	}
	// Wildcard diperlakukan literal: "%" hanya cocok dengan email yang benar-benar mengandung "%".
	got, err = r.List(ctx, domain.UserFilter{Query: "100%", Limit: 10})
	ok(t, err)
	if len(got) != 1 {
		t.Fatalf("escape like %d", len(got))
	}
	got, err = r.List(ctx, domain.UserFilter{Query: "_", Limit: 10})
	ok(t, err)
	if len(got) != 1 {
		t.Fatalf("underscore literal %d", len(got))
	}
	got, err = r.List(ctx, domain.UserFilter{Status: domain.UserStatusPendingVerification, Limit: 10})
	ok(t, err)
	if len(got) != 1 {
		t.Fatalf("status %d", len(got))
	}

	ok(t, r.SetStatus(ctx, users[1].ID, domain.UserStatusActive, domain.UserStatusSuspended, t0))
	u, _ := r.FindByID(ctx, users[1].ID)
	if u.Status != domain.UserStatusSuspended {
		t.Fatal("suspended")
	}
	is(t, r.SetStatus(ctx, users[1].ID, domain.UserStatusActive, domain.UserStatusSuspended, t0), domain.ErrInvalidTransition)
	is(t, r.SetStatus(ctx, uuid.New(), domain.UserStatusActive, domain.UserStatusSuspended, t0), domain.ErrUserNotFound)
	is(t, r.SetStatus(ctx, users[0].ID, domain.UserStatusActive, domain.UserStatusSuspended, t0), domain.ErrUserNotFound)
}

func TestRoleRepository(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	users := NewUserRepository(p)
	r := NewRoleRepository(p)
	a := newUser(t, users, "a@example.com", domain.UserStatusActive)
	b := newUser(t, users, "b@example.com", domain.UserStatusActive)

	roles, err := r.ListByUser(ctx, a.ID)
	ok(t, err)
	if len(roles) != 1 || roles[0] != domain.RoleUser {
		t.Fatalf("%v", roles)
	}
	ok(t, r.Grant(ctx, a.ID, domain.RoleAdmin, uuid.Nil, t0))
	ok(t, r.Grant(ctx, a.ID, domain.RoleAdmin, uuid.Nil, t0)) // idempoten
	ok(t, r.Grant(ctx, b.ID, domain.RoleAdmin, a.ID, t0))
	roles, _ = r.ListByUser(ctx, a.ID)
	if !domain.HasRole(roles, domain.RoleAdmin) || len(roles) != 2 {
		t.Fatalf("%v", roles)
	}
	n, err := r.CountUsers(ctx, domain.RoleAdmin)
	ok(t, err)
	if n != 2 {
		t.Fatalf("count %d", n)
	}
	ok(t, users.SoftDelete(ctx, b.ID, t0))
	if n, _ := r.CountUsers(ctx, domain.RoleAdmin); n != 1 {
		t.Fatalf("count after delete %d", n)
	}
	ok(t, r.Revoke(ctx, a.ID, domain.RoleAdmin))
	ok(t, r.Revoke(ctx, a.ID, domain.RoleAdmin))
	roles, _ = r.ListByUser(ctx, a.ID)
	if domain.HasRole(roles, domain.RoleAdmin) {
		t.Fatal("revoked")
	}
}

func TestMFARepository(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	users := NewUserRepository(p)
	r := NewMFARepository(p)
	u := newUser(t, users, "m@example.com", domain.UserStatusActive)

	_, err := r.Get(ctx, u.ID)
	is(t, err, domain.ErrMFANotFound)
	is(t, r.Enable(ctx, u.ID, 10, t0), domain.ErrMFASetupRequired)
	ok(t, r.UpsertPending(ctx, &domain.UserMFA{UserID: u.ID, SecretEncrypted: "enc1", CreatedAt: t0}))
	ok(t, r.UpsertPending(ctx, &domain.UserMFA{UserID: u.ID, SecretEncrypted: "enc2", CreatedAt: t0}))
	m, err := r.Get(ctx, u.ID)
	ok(t, err)
	if m.SecretEncrypted != "enc2" || m.Status != domain.MFAPending || m.IsEnabled() {
		t.Fatalf("%+v", m)
	}
	ok(t, r.Enable(ctx, u.ID, 100, t0))
	m, _ = r.Get(ctx, u.ID)
	if !m.IsEnabled() || m.LastUsedStep != 100 || m.EnabledAt == nil {
		t.Fatalf("%+v", m)
	}
	is(t, r.UpsertPending(ctx, &domain.UserMFA{UserID: u.ID, SecretEncrypted: "x", CreatedAt: t0}), domain.ErrMFAAlreadyEnabled)
	is(t, r.Enable(ctx, u.ID, 101, t0), domain.ErrMFASetupRequired)

	is(t, r.AdvanceStep(ctx, u.ID, 100, t0), domain.ErrMFACodeReplayed)
	is(t, r.AdvanceStep(ctx, u.ID, 99, t0), domain.ErrMFACodeReplayed)
	ok(t, r.AdvanceStep(ctx, u.ID, 101, t0))

	// Race: hanya satu dari banyak pemakaian step yang sama yang berhasil.
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if r.AdvanceStep(ctx, u.ID, 200, t0) == nil {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("replay race wins=%d", wins.Load())
	}

	ok(t, r.ReplaceRecoveryCodes(ctx, u.ID, [][]byte{h32("a"), h32("b"), h32("c")}, t0))
	if n, _ := r.CountRecoveryCodes(ctx, u.ID); n != 3 {
		t.Fatalf("count %d", n)
	}
	ok(t, r.UseRecoveryCode(ctx, u.ID, h32("a"), t0))
	is(t, r.UseRecoveryCode(ctx, u.ID, h32("a"), t0), domain.ErrInvalidMFACode)
	is(t, r.UseRecoveryCode(ctx, u.ID, h32("zzz"), t0), domain.ErrInvalidMFACode)
	if n, _ := r.CountRecoveryCodes(ctx, u.ID); n != 2 {
		t.Fatalf("count %d", n)
	}
	ok(t, r.ReplaceRecoveryCodes(ctx, u.ID, [][]byte{h32("d")}, t0))
	is(t, r.UseRecoveryCode(ctx, u.ID, h32("b"), t0), domain.ErrInvalidMFACode)
	if n, _ := r.CountRecoveryCodes(ctx, u.ID); n != 1 {
		t.Fatalf("count %d", n)
	}

	ok(t, r.Delete(ctx, u.ID))
	ok(t, r.Delete(ctx, u.ID))
	_, err = r.Get(ctx, u.ID)
	is(t, err, domain.ErrMFANotFound)
	if n, _ := r.CountRecoveryCodes(ctx, u.ID); n != 0 {
		t.Fatalf("codes after delete %d", n)
	}
}

func TestIdentityAndLoginCodeRepository(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	users := NewUserRepository(p)
	r := NewIdentityRepository(p)
	lc := NewLoginCodeRepository(p)
	a := newUser(t, users, "a@example.com", domain.UserStatusActive)
	b := newUser(t, users, "b@example.com", domain.UserStatusActive)

	_, err := r.FindBySubject(ctx, "google", "s1")
	is(t, err, domain.ErrIdentityNotFound)
	id1 := &domain.ExternalIdentity{ID: uuid.New(), UserID: a.ID, Provider: "google", Subject: "s1", Email: "A@example.com", CreatedAt: t0}
	ok(t, r.Create(ctx, id1))
	got, err := r.FindBySubject(ctx, "google", "s1")
	ok(t, err)
	if got.UserID != a.ID || got.Email != "A@example.com" {
		t.Fatalf("%+v", got)
	}
	// subject sama untuk user lain / provider kedua untuk user yang sama.
	is(t, r.Create(ctx, &domain.ExternalIdentity{ID: uuid.New(), UserID: b.ID, Provider: "google", Subject: "s1", Email: "x@x.com", CreatedAt: t0}), domain.ErrIdentityTaken)
	is(t, r.Create(ctx, &domain.ExternalIdentity{ID: uuid.New(), UserID: a.ID, Provider: "google", Subject: "s2", Email: "x@x.com", CreatedAt: t0}), domain.ErrIdentityTaken)
	list, err := r.ListByUser(ctx, a.ID)
	ok(t, err)
	if len(list) != 1 {
		t.Fatal("list")
	}
	ok(t, r.DeleteByUser(ctx, a.ID))
	if list, _ := r.ListByUser(ctx, a.ID); len(list) != 0 {
		t.Fatal("deleted")
	}

	ok(t, lc.Create(ctx, &domain.LoginCode{CodeHash: h32("c1"), UserID: a.ID, Provider: "google", ExpiresAt: t0.Add(time.Minute), CreatedAt: t0}))
	ok(t, lc.Create(ctx, &domain.LoginCode{CodeHash: h32("c2"), UserID: a.ID, Provider: "google", ExpiresAt: t0.Add(time.Minute), CreatedAt: t0}))
	ok(t, lc.Create(ctx, &domain.LoginCode{CodeHash: h32("old"), UserID: a.ID, Provider: "google", ExpiresAt: t0.Add(-time.Minute), CreatedAt: t0}))
	c, err := lc.Consume(ctx, h32("c1"), t0)
	ok(t, err)
	if c.UserID != a.ID || !bytes.Equal(c.CodeHash, h32("c1")) {
		t.Fatalf("%+v", c)
	}
	_, err = lc.Consume(ctx, h32("c1"), t0)
	is(t, err, domain.ErrLoginCodeInvalid)
	_, err = lc.Consume(ctx, h32("c2"), t0.Add(2*time.Minute))
	is(t, err, domain.ErrLoginCodeInvalid)
	n, err := lc.DeleteExpired(ctx, t0)
	ok(t, err)
	if n != 1 {
		t.Fatalf("expired %d", n)
	}
}

func TestAPIKeyRepository(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	users := NewUserRepository(p)
	r := NewAPIKeyRepository(p)
	a := newUser(t, users, "a@example.com", domain.UserStatusActive)
	b := newUser(t, users, "b@example.com", domain.UserStatusActive)

	exp := t0.Add(24 * time.Hour)
	k1 := &domain.APIKey{ID: uuid.New(), UserID: a.ID, Name: "ci", Prefix: "abcd1234", SecretHash: h32("s1"),
		Scopes: []domain.Scope{domain.ScopeRead, domain.ScopeWrite}, ExpiresAt: &exp, CreatedAt: t0}
	k2 := &domain.APIKey{ID: uuid.New(), UserID: a.ID, Name: "ro", Prefix: "efgh5678", SecretHash: h32("s2"),
		Scopes: []domain.Scope{domain.ScopeRead}, CreatedAt: t0.Add(time.Second)}
	ok(t, r.Create(ctx, k1))
	ok(t, r.Create(ctx, k2))
	if err := r.Create(ctx, &domain.APIKey{ID: uuid.New(), UserID: b.ID, Name: "dup", Prefix: "abcd1234",
		SecretHash: h32("x"), Scopes: []domain.Scope{domain.ScopeRead}, CreatedAt: t0}); err == nil {
		t.Fatal("prefix unik")
	}
	got, err := r.FindByPrefix(ctx, "abcd1234")
	ok(t, err)
	if got.ID != k1.ID || !got.HasScope(domain.ScopeWrite) || !bytes.Equal(got.SecretHash, h32("s1")) || got.ExpiresAt == nil {
		t.Fatalf("%+v", got)
	}
	_, err = r.FindByPrefix(ctx, "zzzzzzzz")
	is(t, err, domain.ErrAPIKeyNotFound)

	list, err := r.ListByUser(ctx, a.ID)
	ok(t, err)
	if len(list) != 2 || list[0].ID != k2.ID {
		t.Fatal("list order")
	}
	if n, _ := r.CountActiveByUser(ctx, a.ID, t0); n != 2 {
		t.Fatalf("active %d", n)
	}
	if n, _ := r.CountActiveByUser(ctx, a.ID, exp.Add(time.Second)); n != 1 {
		t.Fatalf("active after expiry %d", n)
	}

	ok(t, r.TouchLastUsed(ctx, k1.ID, t0, t0.Add(-time.Minute)))
	ok(t, r.TouchLastUsed(ctx, k1.ID, t0.Add(10*time.Second), t0.Add(-time.Minute))) // throttled
	got, _ = r.FindByPrefix(ctx, "abcd1234")
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(t0) {
		t.Fatalf("touch %v", got.LastUsedAt)
	}

	// IDOR: user lain tidak bisa mencabut.
	is(t, r.Revoke(ctx, b.ID, k1.ID, t0), domain.ErrAPIKeyNotFound)
	ok(t, r.Revoke(ctx, a.ID, k1.ID, t0))
	is(t, r.Revoke(ctx, a.ID, k1.ID, t0), domain.ErrAPIKeyNotFound)
	ok(t, r.RevokeAllByUser(ctx, a.ID, t0))
	if n, _ := r.CountActiveByUser(ctx, a.ID, t0); n != 0 {
		t.Fatalf("after revoke all %d", n)
	}
	got, _ = r.FindByPrefix(ctx, "efgh5678")
	if got.IsActive(t0) {
		t.Fatal("revoked key not active")
	}
}
