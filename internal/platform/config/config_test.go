package config_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"go-auth-clean/internal/platform/config"
)

// allKeys dikosongkan dulu agar .env developer tidak memengaruhi test.
var allKeys = []string{
	"APP_ENV", "HTTP_ADDR", "LOG_LEVEL", "APP_BASE_URL", "FRONTEND_URL",
	"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT",
	"CORS_ALLOWED_ORIGINS", "TRUSTED_PROXIES",
	"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSL_MODE",
	"JWT_SECRET", "JWT_ISSUER", "ACCESS_TOKEN_TTL", "REFRESH_TOKEN_TTL",
	"RATE_LIMIT_ENABLED", "RATE_LIMIT_GLOBAL_LIMIT", "RATE_LIMIT_GLOBAL_PERIOD", "RATE_LIMIT_GLOBAL_BURST",
	"RATE_LIMIT_AUTH_LIMIT", "RATE_LIMIT_AUTH_PERIOD", "RATE_LIMIT_AUTH_BURST", "RATE_LIMIT_TTL",
	"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASSWORD", "SMTP_FROM", "SMTP_REQUIRE_TLS",
	"ENCRYPTION_KEY", "TOTP_ISSUER", "PASSWORD_RESET_TTL", "OTP_TTL", "OTP_MAX_ATTEMPTS",
	"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "GOOGLE_OAUTH_REDIRECT_URL",
	"SWAGGER_ENABLED", "RECURRING_WORKER_INTERVAL", "DEFAULT_CURRENCY", "DEFAULT_TIMEZONE",
}

