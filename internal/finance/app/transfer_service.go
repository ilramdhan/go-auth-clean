package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

type CreateTransferInput struct {
	UserID        uuid.UUID
	FromAccountID uuid.UUID
	ToAccountID   uuid.UUID
	Amount        string
	Fee           string // kosong = tanpa biaya
	ToAmount      string // wajib bila currency akun tujuan berbeda
	Date          time.Time
	Note          string
}

type UpdateTransferInput struct {
	UserID          uuid.UUID
	ID              uuid.UUID
	ExpectedVersion *int
	FromAccountID   *uuid.UUID
	ToAccountID     *uuid.UUID
	Amount          *string
	Fee             *string // "" atau "0" = hapus biaya
	ToAmount        *string // nil = pakai nilai lama (bila pasangan currency sama)
	Date            *time.Time
	Note            *string
}

type ListTransfersInput struct {
	UserID    uuid.UUID
	From, To  *time.Time
	AccountID *uuid.UUID
	Limit     int
	After     *domain.PageKey
}

// CreateTransfer memindahkan uang antar akun milik user dalam satu DB
// transaction. Biaya (opsional) dicatat sebagai expense "Biaya Admin" dari akun asal.
func (s *Service) CreateTransfer(ctx context.Context, in CreateTransferInput) (*domain.Transfer, error) {
	if in.FromAccountID == in.ToAccountID {
		return nil, domain.ErrSameAccountTransfer
	}
	actor := in.UserID
	var out *domain.Transfer
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		// Kedua akun harus milik owner yang sama dan actor minimal editor di keduanya.
		owner, err := s.writeOwner(ctx, actor, in.FromAccountID, in.ToAccountID)
		if err != nil {
			return err
		}
		in.UserID = owner
		loc, err := s.userLocation(ctx, owner)
		if err != nil {
			return err
		}
		accs, err := s.lockAccounts(ctx, in.UserID, in.FromAccountID, in.ToAccountID)
		if err != nil {
			return err
		}
		from, to := accs[in.FromAccountID], accs[in.ToAccountID]
		amount, fee, err := parseTransferAmounts(in.Amount, in.Fee, from.Currency())
		if err != nil {
			return err
		}
		toAmount, err := parseToAmount(in.ToAmount, to.Currency())
		if err != nil {
			return err
		}
		if toAmount, err = s.autoToAmount(ctx, owner, toAmount, amount, to.Currency(), in.Date); err != nil {
			return err
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		now := s.now()
		tr, err := domain.NewTransfer(domain.NewTransferParams{
			ID: id, UserID: in.UserID, From: from, To: to, Amount: amount, ToAmount: toAmount, Fee: fee,
			Date: in.Date, Note: in.Note, Now: now, Location: loc,
		})
		if err != nil {
			return err
		}
		changes := domain.NewBalanceChanges()
		if err := changes.AddTransfer(tr); err != nil {
			return err
		}
		feeTx, err := s.buildFeeTransaction(ctx, tr, from, now, loc)
		if err != nil {
			return err
		}
		if feeTx != nil {
			if err := changes.AddTransaction(feeTx); err != nil {
				return err
			}
			if err := s.transactions.Create(ctx, feeTx); err != nil {
				return err
			}
			fid := feeTx.ID()
			tr.SetFeeTransaction(&fid)
		}
		changed, err := changes.ApplyTo(accs)
		if err != nil {
			return err
		}
		if err := s.transfers.Create(ctx, tr); err != nil {
			return err
		}
		if err := s.saveAccounts(ctx, changed, now); err != nil {
			return err
		}
		out = tr
		return s.record(ctx, owner, actor, EntityTransfer, tr.ID(), domain.AuditCreate, nil, transferSnapshot(tr))
	})
	return out, err
}

func parseTransferAmounts(amountStr, feeStr string, cur money.Currency) (amount, fee money.Money, err error) {
	amount, err = money.Parse(amountStr, cur)
	if err != nil {
		return money.Money{}, money.Money{}, err
	}
	fee = money.Zero(cur)
	if feeStr != "" {
		if fee, err = money.Parse(feeStr, cur); err != nil {
			return money.Money{}, money.Money{}, &domain.ValidationError{Field: "fee", Reason: err.Error()}
		}
	}
	return amount, fee, nil
}

