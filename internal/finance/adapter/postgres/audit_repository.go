package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/domain"
)

var _ domain.AuditRepository = (*AuditRepository)(nil)

type AuditRepository struct{ base }

func NewAuditRepository(db *pgxpool.Pool) *AuditRepository { return &AuditRepository{base{db}} }

func (r *AuditRepository) Create(ctx context.Context, e *domain.AuditEntry) error {
	const q = `INSERT INTO finance_audit_logs (id, user_id, actor_id, entity, entity_id, action, before, after, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.conn(ctx).Exec(ctx, q, e.ID, e.UserID, e.ActorID, e.Entity, e.EntityID, string(e.Action),
		nullJSON(e.Before), nullJSON(e.After), e.CreatedAt)
	if err != nil {
		return fmt.Errorf("auditRepo.Create: %w", err)
	}
	return nil
}

func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// List keyset (created_at, id) DESC; mengembalikan Limit+1 baris.
func (r *AuditRepository) List(ctx context.Context, userID uuid.UUID, f domain.AuditFilter) ([]*domain.AuditEntry, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	var (
		entity *string
		at     any
		id     *uuid.UUID
	)
	if f.Entity != "" {
		entity = &f.Entity
	}
	if f.After != nil {
		at, id = f.After.At, &f.After.ID
	}
	const q = `SELECT id, user_id, actor_id, entity, entity_id, action, before::text, after::text, created_at
		FROM finance_audit_logs
		WHERE user_id = $1 AND ($2::text IS NULL OR entity = $2) AND ($3::uuid IS NULL OR entity_id = $3)
		  AND ($4::timestamptz IS NULL OR (created_at, id) < ($4, $5::uuid))
		ORDER BY created_at DESC, id DESC LIMIT $6`
	rows, err := r.conn(ctx).Query(ctx, q, userID, entity, f.EntityID, at, id, limit+1)
	if err != nil {
		return nil, fmt.Errorf("auditRepo.List: %w", err)
	}
	defer rows.Close()
	out := []*domain.AuditEntry{}
	for rows.Next() {
		var (
			e             domain.AuditEntry
			action        string
			before, after *string
		)
		if err := rows.Scan(&e.ID, &e.UserID, &e.ActorID, &e.Entity, &e.EntityID, &action, &before, &after, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("auditRepo.List scan: %w", err)
		}
		e.Action = domain.AuditAction(action)
		if before != nil {
			e.Before = []byte(*before)
		}
		if after != nil {
			e.After = []byte(*after)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
