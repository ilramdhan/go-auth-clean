package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

// store adalah fake in-memory untuk semua repository auth.
type store struct {
	mu       sync.Mutex
	users    map[uuid.UUID]*domain.User
	sessions map[uuid.UUID]*domain.Session
	otps     map[uuid.UUID]*domain.OTP
	audit    []domain.AuditEvent

	// P2
	roles    map[uuid.UUID][]domain.Role
	mfa      map[uuid.UUID]*domain.UserMFA
	recovery map[uuid.UUID][]recCode
	idents   map[uuid.UUID]*domain.ExternalIdentity
	codes    map[string]*domain.LoginCode
	keys     map[uuid.UUID]*domain.APIKey

	// error injection per operasi (nama method).
	fail map[string]error
}

func newStore() *store {
	return &store{
		users: map[uuid.UUID]*domain.User{}, sessions: map[uuid.UUID]*domain.Session{},
		otps: map[uuid.UUID]*domain.OTP{}, fail: map[string]error{},
		roles: map[uuid.UUID][]domain.Role{}, mfa: map[uuid.UUID]*domain.UserMFA{},
		recovery: map[uuid.UUID][]recCode{}, idents: map[uuid.UUID]*domain.ExternalIdentity{},
		codes: map[string]*domain.LoginCode{}, keys: map[uuid.UUID]*domain.APIKey{},
	}
}

func (s *store) err(op string) error { return s.fail[op] }

// ===== users =====
type fakeUsers struct{ *store }

func (f fakeUsers) Create(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.Create"); err != nil {
		return err
	}
	for _, x := range f.users {
		if x.Status != domain.UserStatusDeleted && strings.EqualFold(x.Email, u.Email) {
			return domain.ErrEmailTaken
		}
	}
	c := *u
	f.users[u.ID] = &c
	return nil
}

func (f fakeUsers) find(pred func(*domain.User) bool) (*domain.User, error) {
	for _, u := range f.users {
		if u.Status != domain.UserStatusDeleted && pred(u) {
			c := *u
			return &c, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

func (f fakeUsers) FindByEmail(_ context.Context, email string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.FindByEmail"); err != nil {
		return nil, err
	}
	return f.find(func(u *domain.User) bool { return u.Email == email })
}

func (f fakeUsers) FindByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.FindByID"); err != nil {
		return nil, err
	}
	return f.find(func(u *domain.User) bool { return u.ID == id })
}

func (f fakeUsers) get(id uuid.UUID) (*domain.User, error) {
	u, ok := f.users[id]
	if !ok || u.Status == domain.UserStatusDeleted {
		return nil, domain.ErrUserNotFound
	}
	return u, nil
}

func (f fakeUsers) RegisterFailedLogin(_ context.Context, id uuid.UUID, now time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.RegisterFailedLogin"); err != nil {
		return false, err
	}
	u, err := f.get(id)
	if err != nil {
		return false, err
	}
	u.FailedLoginAttempts++
	if u.FailedLoginAttempts >= domain.MaxFailedLoginAttempts && !u.IsLocked(now) {
		t := now.Add(domain.LockDuration)
		u.LockedUntil = &t
		return true, nil
	}
	return false, nil
}

func (f fakeUsers) RecordLogin(_ context.Context, id uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.RecordLogin"); err != nil {
		return err
	}
	u, err := f.get(id)
	if err != nil {
		return err
	}
	u.FailedLoginAttempts, u.LockedUntil, u.LastLoginAt = 0, nil, &now
	return nil
}

func (f fakeUsers) UpdatePassword(_ context.Context, id uuid.UUID, hash string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.UpdatePassword"); err != nil {
		return err
	}
	u, err := f.get(id)
	if err != nil {
		return err
	}
	u.PasswordHash, u.PasswordChangedAt, u.FailedLoginAttempts, u.LockedUntil = hash, &at, 0, nil
	return nil
}

func (f fakeUsers) MarkEmailVerified(_ context.Context, id uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.MarkEmailVerified"); err != nil {
		return err
	}
	u, err := f.get(id)
	if err != nil || u.Status != domain.UserStatusPendingVerification {
		return domain.ErrUserNotFound
	}
	u.Status, u.EmailVerifiedAt = domain.UserStatusActive, &now
	return nil
}

func (f fakeUsers) UpdateProfile(_ context.Context, id uuid.UUID, name string, now time.Time) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.UpdateProfile"); err != nil {
		return nil, err
	}
	u, err := f.get(id)
	if err != nil {
		return nil, err
	}
	u.FullName, u.UpdatedAt = name, now
	c := *u
	return &c, nil
}