// parseToAmount: kosong = ikut amount (hanya sah bila satu currency; domain yang memvalidasi).
func parseToAmount(s string, cur money.Currency) (money.Money, error) {
	if s == "" {
		return money.Money{}, nil
	}
	m, err := money.Parse(s, cur)
	if err != nil {
		return money.Money{}, &domain.ValidationError{Field: "to_amount", Reason: err.Error()}
	}
	return m, nil
}

// updatedToAmount memilih to_amount untuk edit: input eksplisit, atau nilai lama
// bila transfer tetap lintas currency dengan currency tujuan yang sama.
func updatedToAmount(in *string, old *domain.Transfer, from, to *domain.Account) (money.Money, error) {
	if in != nil {
		return parseToAmount(*in, to.Currency())
	}
	if old.IsCrossCurrency() && from.Currency() != to.Currency() && old.ToAmount().Currency() == to.Currency() {
		return old.ToAmount(), nil
	}
	return money.Money{}, nil
}

// buildFeeTransaction membuat expense biaya transfer (nil bila tanpa biaya).
func (s *Service) buildFeeTransaction(ctx context.Context, tr *domain.Transfer, from *domain.Account, now time.Time, loc *time.Location) (*domain.Transaction, error) {
	if !tr.HasFee() {
		return nil, nil //nolint:nilnil // nil = tidak ada biaya
	}
	cat, err := s.categories.Get(ctx, tr.UserID(), domain.FeeCategoryID)
	if err != nil {
		return nil, err
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	return domain.NewTransaction(domain.NewTransactionParams{
		ID: id, UserID: tr.UserID(), Account: from, Category: cat, Type: domain.TxExpense,
		Amount: tr.Fee(), Date: tr.Date(), Note: feeNote(tr.Note()), Source: domain.SourceTransferFee,
		Now: now, Location: loc,
	})
}

func feeNote(note string) string {
	if note == "" {
		return "Biaya transfer"
	}
	r := []rune("Biaya transfer: " + note)
	if len(r) > domain.MaxNoteLength {
		r = r[:domain.MaxNoteLength]
	}
	return string(r)
}

// GetTransfer: pemilik atau member salah satu akun transfer.
func (s *Service) GetTransfer(ctx context.Context, userID, id uuid.UUID) (*domain.Transfer, error) {
	return s.transferFor(ctx, userID, id)
}

func (s *Service) ListTransfers(ctx context.Context, in ListTransfersInput) ([]*domain.Transfer, error) {
	if in.From != nil && in.To != nil && in.To.Before(*in.From) {
		return nil, domain.ErrInvalidRange
	}
	if in.AccountID != nil {
		owner, err := s.readOwner(ctx, in.UserID, *in.AccountID)
		if err != nil {
			return nil, err
		}
		in.UserID = owner
	}
	return s.transfers.List(ctx, in.UserID, domain.TransferFilter{
		From: in.From, To: in.To, AccountID: in.AccountID, Limit: in.Limit, After: in.After,
	})
}

// UpdateTransfer: revert leg lama (sampai 4 akun dikunci urut ID), apply leg baru,
// dan sinkronkan transaksi biaya (buat/ubah/hapus).
func (s *Service) UpdateTransfer(ctx context.Context, in UpdateTransferInput) (*domain.Transfer, error) {
	actor := in.UserID
	var out *domain.Transfer
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		old, err := s.transferFor(ctx, actor, in.ID)
		if err != nil {
			return err
		}
		fromID, toID := old.FromAccountID(), old.ToAccountID()
		if in.FromAccountID != nil {
			fromID = *in.FromAccountID
		}
		if in.ToAccountID != nil {
			toID = *in.ToAccountID
		}
		owner, err := s.writeOwner(ctx, actor, old.FromAccountID(), old.ToAccountID(), fromID, toID)
		if err != nil {
			return err
		}
		in.UserID = owner
		loc, err := s.userLocation(ctx, owner)
		if err != nil {
			return err
		}
		if err := checkVersion(in.ExpectedVersion, old.Version()); err != nil {
			return err
		}
		if fromID == toID {
			return domain.ErrSameAccountTransfer
		}
		accs, err := s.lockAccounts(ctx, in.UserID, old.FromAccountID(), old.ToAccountID(), fromID, toID)
		if err != nil {
			return err
		}
		from, to := accs[fromID], accs[toID]
		amountStr, feeStr := old.Amount().String(), old.Fee().String()
		if in.Amount != nil {
			amountStr = *in.Amount
		}
		if in.Fee != nil {
			feeStr = *in.Fee
		}
		amount, fee, err := parseTransferAmounts(amountStr, feeStr, from.Currency())
		if err != nil {
			return err
		}
		toAmount, err := updatedToAmount(in.ToAmount, old, from, to)
		if err != nil {
			return err
		}
		date, note := old.Date(), old.Note()
		if in.Date != nil {
			date = *in.Date
		}
		if in.Note != nil {
			note = *in.Note
		}
		now := s.now()
		updated, err := old.Change(domain.ChangeTransferParams{
			From: from, To: to, Amount: amount, ToAmount: toAmount, Fee: fee, Date: date, Note: note, Now: now, Location: loc,
		})
		if err != nil {
			return err
		}
		changes := domain.NewBalanceChanges()
		if err := changes.RevertTransfer(old); err != nil {
			return err
		}
		if err := changes.AddTransfer(updated); err != nil {
			return err
		}
		if err := s.syncFee(ctx, old, updated, from, changes, now, loc); err != nil {
			return err
		}
		changed, err := changes.ApplyTo(accs)
		if err != nil {
			return err
		}
		if err := s.transfers.Update(ctx, updated); err != nil {
			return err
		}
		if err := s.saveAccounts(ctx, changed, now); err != nil {
			return err
		}
		out = updated
		return s.record(ctx, owner, actor, EntityTransfer, updated.ID(), domain.AuditUpdate,
			transferSnapshot(old), transferSnapshot(updated))
	})
	return out, err
}

