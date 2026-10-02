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

var _ domain.BudgetRepository = (*BudgetRepository)(nil)

type BudgetRepository struct{ base }

func NewBudgetRepository(db *pgxpool.Pool) *BudgetRepository {
	return &BudgetRepository{base{db}}
}

const budgetCols = `b.id, b.user_id, b.category_id, b.period_month, b.amount, b.currency,
	b.alert_threshold_pct, COALESCE(b.last_alert_level, ''), b.version, b.created_at, b.updated_at`

// spentSQL = total expense kategori budget + sub kategorinya, currency sama,
// dalam bulan budget, tidak terhapus. Dipakai sebagai LATERAL subquery.
const spentSQL = `SELECT COALESCE(SUM(t.amount), 0) AS spent FROM transactions t
	WHERE t.user_id = b.user_id AND t.deleted_at IS NULL AND t.type = 'expense'
	  AND t.currency = b.currency
	  AND t.transaction_date >= b.period_month
	  AND t.transaction_date < (b.period_month + INTERVAL '1 month')
	  AND t.category_id IN (SELECT c.id FROM categories c WHERE c.id = b.category_id OR c.parent_id = b.category_id)`

func scanBudget(row pgx.Row, extra ...any) (*domain.Budget, error) {
	var (
		s          domain.BudgetState
		cur, alert string
		amount     int64
		threshold  int16
	)
	dest := append([]any{&s.ID, &s.UserID, &s.CategoryID, &s.Month, &amount, &cur,
		&threshold, &alert, &s.Version, &s.CreatedAt, &s.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrBudgetNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := currency(cur)
	if err != nil {
		return nil, err
	}
	s.Amount, s.Threshold, s.LastAlert = money.New(amount, c), int(threshold), domain.AlertLevel(alert)
	return domain.RehydrateBudget(s), nil
}

func nullableAlert(l domain.AlertLevel) *string {
	if l == domain.AlertNone {
		return nil
	}
	s := string(l)
	return &s
}

func (r *BudgetRepository) Create(ctx context.Context, b *domain.Budget) error {
	const q = `INSERT INTO budgets (id, user_id, category_id, period_month, amount, currency,
		alert_threshold_pct, last_alert_level, version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	_, err := r.conn(ctx).Exec(ctx, q, b.ID(), b.UserID(), b.CategoryID(), b.Month(), b.Amount().Amount(),
		b.Amount().Currency().Code(), b.Threshold(), nullableAlert(b.LastAlert()), b.Version(), b.CreatedAt(), b.UpdatedAt())
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrBudgetExists
	}
	if err != nil {
		return fmt.Errorf("budgetRepo.Create: %w", err)
	}
	return nil
}

func (r *BudgetRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Budget, error) {
	q := `SELECT ` + budgetCols + ` FROM budgets b WHERE b.id = $1 AND b.user_id = $2 AND b.deleted_at IS NULL`
	b, err := scanBudget(r.conn(ctx).QueryRow(ctx, q, id, userID))
	if err != nil && !errors.Is(err, domain.ErrBudgetNotFound) {
		return nil, fmt.Errorf("budgetRepo.Get: %w", err)
	}
	return b, err
}

func (r *BudgetRepository) Update(ctx context.Context, b *domain.Budget) error {
	const q = `UPDATE budgets SET amount = $3, alert_threshold_pct = $4, last_alert_level = $5,
		updated_at = $6, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $7 AND deleted_at IS NULL RETURNING version`
	var v int
	err := r.conn(ctx).QueryRow(ctx, q, b.ID(), b.UserID(), b.Amount().Amount(), b.Threshold(),
		nullableAlert(b.LastAlert()), b.UpdatedAt(), b.Version()).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.missingOrConflict(ctx, b.UserID(), b.ID())
	}
	if err != nil {
		return fmt.Errorf("budgetRepo.Update: %w", err)
	}
	b.SyncVersion(v)
	return nil
}

func (r *BudgetRepository) SoftDelete(ctx context.Context, b *domain.Budget, at time.Time) error {
	const q = `UPDATE budgets SET deleted_at = $3, updated_at = $3, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $4 AND deleted_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, b.ID(), b.UserID(), at, b.Version())
	if err != nil {
		return fmt.Errorf("budgetRepo.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.missingOrConflict(ctx, b.UserID(), b.ID())
	}
	return nil
}

func (r *BudgetRepository) missingOrConflict(ctx context.Context, userID, id uuid.UUID) error {
	var exists bool
	err := r.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM budgets
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("budgetRepo.exists: %w", err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrBudgetNotFound
}

func (r *BudgetRepository) ListWithSpent(ctx context.Context, userID uuid.UUID, month time.Time) ([]domain.BudgetSpent, error) {
	m := domain.DateOf(month)
	m = time.Date(m.Year(), m.Month(), 1, 0, 0, 0, 0, time.UTC)
	q := `SELECT ` + budgetCols + `, s.spent FROM budgets b
		LEFT JOIN LATERAL (` + spentSQL + `) s ON true
		WHERE b.user_id = $1 AND b.period_month = $2 AND b.deleted_at IS NULL
		ORDER BY b.created_at, b.id`
	rows, err := r.conn(ctx).Query(ctx, q, userID, m)
	if err != nil {
		return nil, fmt.Errorf("budgetRepo.ListWithSpent: %w", err)
	}
	defer rows.Close()
	out := []domain.BudgetSpent{}
	for rows.Next() {
		var spent int64
		b, err := scanBudget(rows, &spent)
		if err != nil {
			return nil, fmt.Errorf("budgetRepo.ListWithSpent scan: %w", err)
		}
		out = append(out, domain.BudgetSpent{Budget: b, Spent: spent})
	}
	return out, rows.Err()
}

func (r *BudgetRepository) Spent(ctx context.Context, b *domain.Budget) (int64, error) {
	q := `SELECT s.spent FROM budgets b LEFT JOIN LATERAL (` + spentSQL + `) s ON true WHERE b.id = $1 AND b.user_id = $2`
	var spent int64
	err := r.conn(ctx).QueryRow(ctx, q, b.ID(), b.UserID()).Scan(&spent)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrBudgetNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("budgetRepo.Spent: %w", err)
	}
	return spent, nil
}
