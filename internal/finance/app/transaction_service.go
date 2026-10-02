package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

// CreateTransactionInput: Amount string major unit; Date tanggal lokal user.
type CreateTransactionInput struct {
	UserID     uuid.UUID
	AccountID  uuid.UUID
	CategoryID uuid.UUID
	Type       string
	Amount     string
	Date       time.Time
	Note       string
	TagIDs     []uuid.UUID // opsional, maks 10
}

// UpdateTransactionInput: field nil = tetap. ExpectedVersion dari If-Match.
type UpdateTransactionInput struct {
	UserID          uuid.UUID
	ID              uuid.UUID
	ExpectedVersion *int
	AccountID       *uuid.UUID
	CategoryID      *uuid.UUID
	Type            *string
	Amount          *string
	Date            *time.Time
	Note            *string
}

// ListTransactionsInput adalah filter list (tanggal inklusif). Bila
// AccountIDs berisi akun bersama (milik satu owner lain), list memakai scope owner.
type ListTransactionsInput struct {
	UserID          uuid.UUID
	From, To        *time.Time
	Type            string
	AccountIDs      []uuid.UUID
	CategoryIDs     []uuid.UUID
	IncludeChildren bool
	MinAmount       string
	MaxAmount       string
	Currency        string
	Query           string
	Tag             string // nama tag (case-insensitive)
	Limit           int
	After           *domain.PageKey
}

// CreateTransaction mencatat income/expense dan menyesuaikan saldo akun dalam
// satu DB transaction (akun dikunci FOR UPDATE untuk mencegah lost update).
func (s *Service) CreateTransaction(ctx context.Context, in CreateTransactionInput) (*domain.Transaction, error) {
	typ, err := domain.ParseTxType(in.Type)
	if err != nil {
		return nil, err
	}
	actor := in.UserID
	var out *domain.Transaction
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		// Akun bersama: transaksi disimpan atas nama owner (kategori, tag,
		// timezone & budget milik owner); actor dicatat di audit trail.
		owner, err := s.writeOwner(ctx, actor, in.AccountID)
		if err != nil {
			return err
		}
		in.UserID = owner
		loc, err := s.userLocation(ctx, owner)
		if err != nil {
			return err
		}
		cat, err := s.categories.Get(ctx, in.UserID, in.CategoryID)
		if err != nil {
			return err
		}
		accs, err := s.lockAccounts(ctx, in.UserID, in.AccountID)
		if err != nil {
			return err
		}
		acc := accs[in.AccountID]
		amount, err := money.Parse(in.Amount, acc.Currency())
		if err != nil {
			return err
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		now := s.now()
		t, err := domain.NewTransaction(domain.NewTransactionParams{
			ID: id, UserID: in.UserID, Account: acc, Category: cat, Type: typ,
			Amount: amount, Date: in.Date, Note: in.Note, Now: now, Location: loc,
		})
		if err != nil {
			return err
		}
		if err := acc.Apply(t); err != nil {
			return err
		}
		if err := s.transactions.Create(ctx, t); err != nil {
			return err
		}
		if err := s.saveAccounts(ctx, []*domain.Account{acc}, now); err != nil {
			return err
		}
		if len(in.TagIDs) > 0 && owner != actor {
			return domain.ErrTagNotFound // tag milik owner tidak boleh ditebak oleh member
		}
		if len(in.TagIDs) > 0 {
			if _, err := s.attachTags(ctx, in.UserID, t.ID(), in.TagIDs); err != nil {
				return err
			}
		}
		if err := s.checkBudgetAlerts(ctx, t, cat, loc); err != nil {
			return err
		}
		out = t
		return s.record(ctx, owner, actor, EntityTransaction, t.ID(), domain.AuditCreate, nil, transactionSnapshot(t))
	})
	return out, err
}

// GetTransaction: pemilik atau member akun transaksi (role apa pun).
func (s *Service) GetTransaction(ctx context.Context, userID, id uuid.UUID) (*domain.Transaction, error) {
	return s.transactionFor(ctx, userID, id)
}

