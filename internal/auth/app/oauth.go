package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

// OAuthEnabled true bila provider dikonfigurasi.
func (s *Service) OAuthEnabled(provider string) bool {
	_, ok := s.oauth[provider]
	return ok
}

// StartOAuth membuat state, PKCE verifier (S256) dan nonce acak. Adapter wajib
// menyimpan ketiganya (cookie terenkripsi, HttpOnly) sampai callback.
func (s *Service) StartOAuth(_ context.Context, provider string) (OAuthStart, error) {
	p, ok := s.oauth[provider]
	if !ok {
		return OAuthStart{}, domain.ErrOAuthDisabled
	}
	state, verifier, nonce := randomToken(), randomToken()+randomToken(), randomToken()
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return OAuthStart{AuthURL: p.AuthCodeURL(state, challenge, nonce), State: state, Verifier: verifier, Nonce: nonce}, nil
}

// OAuthCallback menukar authorization code, lalu:
//   - identity sudah terhubung -> login (kode sekali pakai untuk frontend);
//   - mode link (user sedang login) -> hubungkan identity ke user tsb;
//   - email belum terdaftar -> buat user aktif (email diverifikasi provider);
//   - email sudah terdaftar -> TIDAK auto-link (risiko account takeover),
//     user harus login dengan password lalu link manual.
func (s *Service) OAuthCallback(ctx context.Context, in OAuthCallbackInput) (OAuthCallbackResult, error) {
	p, ok := s.oauth[in.Provider]
	if !ok {
		return OAuthCallbackResult{}, domain.ErrOAuthDisabled
	}
	prof, err := p.Exchange(ctx, in.Code, in.Verifier, in.Nonce)
	if err != nil {
		return OAuthCallbackResult{}, fmt.Errorf("%w: %w", domain.ErrOAuthFailed, err)
	}
	if prof.Subject == "" || prof.Provider != in.Provider {
		return OAuthCallbackResult{}, domain.ErrOAuthFailed
	}
	if !prof.EmailVerified {
		return OAuthCallbackResult{}, domain.ErrOAuthEmailNotVerif
	}
	email, err := domain.NormalizeEmail(prof.Email)
	if err != nil {
		return OAuthCallbackResult{}, domain.ErrOAuthFailed
	}
	prof.Email = email

	ident, err := s.identities.FindBySubject(ctx, prof.Provider, prof.Subject)
	switch {
	case err == nil:
		if in.LinkUserID != uuid.Nil {
			if ident.UserID != in.LinkUserID {
				return OAuthCallbackResult{}, domain.ErrIdentityTaken
			}
			return OAuthCallbackResult{Linked: true}, nil
		}
		return s.oauthLogin(ctx, ident.UserID, prof.Provider, in.Meta)
	case !errors.Is(err, domain.ErrIdentityNotFound):
		return OAuthCallbackResult{}, err
	}

	if in.LinkUserID != uuid.Nil {
		return s.linkIdentity(ctx, in.LinkUserID, prof, in.Meta)
	}

	if _, err := s.users.FindByEmail(ctx, email); err == nil {
		return OAuthCallbackResult{}, domain.ErrOAuthAccountExists
	} else if !errors.Is(err, domain.ErrUserNotFound) {
		return OAuthCallbackResult{}, err
	}
	return s.oauthRegister(ctx, prof, in.Meta)
}

func (s *Service) oauthLogin(ctx context.Context, userID uuid.UUID, provider string, meta RequestMeta) (OAuthCallbackResult, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return OAuthCallbackResult{}, err
	}
	if err := user.LoginError(); err != nil {
		return OAuthCallbackResult{}, err
	}
	var code string
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		c, err := s.issueLoginCode(ctx, user.ID, provider)
		if err != nil {
			return err
		}
		code = c
		return s.record(ctx, domain.AuditEvent{UserID: user.ID, EventType: domain.EventOAuthLogin,
			Metadata: map[string]string{"provider": provider}}, meta)
	})
	if err != nil {
		return OAuthCallbackResult{}, err
	}
	return OAuthCallbackResult{LoginCode: code}, nil
}

