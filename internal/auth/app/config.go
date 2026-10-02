package app

import "time"

// Config berisi aturan bisnis yang bisa diatur dari environment.
type Config struct {
	RefreshTTL       time.Duration
	OTPTTL           time.Duration // OTP verifikasi email
	PasswordResetTTL time.Duration // OTP reset password
	OTPMaxAttempts   int
	// ResendCooldown: jeda minimal antar OTP per (user, purpose).
	ResendCooldown time.Duration
	// OTPDailyQuota: maksimal OTP per (user, purpose) per 24 jam.
	OTPDailyQuota int
	// MaxSessions: maksimal device aktif per user; yang tertua dicabut.
	MaxSessions int
	// UnverifiedRetention: user pending_verification lebih tua dari ini dihapus.
	UnverifiedRetention time.Duration
	// AuditPIIRetention: IP & user agent audit log di-null-kan setelah ini.
	AuditPIIRetention time.Duration
	// OAuthLoginCodeTTL: umur kode sekali pakai dari callback OAuth ke frontend.
	OAuthLoginCodeTTL time.Duration
	// APIKeyMax: maksimal API key aktif per user.
	APIKeyMax int
	// APIKeyDefaultTTL dipakai bila client tidak mengirim masa berlaku.
	APIKeyDefaultTTL time.Duration
	// APIKeyEnv adalah label environment di format key (live/test).
	APIKeyEnv string
	// TOTPIssuer tampil di aplikasi authenticator.
	TOTPIssuer string
}

// withDefaults mengisi nilai nol dengan default yang aman (sesuai PRD).
func (c Config) withDefaults() Config {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	defInt := func(n *int, v int) {
		if *n <= 0 {
			*n = v
		}
	}
	def(&c.RefreshTTL, 7*24*time.Hour)
	def(&c.OTPTTL, 10*time.Minute)
	def(&c.PasswordResetTTL, 30*time.Minute)
	def(&c.ResendCooldown, time.Minute)
	def(&c.UnverifiedRetention, 30*24*time.Hour)
	def(&c.AuditPIIRetention, 90*24*time.Hour)
	defInt(&c.OTPMaxAttempts, 5)
	defInt(&c.OTPDailyQuota, 5)
	defInt(&c.MaxSessions, 10)
	def(&c.OAuthLoginCodeTTL, time.Minute)
	def(&c.APIKeyDefaultTTL, 90*24*time.Hour)
	defInt(&c.APIKeyMax, 10)
	if c.APIKeyEnv != "test" {
		c.APIKeyEnv = "live"
	}
	if c.TOTPIssuer == "" {
		c.TOTPIssuer = "go-auth-clean"
	}
	return c
}
