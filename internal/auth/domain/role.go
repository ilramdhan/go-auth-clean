package domain

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Role adalah nama role RBAC. RoleUser dimiliki semua user secara implisit.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// ParseRole memvalidasi nama role yang dikenal sistem.
func ParseRole(s string) (Role, error) {
	switch r := Role(s); r {
	case RoleUser, RoleAdmin:
		return r, nil
	default:
		return "", ErrInvalidRole
	}
}

// HasRole mengecek apakah want ada di daftar roles.
func HasRole(roles []Role, want Role) bool { return slices.Contains(roles, want) }

// RoleStrings mengubah []Role menjadi []string (mis. untuk claim JWT).
func RoleStrings(roles []Role) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return out
}

type RoleRepository interface {
	// ListByUser selalu menyertakan RoleUser (implisit) di urutan pertama.
	ListByUser(ctx context.Context, userID uuid.UUID) ([]Role, error)
	// Grant idempoten. grantedBy uuid.Nil = sistem (CLI).
	Grant(ctx context.Context, userID uuid.UUID, role Role, grantedBy uuid.UUID, at time.Time) error
	// Revoke idempoten (tidak error bila role tidak dimiliki).
	Revoke(ctx context.Context, userID uuid.UUID, role Role) error
	// CountUsers menghitung user aktif (belum dihapus) yang punya role.
	CountUsers(ctx context.Context, role Role) (int, error)
}
