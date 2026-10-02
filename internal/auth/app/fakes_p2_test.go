package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

type recCode struct {
	hash []byte
	used bool
}

// ===== users (admin) =====

func (f fakeUsers) List(_ context.Context, flt domain.UserFilter) ([]domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.List"); err != nil {
		return nil, err
	}
	var out []domain.User
	for _, u := range f.users {
		if u.Status == domain.UserStatusDeleted || (flt.Status != "" && u.Status != flt.Status) {
			continue
		}
		q := strings.ToLower(flt.Query)
		if q != "" && !strings.Contains(u.Email, q) && !strings.Contains(strings.ToLower(u.FullName), q) {
			continue
		}
		if a := flt.After; a != nil && !afterKeyset(u, a) {
			continue
		}
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID.String() > out[j].ID.String()
	})
	if len(out) > flt.Limit {
		out = out[:flt.Limit]
	}
	return out, nil
}

func (f fakeUsers) SetStatus(_ context.Context, id uuid.UUID, from, to domain.UserStatus, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("users.SetStatus"); err != nil {
		return err
	}
	u, err := f.get(id)
	if err != nil {
		return err
	}
	if u.Status != from {
		return domain.ErrInvalidTransition
	}
	u.Status, u.UpdatedAt = to, now
	return nil
}

// ===== roles =====
type fakeRoles struct{ *store }

func (f fakeRoles) ListByUser(_ context.Context, uid uuid.UUID) ([]domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("roles.ListByUser"); err != nil {
		return nil, err
	}
	return append([]domain.Role{domain.RoleUser}, f.roles[uid]...), nil
}

func (f fakeRoles) Grant(_ context.Context, uid uuid.UUID, r domain.Role, _ uuid.UUID, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("roles.Grant"); err != nil {
		return err
	}
	if !slices.Contains(f.roles[uid], r) {
		f.roles[uid] = append(f.roles[uid], r)
	}
	return nil
}

func (f fakeRoles) Revoke(_ context.Context, uid uuid.UUID, r domain.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("roles.Revoke"); err != nil {
		return err
	}
	f.roles[uid] = slices.DeleteFunc(f.roles[uid], func(x domain.Role) bool { return x == r })
	return nil
}

func (f fakeRoles) CountUsers(_ context.Context, r domain.Role) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, rs := range f.roles {
		if slices.Contains(rs, r) {
			n++
		}
	}
	return n, nil
}

// ===== mfa =====
type fakeMFA struct{ *store }

func (f fakeMFA) Get(_ context.Context, uid uuid.UUID) (*domain.UserMFA, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("mfa.Get"); err != nil {
		return nil, err
	}
	m, ok := f.mfa[uid]
	if !ok {
		return nil, domain.ErrMFANotFound
	}
	c := *m
	return &c, nil
}

func (f fakeMFA) UpsertPending(_ context.Context, m *domain.UserMFA) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("mfa.UpsertPending"); err != nil {
		return err
	}
	if x, ok := f.mfa[m.UserID]; ok && x.IsEnabled() {
		return domain.ErrMFAAlreadyEnabled
	}
	c := *m
	f.mfa[m.UserID] = &c
	return nil
}

func (f fakeMFA) Enable(_ context.Context, uid uuid.UUID, step int64, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("mfa.Enable"); err != nil {
		return err
	}
	m, ok := f.mfa[uid]
	if !ok || m.Status != domain.MFAPending {
		return domain.ErrMFASetupRequired
	}
	m.Status, m.EnabledAt, m.LastUsedStep = domain.MFAEnabled, &now, step
	return nil
}

func (f fakeMFA) AdvanceStep(_ context.Context, uid uuid.UUID, step int64, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("mfa.AdvanceStep"); err != nil {
		return err
	}
	m, ok := f.mfa[uid]
	if !ok || m.LastUsedStep >= step {
		return domain.ErrMFACodeReplayed
	}
	m.LastUsedStep = step
	return nil
}

func (f fakeMFA) Delete(_ context.Context, uid uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("mfa.Delete"); err != nil {
		return err
	}
	delete(f.mfa, uid)
	delete(f.recovery, uid)
	return nil
}

func (f fakeMFA) ReplaceRecoveryCodes(_ context.Context, uid uuid.UUID, hashes [][]byte, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("mfa.ReplaceRecoveryCodes"); err != nil {
		return err
	}
	rc := make([]recCode, 0, len(hashes))
	for _, h := range hashes {
		rc = append(rc, recCode{hash: h})
	}
	f.recovery[uid] = rc
	return nil
}

