package http

import (
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/shared/pagination"
)

// ---------- 2FA ----------

// MFAChallengeResponse dikembalikan /auth/login bila akun memakai 2FA.
type MFAChallengeResponse struct {
	MFARequired bool      `json:"mfa_required" example:"true"`
	MFAToken    string    `json:"mfa_token" example:"eyJhbGciOiJIUzI1NiIs..."`
	ExpiresAt   time.Time `json:"expires_at"`
} //	@name	auth.MFAChallengeResponse

// LoginMFARequest adalah body POST /auth/login/2fa. Code = TOTP 6 digit atau recovery code.
type LoginMFARequest struct {
	MFAToken string `json:"mfa_token" validate:"required,max=2048"`
	Code     string `json:"code" validate:"required,min=6,max=32" example:"123456"`
} //	@name	auth.LoginMFARequest

// MFACodeRequest adalah body enable / regenerate recovery codes (kode TOTP).
type MFACodeRequest struct {
	Code string `json:"code" validate:"required,len=6,numeric" example:"123456"`
} //	@name	auth.MFACodeRequest

// DisableMFARequest butuh password + kode TOTP atau recovery code.
type DisableMFARequest struct {
	Password string `json:"password" validate:"required,max=72"`
	Code     string `json:"code" validate:"required,min=6,max=32" example:"123456"`
} //	@name	auth.DisableMFARequest

// MFASetupResponse berisi secret base32 + URI otpauth:// (render sebagai QR code).
type MFASetupResponse struct {
	Secret     string `json:"secret" example:"JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"`
	OTPAuthURI string `json:"otpauth_uri" example:"otpauth://totp/go-auth-clean:budi@example.com?secret=...&issuer=go-auth-clean"`
} //	@name	auth.MFASetupResponse

// RecoveryCodesResponse: recovery code hanya ditampilkan sekali.
type RecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes" example:"abcde-fghij,klmno-pqrst"`
} //	@name	auth.RecoveryCodesResponse

// MFAStatusResponse adalah status 2FA user.
type MFAStatusResponse struct {
	Enabled                bool       `json:"enabled" example:"true"`
	Pending                bool       `json:"pending" example:"false"`
	RecoveryCodesRemaining int        `json:"recovery_codes_remaining" example:"10"`
	EnabledAt              *time.Time `json:"enabled_at"`
} //	@name	auth.MFAStatusResponse

// ---------- OAuth ----------

// OAuthStartResponse berisi URL tujuan redirect browser (Google consent screen).
type OAuthStartResponse struct {
	AuthURL string `json:"auth_url" example:"https://accounts.google.com/o/oauth2/v2/auth?..."`
} //	@name	auth.OAuthStartResponse

// OAuthExchangeRequest menukar kode sekali pakai (dari redirect callback) dengan token.
type OAuthExchangeRequest struct {
	Code string `json:"code" validate:"required,max=128"`
} //	@name	auth.OAuthExchangeRequest

// IdentityResponse adalah akun eksternal yang terhubung.
type IdentityResponse struct {
	Provider  string    `json:"provider" example:"google"`
	Email     string    `json:"email" example:"budi@gmail.com"`
	CreatedAt time.Time `json:"created_at"`
} //	@name	auth.IdentityResponse

// ---------- API keys ----------

// CreateAPIKeyRequest adalah body POST /users/me/api-keys.
type CreateAPIKeyRequest struct {
	Name   string   `json:"name" validate:"required,min=1,max=100" example:"CI pipeline"`
	Scopes []string `json:"scopes" validate:"required,min=1,max=2,dive,oneof=read write" example:"read"`
	// ExpiresInDays 0/kosong = default server (90 hari). Maksimal 365.
	ExpiresInDays int `json:"expires_in_days,omitempty" validate:"omitempty,min=1,max=365" example:"90"`
} //	@name	auth.CreateAPIKeyRequest

