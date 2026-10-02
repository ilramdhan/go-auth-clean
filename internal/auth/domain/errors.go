package domain

import "errors"

// Error domain murni: tidak tahu soal HTTP/gRPC. Adapter yang memetakan
// error ini ke status code masing-masing protokol.
var (
	ErrUserNotFound       = errors.New("user not found")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrAccountLocked      = errors.New("account temporarily locked")
	ErrAccountInactive    = errors.New("account is not active")
	ErrEmailNotVerified   = errors.New("email not verified")
	ErrAccountSuspended   = errors.New("account suspended")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrInvalidFullName    = errors.New("invalid full name")
	ErrWeakPassword       = errors.New("password does not meet policy")
	ErrPasswordReused     = errors.New("new password must differ from current password")
	ErrNoFieldsToUpdate   = errors.New("no fields to update")

	ErrSessionNotFound = errors.New("session not found")
	ErrSessionInvalid  = errors.New("session expired or revoked")
	ErrTokenReused     = errors.New("refresh token reuse detected")

	ErrOTPNotFound        = errors.New("otp not found")
	ErrInvalidOTP         = errors.New("invalid otp")
	ErrOTPExpired         = errors.New("otp expired")
	ErrOTPTooManyAttempts = errors.New("otp attempts exceeded")
)

// ErrOTPActiveExists: ada OTP aktif lain yang dibuat bersamaan (race resend).
var ErrOTPActiveExists = errors.New("active otp already exists")

// Error P2: 2FA, RBAC, OAuth, API key.
var (
	ErrMFANotEnabled      = errors.New("two-factor authentication is not enabled")
	ErrMFAAlreadyEnabled  = errors.New("two-factor authentication is already enabled")
	ErrMFASetupRequired   = errors.New("two-factor setup has not been started")
	ErrInvalidMFACode     = errors.New("invalid two-factor code")
	ErrMFACodeReplayed    = errors.New("two-factor code already used")
	ErrMFATokenInvalid    = errors.New("mfa token invalid or expired")
	ErrMFANotFound        = errors.New("mfa config not found")
	ErrForbidden          = errors.New("insufficient permission")
	ErrInvalidRole        = errors.New("invalid role")
	ErrCannotModifySelf   = errors.New("cannot perform this action on your own account")
	ErrInvalidTransition  = errors.New("invalid user status transition")
	ErrOAuthDisabled      = errors.New("oauth provider disabled")
	ErrOAuthFailed        = errors.New("oauth authentication failed")
	ErrOAuthEmailNotVerif = errors.New("oauth email not verified by provider")
	ErrIdentityTaken      = errors.New("external identity already linked")
	ErrIdentityNotFound   = errors.New("external identity not found")
	ErrOAuthAccountExists = errors.New("an account with this email already exists; sign in and link it")
	ErrLoginCodeInvalid   = errors.New("login code invalid or expired")
	ErrAPIKeyNotFound     = errors.New("api key not found")
	ErrAPIKeyInvalid      = errors.New("api key invalid, expired or revoked")
	ErrAPIKeyLimit        = errors.New("api key limit reached")
	ErrInvalidAPIKeyName  = errors.New("invalid api key name")
	ErrInvalidScope       = errors.New("invalid api key scope")
	ErrInvalidExpiry      = errors.New("invalid api key expiry")
	ErrInvalidStatus      = errors.New("invalid user status filter")
)
