package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/database"
)

var _ domain.OTPRepository = (*OTPRepository)(nil)

type OTPRepository struct {
	db *pgxpool.Pool
}

func NewOTPRepository(db *pgxpool.Pool) *OTPRepository { return &OTPRepository{db: db} }

func (r *OTPRepository) conn(ctx context.Context) database.DBTX { return database.Conn(ctx, r.db) }

func (r *OTPRepository) InvalidateActive(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose, now time.Time) error {
	const q = `UPDATE verification_tokens SET invalidated_at = $3
		WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL AND invalidated_at IS NULL`
	if _, err := r.conn(ctx).Exec(ctx, q, userID, purpose, now); err != nil {
		return fmt.Errorf("otpRepo.InvalidateActive: %w", err)
	}
	return nil
}

func (r *OTPRepository) Create(ctx context.Context, o *domain.OTP) error {
	const q = `INSERT INTO verification_tokens
			(id, user_id, purpose, target, code_hash, attempts, max_attempts, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $8)`
	_, err := r.conn(ctx).Exec(ctx, q, o.ID, o.UserID, o.Purpose, o.Target, o.CodeHash, o.MaxAttempts, o.CreatedAt, o.ExpiresAt)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgUniqueViolation {
		// Race dua request bersamaan: satu menang, sisanya dianggap sudah ada token aktif.
		return domain.ErrOTPActiveExists
	}
	if err != nil {
		return fmt.Errorf("otpRepo.Create: %w", err)
	}
	return nil
}

func (r *OTPRepository) FindActive(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose) (*domain.OTP, error) {
	const q = `SELECT id, user_id, purpose, target, code_hash, attempts, max_attempts,
			created_at, expires_at, consumed_at, invalidated_at
		FROM verification_tokens
		WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL AND invalidated_at IS NULL`
	var o domain.OTP
	err := r.conn(ctx).QueryRow(ctx, q, userID, purpose).Scan(&o.ID, &o.UserID, &o.Purpose, &o.Target,
		&o.CodeHash, &o.Attempts, &o.MaxAttempts, &o.CreatedAt, &o.ExpiresAt, &o.ConsumedAt, &o.InvalidatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrOTPNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("otpRepo.FindActive: %w", err)
	}
	return &o, nil
}

func (r *OTPRepository) IncrementAttempts(ctx context.Context, id uuid.UUID) (int, error) {
	const q = `UPDATE verification_tokens SET attempts = attempts + 1
		WHERE id = $1 AND attempts < max_attempts AND consumed_at IS NULL AND invalidated_at IS NULL
		RETURNING attempts`
	var n int
	err := r.conn(ctx).QueryRow(ctx, q, id).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrOTPTooManyAttempts
	}
	if err != nil {
		return 0, fmt.Errorf("otpRepo.IncrementAttempts: %w", err)
	}
	return n, nil
}

func (r *OTPRepository) Consume(ctx context.Context, id uuid.UUID, now time.Time) error {
	const q = `UPDATE verification_tokens SET consumed_at = $2
		WHERE id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, id, now)
	if err != nil {
		return fmt.Errorf("otpRepo.Consume: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrInvalidOTP
	}
	return nil
}

func (r *OTPRepository) Stats(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose, since time.Time) (domain.OTPStats, error) {
	const q = `SELECT count(*), max(created_at) FROM verification_tokens
		WHERE user_id = $1 AND purpose = $2 AND created_at >= $3`
	var st domain.OTPStats
	if err := r.conn(ctx).QueryRow(ctx, q, userID, purpose, since).Scan(&st.Count, &st.LastIssued); err != nil {
		return domain.OTPStats{}, fmt.Errorf("otpRepo.Stats: %w", err)
	}
	return st, nil
}

func (r *OTPRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	const q = `DELETE FROM verification_tokens WHERE expires_at < $1`
	tag, err := r.conn(ctx).Exec(ctx, q, before)
	if err != nil {
		return 0, fmt.Errorf("otpRepo.DeleteExpired: %w", err)
	}
	return tag.RowsAffected(), nil
}
