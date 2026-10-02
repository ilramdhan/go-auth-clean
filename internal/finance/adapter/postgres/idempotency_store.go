package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/app"
)

var _ app.IdempotencyStore = (*IdempotencyStore)(nil)

type IdempotencyStore struct{ base }

func NewIdempotencyStore(db *pgxpool.Pool) *IdempotencyStore {
	return &IdempotencyStore{base{db}}
}

// Claim: INSERT ... ON CONFLICT DO NOTHING. Bila key sudah ada (dan belum
// kedaluwarsa) baris dikunci lalu dibandingkan hash-nya. Request paralel
// dengan key sama akan menunggu lock baris unik sampai tx pertama selesai.
func (s *IdempotencyStore) Claim(ctx context.Context, rec app.IdempotencyRecord) (*app.StoredResponse, error) {
	// key kedaluwarsa dianggap tidak ada: hapus dulu agar bisa diklaim ulang.
	if _, err := s.conn(ctx).Exec(ctx, `DELETE FROM idempotency_keys
		WHERE user_id = $1 AND key = $2 AND expires_at <= $3`, rec.UserID, rec.Key, rec.Now); err != nil {
		return nil, fmt.Errorf("idempotencyStore.Claim purge: %w", err)
	}
	const ins = `INSERT INTO idempotency_keys (user_id, key, request_method, request_path, request_hash,
			status, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7)
		ON CONFLICT (user_id, key) DO NOTHING`
	tag, err := s.conn(ctx).Exec(ctx, ins, rec.UserID, rec.Key, rec.Method, rec.Path, rec.RequestHash, rec.Now, rec.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("idempotencyStore.Claim insert: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil, nil //nolint:nilnil // nil = key baru berhasil diklaim
	}
	var (
		hash   []byte
		status string
		code   *int16
		body   []byte
	)
	err = s.conn(ctx).QueryRow(ctx, `SELECT request_hash, status, response_status, response_body
		FROM idempotency_keys WHERE user_id = $1 AND key = $2 FOR UPDATE`, rec.UserID, rec.Key).
		Scan(&hash, &status, &code, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		// baris dihapus di antara INSERT dan SELECT (purge job): minta client retry.
		return nil, app.ErrIdempotencyInProgress
	}
	if err != nil {
		return nil, fmt.Errorf("idempotencyStore.Claim select: %w", err)
	}
	if !app.MatchHash(hash, rec.RequestHash) {
		return nil, app.ErrIdempotencyKeyReused
	}
	if status != "completed" || code == nil {
		return nil, app.ErrIdempotencyInProgress
	}
	return &app.StoredResponse{Status: int(*code), Body: body}, nil
}

func (s *IdempotencyStore) Complete(ctx context.Context, userID uuid.UUID, key string, resp app.StoredResponse) error {
	const q = `UPDATE idempotency_keys SET status = 'completed', response_status = $3, response_body = $4
		WHERE user_id = $1 AND key = $2`
	tag, err := s.conn(ctx).Exec(ctx, q, userID, key, int16(resp.Status), resp.Body) //nolint:gosec // status HTTP < 1000
	if err != nil {
		return fmt.Errorf("idempotencyStore.Complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("idempotencyStore.Complete: key not found")
	}
	return nil
}

func (s *IdempotencyStore) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.conn(ctx).Exec(ctx, `DELETE FROM idempotency_keys WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("idempotencyStore.DeleteExpired: %w", err)
	}
	return tag.RowsAffected(), nil
}
