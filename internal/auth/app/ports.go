// Package app berisi use case (application service) untuk bounded context auth.
// Use case mengatur alur bisnis dan bergantung pada interface (port), bukan
// implementasi konkret seperti bcrypt, JWT, SMTP, atau Postgres.
package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

type PasswordHasher interface {
	Hash(password string) (string, error)
	// Compare mengembalikan error jika password tidak cocok. hash kosong =
	// compare dengan dummy hash (menyamakan timing untuk user tidak ada).
	Compare(hash, password string) error
}

type AccessTokenIssuer interface {
	// Issue membuat access token; roles disimpan sebagai claim "roles".
	Issue(userID, sessionID uuid.UUID, roles []domain.Role, now time.Time) (token string, expiresAt time.Time, err error)
}

// MFATokenIssuer membuat token berumur pendek (aud=mfa) yang menandakan
// password sudah benar dan login tinggal menunggu kode 2FA.
type MFATokenIssuer interface {
	IssueMFA(userID uuid.UUID, now time.Time) (token string, expiresAt time.Time, err error)
	VerifyMFA(token string) (uuid.UUID, error)
}

// TOTP adalah port authenticator (RFC 6238, SHA1, 6 digit, periode 30 detik).
type TOTP interface {
	// GenerateSecret mengembalikan secret base32 baru (160 bit).
	GenerateSecret() (string, error)
	// URI membuat otpauth:// URI untuk QR code.
	URI(secret, account string) string
	// Validate mengecek kode dengan toleransi ±1 step dan mengembalikan step yang cocok.
	Validate(secret, code string, now time.Time) (step int64, ok bool)
}

// SecretSealer mengenkripsi data sensitif yang perlu dibaca kembali (AEAD).
// aad mengikat ciphertext ke pemiliknya (mis. user id).
type SecretSealer interface {
	EncryptString(plaintext string, aad []byte) (string, error)
	DecryptString(ciphertext string, aad []byte) (string, error)
}

// OAuthProvider adalah port login OIDC (Authorization Code + PKCE).
// Implementasi wajib memverifikasi id_token (signature JWKS, iss, aud, exp, nonce).
type OAuthProvider interface {
	AuthCodeURL(state, codeChallenge, nonce string) string
	Exchange(ctx context.Context, code, codeVerifier, nonce string) (domain.ExternalProfile, error)
}

type Clock interface {
	Now() time.Time
}

// TxManager menjalankan fn dalam satu DB transaction. Repository yang dipanggil
// dengan ctx dari fn otomatis ikut transaksi tersebut.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// OTPCodec membuat kode OTP acak dan menghitung/membandingkan hash-nya.
type OTPCodec interface {
	Generate() (string, error)
	Hash(code string) []byte
	// Verify wajib constant-time.
	Verify(hash []byte, code string) bool
}

// Notifier mengirim email transaksional. Implementasi boleh asynchronous;
// error hanya dicatat, tidak menggagalkan use case.
type Notifier interface {
	EmailVerification(ctx context.Context, to, name, code string, ttl time.Duration) error
	PasswordReset(ctx context.Context, to, name, code string, ttl time.Duration) error
	PasswordChanged(ctx context.Context, to, name string) error
}

// UserDeletionHook dipanggil di dalam transaksi hapus akun, sehingga modul
// lain (mis. finance) bisa ikut membersihkan datanya secara atomic.
type UserDeletionHook interface {
	OnUserDeleted(ctx context.Context, userID uuid.UUID, at time.Time) error
}
