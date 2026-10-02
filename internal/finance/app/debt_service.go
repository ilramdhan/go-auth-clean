package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

// Debt adalah hutang/piutang beserta total terbayar.
type Debt struct {
	Debt *domain.Debt
	Paid int64
}

// Remaining adalah sisa yang belum dibayar.
func (d *Debt) Remaining() money.Money {
	return money.New(d.Debt.Remaining(d.Paid), d.Debt.Principal().Currency())
}

type CreateDebtInput struct {
	UserID       uuid.UUID
	Direction    string
	Counterparty string
	Principal    string
	Currency     string // kosong = base currency
	StartDate    time.Time
	DueDate      *time.Time
	Note         string
}

type UpdateDebtInput struct {
	UserID          uuid.UUID
	ID              uuid.UUID
	ExpectedVersion *int
	Counterparty    *string
	Principal       *string
	DueDate         *time.Time
	ClearDueDate    bool
	Note            *string
}

// PayDebtInput: pilih salah satu: TransactionID (tautkan transaksi yang sudah
// ada) atau AccountID+CategoryID (buat transaksi expense/income baru dalam tx
// yang sama). Keduanya kosong = pembayaran tanpa transaksi.
type PayDebtInput struct {
	UserID        uuid.UUID
	DebtID        uuid.UUID
	Amount        string // boleh kosong bila TransactionID diisi
	Date          time.Time
	TransactionID *uuid.UUID
	AccountID     *uuid.UUID
	CategoryID    *uuid.UUID
	Note          string
}

var errDebtsDisabled = &domain.ValidationError{Field: "debt", Reason: "debts are not enabled"}

func (s *Service) CreateDebt(ctx context.Context, in CreateDebtInput) (*Debt, error) {
	if s.debts == nil {
		return nil, errDebtsDisabled
	}
	cur, err := s.currencyOrDefault(ctx, in.UserID, in.Currency)
	if err != nil {
		return nil, err
	}
	principal, err := money.Parse(in.Principal, cur)
	if err != nil {
		return nil, &domain.ValidationError{Field: "principal", Reason: err.Error()}
	}
	loc, err := s.userLocation(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	d, err := domain.NewDebt(domain.NewDebtParams{ID: id, UserID: in.UserID, Direction: in.Direction,
		Counterparty: in.Counterparty, Principal: principal, StartDate: in.StartDate, DueDate: in.DueDate,
		Note: in.Note, Now: s.now(), Location: loc})
	if err != nil {
		return nil, err
	}
	if err := s.debts.Create(ctx, d); err != nil {
		return nil, err
	}
	return &Debt{Debt: d}, nil
}

func (s *Service) GetDebt(ctx context.Context, userID, id uuid.UUID) (*Debt, error) {
	if s.debts == nil {
		return nil, domain.ErrDebtNotFound
	}
	d, err := s.debts.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	paid, err := s.debts.Paid(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return &Debt{Debt: d, Paid: paid}, nil
}

// ListDebts: status kosong = semua.
func (s *Service) ListDebts(ctx context.Context, userID uuid.UUID, status string) ([]*Debt, error) {
	if s.debts == nil {
		return []*Debt{}, nil
	}
	var st *domain.DebtStatus
	switch domain.DebtStatus(status) {
	case "":
	case domain.DebtOpen, domain.DebtSettled:
		v := domain.DebtStatus(status)
		st = &v
	default:
		return nil, &domain.ValidationError{Field: "status", Reason: "must be open or settled"}
	}
	list, err := s.debts.List(ctx, userID, st)
	if err != nil {
		return nil, err
	}
	out := make([]*Debt, 0, len(list))
	for _, x := range list {
		out = append(out, &Debt{Debt: x.Debt, Paid: x.Paid})
	}
	return out, nil
}

func (s *Service) UpdateDebt(ctx context.Context, in UpdateDebtInput) (*Debt, error) {
	if s.debts == nil {
		return nil, domain.ErrDebtNotFound
	}
	var out *Debt
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.debts.GetForUpdate(ctx, in.UserID, in.ID)
		if err != nil {
			return err
		}
		if err := checkVersion(in.ExpectedVersion, d.Version()); err != nil {
			return err
		}
		paid, err := s.debts.Paid(ctx, in.UserID, in.ID)
		if err != nil {
			return err
		}
		c := domain.DebtChange{Counterparty: in.Counterparty, DueDate: in.DueDate, ClearDueDate: in.ClearDueDate,
			Note: in.Note, Paid: paid, Now: s.now()}
		if in.Principal != nil {
			p, err := money.Parse(*in.Principal, d.Principal().Currency())
			if err != nil {
				return &domain.ValidationError{Field: "principal", Reason: err.Error()}
			}
			c.Principal = &p
		}
		if err := d.Update(c); err != nil {
			return err
		}
		if err := s.debts.Update(ctx, d); err != nil {
			return err
		}
		out = &Debt{Debt: d, Paid: paid}
		return nil
	})
	return out, err
}

