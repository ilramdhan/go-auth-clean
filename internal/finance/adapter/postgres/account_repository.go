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

var _ domain.AccountRepository = (*AccountRepository)(nil)

type AccountRepository struct{ base }

func NewAccountRepository(db *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{base{db}}
}

const accountCols = `id, user_id, name, type, currency, initial_balance, current_balance,
	allow_negative, version, archived_at, created_at, updated_at`

func scanAccount(row pgx.Row) (*domain.Account, error) {
	var (
		s            domain.AccountState
		typ, cur     string
		initial, bal int64
	)
	err := row.Scan(&s.ID, &s.UserID, &s.Name, &typ, &cur, &initial, &bal,
		&s.AllowNegative, &s.Version, &s.ArchivedAt, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := currency(cur)
	if err != nil {
		return nil, err
	}
	s.Type = domain.AccountType(typ)
	s.InitialBalance, s.Balance = money.New(initial, c), money.New(bal, c)
	return domain.RehydrateAccount(s), nil
}

func (r *AccountRepository) Create(ctx context.Context, a *domain.Account) error {
	const q = `INSERT INTO accounts (id, user_id, name, type, currency, initial_balance, current_balance,
		allow_negative, version, archived_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
	_, err := r.conn(ctx).Exec(ctx, q, a.ID(), a.UserID(), a.Name(), string(a.Type()), a.Currency().Code(),
		a.InitialBalance().Amount(), a.Balance().Amount(), a.AllowNegative(), a.Version(),
		a.ArchivedAt(), a.CreatedAt(), a.UpdatedAt())
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrDuplicateName
	}
	if err != nil {
		return fmt.Errorf("accountRepo.Create: %w", err)
	}
	return nil
}

func (r *AccountRepository) get(ctx context.Context, userID, id uuid.UUID, lock bool) (*domain.Account, error) {
	q := `SELECT ` + accountCols + ` FROM accounts WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	if lock {
		q += ` FOR UPDATE`
	}
	a, err := scanAccount(r.conn(ctx).QueryRow(ctx, q, id, userID))
	if err != nil && !errors.Is(err, domain.ErrAccountNotFound) {
		return nil, fmt.Errorf("accountRepo.get: %w", err)
	}
	return a, err
}

func (r *AccountRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error) {
	return r.get(ctx, userID, id, false)
}

func (r *AccountRepository) GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error) {
	return r.get(ctx, userID, id, true)
}

func (r *AccountRepository) List(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]*domain.Account, error) {
	q := `SELECT ` + accountCols + ` FROM accounts WHERE user_id = $1 AND deleted_at IS NULL`
	if !includeArchived {
		q += ` AND archived_at IS NULL`
	}
	q += ` ORDER BY created_at, id`
	rows, err := r.conn(ctx).Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("accountRepo.List: %w", err)
	}
	defer rows.Close()
	out := []*domain.Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("accountRepo.List scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *AccountRepository) Update(ctx context.Context, a *domain.Account) error {
	const q = `UPDATE accounts SET name = $3, initial_balance = $4, current_balance = $5,
		allow_negative = $6, archived_at = $7, updated_at = $8, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $9 AND deleted_at IS NULL
		RETURNING version`
	var v int
	err := r.conn(ctx).QueryRow(ctx, q, a.ID(), a.UserID(), a.Name(), a.InitialBalance().Amount(),
		a.Balance().Amount(), a.AllowNegative(), a.ArchivedAt(), a.UpdatedAt(), a.Version()).Scan(&v)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return r.missingOrConflict(ctx, a.UserID(), a.ID())
	case pgCode(err) == pgUniqueViolation:
		return domain.ErrDuplicateName
	case pgCode(err) == pgCheckViolation:
		return domain.ErrInsufficientBalance
	case err != nil:
		return fmt.Errorf("accountRepo.Update: %w", err)
	}
	a.SyncVersion(v)
	return nil
}

// missingOrConflict membedakan baris hilang (404) dengan version berubah (409).
func (r *AccountRepository) missingOrConflict(ctx context.Context, userID, id uuid.UUID) error {
	var exists bool
	err := r.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("accountRepo.exists: %w", err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrAccountNotFound
}

func (r *AccountRepository) HasActivity(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM transactions WHERE user_id = $1 AND account_id = $2 AND deleted_at IS NULL)
		OR EXISTS (SELECT 1 FROM transfers WHERE user_id = $1 AND deleted_at IS NULL
			AND (from_account_id = $2 OR to_account_id = $2))`
	var used bool
	if err := r.conn(ctx).QueryRow(ctx, q, userID, id).Scan(&used); err != nil {
		return false, fmt.Errorf("accountRepo.HasActivity: %w", err)
	}
	return used, nil
}

func (r *AccountRepository) SoftDelete(ctx context.Context, userID, id uuid.UUID, at time.Time) error {
	const q = `UPDATE accounts SET deleted_at = $3, updated_at = $3, version = version + 1
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, id, userID, at)
	if err != nil {
		return fmt.Errorf("accountRepo.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAccountNotFound
	}
	return nil
}
