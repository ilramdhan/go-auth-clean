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

var _ domain.BillRepository = (*BillRepository)(nil)

type BillRepository struct{ base }

func NewBillRepository(db *pgxpool.Pool) *BillRepository { return &BillRepository{base{db}} }

const billCols = `id, user_id, name, amount, currency, account_id, category_id, frequency, by_month_day,
	next_due_date, remind_days_before, last_reminded_for, overdue_for, status, version, created_at, updated_at`

func scanBill(row pgx.Row) (*domain.Bill, error) {
	var (
		s                 domain.BillState
		cur, freq, status string
		amount            int64
		monthDay, remind  int16
	)
	err := row.Scan(&s.ID, &s.UserID, &s.Name, &amount, &cur, &s.AccountID, &s.CategoryID, &freq, &monthDay,
		&s.NextDue, &remind, &s.LastReminded, &s.OverdueFor, &status, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrBillNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := currency(cur)
	if err != nil {
		return nil, err
	}
	s.Amount, s.Frequency, s.Status = money.New(amount, c), domain.Frequency(freq), domain.BillStatus(status)
	s.MonthDay, s.RemindDays = int(monthDay), int(remind)
	return domain.RehydrateBill(s), nil
}

func (r *BillRepository) Create(ctx context.Context, b *domain.Bill) error {
	q := `INSERT INTO bills (` + billCols + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`
	_, err := r.conn(ctx).Exec(ctx, q, b.ID(), b.UserID(), b.Name(), b.Amount().Amount(), b.Amount().Currency().Code(),
		b.AccountID(), b.CategoryID(), string(b.Frequency()), b.MonthDay(), b.NextDueDate(), b.RemindDays(),
		b.LastRemindedFor(), b.OverdueFor(), string(b.Status()), b.Version(), b.CreatedAt(), b.UpdatedAt())
	if err != nil {
		return fmt.Errorf("billRepo.Create: %w", err)
	}
	return nil
}

func (r *BillRepository) get(ctx context.Context, op, suffix string, userID, id uuid.UUID) (*domain.Bill, error) {
	q := `SELECT ` + billCols + ` FROM bills WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL` + suffix
	b, err := scanBill(r.conn(ctx).QueryRow(ctx, q, id, userID))
	if err != nil && !errors.Is(err, domain.ErrBillNotFound) {
		return nil, fmt.Errorf("billRepo.%s: %w", op, err)
	}
	return b, err
}

func (r *BillRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Bill, error) {
	return r.get(ctx, "Get", "", userID, id)
}

func (r *BillRepository) GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*domain.Bill, error) {
	return r.get(ctx, "GetForUpdate", " FOR UPDATE", userID, id)
}

func (r *BillRepository) Update(ctx context.Context, b *domain.Bill) error {
	const q = `UPDATE bills SET name = $3, amount = $4, account_id = $5, category_id = $6, frequency = $7,
		by_month_day = $8, next_due_date = $9, remind_days_before = $10, last_reminded_for = $11, overdue_for = $12,
		status = $13, updated_at = $14, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $15 AND deleted_at IS NULL RETURNING version`
	var v int
	err := r.conn(ctx).QueryRow(ctx, q, b.ID(), b.UserID(), b.Name(), b.Amount().Amount(), b.AccountID(), b.CategoryID(),
		string(b.Frequency()), b.MonthDay(), b.NextDueDate(), b.RemindDays(), b.LastRemindedFor(), b.OverdueFor(),
		string(b.Status()), b.UpdatedAt(), b.Version()).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.missingOrConflict(ctx, b.UserID(), b.ID())
	}
	if err != nil {
		return fmt.Errorf("billRepo.Update: %w", err)
	}
	b.SyncVersion(v)
	return nil
}

func (r *BillRepository) SoftDelete(ctx context.Context, b *domain.Bill, at time.Time) error {
	tag, err := r.conn(ctx).Exec(ctx, `UPDATE bills SET deleted_at = $3, updated_at = $3, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $4 AND deleted_at IS NULL`, b.ID(), b.UserID(), at, b.Version())
	if err != nil {
		return fmt.Errorf("billRepo.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.missingOrConflict(ctx, b.UserID(), b.ID())
	}
	return nil
}

func (r *BillRepository) missingOrConflict(ctx context.Context, userID, id uuid.UUID) error {
	var exists bool
	err := r.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bills
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("billRepo.exists: %w", err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrBillNotFound
}

func (r *BillRepository) many(ctx context.Context, op, q string, args ...any) ([]*domain.Bill, error) {
	rows, err := r.conn(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("billRepo.%s: %w", op, err)
	}
	defer rows.Close()
	out := []*domain.Bill{}
	for rows.Next() {
		b, err := scanBill(rows)
		if err != nil {
			return nil, fmt.Errorf("billRepo.%s scan: %w", op, err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BillRepository) List(ctx context.Context, userID uuid.UUID) ([]*domain.Bill, error) {
	return r.many(ctx, "List", `SELECT `+billCols+` FROM bills WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY next_due_date, id`, userID)
}

// ClaimAttention: tagihan aktif yang sudah masuk jendela pengingat dan belum
// diproses untuk jatuh tempo saat ini — belum diingatkan, atau sudah lewat
// jatuh tempo tetapi belum ditandai overdue.
func (r *BillRepository) ClaimAttention(ctx context.Context, before time.Time, limit int, exclude []uuid.UUID) ([]*domain.Bill, error) {
	if exclude == nil {
		exclude = []uuid.UUID{}
	}
	return r.many(ctx, "ClaimAttention", `SELECT `+billCols+` FROM bills
		WHERE status = 'active' AND deleted_at IS NULL
		  AND next_due_date - remind_days_before <= $1::date
		  AND overdue_for IS DISTINCT FROM next_due_date
		  AND (last_reminded_for IS DISTINCT FROM next_due_date OR next_due_date < $1::date)
		  AND NOT (id = ANY($3))
		ORDER BY next_due_date, id LIMIT $2 FOR UPDATE SKIP LOCKED`, domain.DateOf(before), limit, exclude)
}
