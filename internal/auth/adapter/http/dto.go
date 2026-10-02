package http

import (
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/shared/pagination"
)

// Request/response DTO = kontrak API publik. Sengaja dipisah dari entity domain
// (mirip message protobuf) agar field sensitif seperti PasswordHash tidak bocor.

// RegisterRequest adalah body POST /auth/register.
type RegisterRequest struct {
	Email    string `json:"email" validate:"required,email,max=254" example:"budi@example.com"`
	Password string `json:"password" validate:"required,min=8,max=72" example:"Rahasia123!"`
	FullName string `json:"full_name" validate:"required,min=1,max=100" example:"Budi Santoso"`
} //	@name	auth.RegisterRequest

// LoginRequest adalah body POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email,max=254" example:"budi@example.com"`
	Password string `json:"password" validate:"required,max=72" example:"Rahasia123!"`
} //	@name	auth.LoginRequest

// RefreshRequest adalah body POST /auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required,max=512"`
} //	@name	auth.RefreshRequest

// LogoutAllRequest adalah body opsional POST /auth/logout-all.
type LogoutAllRequest struct {
	// IncludeCurrent=true ikut mencabut sesi yang sedang dipakai.
	IncludeCurrent bool `json:"include_current" example:"false"`
} //	@name	auth.LogoutAllRequest

// VerifyEmailRequest adalah body POST /auth/verify-email.
type VerifyEmailRequest struct {
	Email string `json:"email" validate:"required,email,max=254" example:"budi@example.com"`
	Code  string `json:"code" validate:"required,len=6,numeric" example:"123456"`
} //	@name	auth.VerifyEmailRequest

// EmailRequest adalah body resend verification / forgot password.
type EmailRequest struct {
	Email string `json:"email" validate:"required,email,max=254" example:"budi@example.com"`
} //	@name	auth.EmailRequest

// ResetPasswordRequest adalah body POST /auth/password/reset.
type ResetPasswordRequest struct {
	Email       string `json:"email" validate:"required,email,max=254" example:"budi@example.com"`
	Code        string `json:"code" validate:"required,len=6,numeric" example:"123456"`
	NewPassword string `json:"new_password" validate:"required,min=8,max=72" example:"RahasiaBaru123!"`
} //	@name	auth.ResetPasswordRequest

// ChangePasswordRequest adalah body PUT/POST /users/me/password.
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" validate:"required,max=72"`
	NewPassword string `json:"new_password" validate:"required,min=8,max=72"`
} //	@name	auth.ChangePasswordRequest

// UpdateProfileRequest adalah body PATCH /users/me. Field kosong (absent) tidak diubah.
type UpdateProfileRequest struct {
	FullName *string `json:"full_name,omitempty" validate:"omitempty,min=1,max=100" example:"Budi S."`
} //	@name	auth.UpdateProfileRequest

// DeleteAccountRequest adalah body DELETE /users/me (re-autentikasi password).
type DeleteAccountRequest struct {
	Password string `json:"password" validate:"required,max=72"`
} //	@name	auth.DeleteAccountRequest

// UserResponse adalah representasi publik user (tanpa password hash).
type UserResponse struct {
	ID              uuid.UUID  `json:"id" swaggertype:"string" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6a"`
	Email           string     `json:"email" example:"budi@example.com"`
	FullName        string     `json:"full_name" example:"Budi Santoso"`
	Status          string     `json:"status" example:"active" enums:"pending_verification,active,suspended"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	// Roles selalu memuat "user"; "admin" bila punya akses admin.
	Roles      []string `json:"roles" example:"user"`
	MFAEnabled bool     `json:"mfa_enabled" example:"false"`
	// HasPassword false untuk akun yang dibuat via OAuth (belum set password).
	HasPassword bool `json:"has_password" example:"true"`
} //	@name	auth.UserResponse

// TokenResponse berisi pasangan access + refresh token.
type TokenResponse struct {
	AccessToken           string    `json:"access_token" example:"eyJhbGciOiJIUzI1NiIs..."`
	TokenType             string    `json:"token_type" example:"Bearer"`
	ExpiresIn             int64     `json:"expires_in" example:"900"`
	RefreshToken          string    `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time `json:"refresh_token_expires_at"`
	// SessionID adalah id device session (sama dengan id di GET /users/me/sessions).
	SessionID uuid.UUID `json:"session_id" swaggertype:"string" format:"uuid"`
} //	@name	auth.TokenResponse

// LoginResponse adalah token + profil user.
type LoginResponse struct {
	// MFARequired selalu false di sini; bila 2FA aktif server membalas 200 MFAChallengeEnvelope
	// (mfa_required=true + mfa_token) yang dilanjutkan ke POST /auth/login/2fa.
	MFARequired bool `json:"mfa_required" example:"false"`
	TokenResponse
	User UserResponse `json:"user"`
} //	@name	auth.LoginResponse

