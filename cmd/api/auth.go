package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	authpg "go-auth-clean/internal/auth/adapter/postgres"
	"go-auth-clean/internal/auth/adapter/security"
	authapp "go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/config"
	"go-auth-clean/internal/platform/crypto"
)

// authEnv adalah konfigurasi khusus modul auth (opsional, ada default aman).
// Sengaja dibaca di sini agar platform/config tidak perlu tahu detail auth.
type authEnv struct {
	Service         authapp.Config
	CleanupInterval time.Duration
	EmailRate       config.Rate // per alamat email
	UserRate        config.Rate // per user untuk endpoint sensitif
	// MFATokenTTL: umur mfa_token antara langkah password dan kode 2FA.
	MFATokenTTL time.Duration
	// MFAMaxAttempts: maksimum percobaan kode per mfa_token (challenge).
	MFAMaxAttempts int
	// APIKeyRate: rate limit per API key.
	APIKeyRate config.Rate
}

func loadAuthEnv(cfg config.Config) (authEnv, error) {
	var errs []error
	dur := func(key string, def time.Duration) time.Duration {
		v := os.Getenv(key)
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s: durasi tidak valid", key))
			return def
		}
		return d
	}
	num := func(key string, def int) int {
		v := os.Getenv(key)
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("%s: harus bilangan bulat positif", key))
			return def
		}
		return n
	}

	e := authEnv{
		Service: authapp.Config{
			RefreshTTL:          cfg.JWT.RefreshTokenTTL,
			OTPTTL:              cfg.OTP.TTL,
			PasswordResetTTL:    cfg.Security.PasswordResetTTL,
			OTPMaxAttempts:      cfg.OTP.MaxAttempts,
			ResendCooldown:      dur("AUTH_OTP_RESEND_COOLDOWN", time.Minute),
			OTPDailyQuota:       num("AUTH_OTP_DAILY_QUOTA", 5),
			MaxSessions:         num("AUTH_MAX_SESSIONS", 10),
			UnverifiedRetention: dur("AUTH_UNVERIFIED_RETENTION", 30*24*time.Hour),
			AuditPIIRetention:   dur("AUTH_AUDIT_PII_RETENTION", 90*24*time.Hour),
			OAuthLoginCodeTTL:   dur("AUTH_OAUTH_LOGIN_CODE_TTL", time.Minute),
			APIKeyMax:           num("AUTH_API_KEY_MAX", 10),
			APIKeyDefaultTTL:    dur("AUTH_API_KEY_DEFAULT_TTL", 90*24*time.Hour),
			APIKeyEnv:           apiKeyEnv(cfg),
			TOTPIssuer:          cfg.Security.TOTPIssuer,
		},
		MFATokenTTL:    dur("AUTH_MFA_TOKEN_TTL", 5*time.Minute),
		MFAMaxAttempts: num("AUTH_MFA_MAX_ATTEMPTS", 5),
		APIKeyRate: config.Rate{
			Limit: num("AUTH_API_KEY_RATE_LIMIT", 600), Period: dur("AUTH_API_KEY_RATE_PERIOD", time.Minute),
			Burst: num("AUTH_API_KEY_RATE_BURST", 60),
		},
		CleanupInterval: dur("AUTH_CLEANUP_INTERVAL", time.Hour),
		EmailRate: config.Rate{
			Limit: num("AUTH_EMAIL_RATE_LIMIT", 10), Period: dur("AUTH_EMAIL_RATE_PERIOD", 15*time.Minute),
			Burst: num("AUTH_EMAIL_RATE_BURST", 5),
		},
		UserRate: config.Rate{Limit: 10, Period: time.Hour, Burst: 5},
	}
	if len(errs) > 0 {
		return authEnv{}, fmt.Errorf("auth config: %v", errs)
	}
	return e, nil
}

// apiKeyEnv: label "live" hanya di production supaya key dev/staging mudah dibedakan.
func apiKeyEnv(cfg config.Config) string {
	if cfg.IsProduction() {
		return "live"
	}
	return "test"
}

// wireAuthP2 mengisi dependency fitur P2 (RBAC, 2FA, OAuth, API key) ke deps dan
// mengembalikan sealer (AES-256-GCM) yang juga dipakai untuk cookie state OAuth.
// Provider Google hanya dibuat bila kredensialnya lengkap; selain itu OAuth nonaktif.
func wireAuthP2(ctx context.Context, cfg config.Config, e authEnv, pool *pgxpool.Pool, d *authapp.Deps) (*crypto.SecretBox, error) {
	sealer, err := crypto.NewSecretBox(cfg.Security.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("auth sealer: %w", err)
	}
	d.Roles = authpg.NewRoleRepository(pool)
	d.MFA = authpg.NewMFARepository(pool)
	d.Identities = authpg.NewIdentityRepository(pool)
	d.LoginCodes = authpg.NewLoginCodeRepository(pool)
	d.APIKeys = authpg.NewAPIKeyRepository(pool)
	d.MFATokens = security.NewMFATokenIssuer(cfg.JWT.Secret, cfg.JWT.Issuer, e.MFATokenTTL)
	d.TOTP = security.NewTOTP(cfg.Security.TOTPIssuer)
	d.Sealer = sealer
	if cfg.OAuth.GoogleEnabled() {
		g := cfg.OAuth
		google, err := security.NewGoogleProvider(ctx, g.GoogleClientID, g.GoogleClientSecret, g.GoogleRedirectURL)
		if err != nil {
			return nil, err
		}
		d.OAuthProviders = map[string]authapp.OAuthProvider{domain.ProviderGoogle: google}
	}
	return sealer, nil
}
