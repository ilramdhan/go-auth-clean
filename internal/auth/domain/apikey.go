package domain

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Scope membatasi apa yang boleh dilakukan API key.
type Scope string

const (
	ScopeRead  Scope = "read"  // hanya method aman (GET/HEAD)
	ScopeWrite Scope = "write" // boleh mengubah data
)

const (
	APIKeyNameMaxLength = 100
	APIKeyPrefixLength  = 8
	// APIKeyMaxTTL adalah masa berlaku maksimal API key.
	APIKeyMaxTTL = 365 * 24 * time.Hour
)

// APIKey: secret asli tidak pernah disimpan, hanya SHA-256-nya. Prefix
// (8 karakter acak) dipakai untuk lookup dan ditampilkan ke user.
type APIKey struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	Prefix     string
	SecretHash []byte
	Scopes     []Scope
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

func (k *APIKey) IsActive(now time.Time) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || now.Before(*k.ExpiresAt))
}

func (k *APIKey) HasScope(s Scope) bool { return slices.Contains(k.Scopes, s) }

// ParseScopes memvalidasi, menghapus duplikat, dan mengurutkan scope.
func ParseScopes(raw []string) ([]Scope, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidScope
	}
	var out []Scope
	for _, s := range raw {
		sc := Scope(strings.ToLower(strings.TrimSpace(s)))
		if sc != ScopeRead && sc != ScopeWrite {
			return nil, ErrInvalidScope
		}
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	slices.Sort(out)
	return out, nil
}

// NormalizeAPIKeyName trim + validasi panjang 1..100 tanpa karakter kontrol.
func NormalizeAPIKeyName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(name)
	if n == 0 || n > APIKeyNameMaxLength || !utf8.ValidString(name) {
		return "", ErrInvalidAPIKeyName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidAPIKeyName
		}
	}
	return name, nil
}

type APIKeyRepository interface {
	Create(ctx context.Context, k *APIKey) error
	// FindByPrefix mengembalikan ErrAPIKeyNotFound bila prefix tidak dikenal.
	FindByPrefix(ctx context.Context, prefix string) (*APIKey, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]APIKey, error)
	CountActiveByUser(ctx context.Context, userID uuid.UUID, now time.Time) (int, error)
	// Revoke di-scope user_id (anti IDOR). ErrAPIKeyNotFound bila bukan milik
	// user atau sudah dicabut.
	Revoke(ctx context.Context, userID, id uuid.UUID, now time.Time) error
	RevokeAllByUser(ctx context.Context, userID uuid.UUID, now time.Time) error
	// TouchLastUsed best-effort; hanya update bila last_used_at lebih lama dari olderThan.
	TouchLastUsed(ctx context.Context, id uuid.UUID, now, olderThan time.Time) error
}
