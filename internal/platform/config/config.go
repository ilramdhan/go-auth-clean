// Package config memuat konfigurasi aplikasi dari environment variable (12-factor).
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
	EnvTest        = "test"
)

type Config struct {
	AppEnv   string
	HTTPAddr string
	LogLevel string
	// AppBaseURL: URL publik API (mis. untuk OAuth callback).
	AppBaseURL string
	// FrontendURL: URL aplikasi frontend (link verifikasi email / reset password).
	FrontendURL string

	HTTP      HTTPConfig
	DB        DBConfig
	JWT       JWTConfig
	RateLimit RateLimitConfig
	SMTP      SMTPConfig
	Security  SecurityConfig
	OTP       OTPConfig
	OAuth     OAuthConfig
	Swagger   SwaggerConfig
	Finance   FinanceConfig
}

// IsProduction true bila APP_ENV=production.
func (c Config) IsProduction() bool { return c.AppEnv == EnvProduction }

type HTTPConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	// CORSAllowedOrigins: daftar origin eksplisit (CORS_ALLOWED_ORIGINS, dipisah koma).
	CORSAllowedOrigins []string
	// TrustedProxies: CIDR/IP proxy tepercaya; kosong = abaikan X-Forwarded-For.
	TrustedProxies []string
}

type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
}

// DSN mengembalikan connection string untuk pgx (user/password di-escape).
func (c DBConfig) DSN() string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     c.Host + ":" + c.Port,
		Path:     "/" + c.Name,
		RawQuery: "sslmode=" + url.QueryEscape(c.SSLMode),
	}
	return u.String()
}

