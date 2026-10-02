package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

type CreateBudgetInput struct {
	UserID     uuid.UUID
	CategoryID uuid.UUID
	Month      string // "YYYY-MM"; kosong = bulan berjalan
	Amount     string
	Currency   string // kosong = base currency user
	Threshold  int    // 0 = default 80
}

type UpdateBudgetInput struct {
	UserID          uuid.UUID
	ID              uuid.UUID
	ExpectedVersion *int
	Amount          *string
	Threshold       *int
}

// BudgetView adalah budget beserta progress pemakaiannya.
type BudgetView struct {
	Budget   *domain.Budget
	Progress domain.BudgetProgress
}

func (s *Service) CreateBudget(ctx context.Context, in CreateBudgetInput) (*BudgetView, error) {
	loc, err := s.userLocation(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	month, _, err := domain.ParseMonth(in.Month, s.now(), loc)
	if err != nil {
		return nil, err
	}
	cur, err := s.currencyOrDefault(ctx, in.UserID, in.Currency)
	if err != nil {
		return nil, err
	}
	amount, err := money.Parse(in.Amount, cur)
	if err != nil {
		return nil, err
	}
	var out *BudgetView
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		cat, err := s.categories.Get(ctx, in.UserID, in.CategoryID)
		if err != nil {
			return err
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		b, err := domain.NewBudget(domain.NewBudgetParams{
			ID: id, UserID: in.UserID, Category: cat, Month: month, Amount: amount,
			Threshold: in.Threshold, Now: s.now(),
		})
		if err != nil {
			return err
		}
		if err := s.budgets.Create(ctx, b); err != nil {
			return err
		}
		out, err = s.budgetView(ctx, b)
		return err
	})
	return out, err
}

func (s *Service) budgetView(ctx context.Context, b *domain.Budget) (*BudgetView, error) {
	spent, err := s.budgets.Spent(ctx, b)
	if err != nil {
		return nil, err
	}
	return &BudgetView{Budget: b, Progress: b.Progress(spent)}, nil
}

// GetBudget mengembalikan budget beserta progress.
func (s *Service) GetBudget(ctx context.Context, userID, id uuid.UUID) (*BudgetView, error) {
	b, err := s.budgets.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return s.budgetView(ctx, b)
}

// ListBudgets: semua budget bulan tsb (satu query, spent dihitung on read).
func (s *Service) ListBudgets(ctx context.Context, userID uuid.UUID, month string) ([]BudgetView, error) {
	loc, err := s.userLocation(ctx, userID)
	if err != nil {
		return nil, err
	}
	from, _, err := domain.ParseMonth(month, s.now(), loc)
	if err != nil {
		return nil, err
	}
	rows, err := s.budgets.ListWithSpent(ctx, userID, from)
	if err != nil {
		return nil, err
	}
	out := make([]BudgetView, 0, len(rows))
	for _, r := range rows {
		out = append(out, BudgetView{Budget: r.Budget, Progress: r.Budget.Progress(r.Spent)})
	}
	return out, nil
}

func (s *Service) UpdateBudget(ctx context.Context, in UpdateBudgetInput) (*BudgetView, error) {
	var out *BudgetView
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		b, err := s.budgets.Get(ctx, in.UserID, in.ID)
		if err != nil {
			return err
		}
		if err := checkVersion(in.ExpectedVersion, b.Version()); err != nil {
			return err
		}
		var amount *money.Money
		if in.Amount != nil {
			m, err := money.Parse(*in.Amount, b.Amount().Currency())
			if err != nil {
				return err
			}
			amount = &m
		}
		if err := b.Update(amount, in.Threshold, s.now()); err != nil {
			return err
		}
		// Nominal/threshold berubah: level alert dihitung ulang tanpa notifikasi
		// (notifikasi hanya dipicu transaksi).
		spent, err := s.budgets.Spent(ctx, b)
		if err != nil {
			return err
		}
		b.EvaluateAlert(spent)
		if err := s.budgets.Update(ctx, b); err != nil {
			return err
		}
		out = &BudgetView{Budget: b, Progress: b.Progress(spent)}
		return nil
	})
	return out, err
}

func (s *Service) DeleteBudget(ctx context.Context, userID, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		b, err := s.budgets.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		return s.budgets.SoftDelete(ctx, b, s.now())
	})
}

// checkBudgetAlerts dipanggil setelah expense dibuat: budget bulan transaksi
// yang mencakup kategorinya dievaluasi; level naik -> notifikasi (sekali per
// level). Bulan yang sudah lewat tidak dinotifikasi (PRD F7 edge case).
func (s *Service) checkBudgetAlerts(ctx context.Context, t *domain.Transaction, cat *domain.Category, loc *time.Location) error {
	if t.Type() != domain.TxExpense {
		return nil
	}
	today := domain.Today(s.now(), loc)
	d := t.Date()
	if d.Year() != today.Year() || d.Month() != today.Month() {
		return nil
	}
	rows, err := s.budgets.ListWithSpent(ctx, t.UserID(), d)
	if err != nil {
		return err
	}
	for _, r := range rows {
		b := r.Budget
		if b.CategoryID() != cat.ID() && (cat.ParentID() == nil || *cat.ParentID() != b.CategoryID()) {
			continue
		}
		if b.Amount().Currency() != t.Amount().Currency() {
			continue
		}
		notify, dirty := b.EvaluateAlert(r.Spent)
		if !dirty {
			continue
		}
		if err := s.budgets.Update(ctx, b); err != nil {
			return err
		}
		if notify != domain.AlertNone {
			s.alerts.BudgetThresholdReached(ctx, BudgetAlert{
				UserID: b.UserID(), BudgetID: b.ID(), Level: string(notify), Spent: r.Spent, Amount: b.Amount().Amount(),
			})
		}
	}
	return nil
}
