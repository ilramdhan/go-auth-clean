package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

// CreateAccountInput adalah input pembuatan akun. InitialBalance berupa string
// major unit ("35000", "12.34"); kosong = 0.
type CreateAccountInput struct {
	UserID         uuid.UUID
	Name           string
	Type           string
	Currency       string
	InitialBalance string
	AllowNegative  *bool
}

// UpdateAccountInput: field nil = tidak diubah. ExpectedVersion dari If-Match.
type UpdateAccountInput struct {
	UserID          uuid.UUID
	ID              uuid.UUID
	ExpectedVersion *int
	Name            *string
	InitialBalance  *string
	AllowNegative   *bool
}

func (s *Service) CreateAccount(ctx context.Context, in CreateAccountInput) (*domain.Account, error) {
	typ, err := domain.ParseAccountType(in.Type)
	if err != nil {
		return nil, err
	}
	cur, err := s.currencyOrDefault(ctx, in.UserID, in.Currency)
	if err != nil {
		return nil, err
	}
	initial := money.Zero(cur)
	if in.InitialBalance != "" {
		if initial, err = money.Parse(in.InitialBalance, cur); err != nil {
			return nil, err
		}
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	acc, err := domain.NewAccount(domain.NewAccountParams{
		ID: id, UserID: in.UserID, Name: in.Name, Type: typ,
		InitialBalance: initial, AllowNegative: in.AllowNegative, Now: s.now(),
	})
	if err != nil {
		return nil, err
	}
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.accounts.Create(ctx, acc); err != nil {
			return err
		}
		return s.record(ctx, in.UserID, in.UserID, EntityAccount, acc.ID(), domain.AuditCreate, nil, accountSnapshot(acc))
	})
	if err != nil {
		return nil, err
	}
	return acc, nil
}

// currencyOrDefault: kode kosong = base currency dari settings user.
func (s *Service) currencyOrDefault(ctx context.Context, userID uuid.UUID, code string) (money.Currency, error) {
	if code != "" {
		return money.ParseCurrency(code)
	}
	st, err := s.GetSettings(ctx, userID)
	if err != nil {
		return money.Currency{}, err
	}
	return st.BaseCurrency(), nil
}

// GetAccount: pemilik atau member (role apa pun) boleh membaca.
func (s *Service) GetAccount(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error) {
	owner, _, err := s.accountAccess(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return s.accounts.Get(ctx, owner, id)
}

// lockOwned mengunci akun milik userID. Bila akun ternyata milik user lain
// yang membagikannya ke userID, hasilnya ErrForbidden (hanya owner yang boleh
// mengelola akun); non-anggota tetap ErrAccountNotFound.
func (s *Service) lockOwned(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error) {
	acc, err := s.accounts.GetForUpdate(ctx, userID, id)
	if errors.Is(err, domain.ErrAccountNotFound) {
		if aerr := s.requireOwner(ctx, userID, id); aerr != nil {
			return nil, aerr
		}
	}
	return acc, err
}

func (s *Service) ListAccounts(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]*domain.Account, error) {
	return s.accounts.List(ctx, userID, includeArchived)
}

// UpdateAccount mengubah metadata akun. Saldo awal yang berubah menggeser
// saldo berjalan dalam tx yang sama (baris dikunci FOR UPDATE).
func (s *Service) UpdateAccount(ctx context.Context, in UpdateAccountInput) (*domain.Account, error) {
	var out *domain.Account
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		acc, err := s.lockOwned(ctx, in.UserID, in.ID)
		if err != nil {
			return err
		}
		before := accountSnapshot(acc)
		if err := checkVersion(in.ExpectedVersion, acc.Version()); err != nil {
			return err
		}
		upd := domain.AccountUpdate{Name: in.Name, AllowNegative: in.AllowNegative, Now: s.now()}
		if in.InitialBalance != nil {
			m, err := money.Parse(*in.InitialBalance, acc.Currency())
			if err != nil {
				return err
			}
			upd.InitialBalance = &m
		}
		if err := acc.Update(upd); err != nil {
			return err
		}
		if err := s.accounts.Update(ctx, acc); err != nil {
			return err
		}
		out = acc
		return s.record(ctx, in.UserID, in.UserID, EntityAccount, acc.ID(), domain.AuditUpdate, before, accountSnapshot(acc))
	})
	return out, err
}

func (s *Service) ArchiveAccount(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error) {
	return s.mutateAccount(ctx, userID, id, func(a *domain.Account) { a.Archive(s.now()) })
}

func (s *Service) UnarchiveAccount(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error) {
	return s.mutateAccount(ctx, userID, id, func(a *domain.Account) { a.Unarchive(s.now()) })
}

func (s *Service) mutateAccount(ctx context.Context, userID, id uuid.UUID, fn func(*domain.Account)) (*domain.Account, error) {
	var out *domain.Account
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		acc, err := s.lockOwned(ctx, userID, id)
		if err != nil {
			return err
		}
		before := accountSnapshot(acc)
		wasArchived := acc.IsArchived()
		fn(acc)
		if acc.IsArchived() == wasArchived {
			out = acc // tidak ada perubahan (idempotent)
			return nil
		}
		if err := s.accounts.Update(ctx, acc); err != nil {
			return err
		}
		out = acc
		return s.record(ctx, userID, userID, EntityAccount, acc.ID(), domain.AuditUpdate, before, accountSnapshot(acc))
	})
	return out, err
}

// DeleteAccount hanya boleh bila akun tidak punya transaksi/transfer aktif;
// selain itu user disarankan archive (jebakan #6).
func (s *Service) DeleteAccount(ctx context.Context, userID, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		acc, err := s.lockOwned(ctx, userID, id)
		if err != nil {
			return err
		}
		used, err := s.accounts.HasActivity(ctx, userID, id)
		if err != nil {
			return err
		}
		if used {
			return domain.ErrAccountHasTransactions
		}
		if err := s.accounts.SoftDelete(ctx, userID, id, s.now()); err != nil {
			return err
		}
		return s.record(ctx, userID, userID, EntityAccount, id, domain.AuditDelete, accountSnapshot(acc), nil)
	})
}
