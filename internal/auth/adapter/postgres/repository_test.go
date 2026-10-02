package postgres

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/database"
	"go-auth-clean/internal/platform/database/dbtest"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

var t0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p := dbtest.New(t)
	dbtest.Truncate(t, p, "api_keys", "oauth_login_codes", "user_identities", "user_roles",
		"mfa_recovery_codes", "user_mfa", "audit_logs", "verification_tokens", "sessions", "users")
	return p
}

func newUser(t *testing.T, r *UserRepository, email string, st domain.UserStatus) *domain.User {
	t.Helper()
	u := &domain.User{ID: uuid.New(), Email: email, PasswordHash: "hash", FullName: "Budi", Status: st, CreatedAt: t0, UpdatedAt: t0}
	if st == domain.UserStatusActive {
		u.EmailVerifiedAt = &t0
	}
	if err := r.Create(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func is(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestUserRepository(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	r := NewUserRepository(p)

	u := newUser(t, r, "budi@example.com", domain.UserStatusPendingVerification)
	is(t, r.Create(ctx, &domain.User{ID: uuid.New(), Email: "BUDI@example.com", PasswordHash: "x", FullName: "X",
		Status: domain.UserStatusPendingVerification, CreatedAt: t0, UpdatedAt: t0}), domain.ErrEmailTaken)

	got, err := r.FindByEmail(ctx, "budi@example.com")
	ok(t, err)
	if got.ID != u.ID || got.Status != domain.UserStatusPendingVerification || got.EmailVerifiedAt != nil {
		t.Fatalf("got %+v", got)
	}
	_, err = r.FindByEmail(ctx, "nobody@example.com")
	is(t, err, domain.ErrUserNotFound)
	_, err = r.FindByID(ctx, uuid.New())
	is(t, err, domain.ErrUserNotFound)

	ok(t, r.MarkEmailVerified(ctx, u.ID, t0))
	is(t, r.MarkEmailVerified(ctx, u.ID, t0), domain.ErrUserNotFound) // sudah active
	got, _ = r.FindByID(ctx, u.ID)
	if got.Status != domain.UserStatusActive || got.EmailVerifiedAt == nil {
		t.Fatalf("verify: %+v", got)
	}

	// Lockout: 5 percobaan -> terkunci tepat di percobaan ke-5.
	for i := 1; i <= domain.MaxFailedLoginAttempts; i++ {
		locked, err := r.RegisterFailedLogin(ctx, u.ID, t0)
		ok(t, err)
		if locked != (i == domain.MaxFailedLoginAttempts) {
			t.Fatalf("attempt %d locked=%v", i, locked)
		}
	}
	locked, err := r.RegisterFailedLogin(ctx, u.ID, t0.Add(time.Minute))
	ok(t, err)
	got, _ = r.FindByID(ctx, u.ID)
	if locked || !got.IsLocked(t0.Add(time.Minute)) || !got.LockedUntil.Equal(t0.Add(domain.LockDuration)) {
		t.Fatalf("lock must not extend: locked=%v %+v", locked, got.LockedUntil)
	}
	// Lock kedaluwarsa: hitungan mulai dari 1.
	after := t0.Add(domain.LockDuration + time.Second)
	locked, err = r.RegisterFailedLogin(ctx, u.ID, after)
	ok(t, err)
	got, _ = r.FindByID(ctx, u.ID)
	if locked || got.FailedLoginAttempts != 1 || got.LockedUntil != nil {
		t.Fatalf("reset: %+v", got)
	}
	_, err = r.RegisterFailedLogin(ctx, uuid.New(), t0)
	is(t, err, domain.ErrUserNotFound)

	ok(t, r.RecordLogin(ctx, u.ID, after))
	got, _ = r.FindByID(ctx, u.ID)
	if got.FailedLoginAttempts != 0 || got.LastLoginAt == nil {
		t.Fatalf("record login: %+v", got)
	}

	_, _ = r.RegisterFailedLogin(ctx, u.ID, after)
	ok(t, r.UpdatePassword(ctx, u.ID, "newhash", after))
	got, _ = r.FindByID(ctx, u.ID)
	if got.PasswordHash != "newhash" || got.PasswordChangedAt == nil || got.FailedLoginAttempts != 0 {
		t.Fatalf("update password: %+v", got)
	}
	is(t, r.UpdatePassword(ctx, uuid.New(), "x", after), domain.ErrUserNotFound)

	up, err := r.UpdateProfile(ctx, u.ID, "Budi Baru", after)
	ok(t, err)
	if up.FullName != "Budi Baru" || !up.UpdatedAt.Equal(after) {
		t.Fatalf("profile: %+v", up)
	}
	_, err = r.UpdateProfile(ctx, uuid.New(), "X", after)
	is(t, err, domain.ErrUserNotFound)

	ok(t, r.SoftDelete(ctx, u.ID, after))
	is(t, r.SoftDelete(ctx, u.ID, after), domain.ErrUserNotFound)
	_, err = r.FindByID(ctx, u.ID)
	is(t, err, domain.ErrUserNotFound)
	var email, name, hash string
	ok(t, p.QueryRow(ctx, `SELECT email, full_name, password_hash FROM users WHERE id=$1`, u.ID).Scan(&email, &name, &hash))
	if email == "budi@example.com" || name != "Deleted User" || hash != "" {
		t.Fatalf("PII not anonymized: %s %s", email, name)
	}
	// Email bisa dipakai ulang.
	newUser(t, r, "budi@example.com", domain.UserStatusPendingVerification)
}

func TestUserRepository_PurgeUnverified(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	r := NewUserRepository(p)
	newUser(t, r, "pending@example.com", domain.UserStatusPendingVerification)
	newUser(t, r, "active@example.com", domain.UserStatusActive)
	n, err := r.PurgeUnverified(ctx, t0)
	ok(t, err)
	if n != 0 {
		t.Fatal("cutoff boundary")
	}
	n, err = r.PurgeUnverified(ctx, t0.Add(time.Second))
	ok(t, err)
	if n != 1 {
		t.Fatalf("purged %d", n)
	}
	_, err = r.FindByEmail(ctx, "active@example.com")
	ok(t, err)
}

func TestUserRepository_ConcurrentFailedLogin(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	r := NewUserRepository(p)
	u := newUser(t, r, "race@example.com", domain.UserStatusActive)
	var wg sync.WaitGroup
	var mu sync.Mutex
	lockedCount := 0
	for range 10 {
		wg.Go(func() {
			l, err := r.RegisterFailedLogin(ctx, u.ID, t0)
			if err != nil {
				t.Error(err)
			}
			if l {
				mu.Lock()
				lockedCount++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	got, _ := r.FindByID(ctx, u.ID)
	if lockedCount != 1 || got.LockedUntil == nil {
		t.Fatalf("locked transitions = %d, %+v", lockedCount, got)
	}
}

func newSession(t *testing.T, r *SessionRepository, userID, family uuid.UUID, created time.Time) *domain.Session {
	t.Helper()
	s := &domain.Session{ID: uuid.New(), UserID: userID, FamilyID: family, RefreshTokenHash: []byte(uuid.NewString()),
		ClientIP: "10.0.0.1", UserAgent: "ua", ExpiresAt: created.Add(24 * time.Hour), CreatedAt: created}
	ok(t, r.Create(context.Background(), s))
	return s
}

func TestSessionRepository(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	users := NewUserRepository(p)
	r := NewSessionRepository(p)
	u := newUser(t, users, "a@example.com", domain.UserStatusActive)
	other := newUser(t, users, "b@example.com", domain.UserStatusActive)

	famA := uuid.New()
	a1 := newSession(t, r, u.ID, famA, t0)
	got, err := r.FindByTokenHash(ctx, a1.RefreshTokenHash)
	ok(t, err)
	if got.ID != a1.ID || got.RevokedReason != "" || !got.IsActive(t0) {
		t.Fatalf("got %+v", got)
	}
	_, err = r.FindByTokenHash(ctx, []byte("nope"))
	is(t, err, domain.ErrSessionNotFound)
	_, err = r.FindByID(ctx, uuid.New())
	is(t, err, domain.ErrSessionNotFound)

	// Rotation: hanya sekali.
	ok(t, r.MarkRotated(ctx, a1.ID, t0.Add(time.Minute)))
	is(t, r.MarkRotated(ctx, a1.ID, t0.Add(time.Minute)), domain.ErrSessionInvalid)
	a2 := newSession(t, r, u.ID, famA, t0.Add(time.Minute))
	famB := uuid.New()
	newSession(t, r, u.ID, famB, t0.Add(2*time.Minute))
	newSession(t, r, other.ID, uuid.New(), t0)

	list, err := r.ListActiveByUser(ctx, u.ID, t0.Add(3*time.Minute))
	ok(t, err)
	if len(list) != 2 || list[0].FamilyID != famB || list[1].FamilyID != famA ||
		!list[1].CreatedAt.Equal(t0) || !list[1].LastUsedAt.Equal(a2.CreatedAt) {
		t.Fatalf("list = %+v", list)
	}

	// IDOR: user lain tidak bisa revoke.
	is(t, r.RevokeFamilyForUser(ctx, other.ID, famA, t0, domain.RevokeSessionRevoked), domain.ErrSessionNotFound)
	ok(t, r.RevokeFamilyForUser(ctx, u.ID, famA, t0.Add(3*time.Minute), domain.RevokeSessionRevoked))
	is(t, r.RevokeFamilyForUser(ctx, u.ID, famA, t0.Add(3*time.Minute), domain.RevokeSessionRevoked), domain.ErrSessionNotFound)
	got, _ = r.FindByID(ctx, a2.ID)
	if got.RevokedAt == nil || got.RevokedReason != domain.RevokeSessionRevoked {
		t.Fatalf("revoked: %+v", got)
	}
	// Family expired dianggap tidak ada.
	is(t, r.RevokeFamilyForUser(ctx, u.ID, famB, t0.Add(48*time.Hour), domain.RevokeSessionRevoked), domain.ErrSessionNotFound)

	ok(t, r.RevokeFamily(ctx, famB, t0, domain.RevokeReuseDetected))
	list, _ = r.ListActiveByUser(ctx, u.ID, t0.Add(3*time.Minute))
	if len(list) != 0 {
		t.Fatalf("list after revoke = %+v", list)
	}

	n, err := r.DeleteExpired(ctx, t0.Add(25*time.Hour))
	ok(t, err)
	if n != 4 {
		t.Fatalf("deleted %d", n)
	}
}

func TestSessionRepository_RevokeAllAndExcess(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	r := NewSessionRepository(p)
	u := newUser(t, NewUserRepository(p), "a@example.com", domain.UserStatusActive)
	fams := make([]uuid.UUID, 4)
	for i := range fams {
		fams[i] = uuid.New()
		newSession(t, r, u.ID, fams[i], t0.Add(time.Duration(i)*time.Minute))
	}
	n, err := r.RevokeExcess(ctx, u.ID, 2, t0.Add(time.Hour))
	ok(t, err)
	if n != 2 {
		t.Fatalf("excess revoked %d", n)
	}
	list, _ := r.ListActiveByUser(ctx, u.ID, t0.Add(time.Hour))
	if len(list) != 2 || list[0].FamilyID != fams[3] || list[1].FamilyID != fams[2] {
		t.Fatalf("kept = %+v", list)
	}

	ok(t, r.RevokeAllByUser(ctx, u.ID, fams[3], t0.Add(time.Hour), domain.RevokeLogoutAll))
	list, _ = r.ListActiveByUser(ctx, u.ID, t0.Add(time.Hour))
	if len(list) != 1 || list[0].FamilyID != fams[3] {
		t.Fatalf("except = %+v", list)
	}
	ok(t, r.RevokeAllByUser(ctx, u.ID, uuid.Nil, t0.Add(time.Hour), domain.RevokeLogoutAll))
	list, _ = r.ListActiveByUser(ctx, u.ID, t0.Add(time.Hour))
	if len(list) != 0 {
		t.Fatal("all must be revoked")
	}
}

func newOTP(userID uuid.UUID, purpose domain.OTPPurpose, created time.Time) *domain.OTP {
	return &domain.OTP{ID: uuid.New(), UserID: userID, Purpose: purpose, Target: "a@example.com",
		CodeHash: bytes.Repeat([]byte{1}, 32), MaxAttempts: 3, CreatedAt: created, ExpiresAt: created.Add(10 * time.Minute)}
}

func TestOTPRepository(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	r := NewOTPRepository(p)
	u := newUser(t, NewUserRepository(p), "a@example.com", domain.UserStatusPendingVerification)

	_, err := r.FindActive(ctx, u.ID, domain.OTPEmailVerification)
	is(t, err, domain.ErrOTPNotFound)

	o := newOTP(u.ID, domain.OTPEmailVerification, t0)
	ok(t, r.Create(ctx, o))
	// Satu token aktif per purpose.
	is(t, r.Create(ctx, newOTP(u.ID, domain.OTPEmailVerification, t0)), domain.ErrOTPActiveExists)
	ok(t, r.Create(ctx, newOTP(u.ID, domain.OTPPasswordReset, t0)))

	got, err := r.FindActive(ctx, u.ID, domain.OTPEmailVerification)
	ok(t, err)
	if got.ID != o.ID || len(got.CodeHash) != 32 || got.Attempts != 0 {
		t.Fatalf("got %+v", got)
	}

	for i := 1; i <= 3; i++ {
		n, err := r.IncrementAttempts(ctx, o.ID)
		ok(t, err)
		if n != i {
			t.Fatal(n)
		}
	}
	_, err = r.IncrementAttempts(ctx, o.ID)
	is(t, err, domain.ErrOTPTooManyAttempts)

	ok(t, r.Consume(ctx, o.ID, t0))
	is(t, r.Consume(ctx, o.ID, t0), domain.ErrInvalidOTP)
	_, err = r.FindActive(ctx, u.ID, domain.OTPEmailVerification)
	is(t, err, domain.ErrOTPNotFound)

	// Invalidate + create baru dalam satu tx.
	tx := database.NewTxManager(p)
	o2 := newOTP(u.ID, domain.OTPPasswordReset, t0.Add(time.Minute))
	ok(t, tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := r.InvalidateActive(ctx, u.ID, domain.OTPPasswordReset, t0.Add(time.Minute)); err != nil {
			return err
		}
		return r.Create(ctx, o2)
	}))
	got, _ = r.FindActive(ctx, u.ID, domain.OTPPasswordReset)
	if got.ID != o2.ID {
		t.Fatal("new otp must be active")
	}

	st, err := r.Stats(ctx, u.ID, domain.OTPPasswordReset, t0)
	ok(t, err)
	if st.Count != 2 || st.LastIssued == nil || !st.LastIssued.Equal(o2.CreatedAt) {
		t.Fatalf("stats %+v", st)
	}
	st, err = r.Stats(ctx, uuid.New(), domain.OTPPasswordReset, t0)
	ok(t, err)
	if st.Count != 0 || st.LastIssued != nil {
		t.Fatalf("empty stats %+v", st)
	}

	n, err := r.DeleteExpired(ctx, t0.Add(10*time.Minute+time.Second))
	ok(t, err)
	if n != 2 {
		t.Fatalf("deleted %d", n)
	}
}

func TestAuditRepository(t *testing.T) {
	p := setup(t)
	ctx := context.Background()
	r := NewAuditRepository(p)
	u := newUser(t, NewUserRepository(p), "a@example.com", domain.UserStatusActive)
	sid := uuid.New()

	var ids []uuid.UUID
	for i := range 5 {
		e := &domain.AuditEvent{ID: uuid.New(), UserID: u.ID, EventType: domain.EventLoginSucceeded, Outcome: domain.OutcomeSuccess,
			SessionID: sid, ClientIP: "::ffff:10.0.0.1", UserAgent: "ua", RequestID: "req", Metadata: map[string]string{"k": "v"},
			OccurredAt: t0.Add(time.Duration(i) * time.Minute)}
		ok(t, r.Record(ctx, e))
		ids = append(ids, e.ID)
	}
	// Event tanpa user, IP invalid -> tetap tersimpan.
	ok(t, r.Record(ctx, &domain.AuditEvent{ID: uuid.New(), EventType: domain.EventLoginFailed, Outcome: domain.OutcomeFailure,
		EmailHash: []byte("h"), ClientIP: "not-an-ip", OccurredAt: t0}))

	page, err := r.ListByUser(ctx, u.ID, nil, 2)
	ok(t, err)
	if len(page) != 2 || page[0].ID != ids[4] || page[1].ID != ids[3] {
		t.Fatalf("page1 %+v", page)
	}
	e := page[0]
	if e.ClientIP != "10.0.0.1" || e.SessionID != sid || e.UserAgent != "ua" || e.RequestID != "req" || e.Metadata["k"] != "v" {
		t.Fatalf("event %+v", e)
	}
	page, err = r.ListByUser(ctx, u.ID, &domain.AuditKeyset{OccurredAt: page[1].OccurredAt, ID: page[1].ID}, 10)
	ok(t, err)
	if len(page) != 3 || page[0].ID != ids[2] {
		t.Fatalf("page2 %+v", page)
	}

	n, err := r.AnonymizeBefore(ctx, t0.Add(2*time.Minute))
	ok(t, err)
	if n != 3 {
		t.Fatalf("anonymized %d", n)
	}
	n, _ = r.AnonymizeBefore(ctx, t0.Add(2*time.Minute))
	if n != 0 {
		t.Fatal("idempotent")
	}
	page, _ = r.ListByUser(ctx, u.ID, nil, 10)
	last := page[len(page)-1]
	if last.ClientIP != "" || last.UserAgent != "" || page[0].ClientIP == "" {
		t.Fatalf("anonymize result %+v", last)
	}
}
