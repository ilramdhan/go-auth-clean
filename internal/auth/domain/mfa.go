package domain

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

type MFAStatus string

const (
	MFAPending MFAStatus = "pending"
	MFAEnabled MFAStatus = "enabled"
)

const (
	// RecoveryCodeCount adalah jumlah recovery code yang dibuat saat 2FA aktif.
	RecoveryCodeCount = 10
	// RecoveryCodeLength adalah panjang recovery code (tanpa tanda '-').
	RecoveryCodeLength = 10
	// TOTPDigits adalah panjang kode authenticator.
	TOTPDigits = 6
)

// UserMFA adalah konfigurasi TOTP user. Secret disimpan TERENKRIPSI (bukan
// di-hash) karena harus dibaca kembali untuk verifikasi.
type UserMFA struct {
	UserID          uuid.UUID
	SecretEncrypted string
	Status          MFAStatus
	// LastUsedStep mencegah kode yang sama dipakai dua kali (replay) dalam window.
	LastUsedStep int64
	EnabledAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (m *UserMFA) IsEnabled() bool { return m != nil && m.Status == MFAEnabled }

// NormalizeRecoveryCode membuang spasi/tanda '-' dan menjadikan lowercase,
// sehingga "ABCD-EFGH-12" dan "abcdefgh12" dianggap sama.
func NormalizeRecoveryCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// LooksLikeTOTP true bila kode berupa 6 digit (selain itu dianggap recovery code).
func LooksLikeTOTP(code string) bool {
	if len(code) != TOTPDigits {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

type MFARepository interface {
	// Get mengembalikan ErrMFANotFound bila user belum pernah setup.
	Get(ctx context.Context, userID uuid.UUID) (*UserMFA, error)
	// UpsertPending membuat/mengganti setup pending. ErrMFAAlreadyEnabled
	// bila 2FA sudah aktif (secret aktif tidak boleh ditimpa).
	UpsertPending(ctx context.Context, m *UserMFA) error
	// Enable mengubah pending -> enabled dan mencatat step yang dipakai.
	// ErrMFASetupRequired bila tidak ada setup pending.
	Enable(ctx context.Context, userID uuid.UUID, step int64, now time.Time) error
	// AdvanceStep menyimpan step terakhir secara atomic (WHERE last_used_step < step).
	// ErrMFACodeReplayed bila step <= step terakhir (kode sudah pernah dipakai).
	AdvanceStep(ctx context.Context, userID uuid.UUID, step int64, now time.Time) error
	// Delete menghapus konfigurasi + recovery code (idempoten).
	Delete(ctx context.Context, userID uuid.UUID) error
	// ReplaceRecoveryCodes menghapus code lama dan menyimpan hash baru.
	ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, hashes [][]byte, now time.Time) error
	// UseRecoveryCode menandai code terpakai (sekali pakai, atomic).
	// ErrInvalidMFACode bila tidak ada code cocok yang belum dipakai.
	UseRecoveryCode(ctx context.Context, userID uuid.UUID, hash []byte, now time.Time) error
	CountRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error)
}