func setBase(t *testing.T) {
	t.Helper()
	for _, k := range allKeys {
		t.Setenv(k, "")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

func TestLoad_Defaults(t *testing.T) {
	setBase(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppEnv != config.EnvDevelopment || cfg.IsProduction() {
		t.Fatalf("AppEnv = %q", cfg.AppEnv)
	}
	if cfg.HTTP.ReadHeaderTimeout != 5*time.Second || cfg.JWT.AccessTokenTTL != 15*time.Minute {
		t.Fatal("default durasi salah")
	}
	if !cfg.RateLimit.Enabled || cfg.RateLimit.Global.Limit != 300 || cfg.RateLimit.Auth.Limit != 10 {
		t.Fatalf("rate default = %+v", cfg.RateLimit)
	}
	if cfg.SMTP.Port != 1025 || cfg.SMTP.RequireTLS {
		t.Fatalf("smtp default = %+v", cfg.SMTP.Port)
	}
	if !cfg.Swagger.Enabled || cfg.OAuth.GoogleEnabled() {
		t.Fatal("swagger/oauth default salah")
	}
	if len(cfg.Security.EncryptionKey) != 32 || cfg.Security.PasswordResetTTL != 30*time.Minute {
		t.Fatal("security default salah")
	}
	if cfg.Finance.DefaultCurrency != "IDR" || cfg.Finance.Location == nil || cfg.Finance.Location.String() != "Asia/Jakarta" {
		t.Fatalf("finance = %+v", cfg.Finance)
	}
	if len(cfg.HTTP.CORSAllowedOrigins) != 1 || cfg.HTTP.TrustedProxies != nil {
		t.Fatalf("lists = %v %v", cfg.HTTP.CORSAllowedOrigins, cfg.HTTP.TrustedProxies)
	}
}

func TestLoad_Overrides(t *testing.T) {
	setBase(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://a.com/, https://b.com")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8,127.0.0.1")
	t.Setenv("RATE_LIMIT_ENABLED", "false")
	t.Setenv("SWAGGER_ENABLED", "false")
	t.Setenv("DEFAULT_CURRENCY", "usd")
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "id")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "secret")
	t.Setenv("GOOGLE_OAUTH_REDIRECT_URL", "http://localhost:8080/cb")
	t.Setenv("DB_PASSWORD", "p@ss/word")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(cfg.HTTP.CORSAllowedOrigins, "|"); got != "https://a.com|https://b.com" {
		t.Fatalf("cors = %s", got)
	}
	if len(cfg.HTTP.TrustedProxies) != 2 || cfg.RateLimit.Enabled || cfg.Swagger.Enabled {
		t.Fatal("override tidak terbaca")
	}
	if cfg.Finance.DefaultCurrency != "USD" || !cfg.OAuth.GoogleEnabled() {
		t.Fatal("currency/oauth override salah")
	}
	if dsn := cfg.DB.DSN(); !strings.Contains(dsn, "p%40ss%2Fword") {
		t.Fatal("password DSN harus di-escape")
	}
}

func TestLoad_Production(t *testing.T) {
	setBase(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_BASE_URL", "https://api.example.com")
	t.Setenv("DB_SSL_MODE", "require")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.IsProduction() || cfg.Swagger.Enabled || !cfg.SMTP.RequireTLS {
		t.Fatal("default production salah")
	}
}

func TestLoad_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantMsg string
	}{
		{"bad app env", map[string]string{"APP_ENV": "dev"}, "APP_ENV"},
		{"short jwt", map[string]string{"JWT_SECRET": "short"}, "JWT_SECRET"},
		{"missing encryption key", map[string]string{"ENCRYPTION_KEY": ""}, "ENCRYPTION_KEY"},
		{"wrong size encryption key", map[string]string{"ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 16))}, "ENCRYPTION_KEY"},
		{"bad duration", map[string]string{"ACCESS_TOKEN_TTL": "abc"}, "ACCESS_TOKEN_TTL"},
		{"refresh <= access", map[string]string{"REFRESH_TOKEN_TTL": "1m"}, "REFRESH_TOKEN_TTL"},
		{"bad url", map[string]string{"FRONTEND_URL": "localhost"}, "FRONTEND_URL"},
		{"cors wildcard", map[string]string{"CORS_ALLOWED_ORIGINS": "*"}, "CORS_ALLOWED_ORIGINS"},
		{"cors invalid", map[string]string{"CORS_ALLOWED_ORIGINS": "ftp://x"}, "CORS_ALLOWED_ORIGINS"},
		{"bad int", map[string]string{"SMTP_PORT": "x"}, "SMTP_PORT"},
		{"port range", map[string]string{"SMTP_PORT": "70000"}, "SMTP_PORT"},
		{"bad bool", map[string]string{"RATE_LIMIT_ENABLED": "maybe"}, "RATE_LIMIT_ENABLED"},
		{"zero rate", map[string]string{"RATE_LIMIT_AUTH_BURST": "0"}, "RATE_LIMIT_AUTH"},
		{"otp attempts", map[string]string{"OTP_MAX_ATTEMPTS": "0"}, "OTP_"},
		{"reset ttl", map[string]string{"PASSWORD_RESET_TTL": "0s"}, "PASSWORD_RESET_TTL"},
		{"worker interval", map[string]string{"RECURRING_WORKER_INTERVAL": "1ms"}, "RECURRING_WORKER_INTERVAL"},
		{"currency length", map[string]string{"DEFAULT_CURRENCY": "RUPIAH"}, "DEFAULT_CURRENCY"},
		{"timezone", map[string]string{"DEFAULT_TIMEZONE": "Mars/Base"}, "DEFAULT_TIMEZONE"},
		{"oauth partial", map[string]string{"GOOGLE_OAUTH_CLIENT_ID": "x"}, "GOOGLE_OAUTH"},
		{"prod sslmode", map[string]string{"APP_ENV": "production", "APP_BASE_URL": "https://x.com"}, "DB_SSL_MODE"},
		{"prod https", map[string]string{"APP_ENV": "production", "DB_SSL_MODE": "require"}, "https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBase(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("err = %v, want contains %q", err, tt.wantMsg)
			}
		})
	}
}

func TestLoad_ErrorDoesNotLeakSecret(t *testing.T) {
	setBase(t)
	t.Setenv("ENCRYPTION_KEY", "c3VwZXJzZWNyZXQ=")
	_, err := config.Load()
	if err == nil || strings.Contains(err.Error(), "c3VwZXJzZWNyZXQ") {
		t.Fatalf("err = %v", err)
	}
}
