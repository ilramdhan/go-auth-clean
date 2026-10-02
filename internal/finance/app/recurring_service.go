package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

type CreateRecurringInput struct {
	UserID     uuid.UUID
	AccountID  uuid.UUID
	CategoryID uuid.UUID
	Type       string
	Amount     string
	Note       string
	Frequency  string
	Interval   int
	StartDate  time.Time
	EndDate    *time.Time
	Count      int // eksklusif dengan EndDate
}

// UpdateRecurringInput: nil = tetap. EndDate &nil = hapus end date.
type UpdateRecurringInput struct {
	UserID          uuid.UUID
	ID              uuid.UUID
	ExpectedVersion *int
	AccountID       *uuid.UUID
	CategoryID      *uuid.UUID
	Type            *string
	Amount          *string
	Note            *string
	Frequency       *string
	Interval        *int
	StartDate       *time.Time
	EndDate         **time.Time
	Count           *int
}

func (s *Service) CreateRecurringRule(ctx context.Context, in CreateRecurringInput) (*domain.RecurringRule, error) {
	typ, err := domain.ParseTxType(in.Type)
	if err != nil {
		return nil, err
	}
	acc, err := s.accounts.Get(ctx, in.UserID, in.AccountID)
	if err != nil {
		return nil, err
	}
	cat, err := s.categories.Get(ctx, in.UserID, in.CategoryID)
	if err != nil {
		return nil, err
	}
	amount, err := money.Parse(in.Amount, acc.Currency())
	if err != nil {
		return nil, err
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	r, err := domain.NewRecurringRule(domain.NewRecurringRuleParams{
		ID: id, UserID: in.UserID, Account: acc, Category: cat, Type: typ, Amount: amount, Note: in.Note,
		Frequency: in.Frequency, Interval: in.Interval, StartDate: in.StartDate, EndDate: in.EndDate,
		Count: in.Count, Now: s.now(),
	})
	if err != nil {
		return nil, err
	}
	if err := s.recurring.Create(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Service) GetRecurringRule(ctx context.Context, userID, id uuid.UUID) (*domain.RecurringRule, error) {
	return s.recurring.Get(ctx, userID, id)
}

// ListRecurringRules mengembalikan maksimal limit+1 baris (keyset created_at, id).
func (s *Service) ListRecurringRules(ctx context.Context, userID uuid.UUID, limit int, after *domain.PageKey) ([]*domain.RecurringRule, error) {
	return s.recurring.List(ctx, userID, limit, after)
}

func (s *Service) UpdateRecurringRule(ctx context.Context, in UpdateRecurringInput) (*domain.RecurringRule, error) {
	var out *domain.RecurringRule
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		r, err := s.recurring.Get(ctx, in.UserID, in.ID)
		if err != nil {
			return err
		}
		if err := checkVersion(in.ExpectedVersion, r.Version()); err != nil {
			return err
		}
		accID, catID := r.AccountID(), r.CategoryID()
		if in.AccountID != nil {
			accID = *in.AccountID
		}
		if in.CategoryID != nil {
			catID = *in.CategoryID
		}
		acc, err := s.accounts.Get(ctx, in.UserID, accID)
		if err != nil {
			return err
		}
		cat, err := s.categories.Get(ctx, in.UserID, catID)
		if err != nil {
			return err
		}
		c := domain.RecurringRuleChange{
			Account: acc, Category: cat, Note: in.Note, Frequency: in.Frequency, Interval: in.Interval,
			StartDate: in.StartDate, EndDate: in.EndDate, Count: in.Count, Now: s.now(),
		}
		if in.Type != nil {
			t, err := domain.ParseTxType(*in.Type)
			if err != nil {
				return err
			}
			c.Type = &t
		}
		if in.Amount != nil {
			m, err := money.Parse(*in.Amount, acc.Currency())
			if err != nil {
				return err
			}
			c.Amount = &m
		} else if acc.Currency() != r.Amount().Currency() {
			return money.ErrCurrencyMismatch
		}
		if err := r.Update(c); err != nil {
			return err
		}
		if err := s.recurring.Update(ctx, r); err != nil {
			return err
		}
		out = r
		return nil
	})
	return out, err
}

func (s *Service) DeleteRecurringRule(ctx context.Context, userID, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		r, err := s.recurring.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		return s.recurring.SoftDelete(ctx, r, s.now())
	})
}

func (s *Service) PauseRecurringRule(ctx context.Context, userID, id uuid.UUID, reason string) (*domain.RecurringRule, error) {
	return s.mutateRule(ctx, userID, id, func(r *domain.RecurringRule, _ time.Time) error {
		return r.Pause(reason, s.now())
	})
}

// ResumeRecurringRule melanjutkan rule mulai hari ini (occurrence selama pause dilewati).
func (s *Service) ResumeRecurringRule(ctx context.Context, userID, id uuid.UUID) (*domain.RecurringRule, error) {
	return s.mutateRule(ctx, userID, id, func(r *domain.RecurringRule, today time.Time) error {
		return r.Resume(today, s.now())
	})
}

