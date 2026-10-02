package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

// Otorisasi shared wallet. Aturan:
//   - non-anggota selalu mendapat *NotFound (keberadaan resource tidak bocor);
//   - viewer hanya membaca, editor mencatat transaksi/transfer, owner
//     mengelola akun & member (selain itu ErrForbidden);
//   - data tetap disimpan dengan user_id = owner (FK komposit akun), sehingga
//     setiap operasi member dijalankan dengan scope owner setelah lolos cek.

// accountAccess mengembalikan pemilik akun dan role actor terhadapnya.
func (s *Service) accountAccess(ctx context.Context, actor, accountID uuid.UUID) (uuid.UUID, domain.MemberRole, error) {
	_, err := s.accounts.Get(ctx, actor, accountID)
	if err == nil {
		return actor, domain.RoleOwner, nil
	}
	if !errors.Is(err, domain.ErrAccountNotFound) || s.members == nil {
		return uuid.Nil, "", err
	}
	m, err := s.members.Get(ctx, accountID, actor)
	if errors.Is(err, domain.ErrMemberNotFound) {
		return uuid.Nil, "", domain.ErrAccountNotFound
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	return m.OwnerID, m.Role, nil
}

// requireOwner: hanya pemilik; member mendapat ErrForbidden, non-anggota 404.
func (s *Service) requireOwner(ctx context.Context, actor, accountID uuid.UUID) error {
	_, role, err := s.accountAccess(ctx, actor, accountID)
	if err != nil {
		return err
	}
	if !role.CanManage() {
		return domain.ErrForbidden
	}
	return nil
}

// writeOwner memastikan actor boleh menulis ke semua akun (milik satu owner
// yang sama) dan mengembalikan owner tersebut. Semua akun dicek dulu
// keberadaannya sebelum role agar non-anggota tetap mendapat 404.
func (s *Service) writeOwner(ctx context.Context, actor uuid.UUID, accountIDs ...uuid.UUID) (uuid.UUID, error) {
	return s.ownerWith(ctx, actor, domain.MemberRole.CanWrite, accountIDs...)
}

// readOwner seperti writeOwner tetapi cukup role baca.
func (s *Service) readOwner(ctx context.Context, actor uuid.UUID, accountIDs ...uuid.UUID) (uuid.UUID, error) {
	return s.ownerWith(ctx, actor, domain.MemberRole.CanRead, accountIDs...)
}

func (s *Service) ownerWith(ctx context.Context, actor uuid.UUID, allowed func(domain.MemberRole) bool, accountIDs ...uuid.UUID) (uuid.UUID, error) {
	owner := uuid.Nil
	forbidden := false
	for _, id := range accountIDs {
		o, role, err := s.accountAccess(ctx, actor, id)
		if err != nil {
			return uuid.Nil, err
		}
		if owner != uuid.Nil && o != owner {
			// akun milik owner berbeda tidak bisa digabung dalam satu operasi
			return uuid.Nil, domain.ErrAccountNotFound
		}
		owner = o
		forbidden = forbidden || !allowed(role)
	}
	if forbidden {
		return uuid.Nil, domain.ErrForbidden
	}
	if owner == uuid.Nil {
		owner = actor
	}
	return owner, nil
}

// transactionFor memuat transaksi milik actor atau milik owner akun yang
// dibagikan ke actor (role apa pun). Non-anggota: ErrTransactionNotFound.
func (s *Service) transactionFor(ctx context.Context, actor, id uuid.UUID) (*domain.Transaction, error) {
	t, err := s.transactions.Get(ctx, actor, id)
	if err == nil || !errors.Is(err, domain.ErrTransactionNotFound) || s.members == nil {
		return t, err
	}
	owner, err := s.members.TransactionOwner(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	return s.transactions.Get(ctx, owner, id)
}

// transferFor: seperti transactionFor untuk transfer (anggota salah satu akun).
func (s *Service) transferFor(ctx context.Context, actor, id uuid.UUID) (*domain.Transfer, error) {
	t, err := s.transfers.Get(ctx, actor, id)
	if err == nil || !errors.Is(err, domain.ErrTransferNotFound) || s.members == nil {
		return t, err
	}
	owner, err := s.members.TransferOwner(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	return s.transfers.Get(ctx, owner, id)
}

// ===== audit trail =====

// record menulis satu audit entry di tx yang sedang berjalan. before/after
// berupa snapshot ringkas (lihat audit*Snapshot); nil = tidak ada.
func (s *Service) record(ctx context.Context, owner, actor uuid.UUID, entity string, entityID uuid.UUID,
	action domain.AuditAction, before, after any,
) error {
	if s.audit == nil {
		return nil
	}
	id, err := s.newID()
	if err != nil {
		return err
	}
	e := &domain.AuditEntry{ID: id, UserID: owner, ActorID: actor, Entity: entity, EntityID: entityID,
		Action: action, CreatedAt: s.now()}
	if e.Before, err = snapshotJSON(before); err != nil {
		return err
	}
	if e.After, err = snapshotJSON(after); err != nil {
		return err
	}
	return s.audit.Create(ctx, e)
}

func snapshotJSON(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("finance.audit marshal: %w", err)
	}
	return b, nil
}

// Entity audit.
const (
	EntityAccount     = "account"
	EntityTransaction = "transaction"
	EntityTransfer    = "transfer"
	EntityMember      = "account_member"
)

func accountSnapshot(a *domain.Account) map[string]any {
	return map[string]any{"name": a.Name(), "type": a.Type(), "currency": a.Currency().Code(),
		"initial_balance": a.InitialBalance().String(), "balance": a.Balance().String(),
		"allow_negative": a.AllowNegative(), "archived": a.IsArchived(), "version": a.Version()}
}

func transactionSnapshot(t *domain.Transaction) map[string]any {
	return map[string]any{"account_id": t.AccountID(), "category_id": t.CategoryID(), "type": t.Type(),
		"amount": t.Amount().String(), "currency": t.Amount().Currency().Code(),
		"date": t.Date().Format("2006-01-02"), "note": t.Note(), "source": t.Source(), "version": t.Version()}
}

func transferSnapshot(t *domain.Transfer) map[string]any {
	return map[string]any{"from_account_id": t.FromAccountID(), "to_account_id": t.ToAccountID(),
		"amount": t.Amount().String(), "currency": t.Amount().Currency().Code(),
		"to_amount": t.ToAmount().String(), "to_currency": t.ToAmount().Currency().Code(),
		"fee": t.Fee().String(), "date": t.Date().Format("2006-01-02"), "note": t.Note(), "version": t.Version()}
}

func memberSnapshot(m *domain.AccountMember) map[string]any {
	return map[string]any{"account_id": m.AccountID, "member_id": m.MemberID, "role": m.Role}
}
