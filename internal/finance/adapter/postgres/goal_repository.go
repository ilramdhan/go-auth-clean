package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

var _ domain.GoalRepository = (*GoalRepository)(nil)

type GoalRepository struct{ base }

func NewGoalRepository(db *pgxpool.Pool) *GoalRepository { return &GoalRepository{base{db}} }

const goalCols = `g.id, g.user_id, g.name, g.target_amount, g.currency, g.target_date, g.account_id,
	g.status, g.version, g.created_at, g.updated_at`

func scanGoal(row pgx.Row, extra ...any) (*domain.SavingsGoal, error) {
	var (
		s           domain.SavingsGoalState
		cur, status string
		target      int64
	)
	dest := append([]any{&s.ID, &s.UserID, &s.Name, &target, &cur, &s.TargetDate, &s.AccountID,
		&status, &s.Version, &s.CreatedAt, &s.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGoalNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := currency(cur)
	if err != nil {
		return nil, err
	}
	s.Target, s.Status = money.New(target, c), domain.GoalStatus(status)
	return domain.RehydrateSavingsGoal(s), nil
}

func (r *GoalRepository) Create(ctx context.Context, g *domain.SavingsGoal) error {
	const q = `INSERT INTO savings_goals (id, user_id, name, target_amount, currency, target_date, account_id,
		status, version, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	_, err := r.conn(ctx).Exec(ctx, q, g.ID(), g.UserID(), g.Name(), g.Target().Amount(), g.Target().Currency().Code(),
		g.TargetDate(), g.AccountID(), string(g.Status()), g.Version(), g.CreatedAt(), g.UpdatedAt())
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrDuplicateName
	}
	if err != nil {
		return fmt.Errorf("goalRepo.Create: %w", err)
	}
	return nil
}

func (r *GoalRepository) get(ctx context.Context, op, suffix string, userID, id uuid.UUID) (*domain.SavingsGoal, error) {
	q := `SELECT ` + goalCols + ` FROM savings_goals g WHERE g.id = $1 AND g.user_id = $2 AND g.deleted_at IS NULL` + suffix
	g, err := scanGoal(r.conn(ctx).QueryRow(ctx, q, id, userID))
	if err != nil && !errors.Is(err, domain.ErrGoalNotFound) {
		return nil, fmt.Errorf("goalRepo.%s: %w", op, err)
	}
	return g, err
}

func (r *GoalRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.SavingsGoal, error) {
	return r.get(ctx, "Get", "", userID, id)
}

func (r *GoalRepository) GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*domain.SavingsGoal, error) {
	return r.get(ctx, "GetForUpdate", " FOR UPDATE", userID, id)
}

func (r *GoalRepository) Update(ctx context.Context, g *domain.SavingsGoal) error {
	const q = `UPDATE savings_goals SET name = $3, target_amount = $4, target_date = $5, account_id = $6,
		status = $7, updated_at = $8, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $9 AND deleted_at IS NULL RETURNING version`
	var v int
	err := r.conn(ctx).QueryRow(ctx, q, g.ID(), g.UserID(), g.Name(), g.Target().Amount(), g.TargetDate(),
		g.AccountID(), string(g.Status()), g.UpdatedAt(), g.Version()).Scan(&v)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return r.missingOrConflict(ctx, g.UserID(), g.ID())
	case pgCode(err) == pgUniqueViolation:
		return domain.ErrDuplicateName
	case err != nil:
		return fmt.Errorf("goalRepo.Update: %w", err)
	}
	g.SyncVersion(v)
	return nil
}

func (r *GoalRepository) SoftDelete(ctx context.Context, g *domain.SavingsGoal, at time.Time) error {
	tag, err := r.conn(ctx).Exec(ctx, `UPDATE savings_goals SET deleted_at = $3, updated_at = $3, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $4 AND deleted_at IS NULL`, g.ID(), g.UserID(), at, g.Version())
	if err != nil {
		return fmt.Errorf("goalRepo.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.missingOrConflict(ctx, g.UserID(), g.ID())
	}
	return nil
}

func (r *GoalRepository) missingOrConflict(ctx context.Context, userID, id uuid.UUID) error {
	var exists bool
	err := r.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM savings_goals
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("goalRepo.exists: %w", err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrGoalNotFound
}

func (r *GoalRepository) List(ctx context.Context, userID uuid.UUID) ([]domain.GoalWithSaved, error) {
	q := `SELECT ` + goalCols + `, COALESCE((SELECT SUM(c.amount) FROM goal_contributions c WHERE c.goal_id = g.id), 0)
		FROM savings_goals g WHERE g.user_id = $1 AND g.deleted_at IS NULL ORDER BY g.created_at, g.id`
	rows, err := r.conn(ctx).Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("goalRepo.List: %w", err)
	}
	defer rows.Close()
	out := []domain.GoalWithSaved{}
	for rows.Next() {
		var saved int64
		g, err := scanGoal(rows, &saved)
		if err != nil {
			return nil, fmt.Errorf("goalRepo.List scan: %w", err)
		}
		out = append(out, domain.GoalWithSaved{Goal: g, Saved: saved})
	}
	return out, rows.Err()
}

func (r *GoalRepository) Saved(ctx context.Context, userID, goalID uuid.UUID) (int64, error) {
	var saved int64
	err := r.conn(ctx).QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM goal_contributions
		WHERE goal_id = $1 AND user_id = $2`, goalID, userID).Scan(&saved)
	if err != nil {
		return 0, fmt.Errorf("goalRepo.Saved: %w", err)
	}
	return saved, nil
}

func (r *GoalRepository) AddContribution(ctx context.Context, c *domain.GoalContribution) error {
	const q = `INSERT INTO goal_contributions (id, goal_id, user_id, amount, contribution_date, transfer_id, note, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	_, err := r.conn(ctx).Exec(ctx, q, c.ID, c.GoalID, c.UserID, c.Amount.Amount(), c.Date, c.TransferID, c.Note, c.CreatedAt)
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrDuplicateLink
	}
	if err != nil {
		return fmt.Errorf("goalRepo.AddContribution: %w", err)
	}
	return nil
}

func (r *GoalRepository) ListContributions(ctx context.Context, userID, goalID uuid.UUID) ([]*domain.GoalContribution, error) {
	const q = `SELECT c.id, c.goal_id, c.user_id, c.amount, g.currency, c.contribution_date, c.transfer_id, c.note, c.created_at
		FROM goal_contributions c JOIN savings_goals g ON g.id = c.goal_id
		WHERE c.goal_id = $1 AND c.user_id = $2 ORDER BY c.contribution_date DESC, c.id DESC`
	rows, err := r.conn(ctx).Query(ctx, q, goalID, userID)
	if err != nil {
		return nil, fmt.Errorf("goalRepo.ListContributions: %w", err)
	}
	defer rows.Close()
	out := []*domain.GoalContribution{}
	for rows.Next() {
		var (
			c      domain.GoalContribution
			amount int64
			cur    string
		)
		if err := rows.Scan(&c.ID, &c.GoalID, &c.UserID, &amount, &cur, &c.Date, &c.TransferID, &c.Note, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("goalRepo.ListContributions scan: %w", err)
		}
		cc, err := currency(cur)
		if err != nil {
			return nil, err
		}
		c.Amount, c.Date = money.New(amount, cc), domain.DateOf(c.Date)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *GoalRepository) DeleteContribution(ctx context.Context, userID, goalID, id uuid.UUID) error {
	tag, err := r.conn(ctx).Exec(ctx, `DELETE FROM goal_contributions WHERE id = $1 AND goal_id = $2 AND user_id = $3`, id, goalID, userID)
	if err != nil {
		return fmt.Errorf("goalRepo.DeleteContribution: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrContributionNotFound
	}
	return nil
}
