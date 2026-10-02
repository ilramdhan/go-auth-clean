package app

import (
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

// Input/output use case. Bentuknya mirip message protobuf supaya nanti
// adapter gRPC cukup memetakan field, tanpa mengubah use case.

// RequestMeta adalah info klien untuk session & audit log.
type RequestMeta struct {
	ClientIP  string
	UserAgent string
}

type RegisterInput struct {
	Email    string
	Password string
	FullName string
	Meta     RequestMeta
}

type VerifyEmailInput struct {
	Email string
	Code  string
	Meta  RequestMeta
}

type VerifyEmailResult struct {
	AlreadyVerified bool
}

type ResetPasswordInput struct {
	Email       string
	Code        string
	NewPassword string
	Meta        RequestMeta
}

type LoginInput struct {
	Email     string
	Password  string
	ClientIP  string
	UserAgent string
}

// LoginResult: bila MFARequired=true, Tokens kosong dan client wajib
// memanggil LoginMFA dengan MFAToken + kode authenticator.
type LoginResult struct {
	Tokens            TokenPair
	User              *domain.User
	MFARequired       bool
	MFAToken          string
	MFATokenExpiresAt time.Time
}

type LoginMFAInput struct {
	MFAToken string
	Code     string
	Meta     RequestMeta
}

// Principal adalah identitas hasil autentikasi (JWT atau API key).
type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID // uuid.Nil untuk API key
	Roles     []domain.Role
	APIKeyID  uuid.UUID // != uuid.Nil bila lewat API key
	Scopes    []domain.Scope
}

// MFASetup berisi secret yang harus dimasukkan user ke authenticator.
type MFASetup struct {
	Secret     string
	OTPAuthURI string
}

type MFACodeInput struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Code      string
	Meta      RequestMeta
}

type DisableMFAInput struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Password  string
	Code      string
	Meta      RequestMeta
}

type MFAStatus struct {
	Enabled                bool
	Pending                bool
	RecoveryCodesRemaining int
	EnabledAt              *time.Time
}

// OAuthStart berisi URL redirect ke provider + nilai yang wajib disimpan
// adapter (cookie terenkripsi) sampai callback.
type OAuthStart struct {
	AuthURL  string
	State    string
	Verifier string
	Nonce    string
}

type OAuthCallbackInput struct {
	Provider string
	Code     string
	Verifier string
	Nonce    string
	// LinkUserID != Nil: callback untuk menghubungkan akun ke user yang login.
	LinkUserID uuid.UUID
	Meta       RequestMeta
}

// OAuthCallbackResult: LoginCode diisi untuk login, Linked=true untuk link.
type OAuthCallbackResult struct {
	LoginCode string
	Linked    bool
}

type AdminActionInput struct {
	ActorID  uuid.UUID
	TargetID uuid.UUID
	Meta     RequestMeta
}

type RoleChangeInput struct {
	ActorID  uuid.UUID
	TargetID uuid.UUID
	Role     string
	Meta     RequestMeta
}

type CreateAPIKeyInput struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Name      string
	Scopes    []string
	// ExpiresIn 0 = default (Config.APIKeyDefaultTTL), maksimal 365 hari.
	ExpiresIn time.Duration
	Meta      RequestMeta
}

// CreatedAPIKey: Key (plaintext) hanya dikembalikan sekali saat dibuat.
type CreatedAPIKey struct {
	APIKey domain.APIKey
	Key    string
}

type RevokeAPIKeyInput struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	KeyID     uuid.UUID
	Meta      RequestMeta
}

type RefreshInput struct {
	RefreshToken string
	ClientIP     string
	UserAgent    string
}

type LogoutInput struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Meta      RequestMeta
}

type LogoutAllInput struct {
	UserID         uuid.UUID
	SessionID      uuid.UUID
	IncludeCurrent bool
	Meta           RequestMeta
}

type ChangePasswordInput struct {
	UserID      uuid.UUID
	SessionID   uuid.UUID
	OldPassword string
	NewPassword string
	Meta        RequestMeta
}

type UpdateProfileInput struct {
	UserID   uuid.UUID
	FullName *string // nil = tidak diubah
	Meta     RequestMeta
}

type DeleteAccountInput struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Password  string
	Meta      RequestMeta
}

type RevokeSessionInput struct {
	UserID    uuid.UUID
	SessionID uuid.UUID // sesi yang sedang dipakai (untuk audit)
	FamilyID  uuid.UUID // id sesi (device) yang dicabut
	Meta      RequestMeta
}

// SessionInfo adalah satu device login milik user.
type SessionInfo struct {
	domain.DeviceSession
	Current bool
}

type TokenPair struct {
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
	SessionID             uuid.UUID
	// FamilyID adalah id device session (stabil walau token dirotasi).
	FamilyID uuid.UUID
	roles    []domain.Role
}

// CleanupResult berisi jumlah baris yang dibersihkan job maintenance.
type CleanupResult struct {
	Sessions        int64
	OTPs            int64
	Unverified      int64
	AuditAnonymized int64
	LoginCodes      int64
}