func (s *Service) mutateRule(ctx context.Context, userID, id uuid.UUID, fn func(*domain.RecurringRule, time.Time) error) (*domain.RecurringRule, error) {
	loc, err := s.userLocation(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out *domain.RecurringRule
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		r, err := s.recurring.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		if err := fn(r, domain.Today(s.now(), loc)); err != nil {
			return err
		}
		if err := s.recurring.Update(ctx, r); err != nil {
			return err
		}
		out = r
		return nil
	})
	return out, err
}

// GenerateDue membuat transaksi untuk rule yang jatuh tempo per asOf. Setiap
// rule diproses dalam DB transaction sendiri: rule dikunci dengan FOR UPDATE
// SKIP LOCKED (aman untuk banyak instance), occurrence di-insert dengan
// ON CONFLICT DO NOTHING (idempotent per rule+tanggal), lalu next_run_date
// dimajukan. Rule yang gagal karena aturan bisnis (akun diarsip, saldo kurang,
// dst) di-pause dengan alasan; error infrastruktur dikumpulkan dan rule
// dicoba lagi pada tick berikutnya. Mengembalikan jumlah transaksi dibuat.
func (s *Service) GenerateDue(ctx context.Context, asOf time.Time, batch int) (int, error) {
	if batch <= 0 {
		batch = 100
	}
	// Timezone user paling maju UTC+14: klaim rule s/d besok (UTC), tanggal
	// jatuh tempo sebenarnya dihitung per user di runRule.
	before := domain.DateOf(asOf.UTC()).AddDate(0, 0, 1)
	var (
		total   int
		errs    []error
		exclude []uuid.UUID
	)
	for len(exclude) < batch && ctx.Err() == nil {
		var claimed uuid.UUID
		n := 0
		err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
			rules, err := s.recurring.ClaimDue(ctx, before, 1, exclude)
			if err != nil || len(rules) == 0 {
				return err
			}
			claimed = rules[0].ID()
			n, err = s.runRule(ctx, rules[0], asOf)
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
		total += n
	}
	return total, errors.Join(errs...)
}

// isRuleBlocked: error bisnis yang membuat rule tidak bisa dijalankan sampai user bertindak.
func isRuleBlocked(err error) bool {
	if _, ok := errors.AsType[*domain.ValidationError](err); ok {
		return true
	}
	for _, e := range []error{
		domain.ErrAccountArchived, domain.ErrInsufficientBalance, domain.ErrAccountNotFound,
		domain.ErrCategoryNotFound, domain.ErrCategoryTypeMismatch, money.ErrCurrencyMismatch,
		domain.ErrDateTooOld, domain.ErrDateInFuture, domain.ErrAmountTooLarge, domain.ErrInvalidAmount,
	} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

func (s *Service) runRule(ctx context.Context, r *domain.RecurringRule, asOf time.Time) (int, error) {
	loc, err := s.userLocation(ctx, r.UserID())
	if err != nil {
		return 0, err
	}
	dates := r.DueDates(domain.Today(asOf, loc), domain.MaxCatchUp)
	if len(dates) == 0 {
		return 0, nil // belum jatuh tempo di timezone user
	}
	now := s.now()
	created, applied, blockErr, err := s.createOccurrences(ctx, r, dates, now)
	if err != nil {
		return 0, err
	}
	if applied != nil {
		if err := s.saveAccounts(ctx, []*domain.Account{applied}, now); err != nil {
			return 0, err
		}
	}
	if blockErr != nil {
		if err := r.Pause(blockErr.Error(), now); err != nil {
			return 0, err
		}
	}
	if err := s.recurring.Update(ctx, r); err != nil {
		return 0, err
	}
	return created, nil
}

// createOccurrences membuat transaksi untuk setiap tanggal. blockErr = error
// bisnis yang menghentikan loop (rule akan di-pause); applied = akun yang
// saldonya berubah (nil bila tidak ada).
func (s *Service) createOccurrences(ctx context.Context, r *domain.RecurringRule, dates []time.Time, now time.Time) (created int, applied *domain.Account, blockErr, err error) {
	block := func(e error) (int, *domain.Account, error, error) {
		if isRuleBlocked(e) {
			return created, applied, e, nil
		}
		return 0, nil, nil, e
	}
	cat, err := s.categories.Get(ctx, r.UserID(), r.CategoryID())
	if err != nil {
		return block(err)
	}
	accs, err := s.lockAccounts(ctx, r.UserID(), r.AccountID())
	if err != nil {
		return block(err)
	}
	acc := accs[r.AccountID()]
	for _, d := range dates {
		id, err := s.newID()
		if err != nil {
			return 0, nil, nil, err
		}
		t, err := r.BuildTransaction(id, acc, cat, d, now)
		if err != nil {
			return block(err)
		}
		if err := acc.Apply(t); err != nil {
			return block(err)
		}
		inserted, err := s.transactions.CreateOccurrence(ctx, t)
		if err != nil {
			return 0, nil, nil, err
		}
		if inserted {
			created++
			applied = acc
		} else if err := acc.Revert(t); err != nil { // sudah dibuat run sebelumnya
			return 0, nil, nil, err
		}
		r.MarkRun(d, now)
	}
	return created, applied, nil, nil
}
