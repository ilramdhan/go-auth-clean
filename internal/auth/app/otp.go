package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

func (s *Service) otpTTL(p domain.OTPPurpose) time.Duration {
	if p == domain.OTPPasswordReset {
		return s.cfg.PasswordResetTTL
	}
	return s.cfg.OTPTTL
}

// issueOTP meng-invalidate OTP aktif lama lalu membuat yang baru. Wajib dipanggil
// di dalam transaksi. Mengembalikan kode plaintext untuk dikirim via email.
func (s *Service) issueOTP(ctx context.Context, user *domain.User, purpose domain.OTPPurpose, now time.Time) (string, error) {
	code, err := s.otp.Generate()
	if err != nil {
		return "", fmt.Errorf("generate otp: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate otp id: %w", err)
	}
	if err := s.otps.InvalidateActive(ctx, user.ID, purpose, now); err != nil {
		return "", err
	}
	err = s.otps.Create(ctx, &domain.OTP{
		ID:          id,
		UserID:      user.ID,
		Purpose:     purpose,
		Target:      user.Email,
		CodeHash:    s.otp.Hash(code),
		MaxAttempts: s.cfg.OTPMaxAttempts,
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.otpTTL(purpose)),
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// canIssueOTP menerapkan cooldown dan kuota harian per (user, purpose).
func (s *Service) canIssueOTP(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose, now time.Time) (bool, error) {
	st, err := s.otps.Stats(ctx, userID, purpose, now.Add(-24*time.Hour))
	if err != nil {
		return false, err
	}
	if st.LastIssued != nil && now.Sub(*st.LastIssued) < s.cfg.ResendCooldown {
		return false, nil
	}
	return st.Count < s.cfg.OTPDailyQuota, nil
}

// checkOTP memverifikasi kode: attempts dinaikkan atomic SEBELUM compare supaya
// percobaan paralel tetap terhitung; compare constant-time di OTPCodec.
func (s *Service) checkOTP(ctx context.Context, user *domain.User, purpose domain.OTPPurpose, code string, now time.Time, meta RequestMeta) (*domain.OTP, error) {
	if !domain.ValidOTPFormat(code) {
		return nil, domain.ErrInvalidOTP
	}
	otp, err := s.otps.FindActive(ctx, user.ID, purpose)
	if errors.Is(err, domain.ErrOTPNotFound) {
		return nil, domain.ErrInvalidOTP
	}
	if err != nil {
		return nil, err
	}
	if otp.IsExpired(now) {
		return nil, domain.ErrOTPExpired
	}
	if otp.AttemptsExhausted() {
		return nil, domain.ErrOTPTooManyAttempts
	}
	if _, err := s.otps.IncrementAttempts(ctx, otp.ID); err != nil {
		return nil, err
	}
	if !s.otp.Verify(otp.CodeHash, code) {
		s.recordBestEffort(ctx, domain.AuditEvent{
			UserID: user.ID, EventType: domain.EventOTPFailed, Outcome: domain.OutcomeFailure,
			Metadata: map[string]string{"purpose": string(purpose)},
		}, meta)
		return nil, domain.ErrInvalidOTP
	}
	return otp, nil
}

// VerifyEmail mengaktifkan akun pending_verification dengan OTP 6 digit.
func (s *Service) VerifyEmail(ctx context.Context, in VerifyEmailInput) (VerifyEmailResult, error) {
	email, err := domain.NormalizeEmail(in.Email)
	if err != nil {
		return VerifyEmailResult{}, domain.ErrInvalidOTP
	}
	user, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		// Generic: jangan bocorkan apakah email terdaftar.
		return VerifyEmailResult{}, domain.ErrInvalidOTP
	}
	if err != nil {
		return VerifyEmailResult{}, err
	}
	switch user.Status {
	case domain.UserStatusActive:
		return VerifyEmailResult{AlreadyVerified: true}, nil
	case domain.UserStatusPendingVerification:
	default:
		return VerifyEmailResult{}, domain.ErrInvalidOTP
	}

	now := s.clock.Now()
	otp, err := s.checkOTP(ctx, user, domain.OTPEmailVerification, in.Code, now, in.Meta)
	if err != nil {
		return VerifyEmailResult{}, err
	}

	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		// Consume atomic: dua request paralel dengan kode benar, hanya satu menang.
		if err := s.otps.Consume(ctx, otp.ID, now); err != nil {
			return err
		}
		if err := s.users.MarkEmailVerified(ctx, user.ID, now); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: user.ID, EventType: domain.EventEmailVerified}, in.Meta)
	})
	if err != nil {
		return VerifyEmailResult{}, err
	}
	return VerifyEmailResult{}, nil
}

// ResendVerification selalu sukses dari sisi client (anti-enumeration). OTP
// baru hanya dibuat jika user ada, masih pending, dan lolos cooldown/kuota.
func (s *Service) ResendVerification(ctx context.Context, email string) error {
	return s.requestOTP(ctx, email, domain.OTPEmailVerification, RequestMeta{})
}

// ForgotPassword selalu sukses dari sisi client (anti-enumeration).
func (s *Service) ForgotPassword(ctx context.Context, email string, meta RequestMeta) error {
	return s.requestOTP(ctx, email, domain.OTPPasswordReset, meta)
}

func (s *Service) requestOTP(ctx context.Context, rawEmail string, purpose domain.OTPPurpose, meta RequestMeta) error {
	email, err := domain.NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	user, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !otpAllowedFor(user.Status, purpose) {
		return nil
	}

	now := s.clock.Now()
	ok, err := s.canIssueOTP(ctx, user.ID, purpose, now)
	if err != nil || !ok {
		return err
	}

	var code string
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		c, err := s.issueOTP(ctx, user, purpose, now)
		if err != nil {
			return err
		}
		code = c
		if purpose == domain.OTPPasswordReset {
			return s.record(ctx, domain.AuditEvent{UserID: user.ID, EventType: domain.EventPasswordResetRequested}, meta)
		}
		return nil
	})
	if errors.Is(err, domain.ErrOTPActiveExists) {
		// Request paralel lain sudah membuat OTP: anggap sukses.
		return nil
	}
	if err != nil {
		return err
	}

	ttl := s.otpTTL(purpose)
	s.notify(ctx, string(purpose), func() error {
		if purpose == domain.OTPPasswordReset {
			return s.notifier.PasswordReset(ctx, user.Email, user.FullName, code, ttl)
		}
		return s.notifier.EmailVerification(ctx, user.Email, user.FullName, code, ttl)
	})
	return nil
}

func otpAllowedFor(st domain.UserStatus, p domain.OTPPurpose) bool {
	switch p {
	case domain.OTPEmailVerification:
		return st == domain.UserStatusPendingVerification
	case domain.OTPPasswordReset:
		return st == domain.UserStatusActive || st == domain.UserStatusPendingVerification
	default:
		return false
	}
}