// UpdateTransaction mengedit transaksi: efek lama di-revert dari akun lama,
// efek baru di-apply ke akun baru (bisa akun berbeda), semua dalam satu tx.
func (s *Service) UpdateTransaction(ctx context.Context, in UpdateTransactionInput) (*domain.Transaction, error) {
	actor := in.UserID
	var out *domain.Transaction
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		old, err := s.transactionFor(ctx, actor, in.ID)
		if err != nil {
			return err
		}
		ids := []uuid.UUID{old.AccountID()}
		if in.AccountID != nil {
			ids = append(ids, *in.AccountID)
		}
		owner, err := s.writeOwner(ctx, actor, ids...)
		if err != nil {
			return err
		}
		in.UserID = owner
		loc, err := s.userLocation(ctx, owner)
		if err != nil {
			return err
		}
		if err := old.EnsureEditable(); err != nil {
			return err
		}
		if err := checkVersion(in.ExpectedVersion, old.Version()); err != nil {
			return err
		}
		accountID, categoryID := old.AccountID(), old.CategoryID()
		if in.AccountID != nil {
			accountID = *in.AccountID
		}
		if in.CategoryID != nil {
			categoryID = *in.CategoryID
		}
		typ := old.Type()
		if in.Type != nil {
			if typ, err = domain.ParseTxType(*in.Type); err != nil {
				return err
			}
		}
		cat, err := s.categories.Get(ctx, in.UserID, categoryID)
		if err != nil {
			return err
		}
		accs, err := s.lockAccounts(ctx, in.UserID, old.AccountID(), accountID)
		if err != nil {
			return err
		}
		newAcc := accs[accountID]
		amount := old.Amount()
		if in.Amount != nil {
			if amount, err = money.Parse(*in.Amount, newAcc.Currency()); err != nil {
				return err
			}
		}
		date, note := old.Date(), old.Note()
		if in.Date != nil {
			date = *in.Date
		}
		if in.Note != nil {
			note = *in.Note
		}
		now := s.now()
		updated, err := old.Change(domain.ChangeTransactionParams{
			Account: newAcc, Category: cat, Type: typ, Amount: amount,
			Date: date, Note: note, Now: now, Location: loc,
		})
		if err != nil {
			return err
		}
		changes := domain.NewBalanceChanges()
		if err := changes.RevertTransaction(old); err != nil {
			return err
		}
		if err := changes.AddTransaction(updated); err != nil {
			return err
		}
		changed, err := changes.ApplyTo(accs)
		if err != nil {
			return err
		}
		if err := s.transactions.Update(ctx, updated); err != nil {
			return err
		}
		if err := s.saveAccounts(ctx, changed, now); err != nil {
			return err
		}
		out = updated
		return s.record(ctx, owner, actor, EntityTransaction, updated.ID(), domain.AuditUpdate,
			transactionSnapshot(old), transactionSnapshot(updated))
	})
	return out, err
}

// DeleteTransaction melakukan soft delete dan membalik efek saldo.
func (s *Service) DeleteTransaction(ctx context.Context, userID, id uuid.UUID) error {
	actor := userID
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		t, err := s.transactionFor(ctx, actor, id)
		if err != nil {
			return err
		}
		if userID, err = s.writeOwner(ctx, actor, t.AccountID()); err != nil {
			return err
		}
		if err := t.EnsureEditable(); err != nil {
			return err
		}
		accs, err := s.lockAccounts(ctx, userID, t.AccountID())
		if err != nil {
			return err
		}
		acc := accs[t.AccountID()]
		// Akun arsip bersifat read-only: revert ditolak (ErrAccountArchived).
		if err := acc.Revert(t); err != nil {
			return err
		}
		now := s.now()
		if err := s.transactions.SoftDelete(ctx, t, now); err != nil {
			return err
		}
		if err := s.saveAccounts(ctx, []*domain.Account{acc}, now); err != nil {
			return err
		}
		return s.record(ctx, userID, actor, EntityTransaction, t.ID(), domain.AuditDelete, transactionSnapshot(t), nil)
	})
}

// ListTransactions mengembalikan maksimal Limit+1 baris (has_more dideteksi pemanggil).
func (s *Service) ListTransactions(ctx context.Context, in ListTransactionsInput) ([]*domain.Transaction, error) {
	if len(in.AccountIDs) > 0 {
		owner, err := s.readOwner(ctx, in.UserID, in.AccountIDs...)
		if err != nil {
			return nil, err
		}
		in.UserID = owner
	}
	f, err := s.transactionFilter(ctx, in)
	if err != nil {
		return nil, err
	}
	return s.transactions.List(ctx, in.UserID, f)
}

// transactionFilter memvalidasi input list/export menjadi filter repository.
func (s *Service) transactionFilter(ctx context.Context, in ListTransactionsInput) (domain.TransactionFilter, error) {
	f := domain.TransactionFilter{
		From: in.From, To: in.To, AccountIDs: in.AccountIDs, CategoryIDs: in.CategoryIDs,
		IncludeChildren: in.IncludeChildren, Query: in.Query, TagName: in.Tag, Limit: in.Limit, After: in.After,
	}
	if in.From != nil && in.To != nil && in.To.Before(*in.From) {
		return domain.TransactionFilter{}, domain.ErrInvalidRange
	}
	if in.Type != "" {
		t, err := domain.ParseTxType(in.Type)
		if err != nil {
			return domain.TransactionFilter{}, err
		}
		f.Type = &t
	}
	if in.MinAmount != "" || in.MaxAmount != "" || in.Currency != "" {
		cur, err := s.currencyOrDefault(ctx, in.UserID, in.Currency)
		if err != nil {
			return domain.TransactionFilter{}, err
		}
		f.Currency = &cur
		if in.MinAmount != "" {
			m, err := money.Parse(in.MinAmount, cur)
			if err != nil {
				return domain.TransactionFilter{}, &domain.ValidationError{Field: "min_amount", Reason: err.Error()}
			}
			v := m.Amount()
			f.MinAmount = &v
		}
		if in.MaxAmount != "" {
			m, err := money.Parse(in.MaxAmount, cur)
			if err != nil {
				return domain.TransactionFilter{}, &domain.ValidationError{Field: "max_amount", Reason: err.Error()}
			}
			v := m.Amount()
			f.MaxAmount = &v
		}
		if f.MinAmount != nil && f.MaxAmount != nil && *f.MaxAmount < *f.MinAmount {
			return domain.TransactionFilter{}, &domain.ValidationError{Field: "max_amount", Reason: "must be >= min_amount"}
		}
	}
	return f, nil
}

func (s *Service) userLocation(ctx context.Context, userID uuid.UUID) (*time.Location, error) {
	st, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	return st.Location(), nil
}
