package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/logger"
)

const (
	apiKeyAlphabet    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	apiKeyPrefixChars = "abcdefghijklmnopqrstuvwxyz0123456789"
	apiKeySecretLen   = 32
	// apiKeyTouchEvery membatasi write last_used_at (maks 1x per menit per key).
	apiKeyTouchEvery = time.Minute
)

// CreateAPIKey membuat key baru. Plaintext hanya dikembalikan sekali.
func (s *Service) CreateAPIKey(ctx context.Context, in CreateAPIKeyInput) (CreatedAPIKey, error) {
	name, err := domain.NormalizeAPIKeyName(in.Name)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	scopes, err := domain.ParseScopes(in.Scopes)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	ttl := in.ExpiresIn
	if ttl == 0 {
		ttl = s.cfg.APIKeyDefaultTTL
	}
	if ttl < time.Hour || ttl > domain.APIKeyMaxTTL {
		return CreatedAPIKey{}, domain.ErrInvalidExpiry
	}
	now := s.clock.Now()
	n, err := s.apiKeys.CountActiveByUser(ctx, in.UserID, now)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	if n >= s.cfg.APIKeyMax {
		return CreatedAPIKey{}, domain.ErrAPIKeyLimit
	}
	prefix, err := randomString(apiKeyPrefixChars, domain.APIKeyPrefixLength)
	if err != nil {
		return CreatedAPIKey{}, fmt.Errorf("generate api key prefix: %w", err)
	}
	secret, err := randomString(apiKeyAlphabet, apiKeySecretLen)
	if err != nil {
		return CreatedAPIKey{}, fmt.Errorf("generate api key secret: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return CreatedAPIKey{}, fmt.Errorf("generate api key id: %w", err)
	}
	exp := now.Add(ttl)
	k := domain.APIKey{ID: id, UserID: in.UserID, Name: name, Prefix: prefix, SecretHash: hashSecret(secret),
		Scopes: scopes, ExpiresAt: &exp, CreatedAt: now}
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.apiKeys.Create(ctx, &k); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventAPIKeyCreated,
			Metadata: map[string]string{"api_key_id": id.String(), "prefix": prefix}}, in.Meta)
	})
	if err != nil {
		return CreatedAPIKey{}, err
	}
	key := "gac_" + s.cfg.APIKeyEnv + "_" + prefix + "_" + secret
	return CreatedAPIKey{APIKey: k, Key: key}, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error) {
	return s.apiKeys.ListByUser(ctx, userID)
}

// RevokeAPIKey: key milik user lain = 404 (anti IDOR).
func (s *Service) RevokeAPIKey(ctx context.Context, in RevokeAPIKeyInput) error {
	now := s.clock.Now()
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.apiKeys.Revoke(ctx, in.UserID, in.KeyID, now); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventAPIKeyRevoked,
			Metadata: map[string]string{"api_key_id": in.KeyID.String()}}, in.Meta)
	})
}

// AuthenticateAPIKey memvalidasi key (format, hash constant-time, aktif,
// status user) dan mengembalikan principal beserta scope.
func (s *Service) AuthenticateAPIKey(ctx context.Context, raw string) (Principal, error) {
	prefix, secret, ok := parseAPIKey(raw)
	if !ok {
		return Principal{}, domain.ErrAPIKeyInvalid
	}
	k, err := s.apiKeys.FindByPrefix(ctx, prefix)
	if errors.Is(err, domain.ErrAPIKeyNotFound) {
		return Principal{}, domain.ErrAPIKeyInvalid
	}
	if err != nil {
		return Principal{}, err
	}
	now := s.clock.Now()
	if subtle.ConstantTimeCompare(k.SecretHash, hashSecret(secret)) != 1 || !k.IsActive(now) {
		return Principal{}, domain.ErrAPIKeyInvalid
	}
	user, err := s.users.FindByID(ctx, k.UserID)
	if errors.Is(err, domain.ErrUserNotFound) {
		return Principal{}, domain.ErrAPIKeyInvalid
	}
	if err != nil {
		return Principal{}, err
	}
	if !user.CanLogin() {
		return Principal{}, domain.ErrAPIKeyInvalid
	}
	roles, err := s.roles.ListByUser(ctx, user.ID)
	if err != nil {
		return Principal{}, err
	}
	if err := s.apiKeys.TouchLastUsed(ctx, k.ID, now, now.Add(-apiKeyTouchEvery)); err != nil {
		logger.FromContext(ctx).WarnContext(ctx, "touch api key", slog.Any("error", err))
	}
	return Principal{UserID: user.ID, Roles: roles, APIKeyID: k.ID, Scopes: k.Scopes}, nil
}

// parseAPIKey memecah gac_<env>_<prefix8>_<secret32>.
func parseAPIKey(raw string) (prefix, secret string, ok bool) {
	parts := strings.Split(strings.TrimSpace(raw), "_")
	if len(parts) != 4 || parts[0] != "gac" || (parts[1] != "live" && parts[1] != "test") {
		return "", "", false
	}
	prefix, secret = parts[2], parts[3]
	if len(prefix) != domain.APIKeyPrefixLength || len(secret) != apiKeySecretLen ||
		!onlyChars(prefix, apiKeyPrefixChars) || !onlyChars(secret, apiKeyAlphabet) {
		return "", "", false
	}
	return prefix, secret, true
}

func onlyChars(s, set string) bool {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(set, s[i]) < 0 {
			return false
		}
	}
	return true
}

// hashSecret: SHA-256 cukup karena secret acak 190 bit (bukan password).
func hashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}