// syncFee menyesuaikan transaksi biaya: hapus yang lama (revert), buat yang baru.
func (s *Service) syncFee(ctx context.Context, old, updated *domain.Transfer, from *domain.Account,
	changes *domain.BalanceChanges, now time.Time, loc *time.Location,
) error {
	if fid := old.FeeTransactionID(); fid != nil {
		feeTx, err := s.transactions.Get(ctx, old.UserID(), *fid)
		if err != nil {
			return err
		}
		if err := changes.RevertTransaction(feeTx); err != nil {
			return err
		}
		if err := s.transactions.SoftDelete(ctx, feeTx, now); err != nil {
			return err
		}
	}
	updated.SetFeeTransaction(nil)
	feeTx, err := s.buildFeeTransaction(ctx, updated, from, now, loc)
	if err != nil || feeTx == nil {
		return err
	}
	if err := changes.AddTransaction(feeTx); err != nil {
		return err
	}
	if err := s.transactions.Create(ctx, feeTx); err != nil {
		return err
	}
	fid := feeTx.ID()
	updated.SetFeeTransaction(&fid)
	return nil
}

// DeleteTransfer membalik kedua leg dan menghapus transaksi biaya (bila ada).
func (s *Service) DeleteTransfer(ctx context.Context, userID, id uuid.UUID) error {
	actor := userID
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		tr, err := s.transferFor(ctx, actor, id)
		if err != nil {
			return err
		}
		if userID, err = s.writeOwner(ctx, actor, tr.FromAccountID(), tr.ToAccountID()); err != nil {
			return err
		}
		accs, err := s.lockAccounts(ctx, userID, tr.FromAccountID(), tr.ToAccountID())
		if err != nil {
			return err
		}
		now := s.now()
		changes := domain.NewBalanceChanges()
		if err := changes.RevertTransfer(tr); err != nil {
			return err
		}
		if fid := tr.FeeTransactionID(); fid != nil {
			feeTx, err := s.transactions.Get(ctx, userID, *fid)
			if err != nil {
				return err
			}
			if err := changes.RevertTransaction(feeTx); err != nil {
				return err
			}
			if err := s.transactions.SoftDelete(ctx, feeTx, now); err != nil {
				return err
			}
		}
		changed, err := changes.ApplyTo(accs)
		if err != nil {
			return err
		}
		if err := s.transfers.SoftDelete(ctx, tr, now); err != nil {
			return err
		}
		if err := s.saveAccounts(ctx, changed, now); err != nil {
			return err
		}
		return s.record(ctx, userID, actor, EntityTransfer, tr.ID(), domain.AuditDelete, transferSnapshot(tr), nil)
	})
}
