package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

type CreateBillInput struct {
	UserID     uuid.UUID
	Name       string
	Amount     string
	Currency   string // kosong = currency akun, atau base currency
	AccountID  *uuid.UUID
	CategoryID *uuid.UUID
	Frequency  string
	DueDate    time.Time
	RemindDays *int
}

type UpdateBillInput struct {
	UserID          uuid.UUID
	ID              uuid.UUID
	ExpectedVersion *int
	Name            *string
	Amount          *string
	AccountID       *uuid.UUID
	ClearAccount    bool
	CategoryID      *uuid.UUID
	ClearCategory   bool
	Frequency       *string
	DueDate         *time.Time
	RemindDays      *int
	Paused          *bool
}

// PayBillInput: CreateTransaction = catat expense dari akun/kategori tagihan
// (AccountID/CategoryID/Amount menimpa nilai tagihan bila diisi).
type PayBillInput struct {
	UserID            uuid.UUID
	ID                uuid.UUID
	CreateTransaction bool
	AccountID         *uuid.UUID
	CategoryID        *uuid.UUID
	Amount            string
	Date              *time.Time // nil = hari ini (timezone user)
	Note              string
}

var errBillsDisabled = &domain.ValidationError{Field: "bill", Reason: "bills are not enabled"}

func (s *Service) billLinks(ctx context.Context, userID uuid.UUID, accID, catID *uuid.UUID) (*domain.Account, *domain.Category, error) {
	var (
		acc *domain.Account
		cat *domain.Category
		err error
	)
	if accID != nil {
		if acc, err = s.accounts.Get(ctx, userID, *accID); err != nil {
			return nil, nil, err
		}
	}
	if catID != nil {
		if cat, err = s.categories.Get(ctx, userID, *catID); err != nil {
			return nil, nil, err
		}
	}
	return acc, cat, nil
}

func (s *Service) CreateBill(ctx context.Context, in CreateBillInput) (*domain.Bill, error) {
	if s.bills == nil {
		return nil, errBillsDisabled
	}
	acc, cat, err := s.billLinks(ctx, in.UserID, in.AccountID, in.CategoryID)
	if err != nil {
		return nil, err
	}
	code := in.Currency
	if code == "" && acc != nil {
		code = acc.Currency().Code()
	}
	cur, err := s.currencyOrDefault(ctx, in.UserID, code)
	if err != nil {
		return nil, err
	}
	amount, err := money.Parse(in.Amount, cur)
	if err != nil {
		return nil, &domain.ValidationError{Field: "amount", Reason: err.Error()}
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	b, err := domain.NewBill(domain.NewBillParams{ID: id, UserID: in.UserID, Name: in.Name, Amount: amount,
		Account: acc, Category: cat, Frequency: in.Frequency, DueDate: in.DueDate, RemindDays: in.RemindDays, Now: s.now()})
	if err != nil {
		return nil, err
	}
	if err := s.bills.Create(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Service) GetBill(ctx context.Context, userID, id uuid.UUID) (*domain.Bill, error) {
	if s.bills == nil {
		return nil, domain.ErrBillNotFound
	}
	return s.bills.Get(ctx, userID, id)
}

func (s *Service) ListBills(ctx context.Context, userID uuid.UUID) ([]*domain.Bill, error) {
	if s.bills == nil {
		return []*domain.Bill{}, nil
	}
	return s.bills.List(ctx, userID)
}

func (s *Service) UpdateBill(ctx context.Context, in UpdateBillInput) (*domain.Bill, error) {
	if s.bills == nil {
		return nil, domain.ErrBillNotFound
	}
	var out *domain.Bill
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		b, err := s.bills.GetForUpdate(ctx, in.UserID, in.ID)
		if err != nil {
			return err
		}
		if err := checkVersion(in.ExpectedVersion, b.Version()); err != nil {
			return err
		}
		c := domain.BillChange{Name: in.Name, ClearAccount: in.ClearAccount, ClearCategory: in.ClearCategory,
			Frequency: in.Frequency, DueDate: in.DueDate, RemindDays: in.RemindDays, Paused: in.Paused, Now: s.now()}
		var accID, catID *uuid.UUID
		if !in.ClearAccount {
			accID = in.AccountID
		}
		if !in.ClearCategory {
			catID = in.CategoryID
		}
		if c.Account, c.Category, err = s.billLinks(ctx, in.UserID, accID, catID); err != nil {
			return err
		}
		if in.Amount != nil {
			m, err := money.Parse(*in.Amount, b.Amount().Currency())
			if err != nil {
				return &domain.ValidationError{Field: "amount", Reason: err.Error()}
			}
			c.Amount = &m
		}
		if err := b.Update(c); err != nil {
			return err
		}
		if err := s.bills.Update(ctx, b); err != nil {
			return err
		}
		out = b
		return nil
	})
	return out, err
}

