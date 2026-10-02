// Package postgres berisi implementasi repository auth menggunakan pgx.
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

const pgUniqueViolation = "23505"

// Compile-time check: gagal compile jika struct tidak memenuhi interface.
var _ domain.UserRepository = (*UserRepository)(nil)

type UserRepository struct {
	db *pgxpool.Pool
}

func NewUserRepository(db *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: db}
}

// conn memakai tx dari context (bila use case memanggil dalam WithinTx), selain itu pool.
func (r *UserRepository) conn(ctx context.Context) database.DBTX { return database.Conn(ctx, r.db) }

const userColumns = `id, email, password_hash, full_name, status, failed_login_attempts,
	locked_until, email_verified_at, password_changed_at, last_login_at, created_at, updated_at`

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FullName, &u.Status,
		&u.FailedLoginAttempts, &u.LockedUntil, &u.EmailVerifiedAt, &u.PasswordChangedAt,
		&u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepository) Create(ctx context.Context, u *domain.User) error {
	const q = `INSERT INTO users (id, email, password_hash, full_name, status, email_verified_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.conn(ctx).Exec(ctx, q, u.ID, u.Email, u.PasswordHash, u.FullName, u.Status,
		u.EmailVerifiedAt, u.CreatedAt, u.UpdatedAt)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgUniqueViolation {
		return domain.ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("userRepo.Create: %w", err)
	}
	return nil
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	q := `SELECT ` + userColumns + ` FROM users WHERE email = $1 AND deleted_at IS NULL`
	u, err := scanUser(r.conn(ctx).QueryRow(ctx, q, email))
	if err != nil && !errors.Is(err, domain.ErrUserNotFound) {
		return nil, fmt.Errorf("userRepo.FindByEmail: %w", err)
	}
	return u, err
}

func (r *UserRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	q := `SELECT ` + userColumns + ` FROM users WHERE id = $1 AND deleted_at IS NULL`
	u, err := scanUser(r.conn(ctx).QueryRow(ctx, q, id))
	if err != nil && !errors.Is(err, domain.ErrUserNotFound) {
		return nil, fmt.Errorf("userRepo.FindByID: %w", err)
	}
	return u, err
}

func (r *UserRepository) RegisterFailedLogin(ctx context.Context, id uuid.UUID, now time.Time) (bool, error) {
	// Increment dan lock dalam satu statement (aman dari race). Lock yang sudah
	// kedaluwarsa memulai hitungan dari awal. Row lock men-serialisasi update,
	// sehingga hanya satu percobaan yang tepat mencapai batas (locked=true).
	const q = `UPDATE users SET
			failed_login_attempts = CASE WHEN locked_until IS NOT NULL AND locked_until <= $4
				THEN 1 ELSE failed_login_attempts + 1 END,
			locked_until = CASE
				WHEN locked_until IS NOT NULL AND locked_until > $4 THEN locked_until
				WHEN (CASE WHEN locked_until IS NOT NULL AND locked_until <= $4
					THEN 1 ELSE failed_login_attempts + 1 END) >= $2 THEN $3
				ELSE NULL END,
			updated_at = $4
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING failed_login_attempts = $2`
	var locked bool
	err := r.conn(ctx).QueryRow(ctx, q, id, domain.MaxFailedLoginAttempts, now.Add(domain.LockDuration), now).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, domain.ErrUserNotFound
	}
	if err != nil {
		return false, fmt.Errorf("userRepo.RegisterFailedLogin: %w", err)
	}
	return locked, nil
}

func (r *UserRepository) RecordLogin(ctx context.Context, id uuid.UUID, now time.Time) error {
	const q = `UPDATE users SET failed_login_attempts = 0, locked_until = NULL, last_login_at = $2, updated_at = $2
		WHERE id = $1 AND deleted_at IS NULL`
	return r.execOne(ctx, "RecordLogin", q, id, now)
}

func (r *UserRepository) UpdatePassword(ctx context.Context, id uuid.UUID, hash string, changedAt time.Time) error {
	const q = `UPDATE users SET password_hash = $2, password_changed_at = $3, updated_at = $3,
			failed_login_attempts = 0, locked_until = NULL
		WHERE id = $1 AND deleted_at IS NULL`
	return r.execOne(ctx, "UpdatePassword", q, id, hash, changedAt)
}

func (r *UserRepository) MarkEmailVerified(ctx context.Context, id uuid.UUID, now time.Time) error {
	const q = `UPDATE users SET status = 'active', email_verified_at = $2, updated_at = $2
		WHERE id = $1 AND status = 'pending_verification' AND deleted_at IS NULL`
	return r.execOne(ctx, "MarkEmailVerified", q, id, now)
}

func (r *UserRepository) UpdateProfile(ctx context.Context, id uuid.UUID, fullName string, now time.Time) (*domain.User, error) {
	q := `UPDATE users SET full_name = $2, updated_at = $3
		WHERE id = $1 AND deleted_at IS NULL RETURNING ` + userColumns
	u, err := scanUser(r.conn(ctx).QueryRow(ctx, q, id, fullName, now))
	if err != nil && !errors.Is(err, domain.ErrUserNotFound) {
		return nil, fmt.Errorf("userRepo.UpdateProfile: %w", err)
	}
	return u, err
}

func (r *UserRepository) SoftDelete(ctx context.Context, id uuid.UUID, now time.Time) error {
	// Anonymize PII: email unik per id (tetap valid CITEXT, domain .invalid
	// RFC 2606), password_hash kosong (tidak ada password yang cocok).
	const q = `UPDATE users SET
			email = 'deleted+' || id::text || '@deleted.invalid',
			full_name = 'Deleted User',
			password_hash = '',
			status = 'deleted',
			failed_login_attempts = 0,
			locked_until = NULL,
			deleted_at = $2,
			updated_at = $2
		WHERE id = $1 AND deleted_at IS NULL`
	return r.execOne(ctx, "SoftDelete", q, id, now)
}

func (r *UserRepository) PurgeUnverified(ctx context.Context, before time.Time) (int64, error) {
	const q = `DELETE FROM users WHERE status = 'pending_verification' AND created_at < $1`
	tag, err := r.conn(ctx).Exec(ctx, q, before)
	if err != nil {
		return 0, fmt.Errorf("userRepo.PurgeUnverified: %w", err)
	}
	return tag.RowsAffected(), nil
}

// execOne menjalankan UPDATE yang wajib mengenai tepat satu baris.
func (r *UserRepository) execOne(ctx context.Context, op, q string, args ...any) error {
	tag, err := r.conn(ctx).Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("userRepo.%s: %w", op, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}
