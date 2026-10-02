// Package domain berisi entity, value object, aturan bisnis dan kontrak repository
// untuk bounded context auth. Package ini tidak boleh import database/HTTP.
package domain

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type UserStatus string

const (
	UserStatusPendingVerification UserStatus = "pending_verification"
	UserStatusActive              UserStatus = "active"
	UserStatusSuspended           UserStatus = "suspended"
	UserStatusDeleted             UserStatus = "deleted"
)

const (
	MaxFailedLoginAttempts = 5
	LockDuration           = 15 * time.Minute

	PasswordMinLength = 8
	// bcrypt hanya memproses 72 byte pertama; sisanya diabaikan diam-diam.
	PasswordMaxBytes = 72

	FullNameMaxLength = 100
	EmailMaxLength    = 254
)

type User struct {
	ID                  uuid.UUID
	Email               string
	PasswordHash        string
	FullName            string
	Status              UserStatus
	FailedLoginAttempts int
	LockedUntil         *time.Time
	EmailVerifiedAt     *time.Time
	PasswordChangedAt   *time.Time
	LastLoginAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	// Roles & MFAEnabled tidak disimpan di tabel users; diisi use case bila dibutuhkan.
	Roles      []Role
	MFAEnabled bool
}

// HasPassword false untuk user yang dibuat lewat OAuth (belum set password).
func (u *User) HasPassword() bool { return u.PasswordHash != "" }

// IsLocked mengecek apakah akun sedang dalam masa penalti brute-force.
func (u *User) IsLocked(now time.Time) bool {
	return u.LockedUntil != nil && u.LockedUntil.After(now)
}

func (u *User) CanLogin() bool {
	return u.Status == UserStatusActive
}

// LoginError mengembalikan alasan user tidak boleh login berdasarkan status.
// Dipanggil SETELAH password terbukti benar agar status tidak bocor.
func (u *User) LoginError() error {
	switch u.Status {
	case UserStatusActive:
		return nil
	case UserStatusPendingVerification:
		return ErrEmailNotVerified
	case UserStatusSuspended:
		return ErrAccountSuspended
	default:
		return ErrInvalidCredentials
	}
}

// NormalizeEmail memvalidasi dan menormalisasi email (trim + lowercase).
// Local part non-ASCII ditolak (IDN belum didukung).
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > EmailMaxLength || !isASCII(email) {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email[strings.LastIndexByte(email, '@'):], ".") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// ValidatePassword menerapkan password policy (NIST: panjang, tanpa aturan komposisi).
func ValidatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < PasswordMinLength {
		return fmt.Errorf("%w: minimal %d karakter", ErrWeakPassword, PasswordMinLength)
	}
	if len(pw) > PasswordMaxBytes {
		return fmt.Errorf("%w: maksimal %d byte", ErrWeakPassword, PasswordMaxBytes)
	}
	return nil
}

// ValidatePasswordFor menambahkan aturan: password tidak boleh sama dengan email.
func ValidatePasswordFor(pw, email string) error {
	if err := ValidatePassword(pw); err != nil {
		return err
	}
	if email != "" && strings.EqualFold(strings.TrimSpace(pw), email) {
		return fmt.Errorf("%w: tidak boleh sama dengan email", ErrWeakPassword)
	}
	return nil
}

// NormalizeFullName melakukan trim dan memvalidasi panjang 1..100 rune.
func NormalizeFullName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(name)
	if n == 0 || n > FullNameMaxLength || !utf8.ValidString(name) {
		return "", ErrInvalidFullName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidFullName
		}
	}
	return name, nil
}

type UserRepository interface {
	// Create mengembalikan ErrEmailTaken jika email sudah dipakai.
	Create(ctx context.Context, user *User) error
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	// RegisterFailedLogin menaikkan counter secara atomic dan mengunci akun
	// jika sudah mencapai batas. Menghindari race condition read-modify-write.
	// locked=true bila percobaan ini yang membuat akun terkunci.
	RegisterFailedLogin(ctx context.Context, id uuid.UUID, now time.Time) (locked bool, err error)
	// RecordLogin me-reset counter gagal dan mengisi last_login_at.
	RecordLogin(ctx context.Context, id uuid.UUID, now time.Time) error
	// UpdatePassword juga membuka lockout (failed attempts = 0).
	UpdatePassword(ctx context.Context, id uuid.UUID, hash string, changedAt time.Time) error
	// MarkEmailVerified mengubah pending_verification menjadi active.
	// Mengembalikan ErrUserNotFound jika user tidak ada / bukan pending.
	MarkEmailVerified(ctx context.Context, id uuid.UUID, now time.Time) error
	UpdateProfile(ctx context.Context, id uuid.UUID, fullName string, now time.Time) (*User, error)
	// SoftDelete menandai user deleted dan meng-anonymize PII (email, nama, hash)
	// sehingga email bisa dipakai register ulang.
	SoftDelete(ctx context.Context, id uuid.UUID, now time.Time) error
	// PurgeUnverified menghapus user pending_verification yang dibuat sebelum before.
	PurgeUnverified(ctx context.Context, before time.Time) (int64, error)
	// List untuk admin: keyset (created_at, id) DESC, hanya user belum dihapus.
	List(ctx context.Context, f UserFilter) ([]User, error)
	// SetStatus mengubah status hanya bila status saat ini = from (atomic).
	// ErrInvalidTransition bila status sudah berbeda, ErrUserNotFound bila tidak ada.
	SetStatus(ctx context.Context, id uuid.UUID, from, to UserStatus, now time.Time) error
}

// UserKeyset adalah posisi keyset pagination (created_at, id) DESC.
type UserKeyset struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// UserFilter untuk admin list user. Query mencari substring email/nama.
type UserFilter struct {
	Query  string
	Status UserStatus // "" = semua
	After  *UserKeyset
	Limit  int
}

// ParseUserStatus memvalidasi filter status (deleted tidak bisa difilter).
func ParseUserStatus(s string) (UserStatus, error) {
	switch st := UserStatus(s); st {
	case "", UserStatusPendingVerification, UserStatusActive, UserStatusSuspended:
		return st, nil
	default:
		return "", ErrInvalidStatus
	}
}