func (f fakeUsers) SoftDelete(_ context.Context, id uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.SoftDelete"); err != nil {
		return err
	}
	u, err := f.get(id)
	if err != nil {
		return err
	}
	u.Email, u.FullName, u.PasswordHash = "deleted+"+id.String()+"@deleted.invalid", "Deleted User", ""
	u.Status, u.UpdatedAt = domain.UserStatusDeleted, now
	return nil
}

func (f fakeUsers) PurgeUnverified(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.PurgeUnverified"); err != nil {
		return 0, err
	}
	var n int64
	for id, u := range f.users {
		if u.Status == domain.UserStatusPendingVerification && u.CreatedAt.Before(before) {
			delete(f.users, id)
			n++
		}
	}
	return n, nil
}

// ===== sessions =====
type fakeSessions struct{ *store }

func (f fakeSessions) Create(_ context.Context, s *domain.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.Create"); err != nil {
		return err
	}
	c := *s
	f.sessions[s.ID] = &c
	return nil
}

func (f fakeSessions) FindByTokenHash(_ context.Context, h []byte) (*domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.FindByTokenHash"); err != nil {
		return nil, err
	}
	for _, s := range f.sessions {
		if bytes.Equal(s.RefreshTokenHash, h) {
			c := *s
			return &c, nil
		}
	}
	return nil, domain.ErrSessionNotFound
}

func (f fakeSessions) FindByID(_ context.Context, id uuid.UUID) (*domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.FindByID"); err != nil {
		return nil, err
	}
	s, ok := f.sessions[id]
	if !ok {
		return nil, domain.ErrSessionNotFound
	}
	c := *s
	return &c, nil
}

func (f fakeSessions) MarkRotated(_ context.Context, id uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.MarkRotated"); err != nil {
		return err
	}
	s, ok := f.sessions[id]
	if !ok || s.RotatedAt != nil || s.RevokedAt != nil {
		return domain.ErrSessionInvalid
	}
	s.RotatedAt = &now
	return nil
}

func (f fakeSessions) revoke(pred func(*domain.Session) bool, now time.Time, r domain.RevokeReason) int {
	n := 0
	for _, s := range f.sessions {
		if s.RevokedAt == nil && pred(s) {
			t := now
			s.RevokedAt, s.RevokedReason = &t, r
			n++
		}
	}
	return n
}

func (f fakeSessions) RevokeFamily(_ context.Context, fam uuid.UUID, now time.Time, r domain.RevokeReason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.RevokeFamily"); err != nil {
		return err
	}
	f.revoke(func(s *domain.Session) bool { return s.FamilyID == fam }, now, r)
	return nil
}

func (f fakeSessions) RevokeFamilyForUser(_ context.Context, uid, fam uuid.UUID, now time.Time, r domain.RevokeReason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.RevokeFamilyForUser"); err != nil {
		return err
	}
	active := false
	for _, s := range f.sessions {
		if s.UserID == uid && s.FamilyID == fam && s.IsActive(now) {
			active = true
		}
	}
	if !active {
		return domain.ErrSessionNotFound
	}
	f.revoke(func(s *domain.Session) bool { return s.UserID == uid && s.FamilyID == fam }, now, r)
	return nil
}

func (f fakeSessions) RevokeAllByUser(_ context.Context, uid, except uuid.UUID, now time.Time, r domain.RevokeReason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.RevokeAllByUser"); err != nil {
		return err
	}
	f.revoke(func(s *domain.Session) bool { return s.UserID == uid && s.FamilyID != except }, now, r)
	return nil
}

func (f fakeSessions) activeHeads(uid uuid.UUID, now time.Time) []*domain.Session {
	var out []*domain.Session
	for _, s := range f.sessions {
		if s.UserID == uid && s.IsActive(now) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (f fakeSessions) ListActiveByUser(_ context.Context, uid uuid.UUID, now time.Time) ([]domain.DeviceSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.ListActiveByUser"); err != nil {
		return nil, err
	}
	var out []domain.DeviceSession
	for _, s := range f.activeHeads(uid, now) {
		out = append(out, domain.DeviceSession{FamilyID: s.FamilyID, ClientIP: s.ClientIP, UserAgent: s.UserAgent,
			CreatedAt: s.CreatedAt, LastUsedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt})
	}
	return out, nil
}

func (f fakeSessions) RevokeExcess(_ context.Context, uid uuid.UUID, keep int, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.RevokeExcess"); err != nil {
		return 0, err
	}
	heads := f.activeHeads(uid, now)
	var n int64
	for i := keep; i < len(heads); i++ {
		fam := heads[i].FamilyID
		n += int64(f.revoke(func(s *domain.Session) bool { return s.FamilyID == fam }, now, domain.RevokeSessionLimit))
	}
	return n, nil
}

