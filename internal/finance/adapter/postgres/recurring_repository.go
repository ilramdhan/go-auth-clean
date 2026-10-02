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

var _ domain.RecurringRepository = (*RecurringRepository)(nil)

type RecurringRepository struct{ base }

func NewRecurringRepository(db *pgxpool.Pool) *RecurringRepository {
	return &RecurringRepository{base{db}}
}

const ruleCols = `id, user_id, account_id, category_id, type, amount, currency, note, frequency,
	interval_n, by_month_day, start_date, end_date, next_run_date, last_run_date, status,
	pause_reason, version, created_at, updated_at`

func scanRule(row pgx.Row) (*domain.RecurringRule, error) {
	var (
		s                      domain.RecurringRuleState
		typ, cur, freq, status string
		amount                 int64
		interval, monthDay     int16
	)
	err := row.Scan(&s.ID, &s.UserID, &s.AccountID, &s.CategoryID, &typ, &amount, &cur, &s.Note, &freq,
		&interval, &monthDay, &s.StartDate, &s.EndDate, &s.NextRunDate, &s.LastRunDate, &status,
		&s.PauseReason, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrRecurringNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := currency(cur)
	if err != nil {
		return nil, err
	}
	s.Type, s.Amount, s.Frequency = domain.TxType(typ), money.New(amount, c), domain.Frequency(freq)
	s.Interval, s.MonthDay, s.Status = int(interval), int(monthDay), domain.RuleStatus(status)
	return domain.RehydrateRecurringRule(s), nil
}

func (r *RecurringRepository) Create(ctx context.Context, x *domain.RecurringRule) error {
	q := `INSERT INTO recurring_rules (` + ruleCols + `)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`
	_, err := r.conn(ctx).Exec(ctx, q, x.ID(), x.UserID(), x.AccountID(), x.CategoryID(), string(x.Type()),
		x.Amount().Amount(), x.Amount().Currency().Code(), x.Note(), string(x.Frequency()), x.Interval(),
		x.MonthDay(), x.StartDate(), x.EndDate(), x.NextRunDate(), x.LastRunDate(), string(x.Status()),
		x.PauseReason(), x.Version(), x.CreatedAt(), x.UpdatedAt())
	if err != nil {
		return fmt.Errorf("recurringRepo.Create: %w", err)
	}
	return nil
}

func (r *RecurringRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.RecurringRule, error) {
	q := `SELECT ` + ruleCols + ` FROM recurring_rules WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	x, err := scanRule(r.conn(ctx).QueryRow(ctx, q, id, userID))
	if err != nil && !errors.Is(err, domain.ErrRecurringNotFound) {
		return nil, fmt.Errorf("recurringRepo.Get: %w", err)
	}
	return x, err
}

func (r *RecurringRepository) Update(ctx context.Context, x *domain.RecurringRule) error {
	const q = `UPDATE recurring_rules SET account_id = $3, category_id = $4, type = $5, amount = $6,
		currency = $7, note = $8, frequency = $9, interval_n = $10, by_month_day = $11, start_date = $12,
		end_date = $13, next_run_date = $14, last_run_date = $15, status = $16, pause_reason = $17,
		updated_at = $18, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $19 AND deleted_at IS NULL RETURNING version`
	var v int
	err := r.conn(ctx).QueryRow(ctx, q, x.ID(), x.UserID(), x.AccountID(), x.CategoryID(), string(x.Type()),
		x.Amount().Amount(), x.Amount().Currency().Code(), x.Note(), string(x.Frequency()), x.Interval(),
		x.MonthDay(), x.StartDate(), x.EndDate(), x.NextRunDate(), x.LastRunDate(), string(x.Status()),
		x.PauseReason(), x.UpdatedAt(), x.Version()).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.missingOrConflict(ctx, x.UserID(), x.ID())
	}
	if err != nil {
		return fmt.Errorf("recurringRepo.Update: %w", err)
	}
	x.SyncVersion(v)
	return nil
}

func (r *RecurringRepository) SoftDelete(ctx context.Context, x *domain.RecurringRule, at time.Time) error {
	const q = `UPDATE recurring_rules SET deleted_at = $3, updated_at = $3, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $4 AND deleted_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, x.ID(), x.UserID(), at, x.Version())
	if err != nil {
		return fmt.Errorf("recurringRepo.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.missingOrConflict(ctx, x.UserID(), x.ID())
	}
	return nil
}

func (r *RecurringRepository) missingOrConflict(ctx context.Context, userID, id uuid.UUID) error {
	var exists bool
	err := r.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM recurring_rules
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("recurringRepo.exists: %w", err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrRecurringNotFound
}

func (r *RecurringRepository) many(ctx context.Context, op, q string, args ...any) ([]*domain.RecurringRule, error) {
	rows, err := r.conn(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("recurringRepo.%s: %w", op, err)
	}
	defer rows.Close()
	out := []*domain.RecurringRule{}
	for rows.Next() {
		x, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("recurringRepo.%s scan: %w", op, err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// List keyset (created_at, id) DESC; PageKey.Date dipakai sebagai created_at.
func (r *RecurringRepository) List(ctx context.Context, userID uuid.UUID, limit int, after *domain.PageKey) ([]*domain.RecurringRule, error) {
	if limit <= 0 {
		limit = 20
	}
	if after == nil {
		return r.many(ctx, "List", `SELECT `+ruleCols+` FROM recurring_rules
			WHERE user_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC, id DESC LIMIT $2`, userID, limit+1)
	}
	return r.many(ctx, "List", `SELECT `+ruleCols+` FROM recurring_rules
		WHERE user_id = $1 AND deleted_at IS NULL AND (created_at, id) < ($3, $4)
		ORDER BY created_at DESC, id DESC LIMIT $2`, userID, limit+1, after.Date, after.ID)
}

// ClaimDue mengunci rule jatuh tempo dengan FOR UPDATE SKIP LOCKED sehingga
// beberapa instance worker tidak memproses rule yang sama bersamaan.
func (r *RecurringRepository) ClaimDue(ctx context.Context, before time.Time, limit int, exclude []uuid.UUID) ([]*domain.RecurringRule, error) {
	if exclude == nil {
		exclude = []uuid.UUID{}
	}
	return r.many(ctx, "ClaimDue", `SELECT `+ruleCols+` FROM recurring_rules
		WHERE status = 'active' AND deleted_at IS NULL AND next_run_date <= $1 AND NOT (id = ANY($3))
		ORDER BY next_run_date, id LIMIT $2 FOR UPDATE SKIP LOCKED`, domain.DateOf(before), limit, exclude)
}