func (s *Service) DeleteDebt(ctx context.Context, userID, id uuid.UUID) error {
	if s.debts == nil {
		return domain.ErrDebtNotFound
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.debts.GetForUpdate(ctx, userID, id)
		if err != nil {
			return err
		}
		return s.debts.SoftDelete(ctx, d, s.now())
	})
}

// PayDebt mencatat cicilan (mengurangi sisa) dan menandai settled bila lunas.
// Bila AccountID diisi, transaksi expense (payable) / income (receivable)
// dibuat dalam tx yang sama sehingga saldo akun ikut berubah.
func (s *Service) PayDebt(ctx context.Context, in PayDebtInput) (*domain.DebtPayment, *Debt, error) {
	if s.debts == nil {
		return nil, nil, domain.ErrDebtNotFound
	}
	if in.TransactionID != nil && in.AccountID != nil {
		return nil, nil, &domain.ValidationError{Field: "transaction_id", Reason: "use either transaction_id or account_id"}
	}
	var (
		pay *domain.DebtPayment
		out *Debt
	)
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.debts.GetForUpdate(ctx, in.UserID, in.DebtID)
		if err != nil {
			return err
		}
		if d.Status() == domain.DebtSettled {
			return domain.ErrDebtSettled
		}
		paid, err := s.debts.Paid(ctx, in.UserID, in.DebtID)
		if err != nil {
			return err
		}
		loc, err := s.userLocation(ctx, in.UserID)
		if err != nil {
			return err
		}
		p := domain.NewDebtPaymentParams{Debt: d, Paid: paid, Date: in.Date, Note: in.Note, Now: s.now(), Location: loc}
		if in.Amount != "" {
			if p.Amount, err = money.Parse(in.Amount, d.Principal().Currency()); err != nil {
				return &domain.ValidationError{Field: "amount", Reason: err.Error()}
			}
		} else if in.TransactionID == nil {
			return &domain.ValidationError{Field: "amount", Reason: "required"}
		}
		switch {
		case in.TransactionID != nil:
			if p.Transaction, err = s.transactions.Get(ctx, in.UserID, *in.TransactionID); err != nil {
				return err
			}
		case in.AccountID != nil:
			if p.Transaction, err = s.debtTransaction(ctx, d, in, p.Amount); err != nil {
				return err
			}
		}
		if p.ID, err = s.newID(); err != nil {
			return err
		}
		if pay, err = domain.NewDebtPayment(p); err != nil {
			return err
		}
		if err := s.debts.AddPayment(ctx, pay); err != nil {
			return err
		}
		paid += pay.Amount.Amount()
		if d.ApplyPayments(paid, s.now()) {
			if err := s.debts.Update(ctx, d); err != nil {
				return err
			}
		}
		out = &Debt{Debt: d, Paid: paid}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return pay, out, nil
}

// debtTransaction membuat transaksi manual untuk pembayaran hutang.
func (s *Service) debtTransaction(ctx context.Context, d *domain.Debt, in PayDebtInput, amount money.Money) (*domain.Transaction, error) {
	if in.CategoryID == nil {
		return nil, &domain.ValidationError{Field: "category_id", Reason: "required when account_id is set"}
	}
	typ := domain.TxExpense
	if d.Direction() == domain.DebtReceivable {
		typ = domain.TxIncome
	}
	note := in.Note
	if note == "" {
		note = d.Counterparty()
	}
	return s.CreateTransaction(ctx, CreateTransactionInput{UserID: in.UserID, AccountID: *in.AccountID,
		CategoryID: *in.CategoryID, Type: string(typ), Amount: amountString(amount), Date: in.Date, Note: note})
}

// amountString memformat Money sebagai major unit tanpa simbol untuk money.Parse.
func amountString(m money.Money) string { return m.String() }

func (s *Service) ListDebtPayments(ctx context.Context, userID, debtID uuid.UUID) ([]*domain.DebtPayment, error) {
	if s.debts == nil {
		return nil, domain.ErrDebtNotFound
	}
	if _, err := s.debts.Get(ctx, userID, debtID); err != nil {
		return nil, err
	}
	return s.debts.ListPayments(ctx, userID, debtID)
}

// DeleteDebtPayment menghapus cicilan (transaksi tertaut tidak ikut dihapus;
// hapus lewat endpoint transaksi bila perlu) dan menyelaraskan status.
func (s *Service) DeleteDebtPayment(ctx context.Context, userID, debtID, id uuid.UUID) error {
	if s.debts == nil {
		return domain.ErrDebtNotFound
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		d, err := s.debts.GetForUpdate(ctx, userID, debtID)
		if err != nil {
			return err
		}
		if err := s.debts.DeletePayment(ctx, userID, debtID, id); err != nil {
			return err
		}
		paid, err := s.debts.Paid(ctx, userID, debtID)
		if err != nil {
			return err
		}
		if d.ApplyPayments(paid, s.now()) {
			return s.debts.Update(ctx, d)
		}
		return nil
	})
}
