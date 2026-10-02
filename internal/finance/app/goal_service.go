package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

// Goal adalah savings goal beserta progress-nya (dihitung saat dibaca).
type Goal struct {
	Goal     *domain.SavingsGoal
	Progress domain.GoalProgress
}

type CreateGoalInput struct {
	UserID     uuid.UUID
	Name       string
	Target     string
	Currency   string // kosong = currency akun, atau base currency
	TargetDate *time.Time
	AccountID  *uuid.UUID
}

// UpdateGoalInput: nil = tetap. ClearX menghapus nilai opsional.
type UpdateGoalInput struct {
	UserID          uuid.UUID
	ID              uuid.UUID
	ExpectedVersion *int
	Name            *string
	Target          *string
	TargetDate      *time.Time
	ClearTargetDate bool
	AccountID       *uuid.UUID
	ClearAccount    bool
	Archived        *bool
}

// ContributeInput: TransferID opsional (amount & tanggal diambil dari transfer).
type ContributeInput struct {
	UserID     uuid.UUID
	GoalID     uuid.UUID
	Amount     string // positif; tanda ditentukan Withdraw
	Date       time.Time
	TransferID *uuid.UUID
	Withdraw   bool
	Note       string
}

var errGoalsDisabled = &domain.ValidationError{Field: "goal", Reason: "savings goals are not enabled"}

func (s *Service) CreateGoal(ctx context.Context, in CreateGoalInput) (*Goal, error) {
	if s.goals == nil {
		return nil, errGoalsDisabled
	}
	var acc *domain.Account
	if in.AccountID != nil {
		a, err := s.accounts.Get(ctx, in.UserID, *in.AccountID)
		if err != nil {
			return nil, err
		}
		acc = a
	}
	code := in.Currency
	if code == "" && acc != nil {
		code = acc.Currency().Code()
	}
	cur, err := s.currencyOrDefault(ctx, in.UserID, code)
	if err != nil {
		return nil, err
	}
	target, err := money.Parse(in.Target, cur)
	if err != nil {
		return nil, &domain.ValidationError{Field: "target_amount", Reason: err.Error()}
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	g, err := domain.NewSavingsGoal(domain.NewGoalParams{ID: id, UserID: in.UserID, Name: in.Name, Target: target,
		TargetDate: in.TargetDate, Account: acc, Now: s.now()})
	if err != nil {
		return nil, err
	}
	if err := s.goals.Create(ctx, g); err != nil {
		return nil, err
	}
	return s.withProgress(ctx, g, 0)
}

func (s *Service) withProgress(ctx context.Context, g *domain.SavingsGoal, saved int64) (*Goal, error) {
	loc, err := s.userLocation(ctx, g.UserID())
	if err != nil {
		return nil, err
	}
	return &Goal{Goal: g, Progress: g.Progress(saved, domain.Today(s.now(), loc))}, nil
}

func (s *Service) GetGoal(ctx context.Context, userID, id uuid.UUID) (*Goal, error) {
	if s.goals == nil {
		return nil, domain.ErrGoalNotFound
	}
	g, err := s.goals.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	saved, err := s.goals.Saved(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return s.withProgress(ctx, g, saved)
}

func (s *Service) ListGoals(ctx context.Context, userID uuid.UUID) ([]*Goal, error) {
	if s.goals == nil {
		return []*Goal{}, nil
	}
	list, err := s.goals.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	loc, err := s.userLocation(ctx, userID)
	if err != nil {
		return nil, err
	}
	today := domain.Today(s.now(), loc)
	out := make([]*Goal, 0, len(list))
	for _, x := range list {
		out = append(out, &Goal{Goal: x.Goal, Progress: x.Goal.Progress(x.Saved, today)})
	}
	return out, nil
}

func (s *Service) UpdateGoal(ctx context.Context, in UpdateGoalInput) (*Goal, error) {
	if s.goals == nil {
		return nil, domain.ErrGoalNotFound
	}
	var out *Goal
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		g, err := s.goals.GetForUpdate(ctx, in.UserID, in.ID)
		if err != nil {
			return err
		}
		if err := checkVersion(in.ExpectedVersion, g.Version()); err != nil {
			return err
		}
		now := s.now()
		c := domain.GoalChange{Name: in.Name, TargetDate: in.TargetDate, ClearTargetDate: in.ClearTargetDate,
			ClearAccount: in.ClearAccount, Archived: in.Archived, Now: now}
		if in.Target != nil {
			t, err := money.Parse(*in.Target, g.Target().Currency())
			if err != nil {
				return &domain.ValidationError{Field: "target_amount", Reason: err.Error()}
			}
			c.Target = &t
		}
		if in.AccountID != nil && !in.ClearAccount {
			if c.Account, err = s.accounts.Get(ctx, in.UserID, *in.AccountID); err != nil {
				return err
			}
		}
		if err := g.Update(c); err != nil {
			return err
		}
		saved, err := s.goals.Saved(ctx, in.UserID, in.ID)
		if err != nil {
			return err
		}
		g.Refresh(saved, now)
		if err := s.goals.Update(ctx, g); err != nil {
			return err
		}
		out, err = s.withProgress(ctx, g, saved)
		return err
	})
	return out, err
}