func (f fakeSessions) DeleteExpired(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("sessions.DeleteExpired"); err != nil {
		return 0, err
	}
	var n int64
	for id, s := range f.sessions {
		if s.ExpiresAt.Before(before) {
			delete(f.sessions, id)
			n++
		}
	}
	return n, nil
}

// ===== otps =====
type fakeOTPs struct{ *store }

func (f fakeOTPs) InvalidateActive(_ context.Context, uid uuid.UUID, p domain.OTPPurpose, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("otps.InvalidateActive"); err != nil {
		return err
	}
	for _, o := range f.otps {
		if o.UserID == uid && o.Purpose == p && o.ConsumedAt == nil && o.InvalidatedAt == nil {
			t := now
			o.InvalidatedAt = &t
		}
	}
	return nil
}

func (f fakeOTPs) Create(_ context.Context, o *domain.OTP) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("otps.Create"); err != nil {
		return err
	}
	for _, x := range f.otps {
		if x.UserID == o.UserID && x.Purpose == o.Purpose && x.ConsumedAt == nil && x.InvalidatedAt == nil {
			return domain.ErrOTPActiveExists
		}
	}
	c := *o
	f.otps[o.ID] = &c
	return nil
}

func (f fakeOTPs) active(uid uuid.UUID, p domain.OTPPurpose) *domain.OTP {
	for _, o := range f.otps {
		if o.UserID == uid && o.Purpose == p && o.ConsumedAt == nil && o.InvalidatedAt == nil {
			return o
		}
	}
	return nil
}

func (f fakeOTPs) FindActive(_ context.Context, uid uuid.UUID, p domain.OTPPurpose) (*domain.OTP, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("otps.FindActive"); err != nil {
		return nil, err
	}
	o := f.active(uid, p)
	if o == nil {
		return nil, domain.ErrOTPNotFound
	}
	c := *o
	return &c, nil
}

func (f fakeOTPs) IncrementAttempts(_ context.Context, id uuid.UUID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("otps.IncrementAttempts"); err != nil {
		return 0, err
	}
	o := f.otps[id]
	if o == nil || o.Attempts >= o.MaxAttempts {
		return 0, domain.ErrOTPTooManyAttempts
	}
	o.Attempts++
	return o.Attempts, nil
}

func (f fakeOTPs) Consume(_ context.Context, id uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("otps.Consume"); err != nil {
		return err
	}
	o := f.otps[id]
	if o == nil || o.ConsumedAt != nil || o.InvalidatedAt != nil {
		return domain.ErrInvalidOTP
	}
	o.ConsumedAt = &now
	return nil
}

func (f fakeOTPs) Stats(_ context.Context, uid uuid.UUID, p domain.OTPPurpose, since time.Time) (domain.OTPStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("otps.Stats"); err != nil {
		return domain.OTPStats{}, err
	}
	var st domain.OTPStats
	for _, o := range f.otps {
		if o.UserID == uid && o.Purpose == p && !o.CreatedAt.Before(since) {
			st.Count++
			if st.LastIssued == nil || o.CreatedAt.After(*st.LastIssued) {
				t := o.CreatedAt
				st.LastIssued = &t
			}
		}
	}
	return st, nil
}

func (f fakeOTPs) DeleteExpired(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("otps.DeleteExpired"); err != nil {
		return 0, err
	}
	var n int64
	for id, o := range f.otps {
		if o.ExpiresAt.Before(before) {
			delete(f.otps, id)
			n++
		}
	}
	return n, nil
}

// ===== audit =====
type fakeAudit struct{ *store }

func (f fakeAudit) Record(_ context.Context, e *domain.AuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("audit.Record"); err != nil {
		return err
	}
	f.audit = append(f.audit, *e)
	return nil
}

func (f fakeAudit) ListByUser(_ context.Context, uid uuid.UUID, after *domain.AuditKeyset, limit int) ([]domain.AuditEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("audit.ListByUser"); err != nil {
		return nil, err
	}
	var out []domain.AuditEvent
	for i := len(f.audit) - 1; i >= 0; i-- {
		e := &f.audit[i]
		if e.UserID != uid || (after != nil && !olderThan(e, after)) {
			continue
		}
		out = append(out, *e)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f fakeAudit) AnonymizeBefore(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("audit.AnonymizeBefore"); err != nil {
		return 0, err
	}
	var n int64
	for i := range f.audit {
		if f.audit[i].OccurredAt.Before(before) && f.audit[i].ClientIP != "" {
			f.audit[i].ClientIP, f.audit[i].UserAgent = "", ""
			n++
		}
	}
	return n, nil
}

func (s *store) events(t domain.AuditEventType) []domain.AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.AuditEvent
	for i := range s.audit {
		if e := s.audit[i]; e.EventType == t {
			out = append(out, e)
		}
	}
	return out
}

// ===== ports =====

