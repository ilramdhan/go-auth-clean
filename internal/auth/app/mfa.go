package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

// SetupMFA membuat secret TOTP baru berstatus pending. Secret dienkripsi
// dengan AAD = user id sehingga ciphertext tidak bisa dipindah ke user lain.
func (s *Service) SetupMFA(ctx context.Context, userID uuid.UUID) (MFASetup, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return MFASetup{}, err
	}
	secret, err := s.totp.GenerateSecret()
	if err != nil {
		return MFASetup{}, fmt.Errorf("generate totp secret: %w", err)
	}
	enc, err := s.sealer.EncryptString(secret, userID[:])
	if err != nil {
		return MFASetup{}, fmt.Errorf("encrypt totp secret: %w", err)
	}
	now := s.clock.Now()
	err = s.mfa.UpsertPending(ctx, &domain.UserMFA{
		UserID: userID, SecretEncrypted: enc, Status: domain.MFAPending, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return MFASetup{}, err
	}
	return MFASetup{Secret: secret, OTPAuthURI: s.totp.URI(secret, user.Email)}, nil
}

// EnableMFA memverifikasi kode pertama dari authenticator, mengaktifkan 2FA,
// membuat recovery code, dan mencabut sesi lain (keamanan akun berubah).
func (s *Service) EnableMFA(ctx context.Context, in MFACodeInput) ([]string, error) {
	m, err := s.mfa.Get(ctx, in.UserID)
	if errors.Is(err, domain.ErrMFANotFound) {
		return nil, domain.ErrMFASetupRequired
	}
	if err != nil {
		return nil, err
	}
	if m.IsEnabled() {
		return nil, domain.ErrMFAAlreadyEnabled
	}
	now := s.clock.Now()
	step, err := s.checkTOTP(m, in.Code, now)
	if err != nil {
		s.recordBestEffort(ctx, domain.AuditEvent{
			UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventMFAFailed, Outcome: domain.OutcomeFailure,
			Metadata: map[string]string{"stage": "enable"},
		}, in.Meta)
		return nil, err
	}
	codes, hashes, err := s.newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.mfa.Enable(ctx, in.UserID, step, now); err != nil {
			return err
		}
		if err := s.mfa.ReplaceRecoveryCodes(ctx, in.UserID, hashes, now); err != nil {
			return err
		}
		if err := s.revokeOtherSessions(ctx, in.UserID, in.SessionID, now); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventMFAEnabled}, in.Meta)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableMFA butuh password + kode TOTP/recovery (dua faktor) agar access
// token yang dicuri saja tidak cukup untuk mematikan 2FA.
func (s *Service) DisableMFA(ctx context.Context, in DisableMFAInput) error {
	user, err := s.users.FindByID(ctx, in.UserID)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	if err := s.hasher.Compare(user.PasswordHash, in.Password); err != nil {
		s.registerFailure(ctx, user.ID, now, in.Meta, "disable_mfa")
		return domain.ErrInvalidCredentials
	}
	m, err := s.enabledMFA(ctx, in.UserID)
	if err != nil {
		return err
	}
	if err := s.verifySecondFactor(ctx, m, in.Code, in.SessionID, in.Meta, "disable"); err != nil {
		return err
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.mfa.Delete(ctx, in.UserID); err != nil {
			return err
		}
		if err := s.revokeOtherSessions(ctx, in.UserID, in.SessionID, now); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventMFADisabled}, in.Meta)
	})
}

// RegenerateRecoveryCodes mengganti semua recovery code (butuh kode TOTP).
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, in MFACodeInput) ([]string, error) {
	m, err := s.enabledMFA(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	step, err := s.checkTOTP(m, in.Code, now)
	if err != nil {
		s.recordBestEffort(ctx, domain.AuditEvent{
			UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventMFAFailed, Outcome: domain.OutcomeFailure,
			Metadata: map[string]string{"stage": "regenerate"},
		}, in.Meta)
		return nil, err
	}
	codes, hashes, err := s.newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.mfa.AdvanceStep(ctx, in.UserID, step, now); err != nil {
			return err
		}
		if err := s.mfa.ReplaceRecoveryCodes(ctx, in.UserID, hashes, now); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventMFARecoveryRegenerated}, in.Meta)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// GetMFAStatus untuk halaman pengaturan keamanan.
func (s *Service) GetMFAStatus(ctx context.Context, userID uuid.UUID) (MFAStatus, error) {
	m, err := s.mfa.Get(ctx, userID)
	if errors.Is(err, domain.ErrMFANotFound) {
		return MFAStatus{}, nil
	}
	if err != nil {
		return MFAStatus{}, err
	}
	st := MFAStatus{Enabled: m.IsEnabled(), Pending: m.Status == domain.MFAPending, EnabledAt: m.EnabledAt}
	if st.Enabled {
		if st.RecoveryCodesRemaining, err = s.mfa.CountRecoveryCodes(ctx, userID); err != nil {
			return MFAStatus{}, err
		}
	}
	return st, nil
}

