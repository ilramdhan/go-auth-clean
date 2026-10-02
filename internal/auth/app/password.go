package app

import (
	"context"
	"errors"
	"fmt"

	"go-auth-clean/internal/auth/domain"
)

// ResetPassword mengganti password memakai OTP dari ForgotPassword. Semua sesi
// dicabut dan lockout dibuka. User pending ikut aktif (kepemilikan email terbukti).
func (s *Service) ResetPassword(ctx context.Context, in ResetPasswordInput) error {
	email, err := domain.NormalizeEmail(in.Email)
	if err != nil {
		return domain.ErrInvalidOTP
	}
	// Policy dicek SEBELUM OTP supaya input invalid tidak menghabiskan attempts.
	if err := domain.ValidatePasswordFor(in.NewPassword, email); err != nil {
		return err
	}
	user, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		return domain.ErrInvalidOTP
	}
	if err != nil {
		return err
	}
	if !otpAllowedFor(user.Status, domain.OTPPasswordReset) {
		return domain.ErrInvalidOTP
	}

	now := s.clock.Now()
	otp, err := s.checkOTP(ctx, user, domain.OTPPasswordReset, in.Code, now, in.Meta)
	if err != nil {
		return err
	}
	hash, err := s.hasher.Hash(in.NewPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.otps.Consume(ctx, otp.ID, now); err != nil {
			return err
		}
		if err := s.users.UpdatePassword(ctx, user.ID, hash, now); err != nil {
			return err
		}
		if user.Status == domain.UserStatusPendingVerification {
			if err := s.users.MarkEmailVerified(ctx, user.ID, now); err != nil {
				return err
			}
		}
		if err := s.sessions.RevokeAllByUser(ctx, user.ID, zeroID, now, domain.RevokePasswordReset); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: user.ID, EventType: domain.EventPasswordReset}, in.Meta)
	})
	if err != nil {
		return err
	}
	s.notify(ctx, "password changed", func() error {
		return s.notifier.PasswordChanged(ctx, user.Email, user.FullName)
	})
	return nil
}

// ChangePassword mengganti password (re-auth dengan password lama) dan mencabut
// semua sesi LAIN; device yang sedang dipakai tetap login.
func (s *Service) ChangePassword(ctx context.Context, in ChangePasswordInput) error {
	user, err := s.users.FindByID(ctx, in.UserID)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	if err := s.hasher.Compare(user.PasswordHash, in.OldPassword); err != nil {
		s.registerFailure(ctx, user.ID, now, in.Meta, "change_password")
		return domain.ErrInvalidCredentials
	}
	if err := domain.ValidatePasswordFor(in.NewPassword, user.Email); err != nil {
		return err
	}
	if s.hasher.Compare(user.PasswordHash, in.NewPassword) == nil {
		return domain.ErrPasswordReused
	}

	hash, err := s.hasher.Hash(in.NewPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	keep, err := s.currentFamily(ctx, user.ID, in.SessionID)
	if err != nil {
		return err
	}

	// Ganti password dan revoke session harus atomic: jangan sampai password
	// berubah tapi session lama (yang mungkin dicuri) tetap aktif.
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.users.UpdatePassword(ctx, user.ID, hash, now); err != nil {
			return err
		}
		if err := s.sessions.RevokeAllByUser(ctx, user.ID, keep, now, domain.RevokePasswordChanged); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: user.ID, SessionID: in.SessionID, EventType: domain.EventPasswordChanged}, in.Meta)
	})
	if err != nil {
		return err
	}
	s.notify(ctx, "password changed", func() error {
		return s.notifier.PasswordChanged(ctx, user.Email, user.FullName)
	})
	return nil
}
