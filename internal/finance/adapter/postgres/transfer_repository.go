package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

var _ domain.TransferRepository = (*TransferRepository)(nil)

type TransferRepository struct{ base }

func NewTransferRepository(db *pgxpool.Pool) *TransferRepository {
	return &TransferRepository{base{db}}
}

const transferCols = `id, user_id, from_account_id, to_account_id, amount, fee_amount, currency,
	fee_transaction_id, transfer_date, note, version, created_at, updated_at, to_amount, to_currency`

func scanTransfer(row pgx.Row) (*domain.Transfer, error) {
	var (
		s                     domain.TransferState
		cur, toCur            string
		amount, fee, toAmount int64
	)
	err := row.Scan(&s.ID, &s.UserID, &s.FromAccountID, &s.ToAccountID, &amount, &fee, &cur,
		&s.FeeTransactionID, &s.Date, &s.Note, &s.Version, &s.CreatedAt, &s.UpdatedAt, &toAmount, &toCur)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrTransferNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := currency(cur)
	if err != nil {
		return nil, err
	}
	tc, err := currency(toCur)
	if err != nil {
		return nil, err
	}
	s.Amount, s.Fee, s.ToAmount = money.New(amount, c), money.New(fee, c), money.New(toAmount, tc)
	return domain.RehydrateTransfer(s), nil
}

func (r *TransferRepository) Create(ctx context.Context, t *domain.Transfer) error {
	const q = `INSERT INTO transfers (` + transferCols + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`
	_, err := r.conn(ctx).Exec(ctx, q, t.ID(), t.UserID(), t.FromAccountID(), t.ToAccountID(),
		t.Amount().Amount(), t.Fee().Amount(), t.Amount().Currency().Code(), t.FeeTransactionID(),
		t.Date(), t.Note(), t.Version(), t.CreatedAt(), t.UpdatedAt(),
		t.ToAmount().Amount(), t.ToAmount().Currency().Code())
	if err != nil {
		return fmt.Errorf("transferRepo.Create: %w", err)
	}
	return nil
}

func (r *TransferRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Transfer, error) {
	q := `SELECT ` + transferCols + ` FROM transfers WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	t, err := scanTransfer(r.conn(ctx).QueryRow(ctx, q, id, userID))
	if err != nil && !errors.Is(err, domain.ErrTransferNotFound) {
		return nil, fmt.Errorf("transferRepo.Get: %w", err)
	}
	return t, err
}

func (r *TransferRepository) Update(ctx context.Context, t *domain.Transfer) error {
	const q = `UPDATE transfers SET from_account_id = $3, to_account_id = $4, amount = $5, fee_amount = $6,
		currency = $7, fee_transaction_id = $8, transfer_date = $9, note = $10, updated_at = $11,
		to_amount = $13, to_currency = $14, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $12 AND deleted_at IS NULL
		RETURNING version`
	var v int
	err := r.conn(ctx).QueryRow(ctx, q, t.ID(), t.UserID(), t.FromAccountID(), t.ToAccountID(),
		t.Amount().Amount(), t.Fee().Amount(), t.Amount().Currency().Code(), t.FeeTransactionID(),
		t.Date(), t.Note(), t.UpdatedAt(), t.Version(), t.ToAmount().Amount(), t.ToAmount().Currency().Code()).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.missingOrConflict(ctx, t.UserID(), t.ID())
	}
	if err != nil {
		return fmt.Errorf("transferRepo.Update: %w", err)
	}
	t.SyncVersion(v)
	return nil
}

func (r *TransferRepository) SoftDelete(ctx context.Context, t *domain.Transfer, at time.Time) error {
	const q = `UPDATE transfers SET deleted_at = $3, updated_at = $3, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $4 AND deleted_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, t.ID(), t.UserID(), at, t.Version())
	if err != nil {
		return fmt.Errorf("transferRepo.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.missingOrConflict(ctx, t.UserID(), t.ID())
	}
	return nil
}

func (r *TransferRepository) missingOrConflict(ctx context.Context, userID, id uuid.UUID) error {
	var exists bool
	err := r.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM transfers
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("transferRepo.exists: %w", err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrTransferNotFound
}

func (r *TransferRepository) List(ctx context.Context, userID uuid.UUID, f domain.TransferFilter) ([]*domain.Transfer, error) {
	b := &sqlBuilder{}
	b.add("user_id = %s", userID)
	b.where = append(b.where, "deleted_at IS NULL")
	if f.From != nil {
		b.add("transfer_date >= %s", domain.DateOf(*f.From))
	}
	if f.To != nil {
		b.add("transfer_date <= %s", domain.DateOf(*f.To))
	}
	if f.AccountID != nil {
		b.add("(from_account_id = %[1]s OR to_account_id = %[1]s)", *f.AccountID)
	}
	if f.After != nil {
		b.add("(transfer_date, id) < (%s, %s)", domain.DateOf(f.After.Date), f.After.ID)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT ` + transferCols + ` FROM transfers WHERE ` + strings.Join(b.where, " AND ") +
		` ORDER BY transfer_date DESC, id DESC LIMIT ` + b.arg(limit+1)
	rows, err := r.conn(ctx).Query(ctx, q, b.args...)
	if err != nil {
		return nil, fmt.Errorf("transferRepo.List: %w", err)
	}
	defer rows.Close()
	out := make([]*domain.Transfer, 0, limit+1)
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, fmt.Errorf("transferRepo.List scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
