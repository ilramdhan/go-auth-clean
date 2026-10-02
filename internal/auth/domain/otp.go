package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type OTPPurpose string

const (
	OTPEmailVerification OTPPurpose = "email_verification"
	OTPPasswordReset     OTPPurpose = "password_reset"
)

const OTPLength = 6

// OTP adalah kode sekali pakai. Kode asli tidak disimpan, hanya HMAC-nya.
type OTP struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Purpose       OTPPurpose
	Target        string
	CodeHash      []byte
	Attempts      int
	MaxAttempts   int
	CreatedAt     time.Time
	ExpiresAt     time.Time
	ConsumedAt    *time.Time
	InvalidatedAt *time.Time
}

func (o *OTP) IsExpired(now time.Time) bool { return !now.Before(o.ExpiresAt) }

func (o *OTP) AttemptsExhausted() bool { return o.Attempts >= o.MaxAttempts }

// ValidOTPFormat memastikan kode tepat 6 digit angka ASCII.
func ValidOTPFormat(code string) bool {
	if len(code) != OTPLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

// OTPStats dipakai untuk cooldown dan kuota harian.
type OTPStats struct {
	Count      int
	LastIssued *time.Time
}

type OTPRepository interface {
	// InvalidateActive menandai token aktif (user, purpose) sebagai tidak berlaku.
	// Wajib dipanggil sebelum Create dalam transaksi yang sama.
	InvalidateActive(ctx context.Context, userID uuid.UUID, purpose OTPPurpose, now time.Time) error
	Create(ctx context.Context, otp *OTP) error
	// FindActive mengembalikan token belum dikonsumsi/di-invalidate (boleh expired).
	FindActive(ctx context.Context, userID uuid.UUID, purpose OTPPurpose) (*OTP, error)
	// IncrementAttempts menaikkan attempts secara atomic SEBELUM compare.
	// ErrOTPTooManyAttempts bila sudah mencapai max_attempts.
	IncrementAttempts(ctx context.Context, id uuid.UUID) (attempts int, err error)
	// Consume hanya berhasil untuk satu request (ErrInvalidOTP bila sudah dipakai).
	Consume(ctx context.Context, id uuid.UUID, now time.Time) error
	Stats(ctx context.Context, userID uuid.UUID, purpose OTPPurpose, since time.Time) (OTPStats, error)
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}