// LoginMFA adalah langkah kedua login: tukar mfa_token + kode dengan token.
// Kode boleh TOTP 6 digit atau recovery code. Kegagalan dihitung ke lockout
// akun yang sama dengan password (anti brute force 10^6 kombinasi).
func (s *Service) LoginMFA(ctx context.Context, in LoginMFAInput) (LoginResult, error) {
	userID, err := s.mfaTokens.VerifyMFA(in.MFAToken)
	if err != nil {
		return LoginResult{}, domain.ErrMFATokenInvalid
	}
	user, err := s.users.FindByID(ctx, userID)
	if errors.Is(err, domain.ErrUserNotFound) {
		return LoginResult{}, domain.ErrMFATokenInvalid
	}
	if err != nil {
		return LoginResult{}, err
	}
	now := s.clock.Now()
	if user.IsLocked(now) {
		return LoginResult{}, domain.ErrAccountLocked
	}
	if err := user.LoginError(); err != nil {
		return LoginResult{}, err
	}
	m, err := s.enabledMFA(ctx, userID)
	if errors.Is(err, domain.ErrMFANotEnabled) {
		// 2FA dimatikan setelah token dibuat: token tidak berlaku lagi.
		return LoginResult{}, domain.ErrMFATokenInvalid
	}
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.verifySecondFactor(ctx, m, in.Code, uuid.Nil, in.Meta, "login"); err != nil {
		if errors.Is(err, domain.ErrInvalidMFACode) || errors.Is(err, domain.ErrMFACodeReplayed) {
			s.registerFailure(ctx, user.ID, now, in.Meta, "bad_mfa_code")
		}
		return LoginResult{}, err
	}
	return s.completeLogin(ctx, user, in.Meta, now, map[string]string{"mfa": "true"})
}

func (s *Service) enabledMFA(ctx context.Context, userID uuid.UUID) (*domain.UserMFA, error) {
	m, err := s.mfa.Get(ctx, userID)
	if errors.Is(err, domain.ErrMFANotFound) {
		return nil, domain.ErrMFANotEnabled
	}
	if err != nil {
		return nil, err
	}
	if !m.IsEnabled() {
		return nil, domain.ErrMFANotEnabled
	}
	return m, nil
}

// checkTOTP mendekripsi secret dan memvalidasi kode, termasuk anti-replay
// (step harus > step terakhir yang dipakai).
func (s *Service) checkTOTP(m *domain.UserMFA, code string, now time.Time) (int64, error) {
	code = strings.TrimSpace(code)
	if !domain.LooksLikeTOTP(code) {
		return 0, domain.ErrInvalidMFACode
	}
	secret, err := s.sealer.DecryptString(m.SecretEncrypted, m.UserID[:])
	if err != nil {
		return 0, fmt.Errorf("decrypt totp secret: %w", err)
	}
	step, ok := s.totp.Validate(secret, code, now)
	if !ok {
		return 0, domain.ErrInvalidMFACode
	}
	if step <= m.LastUsedStep {
		return 0, domain.ErrMFACodeReplayed
	}
	return step, nil
}

// verifySecondFactor menerima TOTP (6 digit) atau recovery code.
func (s *Service) verifySecondFactor(ctx context.Context, m *domain.UserMFA, code string, sessionID uuid.UUID, meta RequestMeta, stage string) error {
	now := s.clock.Now()
	fail := func(err error) error {
		s.recordBestEffort(ctx, domain.AuditEvent{
			UserID: m.UserID, SessionID: sessionID, EventType: domain.EventMFAFailed, Outcome: domain.OutcomeFailure,
			Metadata: map[string]string{"stage": stage},
		}, meta)
		return err
	}
	if domain.LooksLikeTOTP(strings.TrimSpace(code)) {
		step, err := s.checkTOTP(m, code, now)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidMFACode) || errors.Is(err, domain.ErrMFACodeReplayed) {
				return fail(err)
			}
			return err
		}
		// AdvanceStep atomic: dua request paralel dengan kode sama, satu gagal.
		if err := s.mfa.AdvanceStep(ctx, m.UserID, step, now); err != nil {
			if errors.Is(err, domain.ErrMFACodeReplayed) {
				return fail(err)
			}
			return err
		}
		return nil
	}
	rc := domain.NormalizeRecoveryCode(code)
	if len(rc) != domain.RecoveryCodeLength {
		return fail(domain.ErrInvalidMFACode)
	}
	if err := s.mfa.UseRecoveryCode(ctx, m.UserID, s.otp.Hash(rc), now); err != nil {
		if errors.Is(err, domain.ErrInvalidMFACode) {
			return fail(err)
		}
		return err
	}
	s.recordBestEffort(ctx, domain.AuditEvent{
		UserID: m.UserID, SessionID: sessionID, EventType: domain.EventMFARecoveryCodeUsed,
		Metadata: map[string]string{"stage": stage},
	}, meta)
	return nil
}

// recoveryAlphabet tanpa karakter ambigu (0/o, 1/l/i).
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// newRecoveryCodes membuat 10 code acak (format xxxxx-xxxxx) + hash HMAC-nya.
func (s *Service) newRecoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, domain.RecoveryCodeCount)
	hashes := make([][]byte, domain.RecoveryCodeCount)
	for i := range codes {
		raw, err := randomString(recoveryAlphabet, domain.RecoveryCodeLength)
		if err != nil {
			return nil, nil, fmt.Errorf("generate recovery code: %w", err)
		}
		half := domain.RecoveryCodeLength / 2
		codes[i] = raw[:half] + "-" + raw[half:]
		hashes[i] = s.otp.Hash(raw)
	}
	return codes, hashes, nil
}

// randomString memakai rejection sampling agar distribusi karakter seragam.
func randomString(alphabet string, n int) (string, error) {
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) < limit && len(out) < n {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	return string(out), nil
}

// revokeOtherSessions mencabut semua device kecuali yang sedang dipakai.
func (s *Service) revokeOtherSessions(ctx context.Context, userID, sessionID uuid.UUID, now time.Time) error {
	fam, err := s.currentFamily(ctx, userID, sessionID)
	if err != nil {
		return err
	}
	return s.sessions.RevokeAllByUser(ctx, userID, fam, now, domain.RevokeMFAChanged)
}
