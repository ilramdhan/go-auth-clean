package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const ProviderGoogle = "google"

// ExternalIdentity menghubungkan user dengan akun provider OAuth/OIDC.
// Subject (claim "sub") stabil, sedangkan email bisa berubah di sisi provider.
type ExternalIdentity struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Provider  string
	Subject   string
	Email     string
	CreatedAt time.Time
}

// ExternalProfile adalah hasil verifikasi id_token dari provider.
type ExternalProfile struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

type IdentityRepository interface {
	// FindBySubject mengembalikan ErrIdentityNotFound bila belum terhubung.
	FindBySubject(ctx context.Context, provider, subject string) (*ExternalIdentity, error)
	// Create mengembalikan ErrIdentityTaken bila subject sudah terhubung ke
	// user lain atau user sudah punya identity untuk provider ini.
	Create(ctx context.Context, i *ExternalIdentity) error
	ListByUser(ctx context.Context, userID uuid.UUID) ([]ExternalIdentity, error)
	DeleteByUser(ctx context.Context, userID uuid.UUID) error
}

// LoginCode adalah kode sekali pakai berumur pendek yang diberikan ke frontend
// setelah callback OAuth, lalu ditukar dengan token (token tidak lewat URL).
type LoginCode struct {
	CodeHash  []byte
	UserID    uuid.UUID
	Provider  string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type LoginCodeRepository interface {
	Create(ctx context.Context, c *LoginCode) error
	// Consume menghapus code dan mengembalikan pemiliknya secara atomic.
	// ErrLoginCodeInvalid bila tidak ada / expired / sudah dipakai.
	Consume(ctx context.Context, hash []byte, now time.Time) (*LoginCode, error)
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}