type JWTConfig struct {
	Secret          []byte
	Issuer          string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

// Rate adalah N request per Period dengan burst tertentu (token bucket).
type Rate struct {
	Limit  int
	Period time.Duration
	Burst  int
}

type RateLimitConfig struct {
	Enabled bool
	// Global: semua endpoint per IP (default 300/menit burst 50).
	Global Rate
	// Auth: endpoint auth sensitif (login, register, otp, reset) per IP (default 10/menit burst 5).
	Auth Rate
	// TTL: key idle lebih lama dari ini dibuang dari memory.
	TTL time.Duration
}

type SMTPConfig struct {
	Host       string
	Port       int
	Username   string
	Password   string
	From       string
	RequireTLS bool
}

type SecurityConfig struct {
	// EncryptionKey: 32 byte (dari ENCRYPTION_KEY base64) untuk AES-256-GCM secret at rest.
	EncryptionKey []byte
	TOTPIssuer    string
	// PasswordResetTTL: masa berlaku token reset password.
	PasswordResetTTL time.Duration
}

type OTPConfig struct {
	TTL         time.Duration
	MaxAttempts int
}

type OAuthConfig struct {
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
}

// GoogleEnabled true bila semua kredensial Google OAuth terisi.
func (o OAuthConfig) GoogleEnabled() bool {
	return o.GoogleClientID != "" && o.GoogleClientSecret != "" && o.GoogleRedirectURL != ""
}

type SwaggerConfig struct {
	Enabled bool
}

type FinanceConfig struct {
	RecurringWorkerInterval time.Duration
	DefaultCurrency         string
	DefaultTimezone         string
	// Location hasil time.LoadLocation(DefaultTimezone).
	Location *time.Location
}

// Load membaca env var dan memvalidasinya. Aplikasi harus gagal start (fail fast)
// jika konfigurasi tidak valid, bukan error di tengah jalan.
func Load() (Config, error) {
	p := &parser{}

	appEnv := getEnv("APP_ENV", EnvDevelopment)
	cfg := Config{
		AppEnv:      appEnv,
		HTTPAddr:    getEnv("HTTP_ADDR", ":8080"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		AppBaseURL:  strings.TrimRight(getEnv("APP_BASE_URL", "http://localhost:8080"), "/"),
		FrontendURL: strings.TrimRight(getEnv("FRONTEND_URL", "http://localhost:3000"), "/"),
		HTTP: HTTPConfig{
			ReadHeaderTimeout:  p.duration("HTTP_READ_HEADER_TIMEOUT", "5s"),
			ReadTimeout:        p.duration("HTTP_READ_TIMEOUT", "10s"),
			WriteTimeout:       p.duration("HTTP_WRITE_TIMEOUT", "15s"),
			IdleTimeout:        p.duration("HTTP_IDLE_TIMEOUT", "60s"),
			ShutdownTimeout:    p.duration("HTTP_SHUTDOWN_TIMEOUT", "10s"),
			CORSAllowedOrigins: splitList(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")),
			TrustedProxies:     splitList(os.Getenv("TRUSTED_PROXIES")),
		},
		DB: DBConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnv("DB_PORT", "5432"),
			User:     getEnv("DB_USER", "postgres"),
			Password: os.Getenv("DB_PASSWORD"),
			Name:     getEnv("DB_NAME", "go_auth_db"),
			SSLMode:  getEnv("DB_SSL_MODE", "disable"),
		},
		JWT: JWTConfig{
			Secret:          []byte(os.Getenv("JWT_SECRET")),
			Issuer:          getEnv("JWT_ISSUER", "go-auth-clean"),
			AccessTokenTTL:  p.duration("ACCESS_TOKEN_TTL", "15m"),
			RefreshTokenTTL: p.duration("REFRESH_TOKEN_TTL", "168h"),
		},
		RateLimit: RateLimitConfig{
			Enabled: p.boolean("RATE_LIMIT_ENABLED", true),
			Global: Rate{
				Limit:  p.integer("RATE_LIMIT_GLOBAL_LIMIT", 300),
				Period: p.duration("RATE_LIMIT_GLOBAL_PERIOD", "1m"),
				Burst:  p.integer("RATE_LIMIT_GLOBAL_BURST", 50),
			},
			Auth: Rate{
				Limit:  p.integer("RATE_LIMIT_AUTH_LIMIT", 10),
				Period: p.duration("RATE_LIMIT_AUTH_PERIOD", "1m"),
				Burst:  p.integer("RATE_LIMIT_AUTH_BURST", 5),
			},
			TTL: p.duration("RATE_LIMIT_TTL", "10m"),
		},
		SMTP: SMTPConfig{
			Host:       getEnv("SMTP_HOST", "localhost"),
			Port:       p.integer("SMTP_PORT", 1025),
			Username:   os.Getenv("SMTP_USER"),
			Password:   os.Getenv("SMTP_PASSWORD"),
			From:       getEnv("SMTP_FROM", "Go Auth <no-reply@localhost>"),
			RequireTLS: p.boolean("SMTP_REQUIRE_TLS", appEnv == EnvProduction),
		},
		Security: SecurityConfig{
			EncryptionKey:    p.base64Key("ENCRYPTION_KEY", 32),
			TOTPIssuer:       getEnv("TOTP_ISSUER", "go-auth-clean"),
			PasswordResetTTL: p.duration("PASSWORD_RESET_TTL", "30m"),
		},
		OTP: OTPConfig{
			TTL:         p.duration("OTP_TTL", "10m"),
			MaxAttempts: p.integer("OTP_MAX_ATTEMPTS", 5),
		},
		OAuth: OAuthConfig{
			GoogleClientID:     os.Getenv("GOOGLE_OAUTH_CLIENT_ID"),
			GoogleClientSecret: os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET"),
			GoogleRedirectURL:  os.Getenv("GOOGLE_OAUTH_REDIRECT_URL"),
		},
		Swagger: SwaggerConfig{
			Enabled: p.boolean("SWAGGER_ENABLED", appEnv != EnvProduction),
		},
		Finance: FinanceConfig{
			RecurringWorkerInterval: p.duration("RECURRING_WORKER_INTERVAL", "5m"),
			DefaultCurrency:         strings.ToUpper(getEnv("DEFAULT_CURRENCY", "IDR")),
			DefaultTimezone:         getEnv("DEFAULT_TIMEZONE", "Asia/Jakarta"),
		},
	}

	cfg.validate(p)
	return cfg, errors.Join(p.errs...)
}

func (c *Config) validate(p *parser) {
	if !slices.Contains([]string{EnvDevelopment, EnvStaging, EnvProduction, EnvTest}, c.AppEnv) {
		p.addf("APP_ENV harus salah satu dari development|staging|production|test")
	}
	if len(c.JWT.Secret) < 32 {
		p.addf("JWT_SECRET wajib diisi minimal 32 byte")
	}
	if c.JWT.AccessTokenTTL <= 0 || c.JWT.RefreshTokenTTL <= c.JWT.AccessTokenTTL {
		p.addf("ACCESS_TOKEN_TTL harus > 0 dan REFRESH_TOKEN_TTL harus lebih besar")
	}
	for _, u := range []struct{ key, val string }{{"APP_BASE_URL", c.AppBaseURL}, {"FRONTEND_URL", c.FrontendURL}} {
		if !validURL(u.val) {
			p.addf("%s harus URL absolut http(s)", u.key)
		}
	}
	for _, o := range c.HTTP.CORSAllowedOrigins {
		if o == "*" {
			// Credentials (cookie refresh token) membutuhkan origin eksplisit.
			p.addf("CORS_ALLOWED_ORIGINS tidak boleh '*'; gunakan origin eksplisit")
			continue
		}
		if !validURL(o) {
			p.addf("CORS_ALLOWED_ORIGINS berisi origin tidak valid: %q", o)
		}
	}
	for _, r := range []struct {
		key  string
		rate Rate
	}{{"RATE_LIMIT_GLOBAL", c.RateLimit.Global}, {"RATE_LIMIT_AUTH", c.RateLimit.Auth}} {
		if r.rate.Limit <= 0 || r.rate.Period <= 0 || r.rate.Burst <= 0 {
			p.addf("%s_LIMIT/_PERIOD/_BURST harus > 0", r.key)
		}
	}
	if c.SMTP.Port <= 0 || c.SMTP.Port > 65535 {
		p.addf("SMTP_PORT tidak valid")
	}
	if c.OTP.TTL <= 0 || c.OTP.MaxAttempts <= 0 {
		p.addf("OTP_TTL dan OTP_MAX_ATTEMPTS harus > 0")
	}
	if c.Security.PasswordResetTTL <= 0 {
		p.addf("PASSWORD_RESET_TTL harus > 0")
	}
	if c.Finance.RecurringWorkerInterval < time.Second {
		p.addf("RECURRING_WORKER_INTERVAL minimal 1s")
	}
	if len(c.Finance.DefaultCurrency) != 3 {
		p.addf("DEFAULT_CURRENCY harus kode ISO 4217 3 huruf")
	}
	loc, err := time.LoadLocation(c.Finance.DefaultTimezone)
	if err != nil {
		p.addf("DEFAULT_TIMEZONE tidak valid: %v", err)
	}
	c.Finance.Location = loc

	g := c.OAuth
	if (g.GoogleClientID != "" || g.GoogleClientSecret != "" || g.GoogleRedirectURL != "") && !g.GoogleEnabled() {
		p.addf("GOOGLE_OAUTH_CLIENT_ID/SECRET/REDIRECT_URL harus diisi semua atau dikosongkan semua")
	}

	if c.IsProduction() {
		if c.DB.SSLMode == "disable" {
			p.addf("DB_SSL_MODE=disable tidak diizinkan di production")
		}
		if !strings.HasPrefix(c.AppBaseURL, "https://") {
			p.addf("APP_BASE_URL wajib https di production")
		}
	}
}

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// parser mengumpulkan semua error parsing agar dilaporkan sekaligus.
type parser struct{ errs []error }

func (p *parser) addf(format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf(format, args...))
}

func (p *parser) duration(key, def string) time.Duration {
	d, err := time.ParseDuration(getEnv(key, def))
	if err != nil {
		p.addf("%s: %w", key, err)
	}
	return d
}

func (p *parser) integer(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.addf("%s harus bilangan bulat", key)
	}
	return n
}

func (p *parser) boolean(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.addf("%s harus true/false", key)
	}
	return b
}

// base64Key membaca key base64 dengan panjang tepat n byte (nilai tidak pernah dicetak).
func (p *parser) base64Key(key string, n int) []byte {
	v := os.Getenv(key)
	if v == "" {
		p.addf("%s wajib diisi (generate: openssl rand -base64 %d)", key, n)
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) != n {
		p.addf("%s harus base64 dari tepat %d byte", key, n)
		return nil
	}
	return b
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, strings.TrimRight(part, "/"))
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
