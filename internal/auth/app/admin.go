package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

// AuthorizeAdmin memeriksa ulang role admin dari DB (bukan dari JWT) untuk
// aksi destruktif: role yang baru dicabut langsung berlaku.
func (s *Service) AuthorizeAdmin(ctx context.Context, actorID uuid.UUID) error {
	roles, err := s.roles.ListByUser(ctx, actorID)
	if err != nil {
		return err
	}
	if !domain.HasRole(roles, domain.RoleAdmin) {
		return domain.ErrForbidden
	}
	return nil
}

// ListUsers untuk admin (keyset pagination, pencarian email/nama).
func (s *Service) ListUsers(ctx context.Context, actorID uuid.UUID, f domain.UserFilter) ([]domain.User, error) {
	if err := s.AuthorizeAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	if f.Limit <= 0 {
		return nil, errors.New("list users: limit must be > 0")
	}
	return s.users.List(ctx, f)
}

// GetUser untuk admin: profil + role + status 2FA.
func (s *Service) GetUser(ctx context.Context, actorID, targetID uuid.UUID) (*domain.User, error) {
	if err := s.AuthorizeAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	return s.Me(ctx, targetID)
}

// SuspendUser menangguhkan user dan mencabut semua sesi + API key-nya.
func (s *Service) SuspendUser(ctx context.Context, in AdminActionInput) error {
	return s.changeStatus(ctx, in, domain.UserStatusActive, domain.UserStatusSuspended, domain.EventUserSuspended)
}

// ActivateUser memulihkan user yang ditangguhkan.
func (s *Service) ActivateUser(ctx context.Context, in AdminActionInput) error {
	return s.changeStatus(ctx, in, domain.UserStatusSuspended, domain.UserStatusActive, domain.EventUserActivated)
}

func (s *Service) changeStatus(ctx context.Context, in AdminActionInput, from, to domain.UserStatus, ev domain.AuditEventType) error {
	if err := s.AuthorizeAdmin(ctx, in.ActorID); err != nil {
		return err
	}
	if in.ActorID == in.TargetID {
		return domain.ErrCannotModifySelf
	}
	now := s.clock.Now()
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.users.SetStatus(ctx, in.TargetID, from, to, now); err != nil {
			return err
		}
		if to == domain.UserStatusSuspended {
			if err := s.sessions.RevokeAllByUser(ctx, in.TargetID, uuid.Nil, now, domain.RevokeUserSuspended); err != nil {
				return err
			}
			if err := s.apiKeys.RevokeAllByUser(ctx, in.TargetID, now); err != nil {
				return err
			}
		}
		return s.recordAdmin(ctx, in.ActorID, in.TargetID, ev, nil, in.Meta)
	})
}

// GrantRole / RevokeRole lewat API admin.
func (s *Service) GrantRole(ctx context.Context, in RoleChangeInput) error {
	role, err := domain.ParseRole(in.Role)
	if err != nil {
		return err
	}
	if err := s.AuthorizeAdmin(ctx, in.ActorID); err != nil {
		return err
	}
	if role == domain.RoleUser {
		return nil // implisit
	}
	if _, err := s.users.FindByID(ctx, in.TargetID); err != nil {
		return err
	}
	now := s.clock.Now()
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.roles.Grant(ctx, in.TargetID, role, in.ActorID, now); err != nil {
			return err
		}
		return s.recordAdmin(ctx, in.ActorID, in.TargetID, domain.EventRoleGranted, map[string]string{"role": string(role)}, in.Meta)
	})
}

func (s *Service) RevokeRole(ctx context.Context, in RoleChangeInput) error {
	role, err := domain.ParseRole(in.Role)
	if err != nil {
		return err
	}
	if role == domain.RoleUser {
		return domain.ErrInvalidRole
	}
	if err := s.AuthorizeAdmin(ctx, in.ActorID); err != nil {
		return err
	}
	// Admin tidak bisa mencabut role admin dirinya sendiri (mencegah lockout).
	if in.ActorID == in.TargetID {
		return domain.ErrCannotModifySelf
	}
	if _, err := s.users.FindByID(ctx, in.TargetID); err != nil {
		return err
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.roles.Revoke(ctx, in.TargetID, role); err != nil {
			return err
		}
		return s.recordAdmin(ctx, in.ActorID, in.TargetID, domain.EventRoleRevoked, map[string]string{"role": string(role)}, in.Meta)
	})
}

// UserAuditLog untuk admin melihat audit log user lain.
func (s *Service) UserAuditLog(ctx context.Context, actorID, targetID uuid.UUID, after *domain.AuditKeyset, limit int) ([]domain.AuditEvent, error) {
	if err := s.AuthorizeAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	if _, err := s.users.FindByID(ctx, targetID); err != nil {
		return nil, err
	}
	return s.ListSecurityEvents(ctx, targetID, after, limit)
}

// PromoteByEmail dipakai CLI bootstrap (`api promote --email`) untuk admin
// pertama. Tidak ada actor (sistem). Idempoten.
func (s *Service) PromoteByEmail(ctx context.Context, email string) (*domain.User, error) {
	email, err := domain.NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	user, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.roles.Grant(ctx, user.ID, domain.RoleAdmin, uuid.Nil, now); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{
			UserID: user.ID, EventType: domain.EventRoleGranted,
			Metadata: map[string]string{"role": string(domain.RoleAdmin), "actor": "cli"},
		}, RequestMeta{})
	})
	if err != nil {
		return nil, fmt.Errorf("promote: %w", err)
	}
	return user, nil
}

// recordAdmin mencatat event di audit log user target, dengan actor di metadata.
func (s *Service) recordAdmin(ctx context.Context, actor, target uuid.UUID, ev domain.AuditEventType, md map[string]string, meta RequestMeta) error {
	m := map[string]string{"actor_id": actor.String()}
	for k, v := range md {
		m[k] = v
	}
	return s.record(ctx, domain.AuditEvent{UserID: target, EventType: ev, Metadata: m}, meta)
}