// fakeHasher: hash = "h:"+pw (cepat untuk test).
type fakeHasher struct {
	compares int
	hashErr  error
}

func (h *fakeHasher) Hash(pw string) (string, error) {
	if h.hashErr != nil {
		return "", h.hashErr
	}
	return "h:" + pw, nil
}

func (h *fakeHasher) Compare(hash, pw string) error {
	h.compares++
	if hash == "" || hash != "h:"+pw {
		return errors.New("mismatch")
	}
	return nil
}

type fakeIssuer struct{ err error }

func (i fakeIssuer) Issue(uid, sid uuid.UUID, _ []domain.Role, now time.Time) (string, time.Time, error) {
	if i.err != nil {
		return "", time.Time{}, i.err
	}
	return "at:" + uid.String() + ":" + sid.String(), now.Add(15 * time.Minute), nil
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// fakeTx: menyimpan snapshot dan me-rollback bila fn error (mendekati DB nyata).
type fakeTx struct{ s *store }

func clonePtrMap[K comparable, V any](m map[K]*V) map[K]*V {
	out := make(map[K]*V, len(m))
	for k, v := range m {
		c := *v
		out[k] = &c
	}
	return out
}

func cloneSliceMap[K comparable, V any](m map[K][]V) map[K][]V {
	out := make(map[K][]V, len(m))
	for k, v := range m {
		out[k] = slices.Clone(v)
	}
	return out
}

func (s *store) snapshot() *store {
	return &store{
		users: clonePtrMap(s.users), sessions: clonePtrMap(s.sessions), otps: clonePtrMap(s.otps),
		audit: slices.Clone(s.audit), roles: cloneSliceMap(s.roles), mfa: clonePtrMap(s.mfa),
		recovery: cloneSliceMap(s.recovery), idents: clonePtrMap(s.idents), codes: clonePtrMap(s.codes),
		keys: clonePtrMap(s.keys),
	}
}

func (t fakeTx) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	t.s.mu.Lock()
	snap := t.s.snapshot()
	t.s.mu.Unlock()

	err := fn(ctx)
	if err != nil {
		t.s.mu.Lock()
		t.s.users, t.s.sessions, t.s.otps, t.s.audit = snap.users, snap.sessions, snap.otps, snap.audit
		t.s.roles, t.s.mfa, t.s.recovery, t.s.idents = snap.roles, snap.mfa, snap.recovery, snap.idents
		t.s.codes, t.s.keys = snap.codes, snap.keys
		t.s.mu.Unlock()
	}
	return err
}

// fakeCodec: kode deterministik berurutan; hash = "x"+code dipad 32 byte.
type fakeCodec struct {
	next   []string
	genErr error
}

func (c *fakeCodec) Generate() (string, error) {
	if c.genErr != nil {
		return "", c.genErr
	}
	if len(c.next) > 0 {
		v := c.next[0]
		c.next = c.next[1:]
		return v, nil
	}
	return "123456", nil
}

func (c *fakeCodec) Hash(code string) []byte { return []byte("hash:" + code) }

func (c *fakeCodec) Verify(h []byte, code string) bool { return string(h) == "hash:"+code }

type sentMail struct {
	kind, to, code string
}

type fakeNotifier struct {
	mu   sync.Mutex
	sent []sentMail
	err  error
}

func (n *fakeNotifier) add(kind, to, code string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.err != nil {
		return n.err
	}
	n.sent = append(n.sent, sentMail{kind, to, code})
	return nil
}

func (n *fakeNotifier) EmailVerification(_ context.Context, to, _, code string, _ time.Duration) error {
	return n.add("verify", to, code)
}

func (n *fakeNotifier) PasswordReset(_ context.Context, to, _, code string, _ time.Duration) error {
	return n.add("reset", to, code)
}

func (n *fakeNotifier) PasswordChanged(_ context.Context, to, _ string) error {
	return n.add("changed", to, "")
}

func (n *fakeNotifier) last() (sentMail, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.sent) == 0 {
		return sentMail{}, false
	}
	return n.sent[len(n.sent)-1], true
}

func (n *fakeNotifier) count(kind string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, m := range n.sent {
		if m.kind == kind {
			c++
		}
	}
	return c
}

type hookFunc func(ctx context.Context, id uuid.UUID, at time.Time) error

func (h hookFunc) OnUserDeleted(ctx context.Context, id uuid.UUID, at time.Time) error {
	return h(ctx, id, at)
}

// olderThan: urutan keyset (occurred_at, id) DESC.
func olderThan(e *domain.AuditEvent, k *domain.AuditKeyset) bool {
	return e.OccurredAt.Before(k.OccurredAt) ||
		(e.OccurredAt.Equal(k.OccurredAt) && e.ID.String() < k.ID.String())
}