func (s *Service) DeleteBill(ctx context.Context, userID, id uuid.UUID) error {
	if s.bills == nil {
		return domain.ErrBillNotFound
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		b, err := s.bills.GetForUpdate(ctx, userID, id)
		if err != nil {
			return err
		}
		return s.bills.SoftDelete(ctx, b, s.now())
	})
}

// PayBill menandai tagihan dibayar (jatuh tempo maju / done) dan opsional
// mencatat expense dalam tx yang sama.
func (s *Service) PayBill(ctx context.Context, in PayBillInput) (*domain.Bill, *domain.Transaction, error) {
	if s.bills == nil {
		return nil, nil, domain.ErrBillNotFound
	}
	var (
		out *domain.Bill
		tx  *domain.Transaction
	)
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		b, err := s.bills.GetForUpdate(ctx, in.UserID, in.ID)
		if err != nil {
			return err
		}
		if b.Status() == domain.BillDone {
			return domain.ErrBillDone
		}
		if in.CreateTransaction {
			if tx, err = s.billTransaction(ctx, b, in); err != nil {
				return err
			}
		}
		if err := b.MarkPaid(s.now()); err != nil {
			return err
		}
		if err := s.bills.Update(ctx, b); err != nil {
			return err
		}
		out = b
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return out, tx, nil
}

func (s *Service) billTransaction(ctx context.Context, b *domain.Bill, in PayBillInput) (*domain.Transaction, error) {
	accID, catID := b.AccountID(), b.CategoryID()
	if in.AccountID != nil {
		accID = in.AccountID
	}
	if in.CategoryID != nil {
		catID = in.CategoryID
	}
	if accID == nil {
		return nil, &domain.ValidationError{Field: "account_id", Reason: "required to create a transaction"}
	}
	if catID == nil {
		return nil, &domain.ValidationError{Field: "category_id", Reason: "required to create a transaction"}
	}
	amount := in.Amount
	if amount == "" {
		amount = b.Amount().String()
	}
	var date time.Time
	if in.Date != nil {
		date = *in.Date
	} else {
		loc, err := s.userLocation(ctx, in.UserID)
		if err != nil {
			return nil, err
		}
		date = domain.Today(s.now(), loc)
	}
	note := in.Note
	if note == "" {
		note = b.Name()
	}
	return s.CreateTransaction(ctx, CreateTransactionInput{UserID: in.UserID, AccountID: *accID, CategoryID: *catID,
		Type: string(domain.TxExpense), Amount: amount, Date: date, Note: note})
}

// ProcessBills mengirim pengingat jatuh tempo dan menandai tagihan overdue.
// Satu tagihan per tx (FOR UPDATE SKIP LOCKED) sehingga aman multi-instance;
// notifikasi dikirim setelah commit dan hanya sekali per jatuh tempo.
// Mengembalikan jumlah notifikasi yang dikirim.
func (s *Service) ProcessBills(ctx context.Context, asOf time.Time, batch int) (int, error) {
	if s.bills == nil {
		return 0, nil
	}
	if batch <= 0 {
		batch = 100
	}
	// timezone paling maju UTC+14: klaim s/d besok (UTC), hari lokal dihitung per user
	before := domain.DateOf(asOf.UTC()).AddDate(0, 0, 1)
	var (
		sent    int
		errs    []error
		exclude []uuid.UUID
	)
	for len(exclude) < batch && ctx.Err() == nil {
		var (
			claimed uuid.UUID
			notes   []BillNotice
		)
		err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
			notes = nil
			bills, err := s.bills.ClaimAttention(ctx, before, 1, exclude)
			if err != nil || len(bills) == 0 {
				return err
			}
			claimed = bills[0].ID()
			notes, err = s.processBill(ctx, bills[0], asOf)
			return err
		})
		if claimed == uuid.Nil {
			if err != nil {
				errs = append(errs, err)
			}
			break
		}
		exclude = append(exclude, claimed)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, n := range notes {
			s.billNotices.BillNotice(ctx, n)
			sent++
		}
	}
	return sent, errors.Join(errs...)
}

func (s *Service) processBill(ctx context.Context, b *domain.Bill, asOf time.Time) ([]BillNotice, error) {
	loc, err := s.userLocation(ctx, b.UserID())
	if err != nil {
		return nil, err
	}
	today, now := domain.Today(asOf, loc), s.now()
	notice := func(kind string) BillNotice {
		return BillNotice{UserID: b.UserID(), BillID: b.ID(), Name: b.Name(), Kind: kind, DueDate: b.NextDueDate(),
			Amount: b.Amount().String(), Currency: b.Amount().Currency().Code()}
	}
	var out []BillNotice
	if b.MarkOverdue(today, now) {
		out = append(out, notice(BillOverdue))
		b.MarkReminded(now) // sudah lewat: pengingat "due soon" tidak relevan lagi
	} else if b.ShouldRemind(today) {
		out = append(out, notice(BillDueSoon))
		b.MarkReminded(now)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, s.bills.Update(ctx, b)
}