func (s *Service) linkIdentity(ctx context.Context, userID uuid.UUID, prof domain.ExternalProfile, meta RequestMeta) (OAuthCallbackResult, error) {
	if _, err := s.users.FindByID(ctx, userID); err != nil {
		return OAuthCallbackResult{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return OAuthCallbackResult{}, fmt.Errorf("generate identity id: %w", err)
	}
	now := s.clock.Now()
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.identities.Create(ctx, &domain.ExternalIdentity{
			ID: id, UserID: userID, Provider: prof.Provider, Subject: prof.Subject, Email: prof.Email, CreatedAt: now,
		}); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: userID, EventType: domain.EventOAuthLinked,
			Metadata: map[string]string{"provider": prof.Provider}}, meta)
	})
	if err != nil {
		return OAuthCallbackResult{}, err
	}
	return OAuthCallbackResult{Linked: true}, nil
}

// oauthRegister membuat user baru tanpa password (password_hash kosong:
// login password selalu gagal; user bisa set password lewat forgot password).
func (s *Service) oauthRegister(ctx context.Context, prof domain.ExternalProfile, meta RequestMeta) (OAuthCallbackResult, error) {
	name, err := domain.NormalizeFullName(prof.Name)
	if err != nil {
		name = prof.Email[:strings.IndexByte(prof.Email, '@')]
		if name, err = domain.NormalizeFullName(name); err != nil {
			name = "User"
		}
	}
	uid, err := uuid.NewV7()
	if err != nil {
		return OAuthCallbackResult{}, fmt.Errorf("generate user id: %w", err)
	}
	iid, err := uuid.NewV7()
	if err != nil {
		return OAuthCallbackResult{}, fmt.Errorf("generate identity id: %w", err)
	}
	now := s.clock.Now()
	user := &domain.User{ID: uid, Email: prof.Email, FullName: name, Status: domain.UserStatusActive,
		EmailVerifiedAt: &now, CreatedAt: now, UpdatedAt: now}
	var code string
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.users.Create(ctx, user); err != nil {
			if errors.Is(err, domain.ErrEmailTaken) {
				return domain.ErrOAuthAccountExists
			}
			return err
		}
		if err := s.identities.Create(ctx, &domain.ExternalIdentity{
			ID: iid, UserID: uid, Provider: prof.Provider, Subject: prof.Subject, Email: prof.Email, CreatedAt: now,
		}); err != nil {
			return err
		}
		if err := s.record(ctx, domain.AuditEvent{UserID: uid, EventType: domain.EventUserRegistered,
			Metadata: map[string]string{"provider": prof.Provider}}, meta); err != nil {
			return err
		}
		c, err := s.issueLoginCode(ctx, uid, prof.Provider)
		if err != nil {
			return err
		}
		code = c
		return s.record(ctx, domain.AuditEvent{UserID: uid, EventType: domain.EventOAuthLogin,
			Metadata: map[string]string{"provider": prof.Provider}}, meta)
	})
	if err != nil {
		return OAuthCallbackResult{}, err
	}
	return OAuthCallbackResult{LoginCode: code}, nil
}

func (s *Service) issueLoginCode(ctx context.Context, userID uuid.UUID, provider string) (string, error) {
	code := randomToken()
	now := s.clock.Now()
	err := s.loginCodes.Create(ctx, &domain.LoginCode{
		CodeHash: hashRefreshToken(code), UserID: userID, Provider: provider,
		ExpiresAt: now.Add(s.cfg.OAuthLoginCodeTTL), CreatedAt: now,
	})
	return code, err
}

// ExchangeOAuthCode menukar kode sekali pakai dari callback dengan token.
// Bila 2FA aktif, hasilnya mfa_token (2FA tetap wajib untuk login OAuth).
func (s *Service) ExchangeOAuthCode(ctx context.Context, code string, meta RequestMeta) (LoginResult, error) {
	now := s.clock.Now()
	lc, err := s.loginCodes.Consume(ctx, hashRefreshToken(code), now)
	if err != nil {
		return LoginResult{}, err
	}
	user, err := s.users.FindByID(ctx, lc.UserID)
	if errors.Is(err, domain.ErrUserNotFound) {
		return LoginResult{}, domain.ErrLoginCodeInvalid
	}
	if err != nil {
		return LoginResult{}, err
	}
	if err := user.LoginError(); err != nil {
		return LoginResult{}, err
	}
	return s.loginOrChallenge(ctx, user, meta, now, map[string]string{"method": "oauth", "provider": lc.Provider})
}

// ListIdentities mengembalikan akun eksternal yang terhubung ke user.
func (s *Service) ListIdentities(ctx context.Context, userID uuid.UUID) ([]domain.ExternalIdentity, error) {
	return s.identities.ListByUser(ctx, userID)
}

// randomToken: 32 byte acak, base64url (256 bit).
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