func (f fakeMFA) UseRecoveryCode(_ context.Context, uid uuid.UUID, h []byte, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("mfa.UseRecoveryCode"); err != nil {
		return err
	}
	for i, c := range f.recovery[uid] {
		if !c.used && bytes.Equal(c.hash, h) {
			f.recovery[uid][i].used = true
			return nil
		}
	}
	return domain.ErrInvalidMFACode
}

func (f fakeMFA) CountRecoveryCodes(_ context.Context, uid uuid.UUID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("mfa.CountRecoveryCodes"); err != nil {
		return 0, err
	}
	n := 0
	for _, c := range f.recovery[uid] {
		if !c.used {
			n++
		}
	}
	return n, nil
}

// ===== identities & login codes =====
type fakeIdentities struct{ *store }

func (f fakeIdentities) FindBySubject(_ context.Context, p, sub string) (*domain.ExternalIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("identities.FindBySubject"); err != nil {
		return nil, err
	}
	for _, i := range f.idents {
		if i.Provider == p && i.Subject == sub {
			c := *i
			return &c, nil
		}
	}
	return nil, domain.ErrIdentityNotFound
}

func (f fakeIdentities) Create(_ context.Context, i *domain.ExternalIdentity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("identities.Create"); err != nil {
		return err
	}
	for _, x := range f.idents {
		if (x.Provider == i.Provider && x.Subject == i.Subject) || (x.UserID == i.UserID && x.Provider == i.Provider) {
			return domain.ErrIdentityTaken
		}
	}
	c := *i
	f.idents[i.ID] = &c
	return nil
}

func (f fakeIdentities) ListByUser(_ context.Context, uid uuid.UUID) ([]domain.ExternalIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("identities.ListByUser"); err != nil {
		return nil, err
	}
	var out []domain.ExternalIdentity
	for _, i := range f.idents {
		if i.UserID == uid {
			out = append(out, *i)
		}
	}
	return out, nil
}

func (f fakeIdentities) DeleteByUser(_ context.Context, uid uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("identities.DeleteByUser"); err != nil {
		return err
	}
	for k, i := range f.idents {
		if i.UserID == uid {
			delete(f.idents, k)
		}
	}
	return nil
}

type fakeLoginCodes struct{ *store }

func (f fakeLoginCodes) Create(_ context.Context, c *domain.LoginCode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("codes.Create"); err != nil {
		return err
	}
	x := *c
	f.codes[string(c.CodeHash)] = &x
	return nil
}

func (f fakeLoginCodes) Consume(_ context.Context, h []byte, now time.Time) (*domain.LoginCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("codes.Consume"); err != nil {
		return nil, err
	}
	c, ok := f.codes[string(h)]
	if !ok {
		return nil, domain.ErrLoginCodeInvalid
	}
	delete(f.codes, string(h))
	if !now.Before(c.ExpiresAt) {
		return nil, domain.ErrLoginCodeInvalid
	}
	return c, nil
}

func (f fakeLoginCodes) DeleteExpired(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("codes.DeleteExpired"); err != nil {
		return 0, err
	}
	var n int64
	for k, c := range f.codes {
		if c.ExpiresAt.Before(before) {
			delete(f.codes, k)
			n++
		}
	}
	return n, nil
}

// ===== api keys =====
type fakeAPIKeys struct{ *store }

func (f fakeAPIKeys) Create(_ context.Context, k *domain.APIKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("keys.Create"); err != nil {
		return err
	}
	c := *k
	f.keys[k.ID] = &c
	return nil
}

func (f fakeAPIKeys) FindByPrefix(_ context.Context, p string) (*domain.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("keys.FindByPrefix"); err != nil {
		return nil, err
	}
	for _, k := range f.keys {
		if k.Prefix == p {
			c := *k
			return &c, nil
		}
	}
	return nil, domain.ErrAPIKeyNotFound
}

func (f fakeAPIKeys) ListByUser(_ context.Context, uid uuid.UUID) ([]domain.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("keys.ListByUser"); err != nil {
		return nil, err
	}
	var out []domain.APIKey
	for _, k := range f.keys {
		if k.UserID == uid {
			out = append(out, *k)
		}
	}
	return out, nil
}

