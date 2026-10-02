package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

var zeroID = uuid.Nil

// UpdateProfile mengubah field yang dikirim saja (saat ini hanya full_name).
func (s *Service) UpdateProfile(ctx context.Context, in UpdateProfileInput) (*domain.User, error) {
	if in.FullName == nil {
		return nil, domain.ErrNoFieldsToUpdate
	}
	name, err := domain.NormalizeFullName(*in.FullName)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	var user *domain.User
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		u, err := s.users.UpdateProfile(ctx, in.UserID, name, now)
		if err != nil {
			return err
		}
		user = u
		return s.record(ctx, domain.AuditEvent{
			UserID: in.UserID, EventType: domain.EventProfileUpdated,
			Metadata: map[string]string{"fields": "full_name"},
		}, in.Meta)
	})
	return user, err
}

// DeleteAccount melakukan soft delete + anonymize setelah re-auth password.
// Semua sesi dicabut; email bisa dipakai register ulang.
func (s *Service) DeleteAccount(ctx context.Context, in DeleteAccountInput) error {
	user, err := s.users.FindByID(ctx, in.UserID)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	if err := s.hasher.Compare(user.PasswordHash, in.Password); err != nil {
		s.registerFailure(ctx, user.ID, now, in.Meta, "delete_account")
		return domain.ErrInvalidCredentials
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.sessions.RevokeAllByUser(ctx, user.ID, zeroID, now, domain.RevokeAccountDeleted); err != nil {
			return err
		}
		if err := s.record(ctx, domain.AuditEvent{UserID: user.ID, SessionID: in.SessionID, EventType: domain.EventAccountDeleted}, in.Meta); err != nil {
			return err
		}
		if err := s.apiKeys.RevokeAllByUser(ctx, user.ID, now); err != nil {
			return err
		}
		if err := s.mfa.Delete(ctx, user.ID); err != nil {
			return err
		}
		// Identity dihapus supaya akun Google bisa dipakai register ulang.
		if err := s.identities.DeleteByUser(ctx, user.ID); err != nil {
			return err
		}
		if err := s.users.SoftDelete(ctx, user.ID, now); err != nil {
			return err
		}
		for _, h := range s.hooks {
			if err := h.OnUserDeleted(ctx, user.ID, now); err != nil {
				return fmt.Errorf("user deletion hook: %w", err)
			}
		}
		return nil
	})
}

// ListSessions mengembalikan device aktif milik user; Current menandai device ini.
func (s *Service) ListSessions(ctx context.Context, userID, currentSessionID uuid.UUID) ([]SessionInfo, error) {
	current, err := s.currentFamily(ctx, userID, currentSessionID)
	if err != nil {
		return nil, err
	}
	list, err := s.sessions.ListActiveByUser(ctx, userID, s.clock.Now())
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(list))
	for _, d := range list {
		out = append(out, SessionInfo{DeviceSession: d, Current: d.FamilyID == current})
	}
	return out, nil
}

// RevokeSession mencabut satu device milik user (IDOR: milik orang lain = 404).
func (s *Service) RevokeSession(ctx context.Context, in RevokeSessionInput) error {
	now := s.clock.Now()
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.sessions.RevokeFamilyForUser(ctx, in.UserID, in.FamilyID, now, domain.RevokeSessionRevoked); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{
			UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventSessionRevoked,
			Metadata: map[string]string{"revoked_session_id": in.FamilyID.String()},
		}, in.Meta)
	})
}

// ListSecurityEvents mengembalikan audit log milik user (keyset, terbaru dulu).
// limit dipakai apa adanya; adapter yang meminta limit+1 untuk deteksi has_more.
func (s *Service) ListSecurityEvents(ctx context.Context, userID uuid.UUID, after *domain.AuditKeyset, limit int) ([]domain.AuditEvent, error) {
	if limit <= 0 {
		return nil, errors.New("list security events: limit must be > 0")
	}
	return s.auditLog.ListByUser(ctx, userID, after, limit)
}

// Cleanup menghapus data kedaluwarsa. Aman dijalankan berulang (idempoten).
func (s *Service) Cleanup(ctx context.Context) (CleanupResult, error) {
	now := s.clock.Now()
	var res CleanupResult
	var err error
	if res.Sessions, err = s.sessions.DeleteExpired(ctx, now); err != nil {
		return res, err
	}
	// OTP disimpan 24 jam setelah expired karena dipakai hitungan kuota harian.
	if res.OTPs, err = s.otps.DeleteExpired(ctx, now.Add(-24*time.Hour)); err != nil {
		return res, err
	}
	if res.Unverified, err = s.users.PurgeUnverified(ctx, now.Add(-s.cfg.UnverifiedRetention)); err != nil {
		return res, err
	}
	if res.AuditAnonymized, err = s.auditLog.AnonymizeBefore(ctx, now.Add(-s.cfg.AuditPIIRetention)); err != nil {
		return res, err
	}
	if res.LoginCodes, err = s.loginCodes.DeleteExpired(ctx, now); err != nil {
		return res, err
	}
	return res, nil
}