func (s *Service) DeleteGoal(ctx context.Context, userID, id uuid.UUID) error {
	if s.goals == nil {
		return domain.ErrGoalNotFound
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		g, err := s.goals.GetForUpdate(ctx, userID, id)
		if err != nil {
			return err
		}
		return s.goals.SoftDelete(ctx, g, s.now())
	})
}

// Contribute mencatat setoran/penarikan goal lalu memperbarui status achieved.
// Goal dikunci FOR UPDATE agar total kontribusi konsisten.
func (s *Service) Contribute(ctx context.Context, in ContributeInput) (*domain.GoalContribution, *Goal, error) {
	if s.goals == nil {
		return nil, nil, domain.ErrGoalNotFound
	}
	var (
		c   *domain.GoalContribution
		out *Goal
	)
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		g, err := s.goals.GetForUpdate(ctx, in.UserID, in.GoalID)
		if err != nil {
			return err
		}
		loc, err := s.userLocation(ctx, in.UserID)
		if err != nil {
			return err
		}
		p := domain.NewContributionParams{Goal: g, Date: in.Date, Withdraw: in.Withdraw, Note: in.Note, Now: s.now(), Location: loc}
		if in.TransferID != nil {
			// hanya transfer milik user sendiri (goal tidak dibagikan)
			if p.Transfer, err = s.transfers.Get(ctx, in.UserID, *in.TransferID); err != nil {
				return err
			}
		} else {
			amt, err := money.Parse(in.Amount, g.Target().Currency())
			if err != nil {
				return &domain.ValidationError{Field: "amount", Reason: err.Error()}
			}
			if in.Withdraw {
				if amt, err = amt.Neg(); err != nil {
					return err
				}
			}
			p.Amount = amt
		}
		if p.ID, err = s.newID(); err != nil {
			return err
		}
		saved, err := s.goals.Saved(ctx, in.UserID, in.GoalID)
		if err != nil {
			return err
		}
		if c, err = domain.NewGoalContribution(p); err != nil {
			return err
		}
		if in.Withdraw && saved+c.Amount.Amount() < 0 {
			return &domain.ValidationError{Field: "amount", Reason: "withdrawal exceeds saved amount"}
		}
		if err := s.goals.AddContribution(ctx, c); err != nil {
			return err
		}
		saved += c.Amount.Amount()
		if g.Refresh(saved, s.now()) {
			if err := s.goals.Update(ctx, g); err != nil {
				return err
			}
		}
		out, err = s.withProgress(ctx, g, saved)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return c, out, nil
}

func (s *Service) ListContributions(ctx context.Context, userID, goalID uuid.UUID) ([]*domain.GoalContribution, error) {
	if s.goals == nil {
		return nil, domain.ErrGoalNotFound
	}
	if _, err := s.goals.Get(ctx, userID, goalID); err != nil {
		return nil, err
	}
	return s.goals.ListContributions(ctx, userID, goalID)
}

// DeleteContribution menghapus kontribusi (mis. salah input) dan menyelaraskan status.
func (s *Service) DeleteContribution(ctx context.Context, userID, goalID, id uuid.UUID) error {
	if s.goals == nil {
		return domain.ErrGoalNotFound
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		g, err := s.goals.GetForUpdate(ctx, userID, goalID)
		if err != nil {
			return err
		}
		if err := s.goals.DeleteContribution(ctx, userID, goalID, id); err != nil {
			return err
		}
		saved, err := s.goals.Saved(ctx, userID, goalID)
		if err != nil {
			return err
		}
		if g.Refresh(saved, s.now()) {
			return s.goals.Update(ctx, g)
		}
		return nil
	})
}
