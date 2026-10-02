package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AuditAction adalah jenis perubahan yang dicatat.
type AuditAction string

const (
	AuditCreate AuditAction = "create"
	AuditUpdate AuditAction = "update"
	AuditDelete AuditAction = "delete"
)

// AuditEntry adalah satu catatan audit trail (append-only). Before/After
// berupa snapshot JSON ringkas (tanpa data sensitif).
type AuditEntry struct {
	ID        uuid.UUID
	UserID    uuid.UUID // pemilik data
	ActorID   uuid.UUID // yang melakukan perubahan
	Entity    string
	EntityID  uuid.UUID
	Action    AuditAction
	Before    json.RawMessage
	After     json.RawMessage
	CreatedAt time.Time
}

// AuditFilter memfilter audit log; nil = tanpa filter.
type AuditFilter struct {
	Entity   string
	EntityID *uuid.UUID
	Limit    int
	After    *AuditCursor
}

// AuditCursor adalah posisi keyset (created_at, id) DESC.
type AuditCursor struct {
	At time.Time
	ID uuid.UUID
}
