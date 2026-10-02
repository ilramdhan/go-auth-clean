package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/database"
)

var _ domain.SessionRepository = (*SessionRepository)(nil)

type SessionRepository struct {
	db *pgxpool.Pool
}

func NewSessionRepository(db *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{db: db}
}

// conn memakai tx dari context (bila use case memanggil dalam WithinTx), selain itu pool.
func (r *SessionRepository) conn(ctx context.Context) database.DBTX { return database.Conn(ctx, r.db) }

const sessionColumns = `id, user_id, family_id, refresh_token_hash, client_ip, user_agent,
	expires_at, rotated_at, revoked_at, COALESCE(revoked_reason, ''), created_at`

func scanSession(row pgx.Row) (*domain.Session, error) {
	var s domain.Session
	err := row.Scan(&s.ID, &s.UserID, &s.FamilyID, &s.RefreshTokenHash, &s.ClientIP, &s.UserAgent,
		&s.ExpiresAt, &s.RotatedAt, &s.RevokedAt, &s.RevokedReason, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SessionRepository) Create(ctx context.Context, s *domain.Session) error {
	const q = `INSERT INTO sessions (id, user_id, family_id, refresh_token_hash, client_ip, user_agent, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.conn(ctx).Exec(ctx, q, s.ID, s.UserID, s.FamilyID, s.RefreshTokenHash, s.ClientIP, s.UserAgent, s.ExpiresAt, s.CreatedAt)
	if err != nil {
		return fmt.Errorf("sessionRepo.Create: %w", err)
	}
	return nil
}

func (r *SessionRepository) FindByTokenHash(ctx context.Context, hash []byte) (*domain.Session, error) {
	q := `SELECT ` + sessionColumns + ` FROM sessions WHERE refresh_token_hash = $1`
	s, err := scanSession(r.conn(ctx).QueryRow(ctx, q, hash))
	if err != nil && !errors.Is(err, domain.ErrSessionNotFound) {
		return nil, fmt.Errorf("sessionRepo.FindByTokenHash: %w", err)
	}
	return s, err
}

func (r *SessionRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Session, error) {
	q := `SELECT ` + sessionColumns + ` FROM sessions WHERE id = $1`
	s, err := scanSession(r.conn(ctx).QueryRow(ctx, q, id))
	if err != nil && !errors.Is(err, domain.ErrSessionNotFound) {
		return nil, fmt.Errorf("sessionRepo.FindByID: %w", err)
	}
	return s, err
}

func (r *SessionRepository) MarkRotated(ctx context.Context, id uuid.UUID, now time.Time) error {
	const q = `UPDATE sessions SET rotated_at = $2
		WHERE id = $1 AND rotated_at IS NULL AND revoked_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, id, now)
	if err != nil {
		return fmt.Errorf("sessionRepo.MarkRotated: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrSessionInvalid
	}
	return nil
}

func (r *SessionRepository) RevokeFamily(ctx context.Context, familyID uuid.UUID, now time.Time, reason domain.RevokeReason) error {
	const q = `UPDATE sessions SET revoked_at = $2, revoked_reason = $3 WHERE family_id = $1 AND revoked_at IS NULL`
	if _, err := r.conn(ctx).Exec(ctx, q, familyID, now, reason); err != nil {
		return fmt.Errorf("sessionRepo.RevokeFamily: %w", err)
	}
	return nil
}

func (r *SessionRepository) RevokeFamilyForUser(ctx context.Context, userID, familyID uuid.UUID, now time.Time, reason domain.RevokeReason) error {
	// Hanya family yang masih punya token aktif (belum expired) yang dianggap ada.
	const q = `WITH active AS (
			SELECT 1 FROM sessions
			WHERE user_id = $1 AND family_id = $2 AND revoked_at IS NULL AND rotated_at IS NULL AND expires_at > $3
			LIMIT 1
		)
		UPDATE sessions SET revoked_at = $3, revoked_reason = $4
		WHERE user_id = $1 AND family_id = $2 AND revoked_at IS NULL AND EXISTS (SELECT 1 FROM active)`
	tag, err := r.conn(ctx).Exec(ctx, q, userID, familyID, now, reason)
	if err != nil {
		return fmt.Errorf("sessionRepo.RevokeFamilyForUser: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrSessionNotFound
	}
	return nil
}

func (r *SessionRepository) RevokeAllByUser(ctx context.Context, userID, exceptFamily uuid.UUID, now time.Time, reason domain.RevokeReason) error {
	const q = `UPDATE sessions SET revoked_at = $2, revoked_reason = $3
		WHERE user_id = $1 AND revoked_at IS NULL AND family_id <> $4`
	if _, err := r.conn(ctx).Exec(ctx, q, userID, now, reason, exceptFamily); err != nil {
		return fmt.Errorf("sessionRepo.RevokeAllByUser: %w", err)
	}
	return nil
}

func (r *SessionRepository) ListActiveByUser(ctx context.Context, userID uuid.UUID, now time.Time) ([]domain.DeviceSession, error) {
	// Satu baris aktif (belum rotated/revoked) per family = satu device session.
	const q = `SELECT s.family_id, s.client_ip, s.user_agent,
			(SELECT MIN(f.created_at) FROM sessions f WHERE f.family_id = s.family_id),
			s.created_at, s.expires_at
		FROM sessions s
		WHERE s.user_id = $1 AND s.revoked_at IS NULL AND s.rotated_at IS NULL AND s.expires_at > $2
		ORDER BY s.created_at DESC
		LIMIT 100`
	rows, err := r.conn(ctx).Query(ctx, q, userID, now)
	if err != nil {
		return nil, fmt.Errorf("sessionRepo.ListActiveByUser: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.DeviceSession, error) {
		var d domain.DeviceSession
		err := row.Scan(&d.FamilyID, &d.ClientIP, &d.UserAgent, &d.CreatedAt, &d.LastUsedAt, &d.ExpiresAt)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("sessionRepo.ListActiveByUser: %w", err)
	}
	return out, nil
}

func (r *SessionRepository) RevokeExcess(ctx context.Context, userID uuid.UUID, keep int, now time.Time) (int64, error) {
	const q = `WITH ranked AS (
			SELECT family_id, row_number() OVER (ORDER BY created_at DESC, id DESC) AS rn
			FROM sessions
			WHERE user_id = $1 AND revoked_at IS NULL AND rotated_at IS NULL AND expires_at > $3
		)
		UPDATE sessions SET revoked_at = $3, revoked_reason = 'session_limit'
		WHERE user_id = $1 AND revoked_at IS NULL
			AND family_id IN (SELECT family_id FROM ranked WHERE rn > $2)`
	tag, err := r.conn(ctx).Exec(ctx, q, userID, keep, now)
	if err != nil {
		return 0, fmt.Errorf("sessionRepo.RevokeExcess: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *SessionRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	const q = `DELETE FROM sessions WHERE expires_at < $1`
	tag, err := r.conn(ctx).Exec(ctx, q, before)
	if err != nil {
		return 0, fmt.Errorf("sessionRepo.DeleteExpired: %w", err)
	}
	return tag.RowsAffected(), nil
}
