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

var _ domain.DebtRepository = (*DebtRepository)(nil)

type DebtRepository struct{ base }

func NewDebtRepository(db *pgxpool.Pool) *DebtRepository { return &DebtRepository{base{db}} }

const debtCols = `d.id, d.user_id, d.direction, d.counterparty, d.principal, d.currency, d.start_date, d.due_date,
	d.note, d.status, d.version, d.created_at, d.updated_at`

func scanDebt(row pgx.Row, extra ...any) (*domain.Debt, error) {
	var (
		s                domain.DebtState
		dir, cur, status string
		principal        int64
	)
	dest := append([]any{&s.ID, &s.UserID, &dir, &s.Counterparty, &principal, &cur, &s.StartDate, &s.DueDate,
		&s.Note, &status, &s.Version, &s.CreatedAt, &s.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrDebtNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := currency(cur)
	if err != nil {
		return nil, err
	}
	s.Direction, s.Principal, s.Status = domain.DebtDirection(dir), money.New(principal, c), domain.DebtStatus(status)
	return domain.RehydrateDebt(s), nil
}

func (r *DebtRepository) Create(ctx context.Context, d *domain.Debt) error {
	const q = `INSERT INTO debts (id, user_id, direction, counterparty, principal, currency, start_date, due_date,
		note, status, version, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	_, err := r.conn(ctx).Exec(ctx, q, d.ID(), d.UserID(), string(d.Direction()), d.Counterparty(), d.Principal().Amount(),
		d.Principal().Currency().Code(), d.StartDate(), d.DueDate(), d.Note(), string(d.Status()), d.Version(),
		d.CreatedAt(), d.UpdatedAt())
	if err != nil {
		return fmt.Errorf("debtRepo.Create: %w", err)
	}
	return nil
}

func (r *DebtRepository) get(ctx context.Context, op, suffix string, userID, id uuid.UUID) (*domain.Debt, error) {
	q := `SELECT ` + debtCols + ` FROM debts d WHERE d.id = $1 AND d.user_id = $2 AND d.deleted_at IS NULL` + suffix
	d, err := scanDebt(r.conn(ctx).QueryRow(ctx, q, id, userID))
	if err != nil && !errors.Is(err, domain.ErrDebtNotFound) {
		return nil, fmt.Errorf("debtRepo.%s: %w", op, err)
	}
	return d, err
}

func (r *DebtRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Debt, error) {
	return r.get(ctx, "Get", "", userID, id)
}

func (r *DebtRepository) GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*domain.Debt, error) {
	return r.get(ctx, "GetForUpdate", " FOR UPDATE", userID, id)
}

func (r *DebtRepository) Update(ctx context.Context, d *domain.Debt) error {
	const q = `UPDATE debts SET counterparty = $3, principal = $4, due_date = $5, note = $6, status = $7,
		updated_at = $8, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $9 AND deleted_at IS NULL RETURNING version`
	var v int
	err := r.conn(ctx).QueryRow(ctx, q, d.ID(), d.UserID(), d.Counterparty(), d.Principal().Amount(), d.DueDate(),
		d.Note(), string(d.Status()), d.UpdatedAt(), d.Version()).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.missingOrConflict(ctx, d.UserID(), d.ID())
	}
	if err != nil {
		return fmt.Errorf("debtRepo.Update: %w", err)
	}
	d.SyncVersion(v)
	return nil
}

func (r *DebtRepository) SoftDelete(ctx context.Context, d *domain.Debt, at time.Time) error {
	tag, err := r.conn(ctx).Exec(ctx, `UPDATE debts SET deleted_at = $3, updated_at = $3, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $4 AND deleted_at IS NULL`, d.ID(), d.UserID(), at, d.Version())
	if err != nil {
		return fmt.Errorf("debtRepo.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.missingOrConflict(ctx, d.UserID(), d.ID())
	}
	return nil
}

func (r *DebtRepository) missingOrConflict(ctx context.Context, userID, id uuid.UUID) error {
	var exists bool
	err := r.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM debts
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("debtRepo.exists: %w", err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrDebtNotFound
}

func (r *DebtRepository) List(ctx context.Context, userID uuid.UUID, status *domain.DebtStatus) ([]domain.DebtWithPaid, error) {
	var st *string
	if status != nil {
		s := string(*status)
		st = &s
	}
	q := `SELECT ` + debtCols + `, COALESCE((SELECT SUM(p.amount) FROM debt_payments p WHERE p.debt_id = d.id), 0)
		FROM debts d WHERE d.user_id = $1 AND d.deleted_at IS NULL AND ($2::text IS NULL OR d.status = $2)
		ORDER BY d.created_at DESC, d.id DESC`
	rows, err := r.conn(ctx).Query(ctx, q, userID, st)
	if err != nil {
		return nil, fmt.Errorf("debtRepo.List: %w", err)
	}
	defer rows.Close()
	out := []domain.DebtWithPaid{}
	for rows.Next() {
		var paid int64
		d, err := scanDebt(rows, &paid)
		if err != nil {
			return nil, fmt.Errorf("debtRepo.List scan: %w", err)
		}
		out = append(out, domain.DebtWithPaid{Debt: d, Paid: paid})
	}
	return out, rows.Err()
}

func (r *DebtRepository) Paid(ctx context.Context, userID, debtID uuid.UUID) (int64, error) {
	var paid int64
	err := r.conn(ctx).QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM debt_payments
		WHERE debt_id = $1 AND user_id = $2`, debtID, userID).Scan(&paid)
	if err != nil {
		return 0, fmt.Errorf("debtRepo.Paid: %w", err)
	}
	return paid, nil
}

func (r *DebtRepository) AddPayment(ctx context.Context, p *domain.DebtPayment) error {
	const q = `INSERT INTO debt_payments (id, debt_id, user_id, amount, payment_date, transaction_id, note, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	_, err := r.conn(ctx).Exec(ctx, q, p.ID, p.DebtID, p.UserID, p.Amount.Amount(), p.Date, p.TransactionID, p.Note, p.CreatedAt)
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrDuplicateLink
	}
	if err != nil {
		return fmt.Errorf("debtRepo.AddPayment: %w", err)
	}
	return nil
}

func (r *DebtRepository) ListPayments(ctx context.Context, userID, debtID uuid.UUID) ([]*domain.DebtPayment, error) {
	const q = `SELECT p.id, p.debt_id, p.user_id, p.amount, d.currency, p.payment_date, p.transaction_id, p.note, p.created_at
		FROM debt_payments p JOIN debts d ON d.id = p.debt_id
		WHERE p.debt_id = $1 AND p.user_id = $2 ORDER BY p.payment_date DESC, p.id DESC`
	rows, err := r.conn(ctx).Query(ctx, q, debtID, userID)
	if err != nil {
		return nil, fmt.Errorf("debtRepo.ListPayments: %w", err)
	}
	defer rows.Close()
	out := []*domain.DebtPayment{}
	for rows.Next() {
		var (
			p      domain.DebtPayment
			amount int64
			cur    string
		)
		if err := rows.Scan(&p.ID, &p.DebtID, &p.UserID, &amount, &cur, &p.Date, &p.TransactionID, &p.Note, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("debtRepo.ListPayments scan: %w", err)
		}
		c, err := currency(cur)
		if err != nil {
			return nil, err
		}
		p.Amount, p.Date = money.New(amount, c), domain.DateOf(p.Date)
		out = append(out, &p)
	}
	return out, rows.Err()
}

// DeletePayment: cicilan yang tidak ditemukan -> ErrDebtNotFound (404).
func (r *DebtRepository) DeletePayment(ctx context.Context, userID, debtID, id uuid.UUID) error {
	tag, err := r.conn(ctx).Exec(ctx, `DELETE FROM debt_payments WHERE id = $1 AND debt_id = $2 AND user_id = $3`, id, debtID, userID)
	if err != nil {
		return fmt.Errorf("debtRepo.DeletePayment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDebtNotFound
	}
	return nil
}