// VerifyEmailResponse adalah hasil verifikasi email.
type VerifyEmailResponse struct {
	Verified        bool `json:"verified" example:"true"`
	AlreadyVerified bool `json:"already_verified" example:"false"`
} //	@name	auth.VerifyEmailResponse

// AcceptedResponse dipakai untuk endpoint anti-enumeration (selalu 202).
type AcceptedResponse struct {
	Message string `json:"message" example:"jika email terdaftar, kode telah dikirim"`
} //	@name	auth.AcceptedResponse

// SessionResponse adalah satu perangkat yang sedang login.
type SessionResponse struct {
	ID         uuid.UUID `json:"id" swaggertype:"string" format:"uuid"`
	ClientIP   string    `json:"client_ip" example:"203.0.113.10"`
	UserAgent  string    `json:"user_agent" example:"Mozilla/5.0"`
	Current    bool      `json:"current" example:"true"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
} //	@name	auth.SessionResponse

// SecurityEventResponse adalah satu entri audit keamanan milik user.
type SecurityEventResponse struct {
	ID         uuid.UUID         `json:"id" swaggertype:"string" format:"uuid"`
	EventType  string            `json:"event_type" example:"login_succeeded"`
	Outcome    string            `json:"outcome" example:"success" enums:"success,failure"`
	ClientIP   string            `json:"client_ip,omitempty" example:"203.0.113.10"`
	UserAgent  string            `json:"user_agent,omitempty" example:"Mozilla/5.0"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	OccurredAt time.Time         `json:"occurred_at"`
} //	@name	auth.SecurityEventResponse

func toUserResponse(u *domain.User) UserResponse {
	return UserResponse{
		ID:              u.ID,
		Email:           u.Email,
		FullName:        u.FullName,
		Status:          string(u.Status),
		EmailVerifiedAt: u.EmailVerifiedAt,
		LastLoginAt:     u.LastLoginAt,
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
		Roles:           roleStrings(u.Roles),
		MFAEnabled:      u.MFAEnabled,
		HasPassword:     u.HasPassword(),
	}
}

func roleStrings(roles []domain.Role) []string {
	out := domain.RoleStrings(roles)
	if len(out) == 0 {
		out = []string{string(domain.RoleUser)}
	}
	return out
}

func toTokenResponse(t app.TokenPair, now time.Time) TokenResponse {
	return TokenResponse{
		AccessToken:           t.AccessToken,
		TokenType:             "Bearer",
		ExpiresIn:             max(int64(t.AccessTokenExpiresAt.Sub(now).Seconds()), 0),
		RefreshToken:          t.RefreshToken,
		RefreshTokenExpiresAt: t.RefreshTokenExpiresAt,
		SessionID:             t.FamilyID,
	}
}

func toSessionResponse(s app.SessionInfo) SessionResponse {
	return SessionResponse{
		ID: s.FamilyID, ClientIP: s.ClientIP, UserAgent: s.UserAgent, Current: s.Current,
		CreatedAt: s.CreatedAt, LastUsedAt: s.LastUsedAt, ExpiresAt: s.ExpiresAt,
	}
}

func toSecurityEventResponse(e domain.AuditEvent) SecurityEventResponse {
	return SecurityEventResponse{
		ID: e.ID, EventType: string(e.EventType), Outcome: string(e.Outcome),
		ClientIP: e.ClientIP, UserAgent: e.UserAgent, Metadata: e.Metadata, OccurredAt: e.OccurredAt,
	}
}

// Named response wrapper untuk swagger (envelope {"data": ...}).

// UserEnvelope adalah response {"data": UserResponse}.
type UserEnvelope struct {
	Data UserResponse `json:"data"`
} //	@name	auth.UserEnvelope

// TokenEnvelope adalah response {"data": TokenResponse}.
type TokenEnvelope struct {
	Data TokenResponse `json:"data"`
} //	@name	auth.TokenEnvelope

// LoginEnvelope adalah response {"data": LoginResponse}.
type LoginEnvelope struct {
	Data LoginResponse `json:"data"`
} //	@name	auth.LoginEnvelope

// VerifyEmailEnvelope adalah response {"data": VerifyEmailResponse}.
type VerifyEmailEnvelope struct {
	Data VerifyEmailResponse `json:"data"`
} //	@name	auth.VerifyEmailEnvelope

// AcceptedEnvelope adalah response {"data": AcceptedResponse}.
type AcceptedEnvelope struct {
	Data AcceptedResponse `json:"data"`
} //	@name	auth.AcceptedEnvelope

// SessionListEnvelope adalah response {"data": [SessionResponse]}.
type SessionListEnvelope struct {
	Data []SessionResponse `json:"data"`
} //	@name	auth.SessionListEnvelope

// SecurityEventListEnvelope adalah response {"data": [...], "meta": PageMeta}.
type SecurityEventListEnvelope struct {
	Data []SecurityEventResponse `json:"data"`
	Meta pagination.PageMeta     `json:"meta"`
} //	@name	auth.SecurityEventListEnvelope
