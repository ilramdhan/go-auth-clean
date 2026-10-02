package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

// AddMemberInput: isi MemberID atau Email (dicari lewat UserDirectory).
type AddMemberInput struct {
	OwnerID   uuid.UUID
	AccountID uuid.UUID
	MemberID  *uuid.UUID
	Email     string
	Role      string
}

// errMembersDisabled: shared wallet tidak dikonfigurasi.
var errMembersDisabled = &domain.ValidationError{Field: "account_id", Reason: "shared wallets are not enabled"}

// AddMember membagikan akun ke user lain. Hanya owner; member lain 403,
// non-anggota 404. Email yang tidak terdaftar -> ErrUserNotFound.
func (s *Service) AddMember(ctx context.Context, in AddMemberInput) (*domain.AccountMember, error) {
	if s.members == nil {
		return nil, errMembersDisabled
	}
	if _, err := domain.ParseMemberRole(in.Role); err != nil {
		return nil, err
	}
	var out *domain.AccountMember
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.requireOwner(ctx, in.OwnerID, in.AccountID); err != nil {
			return err
		}
		acc, err := s.accounts.GetForUpdate(ctx, in.OwnerID, in.AccountID)
		if err != nil {
			return err
		}
		memberID, err := s.resolveMember(ctx, in.MemberID, in.Email)
		if err != nil {
			return err
		}
		m, err := domain.NewAccountMember(acc, in.OwnerID, memberID, in.Role, s.now())
		if err != nil {
			return err
		}
		if err := s.members.Add(ctx, m); err != nil {
			return err
		}
		out = m
		return s.record(ctx, in.OwnerID, in.OwnerID, EntityMember, acc.ID(), domain.AuditCreate, nil, memberSnapshot(m))
	})
	return out, err
}

func (s *Service) resolveMember(ctx context.Context, id *uuid.UUID, email string) (uuid.UUID, error) {
	email = strings.TrimSpace(email)
	switch {
	case id != nil && email != "":
		return uuid.Nil, &domain.ValidationError{Field: "member_id", Reason: "use either member_id or email"}
	case id != nil:
		return *id, nil
	case email == "":
		return uuid.Nil, &domain.ValidationError{Field: "member_id", Reason: "member_id or email is required"}
	case s.users == nil:
		return uuid.Nil, &domain.ValidationError{Field: "email", Reason: "invite by email is not available"}
	}
	uid, err := s.users.LookupByEmail(ctx, email)
	if err != nil {
		return uuid.Nil, err
	}
	if uid == uuid.Nil {
		return uuid.Nil, domain.ErrUserNotFound
	}
	return uid, nil
}

// ListMembers: semua anggota akun (owner & member) boleh melihat daftar.
func (s *Service) ListMembers(ctx context.Context, actor, accountID uuid.UUID) ([]*domain.AccountMember, error) {
	if s.members == nil {
		return nil, errMembersDisabled
	}
	owner, _, err := s.accountAccess(ctx, actor, accountID)
	if err != nil {
		return nil, err
	}
	return s.members.List(ctx, owner, accountID)
}

// UpdateMemberRole: hanya owner.
func (s *Service) UpdateMemberRole(ctx context.Context, owner, accountID, memberID uuid.UUID, role string) (*domain.AccountMember, error) {
	if s.members == nil {
		return nil, errMembersDisabled
	}
	r, err := domain.ParseMemberRole(role)
	if err != nil {
		return nil, err
	}
	var out *domain.AccountMember
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.requireOwner(ctx, owner, accountID); err != nil {
			return err
		}
		m, err := s.members.Get(ctx, accountID, memberID)
		if err != nil {
			return err
		}
		before := memberSnapshot(m)
		if m.Role == r {
			out = m
			return nil
		}
		m.Role, m.UpdatedAt = r, s.now()
		if err := s.members.UpdateRole(ctx, m); err != nil {
			return err
		}
		out = m
		return s.record(ctx, owner, owner, EntityMember, accountID, domain.AuditUpdate, before, memberSnapshot(m))
	})
	return out, err
}

// RemoveMember: owner mencabut akses siapa pun; member boleh keluar sendiri.
func (s *Service) RemoveMember(ctx context.Context, actor, accountID, memberID uuid.UUID) error {
	if s.members == nil {
		return errMembersDisabled
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		owner, role, err := s.accountAccess(ctx, actor, accountID)
		if err != nil {
			return err
		}
		if !role.CanManage() && actor != memberID {
			return domain.ErrForbidden
		}
		m, err := s.members.Get(ctx, accountID, memberID)
		if err != nil {
			return err
		}
		if err := s.members.Remove(ctx, accountID, memberID); err != nil {
			return err
		}
		return s.record(ctx, owner, actor, EntityMember, accountID, domain.AuditDelete, memberSnapshot(m), nil)
	})
}

// ListSharedAccounts: akun milik user lain yang dibagikan ke userID.
func (s *Service) ListSharedAccounts(ctx context.Context, userID uuid.UUID) ([]domain.SharedAccount, error) {
	if s.members == nil {
		return []domain.SharedAccount{}, nil
	}
	return s.members.ListShared(ctx, userID)
}