func (f fakeAPIKeys) CountActiveByUser(_ context.Context, uid uuid.UUID, now time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("keys.CountActiveByUser"); err != nil {
		return 0, err
	}
	n := 0
	for _, k := range f.keys {
		if k.UserID == uid && k.IsActive(now) {
			n++
		}
	}
	return n, nil
}

func (f fakeAPIKeys) Revoke(_ context.Context, uid, id uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("keys.Revoke"); err != nil {
		return err
	}
	k, ok := f.keys[id]
	if !ok || k.UserID != uid || k.RevokedAt != nil {
		return domain.ErrAPIKeyNotFound
	}
	k.RevokedAt = &now
	return nil
}

func (f fakeAPIKeys) RevokeAllByUser(_ context.Context, uid uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("keys.RevokeAllByUser"); err != nil {
		return err
	}
	for _, k := range f.keys {
		if k.UserID == uid && k.RevokedAt == nil {
			t := now
			k.RevokedAt = &t
		}
	}
	return nil
}

func (f fakeAPIKeys) TouchLastUsed(_ context.Context, id uuid.UUID, now, older time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err("keys.TouchLastUsed"); err != nil {
		return err
	}
	if k, ok := f.keys[id]; ok && (k.LastUsedAt == nil || k.LastUsedAt.Before(older)) {
		t := now
		k.LastUsedAt = &t
	}
	return nil
}

// ===== ports P2 =====

// fakeMFATokens: token = "mfa:"+uid.
type fakeMFATokens struct{ err error }

func (f fakeMFATokens) IssueMFA(uid uuid.UUID, now time.Time) (string, time.Time, error) {
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	return "mfa:" + uid.String(), now.Add(5 * time.Minute), nil
}

func (fakeMFATokens) VerifyMFA(tok string) (uuid.UUID, error) {
	s, ok := strings.CutPrefix(tok, "mfa:")
	if !ok {
		return uuid.Nil, errors.New("bad token")
	}
	return uuid.Parse(s)
}

// fakeTOTP: kode valid = 6 digit terakhir dari step (now/30), toleransi ±1.
type fakeTOTP struct{ genErr error }

func (f fakeTOTP) GenerateSecret() (string, error) {
	if f.genErr != nil {
		return "", f.genErr
	}
	return "JBSWY3DPEHPK3PXP", nil
}

func (fakeTOTP) URI(secret, account string) string {
	return "otpauth://totp/test:" + account + "?secret=" + secret
}

func (fakeTOTP) Validate(_, code string, now time.Time) (int64, bool) {
	cur := now.Unix() / 30
	for _, st := range []int64{cur - 1, cur, cur + 1} {
		if totpCode(st) == code {
			return st, true
		}
	}
	return 0, false
}

func totpCode(step int64) string {
	return fmt.Sprintf("%06d", step%1000000)
}

// fakeSealer: "enc:"+aadhex+":"+pt; decrypt gagal bila aad beda.
type fakeSealer struct{ err error }

func (f fakeSealer) EncryptString(pt string, aad []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "enc:" + string(aad) + ":" + pt, nil
}

func (f fakeSealer) DecryptString(ct string, aad []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	rest, ok := strings.CutPrefix(ct, "enc:"+string(aad)+":")
	if !ok {
		return "", errors.New("aad mismatch")
	}
	return rest, nil
}

// fakeOAuth mengembalikan profile tetap.
type fakeOAuth struct {
	prof domain.ExternalProfile
	err  error
}

func (fakeOAuth) AuthCodeURL(state, challenge, nonce string) string {
	return "https://idp.test/auth?state=" + state + "&code_challenge=" + challenge + "&nonce=" + nonce
}

func (f *fakeOAuth) Exchange(_ context.Context, code, verifier, nonce string) (domain.ExternalProfile, error) {
	if f.err != nil {
		return domain.ExternalProfile{}, f.err
	}
	if code == "" || verifier == "" || nonce == "" {
		return domain.ExternalProfile{}, errors.New("missing params")
	}
	return f.prof, nil
}

// afterKeyset: u berada setelah cursor a pada urutan (created_at DESC, id DESC).
func afterKeyset(u *domain.User, a *domain.UserKeyset) bool {
	if u.CreatedAt.Equal(a.CreatedAt) {
		return u.ID.String() < a.ID.String()
	}
	return u.CreatedAt.Before(a.CreatedAt)
}
