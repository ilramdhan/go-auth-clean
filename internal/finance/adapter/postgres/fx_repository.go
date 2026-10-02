package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/domain"
)

var _ domain.ExchangeRateRepository = (*ExchangeRateRepository)(nil)

type ExchangeRateRepository struct{ base }

func NewExchangeRateRepository(db *pgxpool.Pool) *ExchangeRateRepository {
	return &ExchangeRateRepository{base{db}}
}

const rateCols = `id, user_id, base, quote, rate::text, as_of, created_at`

func scanRate(row pgx.Row) (*domain.ExchangeRate, error) {
	var (
		s           domain.ExchangeRateState
		b, q, rText string
	)
	err := row.Scan(&s.ID, &s.UserID, &b, &q, &rText, &s.AsOf, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrRateNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.Base, err = currency(b); err != nil {
		return nil, err
	}
	if s.Quote, err = currency(q); err != nil {
		return nil, err
	}
	r, ok := new(big.Rat).SetString(rText)
	if !ok {
		return nil, fmt.Errorf("invalid rate %q in database", rText)
	}
	s.Rate = r
	return domain.RehydrateExchangeRate(s), nil
}

func (r *ExchangeRateRepository) Create(ctx context.Context, e *domain.ExchangeRate) error {
	const q = `INSERT INTO exchange_rates (id, user_id, base, quote, rate, as_of, created_at)
		VALUES ($1,$2,$3,$4,$5::numeric,$6,$7)`
	_, err := r.conn(ctx).Exec(ctx, q, e.ID(), e.UserID(), e.Base().Code(), e.Quote().Code(), e.RateString(), e.AsOf(), e.CreatedAt())
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrRateExists
	}
	if err != nil {
		return fmt.Errorf("rateRepo.Create: %w", err)
	}
	return nil
}

func (r *ExchangeRateRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.ExchangeRate, error) {
	e, err := scanRate(r.conn(ctx).QueryRow(ctx, `SELECT `+rateCols+` FROM exchange_rates WHERE id = $1 AND user_id = $2`, id, userID))
	if err != nil && !errors.Is(err, domain.ErrRateNotFound) {
		return nil, fmt.Errorf("rateRepo.Get: %w", err)
	}
	return e, err
}

func (r *ExchangeRateRepository) Update(ctx context.Context, e *domain.ExchangeRate) error {
	tag, err := r.conn(ctx).Exec(ctx, `UPDATE exchange_rates SET rate = $3::numeric WHERE id = $1 AND user_id = $2`,
		e.ID(), e.UserID(), e.RateString())
	if err != nil {
		return fmt.Errorf("rateRepo.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrRateNotFound
	}
	return nil
}

func (r *ExchangeRateRepository) Delete(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := r.conn(ctx).Exec(ctx, `DELETE FROM exchange_rates WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("rateRepo.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrRateNotFound
	}
	return nil
}

func (r *ExchangeRateRepository) List(ctx context.Context, userID uuid.UUID) ([]*domain.ExchangeRate, error) {
	rows, err := r.conn(ctx).Query(ctx, `SELECT `+rateCols+` FROM exchange_rates WHERE user_id = $1
		ORDER BY base, quote, as_of DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("rateRepo.List: %w", err)
	}
	defer rows.Close()
	out := []*domain.ExchangeRate{}
	for rows.Next() {
		e, err := scanRate(rows)
		if err != nil {
			return nil, fmt.Errorf("rateRepo.List scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
