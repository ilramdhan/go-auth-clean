package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type AuditEventType string

const (
	EventUserRegistered         AuditEventType = "user_registered"
	EventEmailVerified          AuditEventType = "email_verified"
	EventOTPFailed              AuditEventType = "otp_failed"
	EventLoginSucceeded         AuditEventType = "login_succeeded"
	EventLoginFailed            AuditEventType = "login_failed"
	EventAccountLocked          AuditEventType = "account_locked"
	EventRefreshReuseDetected   AuditEventType = "refresh_token_reuse_detected"
	EventLogout                 AuditEventType = "logout"
	EventLogoutAll              AuditEventType = "logout_all"
	EventSessionRevoked         AuditEventType = "session_revoked"
	EventPasswordChanged        AuditEventType = "password_changed"
	EventPasswordResetRequested AuditEventType = "password_reset_requested"
	EventPasswordReset          AuditEventType = "password_reset"
	EventProfileUpdated         AuditEventType = "profile_updated"
	EventAccountDeleted         AuditEventType = "account_deleted"

	EventMFAEnabled             AuditEventType = "mfa_enabled"
	EventMFADisabled            AuditEventType = "mfa_disabled"
	EventMFAFailed              AuditEventType = "mfa_failed"
	EventMFARecoveryCodeUsed    AuditEventType = "mfa_recovery_code_used"
	EventMFARecoveryRegenerated AuditEventType = "mfa_recovery_codes_regenerated"
	EventOAuthLogin             AuditEventType = "oauth_login"
	EventOAuthLinked            AuditEventType = "oauth_linked"
	EventUserSuspended          AuditEventType = "user_suspended"
	EventUserActivated          AuditEventType = "user_activated"
	EventRoleGranted            AuditEventType = "role_granted"
	EventRoleRevoked            AuditEventType = "role_revoked"
	EventAPIKeyCreated          AuditEventType = "api_key_created"
	EventAPIKeyRevoked          AuditEventType = "api_key_revoked"
)

type AuditOutcome string

const (
	OutcomeSuccess AuditOutcome = "success"
	OutcomeFailure AuditOutcome = "failure"
)

// AuditEvent adalah catatan append-only. Metadata TIDAK BOLEH berisi
// password, token, atau OTP.
type AuditEvent struct {
	ID         uuid.UUID
	UserID     uuid.UUID // uuid.Nil = tidak terkait user (mis. email tak dikenal)
	EventType  AuditEventType
	Outcome    AuditOutcome
	EmailHash  []byte
	SessionID  uuid.UUID // uuid.Nil = tidak ada
	ClientIP   string
	UserAgent  string
	RequestID  string
	Metadata   map[string]string
	OccurredAt time.Time
}

// AuditKeyset adalah posisi keyset pagination (occurred_at, id) DESC.
type AuditKeyset struct {
	OccurredAt time.Time
	ID         uuid.UUID
}

type AuditRepository interface {
	Record(ctx context.Context, e *AuditEvent) error
	// ListByUser mengembalikan maksimal limit event milik user, lebih lama dari after.
	ListByUser(ctx context.Context, userID uuid.UUID, after *AuditKeyset, limit int) ([]AuditEvent, error)
	// AnonymizeBefore menghapus IP & user agent event lebih lama dari before (retensi PII).
	AnonymizeBefore(ctx context.Context, before time.Time) (int64, error)
}
