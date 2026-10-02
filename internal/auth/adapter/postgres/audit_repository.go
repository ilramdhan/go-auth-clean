package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/database"
)

var _ domain.AuditRepository = (*AuditRepository)(nil)

type AuditRepository struct {
	db *pgxpool.Pool
}

func NewAuditRepository(db *pgxpool.Pool) *AuditRepository { return &AuditRepository{db: db} }

func (r *AuditRepository) conn(ctx context.Context) database.DBTX { return database.Conn(ctx, r.db) }

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// parseIP: IP tidak valid disimpan NULL agar insert audit tidak gagal.
func parseIP(s string) *string {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return nil
	}
	v := a.Unmap().String()
	return &v
}

func (r *AuditRepository) Record(ctx context.Context, e *domain.AuditEvent) error {
	meta := e.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("auditRepo.Record: %w", err)
	}
	const q = `INSERT INTO audit_logs
			(id, user_id, event_type, outcome, email_hash, session_id, ip_address, user_agent, request_id, metadata, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::inet, $8, $9, $10, $11)`
	_, err = r.conn(ctx).Exec(ctx, q, e.ID, nullUUID(e.UserID), e.EventType, e.Outcome, e.EmailHash,
		nullUUID(e.SessionID), parseIP(e.ClientIP), nullString(e.UserAgent), nullString(e.RequestID), b, e.OccurredAt)
	if err != nil {
		return fmt.Errorf("auditRepo.Record: %w", err)
	}
	return nil
}

func (r *AuditRepository) ListByUser(ctx context.Context, userID uuid.UUID, after *domain.AuditKeyset, limit int) ([]domain.AuditEvent, error) {
	const cols = `SELECT id, user_id, event_type, outcome, email_hash, session_id, host(ip_address),
			user_agent, request_id, metadata, occurred_at
		FROM audit_logs WHERE user_id = $1`
	var (
		rows pgx.Rows
		err  error
	)
	if after != nil {
		rows, err = r.conn(ctx).Query(ctx, cols+` AND (occurred_at, id) < ($2, $3)
			ORDER BY occurred_at DESC, id DESC LIMIT $4`, userID, after.OccurredAt, after.ID, limit)
	} else {
		rows, err = r.conn(ctx).Query(ctx, cols+` ORDER BY occurred_at DESC, id DESC LIMIT $2`, userID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("auditRepo.ListByUser: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.AuditEvent, error) {
		var (
			e             domain.AuditEvent
			uid, sid      *uuid.UUID
			ip, ua, reqID *string
			meta          []byte
		)
		if err := row.Scan(&e.ID, &uid, &e.EventType, &e.Outcome, &e.EmailHash, &sid, &ip, &ua, &reqID, &meta, &e.OccurredAt); err != nil {
			return e, err
		}
		if uid != nil {
			e.UserID = *uid
		}
		if sid != nil {
			e.SessionID = *sid
		}
		e.ClientIP, e.UserAgent, e.RequestID = deref(ip), deref(ua), deref(reqID)
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &e.Metadata); err != nil {
				return e, err
			}
		}
		return e, nil
	})
	if err != nil {
		return nil, fmt.Errorf("auditRepo.ListByUser: %w", err)
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *AuditRepository) AnonymizeBefore(ctx context.Context, before time.Time) (int64, error) {
	const q = `UPDATE audit_logs SET ip_address = NULL, user_agent = NULL, email_hash = NULL
		WHERE occurred_at < $1 AND (ip_address IS NOT NULL OR user_agent IS NOT NULL OR email_hash IS NOT NULL)`
	tag, err := r.conn(ctx).Exec(ctx, q, before)
	if err != nil {
		return 0, fmt.Errorf("auditRepo.AnonymizeBefore: %w", err)
	}
	return tag.RowsAffected(), nil
}
