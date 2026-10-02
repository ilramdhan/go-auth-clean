package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RevokeReason menjelaskan kenapa session dicabut (untuk audit/forensik).
type RevokeReason string

const (
	RevokeLogout          RevokeReason = "logout"
	RevokeLogoutAll       RevokeReason = "logout_all"
	RevokePasswordChanged RevokeReason = "password_changed"
	RevokePasswordReset   RevokeReason = "password_reset"
	RevokeReuseDetected   RevokeReason = "reuse_detected"
	RevokeSessionRevoked  RevokeReason = "session_revoked"
	RevokeAccountDeleted  RevokeReason = "account_deleted"
	RevokeSessionLimit    RevokeReason = "session_limit"
	RevokeUserSuspended   RevokeReason = "user_suspended"
	RevokeMFAChanged      RevokeReason = "mfa_changed"
)

// Session merepresentasikan satu refresh token. Setiap refresh membuat
// session baru dengan FamilyID yang sama (rotation). Token asli tidak pernah
// disimpan, hanya hash SHA-256-nya. Satu family = satu "device login".
type Session struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	FamilyID         uuid.UUID
	RefreshTokenHash []byte
	ClientIP         string
	UserAgent        string
	ExpiresAt        time.Time
	RotatedAt        *time.Time
	RevokedAt        *time.Time
	RevokedReason    RevokeReason
	CreatedAt        time.Time
}

func (s *Session) IsActive(now time.Time) bool {
	return s.RevokedAt == nil && s.RotatedAt == nil && now.Before(s.ExpiresAt)
}

// DeviceSession adalah ringkasan satu family aktif untuk ditampilkan ke user.
type DeviceSession struct {
	FamilyID   uuid.UUID
	ClientIP   string
	UserAgent  string
	CreatedAt  time.Time // login pertama family ini
	LastUsedAt time.Time // refresh terakhir
	ExpiresAt  time.Time
}

type SessionRepository interface {
	Create(ctx context.Context, s *Session) error
	FindByTokenHash(ctx context.Context, hash []byte) (*Session, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Session, error)
	// MarkRotated menandai session sudah ditukar. Mengembalikan ErrSessionInvalid
	// jika session sudah tidak aktif (dipakai untuk mendeteksi reuse secara atomic).
	MarkRotated(ctx context.Context, id uuid.UUID, now time.Time) error
	RevokeFamily(ctx context.Context, familyID uuid.UUID, now time.Time, reason RevokeReason) error
	// RevokeFamilyForUser selalu di-scope user_id (anti IDOR). ErrSessionNotFound
	// bila family tidak ada / bukan milik user / sudah tidak aktif.
	RevokeFamilyForUser(ctx context.Context, userID, familyID uuid.UUID, now time.Time, reason RevokeReason) error
	// RevokeAllByUser mencabut semua session user kecuali family exceptFamily
	// (uuid.Nil = tanpa pengecualian).
	RevokeAllByUser(ctx context.Context, userID, exceptFamily uuid.UUID, now time.Time, reason RevokeReason) error
	ListActiveByUser(ctx context.Context, userID uuid.UUID, now time.Time) ([]DeviceSession, error)
	// RevokeExcess menyisakan keep family aktif terbaru, sisanya dicabut.
	RevokeExcess(ctx context.Context, userID uuid.UUID, keep int, now time.Time) (int64, error)
	// DeleteExpired menghapus baris yang expires_at < before.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}