// APIKeyResponse adalah metadata API key (secret tidak pernah dikembalikan lagi).
type APIKeyResponse struct {
	ID         uuid.UUID  `json:"id" swaggertype:"string" format:"uuid"`
	Name       string     `json:"name" example:"CI pipeline"`
	Prefix     string     `json:"prefix" example:"ab12cd34"`
	Scopes     []string   `json:"scopes" example:"read"`
	Active     bool       `json:"active" example:"true"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
} //	@name	auth.APIKeyResponse

// CreatedAPIKeyResponse: field key (plaintext) HANYA muncul sekali di sini.
type CreatedAPIKeyResponse struct {
	APIKeyResponse
	Key string `json:"key" example:"gac_live_ab12cd34_0123456789abcdefghijABCDEFGHIJ01"`
} //	@name	auth.CreatedAPIKeyResponse

// ---------- Admin ----------

// RoleRequest adalah body grant/revoke role.
type RoleRequest struct {
	Role string `json:"role" validate:"required,oneof=user admin" example:"admin"`
} //	@name	auth.RoleRequest

func toAPIKeyResponse(k domain.APIKey, now time.Time) APIKeyResponse {
	scopes := make([]string, len(k.Scopes))
	for i, s := range k.Scopes {
		scopes[i] = string(s)
	}
	return APIKeyResponse{ID: k.ID, Name: k.Name, Prefix: k.Prefix, Scopes: scopes, Active: k.IsActive(now),
		ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt, CreatedAt: k.CreatedAt}
}

func toMFAStatusResponse(s app.MFAStatus) MFAStatusResponse {
	return MFAStatusResponse{Enabled: s.Enabled, Pending: s.Pending, RecoveryCodesRemaining: s.RecoveryCodesRemaining, EnabledAt: s.EnabledAt}
}

// ---------- Envelopes ----------

// MFAChallengeEnvelope adalah response {"data": MFAChallengeResponse}.
type MFAChallengeEnvelope struct {
	Data MFAChallengeResponse `json:"data"`
} //	@name	auth.MFAChallengeEnvelope

// MFASetupEnvelope adalah response {"data": MFASetupResponse}.
type MFASetupEnvelope struct {
	Data MFASetupResponse `json:"data"`
} //	@name	auth.MFASetupEnvelope

// RecoveryCodesEnvelope adalah response {"data": RecoveryCodesResponse}.
type RecoveryCodesEnvelope struct {
	Data RecoveryCodesResponse `json:"data"`
} //	@name	auth.RecoveryCodesEnvelope

// MFAStatusEnvelope adalah response {"data": MFAStatusResponse}.
type MFAStatusEnvelope struct {
	Data MFAStatusResponse `json:"data"`
} //	@name	auth.MFAStatusEnvelope

// OAuthStartEnvelope adalah response {"data": OAuthStartResponse}.
type OAuthStartEnvelope struct {
	Data OAuthStartResponse `json:"data"`
} //	@name	auth.OAuthStartEnvelope

// IdentityListEnvelope adalah response {"data": [IdentityResponse]}.
type IdentityListEnvelope struct {
	Data []IdentityResponse `json:"data"`
} //	@name	auth.IdentityListEnvelope

// APIKeyListEnvelope adalah response {"data": [APIKeyResponse]}.
type APIKeyListEnvelope struct {
	Data []APIKeyResponse `json:"data"`
} //	@name	auth.APIKeyListEnvelope

// CreatedAPIKeyEnvelope adalah response {"data": CreatedAPIKeyResponse}.
type CreatedAPIKeyEnvelope struct {
	Data CreatedAPIKeyResponse `json:"data"`
} //	@name	auth.CreatedAPIKeyEnvelope

// UserListEnvelope adalah response {"data": [UserResponse], "meta": PageMeta}.
type UserListEnvelope struct {
	Data []UserResponse      `json:"data"`
	Meta pagination.PageMeta `json:"meta"`
} //	@name	auth.UserListEnvelope

// WhoAmIResponse adalah principal pemanggil (access token atau API key).
type WhoAmIResponse struct {
	UserID     uuid.UUID  `json:"user_id" swaggertype:"string" format:"uuid"`
	AuthMethod string     `json:"auth_method" example:"api_key" enums:"access_token,api_key"`
	Roles      []string   `json:"roles" example:"user"`
	SessionID  *uuid.UUID `json:"session_id,omitempty" swaggertype:"string" format:"uuid"`
	APIKeyID   *uuid.UUID `json:"api_key_id,omitempty" swaggertype:"string" format:"uuid"`
	Scopes     []string   `json:"scopes" example:"read"`
} //	@name	auth.WhoAmIResponse

// WhoAmIEnvelope adalah response {"data": WhoAmIResponse}.
type WhoAmIEnvelope struct {
	Data WhoAmIResponse `json:"data"`
} //	@name	auth.WhoAmIEnvelope
