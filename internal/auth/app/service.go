package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/logger"
	"go-auth-clean/internal/platform/requestid"
)

// Deps adalah semua port yang dibutuhkan Service. Struct dipakai (bukan
// parameter panjang) supaya penambahan dependency tidak memecah pemanggil.
type Deps struct {
	Users    domain.UserRepository
	Sessions domain.SessionRepository
	OTPs     domain.OTPRepository
	Audit    domain.AuditRepository
	Hasher   PasswordHasher
	Tokens   AccessTokenIssuer
	OTP      OTPCodec
	Notifier Notifier
	Clock    Clock
	Tx       TxManager
	// DeletionHooks opsional: dipanggil dalam transaksi hapus akun.
	DeletionHooks []UserDeletionHook

	// P2: RBAC, 2FA, OAuth, API key.
	Roles      domain.RoleRepository
	MFA        domain.MFARepository
	Identities domain.IdentityRepository
	LoginCodes domain.LoginCodeRepository
	APIKeys    domain.APIKeyRepository
	MFATokens  MFATokenIssuer
	TOTP       TOTP
	Sealer     SecretSealer
	// OAuthProviders: key = nama provider (mis. "google"). Kosong = OAuth nonaktif.
	OAuthProviders map[string]OAuthProvider
}

type Service struct {
	users    domain.UserRepository
	sessions domain.SessionRepository
	otps     domain.OTPRepository
	auditLog domain.AuditRepository
	hasher   PasswordHasher
	tokens   AccessTokenIssuer
	otp      OTPCodec
	notifier Notifier
	clock    Clock
	tx       TxManager
	hooks    []UserDeletionHook
	cfg      Config

	roles      domain.RoleRepository
	mfa        domain.MFARepository
	identities domain.IdentityRepository
	loginCodes domain.LoginCodeRepository
	apiKeys    domain.APIKeyRepository
	mfaTokens  MFATokenIssuer
	totp       TOTP
	sealer     SecretSealer
	oauth      map[string]OAuthProvider
}

func NewService(d Deps, cfg Config) *Service {
	return &Service{
		users:    d.Users,
		sessions: d.Sessions,
		otps:     d.OTPs,
		auditLog: d.Audit,
		hasher:   d.Hasher,
		tokens:   d.Tokens,
		otp:      d.OTP,
		notifier: d.Notifier,
		clock:    d.Clock,
		tx:       d.Tx,
		hooks:    d.DeletionHooks,
		cfg:      cfg.withDefaults(),

		roles:      d.Roles,
		mfa:        d.MFA,
		identities: d.Identities,
		loginCodes: d.LoginCodes,
		apiKeys:    d.APIKeys,
		mfaTokens:  d.MFATokens,
		totp:       d.TOTP,
		sealer:     d.Sealer,
		oauth:      d.OAuthProviders,
	}
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (*domain.User, error) {
	email, err := domain.NormalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	name, err := domain.NormalizeFullName(in.FullName)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidatePasswordFor(in.Password, email); err != nil {
		return nil, err
	}

	// Hash di luar transaksi: bcrypt lambat, jangan menahan koneksi DB.
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate user id: %w", err)
	}

	now := s.clock.Now()
	user := &domain.User{
		ID:           id,
		Email:        email,
		PasswordHash: hash,
		FullName:     name,
		Status:       domain.UserStatusPendingVerification,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	var code string
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		// Tidak perlu cek FindByEmail dulu: unique index di DB adalah sumber kebenaran,
		// repository mengubah unique violation menjadi domain.ErrEmailTaken.
		if err := s.users.Create(ctx, user); err != nil {
			return err
		}
		c, err := s.issueOTP(ctx, user, domain.OTPEmailVerification, now)
		if err != nil {
			return err
		}
		code = c
		return s.record(ctx, domain.AuditEvent{UserID: user.ID, EventType: domain.EventUserRegistered}, in.Meta)
	})
	if err != nil {
		return nil, err
	}

	// Email dikirim SETELAH commit: kalau tx rollback, email tidak boleh terkirim.
	s.notify(ctx, "email verification", func() error {
		return s.notifier.EmailVerification(ctx, user.Email, user.FullName, code, s.cfg.OTPTTL)
	})
	return user, nil
}

func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	now := s.clock.Now()
	meta := RequestMeta{ClientIP: in.ClientIP, UserAgent: in.UserAgent}

	email, err := domain.NormalizeEmail(in.Email)
	if err != nil {
		_ = s.hasher.Compare("", in.Password)
		return LoginResult{}, domain.ErrInvalidCredentials
	}

	user, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		// Tetap jalankan compare supaya waktu respons sama dengan email yang
		// terdaftar (mencegah user enumeration lewat timing attack).
		_ = s.hasher.Compare("", in.Password)
		s.recordBestEffort(ctx, domain.AuditEvent{
			EventType: domain.EventLoginFailed, Outcome: domain.OutcomeFailure,
			EmailHash: hashEmail(email), Metadata: map[string]string{"reason": "unknown_email"},
		}, meta)
		return LoginResult{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}

	if user.IsLocked(now) {
		_ = s.hasher.Compare("", in.Password)
		return LoginResult{}, domain.ErrAccountLocked
	}

	if err := s.hasher.Compare(user.PasswordHash, in.Password); err != nil {
		s.registerFailure(ctx, user.ID, now, meta, "bad_password")
		return LoginResult{}, domain.ErrInvalidCredentials
	}

	// Status dicek SETELAH password benar, supaya status akun tidak bisa
	// dipetakan penyerang tanpa password.
	if err := user.LoginError(); err != nil {
		return LoginResult{}, err
	}

	return s.loginOrChallenge(ctx, user, meta, now, nil)
}

// loginOrChallenge: bila 2FA aktif, kembalikan mfa_token (belum ada sesi);
// selain itu langsung buat sesi.
func (s *Service) loginOrChallenge(ctx context.Context, user *domain.User, meta RequestMeta, now time.Time, auditMeta map[string]string) (LoginResult, error) {
	m, err := s.mfa.Get(ctx, user.ID)
	if err != nil && !errors.Is(err, domain.ErrMFANotFound) {
		return LoginResult{}, err
	}
	if m.IsEnabled() {
		tok, exp, err := s.mfaTokens.IssueMFA(user.ID, now)
		if err != nil {
			return LoginResult{}, fmt.Errorf("issue mfa token: %w", err)
		}
		return LoginResult{User: user, MFARequired: true, MFAToken: tok, MFATokenExpiresAt: exp}, nil
	}
	return s.completeLogin(ctx, user, meta, now, auditMeta)
}

// completeLogin membuat device session baru setelah semua faktor terverifikasi.
func (s *Service) completeLogin(ctx context.Context, user *domain.User, meta RequestMeta, now time.Time, auditMeta map[string]string) (LoginResult, error) {
	familyID, err := uuid.NewV7()
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate family id: %w", err)
	}

	var tokens TokenPair
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.users.RecordLogin(ctx, user.ID, now); err != nil {
			return err
		}
		t, err := s.startSession(ctx, user.ID, familyID, meta.ClientIP, meta.UserAgent, now)
		if err != nil {
			return err
		}
		tokens = t
		// Batasi jumlah device aktif: yang tertua dicabut.
		if _, err := s.sessions.RevokeExcess(ctx, user.ID, s.cfg.MaxSessions, now); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{
			UserID: user.ID, SessionID: tokens.SessionID, EventType: domain.EventLoginSucceeded, Metadata: auditMeta,
		}, meta)
	})
	if err != nil {
		return LoginResult{}, err
	}
	user.FailedLoginAttempts, user.LockedUntil, user.LastLoginAt = 0, nil, &now
	user.Roles = tokens.roles
	return LoginResult{Tokens: tokens, User: user}, nil
}

// registerFailure menaikkan counter lockout dan mencatat audit. Error hanya
// di-log (tidak mengubah respons ke client).
func (s *Service) registerFailure(ctx context.Context, userID uuid.UUID, now time.Time, meta RequestMeta, reason string) {
	locked, err := s.users.RegisterFailedLogin(ctx, userID, now)
	if err != nil {
		logger.FromContext(ctx).WarnContext(ctx, "register failed login", slog.Any("error", err))
	}
	s.recordBestEffort(ctx, domain.AuditEvent{
		UserID: userID, EventType: domain.EventLoginFailed, Outcome: domain.OutcomeFailure,
		Metadata: map[string]string{"reason": reason},
	}, meta)
	if locked {
		s.recordBestEffort(ctx, domain.AuditEvent{
			UserID: userID, EventType: domain.EventAccountLocked, Outcome: domain.OutcomeFailure,
		}, meta)
	}
}

// Refresh menukar refresh token lama dengan pasangan token baru (rotation).
// Jika token yang sudah pernah ditukar dipakai lagi, kemungkinan token dicuri,
// maka seluruh family session di-revoke.
func (s *Service) Refresh(ctx context.Context, in RefreshInput) (TokenPair, error) {
	now := s.clock.Now()
	meta := RequestMeta{ClientIP: in.ClientIP, UserAgent: in.UserAgent}

	sess, err := s.sessions.FindByTokenHash(ctx, hashRefreshToken(in.RefreshToken))
	if errors.Is(err, domain.ErrSessionNotFound) {
		return TokenPair{}, domain.ErrSessionInvalid
	}
	if err != nil {
		return TokenPair{}, err
	}

	if sess.RotatedAt != nil {
		// Revoke di-commit dulu, baru kembalikan error reuse ke client.
		err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
			if err := s.sessions.RevokeFamily(ctx, sess.FamilyID, now, domain.RevokeReuseDetected); err != nil {
				return err
			}
			return s.record(ctx, domain.AuditEvent{
				UserID: sess.UserID, SessionID: sess.ID, EventType: domain.EventRefreshReuseDetected,
				Outcome: domain.OutcomeFailure,
			}, meta)
		})
		if err != nil {
			return TokenPair{}, err
		}
		logger.FromContext(ctx).WarnContext(ctx, "refresh token reuse detected",
			slog.String("user_id", sess.UserID.String()),
			slog.String("family_id", sess.FamilyID.String()))
		return TokenPair{}, domain.ErrTokenReused
	}
	if !sess.IsActive(now) {
		return TokenPair{}, domain.ErrSessionInvalid
	}

	user, err := s.users.FindByID(ctx, sess.UserID)
	if errors.Is(err, domain.ErrUserNotFound) {
		return TokenPair{}, domain.ErrSessionInvalid
	}
	if err != nil {
		return TokenPair{}, err
	}
	if !user.CanLogin() {
		return TokenPair{}, domain.ErrAccountInactive
	}

	var tokens TokenPair
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		// MarkRotated bersifat atomic (WHERE rotated_at IS NULL), sehingga dua request
		// refresh paralel dengan token yang sama hanya satu yang berhasil.
		if err := s.sessions.MarkRotated(ctx, sess.ID, now); err != nil {
			return err
		}
		t, err := s.startSession(ctx, user.ID, sess.FamilyID, in.ClientIP, in.UserAgent, now)
		tokens = t
		return err
	})
	return tokens, err
}

// Logout mencabut device session (family) yang sedang dipakai. Idempoten.
func (s *Service) Logout(ctx context.Context, in LogoutInput) error {
	sess, err := s.sessions.FindByID(ctx, in.SessionID)
	if err != nil {
		return err
	}
	if sess.UserID != in.UserID {
		return domain.ErrSessionNotFound
	}
	now := s.clock.Now()
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.sessions.RevokeFamily(ctx, sess.FamilyID, now, domain.RevokeLogout); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventLogout}, in.Meta)
	})
}

// LogoutAll mencabut semua device; IncludeCurrent=false menyisakan device ini.
func (s *Service) LogoutAll(ctx context.Context, in LogoutAllInput) error {
	except := uuid.Nil
	if !in.IncludeCurrent {
		fam, err := s.currentFamily(ctx, in.UserID, in.SessionID)
		if err != nil {
			return err
		}
		except = fam
	}
	now := s.clock.Now()
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.sessions.RevokeAllByUser(ctx, in.UserID, except, now, domain.RevokeLogoutAll); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEvent{
			UserID: in.UserID, SessionID: in.SessionID, EventType: domain.EventLogoutAll,
			Metadata: map[string]string{"include_current": fmt.Sprint(in.IncludeCurrent)},
		}, in.Meta)
	})
}

// Me mengembalikan profil beserta role dan status 2FA.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	u, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.Roles, err = s.roles.ListByUser(ctx, userID); err != nil {
		return nil, err
	}
	m, err := s.mfa.Get(ctx, userID)
	if err != nil && !errors.Is(err, domain.ErrMFANotFound) {
		return nil, err
	}
	u.MFAEnabled = m.IsEnabled()
	return u, nil
}

func (s *Service) startSession(ctx context.Context, userID, familyID uuid.UUID, ip, ua string, now time.Time) (TokenPair, error) {
	sessionID, err := uuid.NewV7()
	if err != nil {
		return TokenPair{}, fmt.Errorf("generate session id: %w", err)
	}

	// Role dibaca ulang setiap sesi dibuat/dirotasi, sehingga perubahan role
	// berlaku paling lambat satu umur access token.
	roles, err := s.roles.ListByUser(ctx, userID)
	if err != nil {
		return TokenPair{}, err
	}

	refreshToken, refreshHash := newRefreshToken()
	sess := &domain.Session{
		ID:               sessionID,
		UserID:           userID,
		FamilyID:         familyID,
		RefreshTokenHash: refreshHash,
		ClientIP:         ip,
		UserAgent:        truncate(ua, 512),
		ExpiresAt:        now.Add(s.cfg.RefreshTTL),
		CreatedAt:        now,
	}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return TokenPair{}, err
	}

	accessToken, accessExp, err := s.tokens.Issue(userID, sessionID, roles, now)
	if err != nil {
		return TokenPair{}, fmt.Errorf("issue access token: %w", err)
	}

	return TokenPair{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessExp,
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: sess.ExpiresAt,
		SessionID:             sessionID,
		FamilyID:              familyID,
		roles:                 roles,
	}, nil
}

// AuthenticateSession memastikan session milik access token belum di-revoke.
// Dipanggil oleh middleware supaya logout langsung berlaku tanpa menunggu JWT expired.
func (s *Service) AuthenticateSession(ctx context.Context, sessionID uuid.UUID) error {
	sess, err := s.sessions.FindByID(ctx, sessionID)
	if errors.Is(err, domain.ErrSessionNotFound) {
		return domain.ErrSessionInvalid
	}
	if err != nil {
		return err
	}
	if sess.RevokedAt != nil {
		return domain.ErrSessionInvalid
	}
	return nil
}

// currentFamily mengambil family dari session yang sedang dipakai (milik user).
func (s *Service) currentFamily(ctx context.Context, userID, sessionID uuid.UUID) (uuid.UUID, error) {
	if sessionID == uuid.Nil {
		return uuid.Nil, nil
	}
	sess, err := s.sessions.FindByID(ctx, sessionID)
	if errors.Is(err, domain.ErrSessionNotFound) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	if sess.UserID != userID {
		return uuid.Nil, nil
	}
	return sess.FamilyID, nil
}

// record menulis audit log memakai ctx (ikut transaksi bila ada).
func (s *Service) record(ctx context.Context, e domain.AuditEvent, meta RequestMeta) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate audit id: %w", err)
	}
	e.ID = id
	if e.Outcome == "" {
		e.Outcome = domain.OutcomeSuccess
	}
	e.ClientIP = meta.ClientIP
	e.UserAgent = truncate(meta.UserAgent, 512)
	e.RequestID = requestid.FromContext(ctx)
	e.OccurredAt = s.clock.Now()
	return s.auditLog.Record(ctx, &e)
}

// recordBestEffort dipakai untuk event tanpa transaksi (mis. login gagal):
// kegagalan audit tidak boleh menggagalkan respons, tapi tetap di-log.
func (s *Service) recordBestEffort(ctx context.Context, e domain.AuditEvent, meta RequestMeta) {
	if err := s.record(ctx, e, meta); err != nil {
		logger.FromContext(ctx).WarnContext(ctx, "record audit event",
			slog.String("event", string(e.EventType)), slog.Any("error", err))
	}
}

// notify menjalankan pengiriman email; error hanya di-log (registrasi/reset
// tetap sukses, user bisa minta kirim ulang).
func (s *Service) notify(ctx context.Context, kind string, fn func() error) {
	if err := fn(); err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "send notification",
			slog.String("kind", kind), slog.Any("error", err))
	}
}

// hashEmail dipakai untuk audit tanpa menyimpan email mentah.
func hashEmail(email string) []byte {
	sum := sha256.Sum256([]byte(email))
	return sum[:]
}

func truncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Potong di batas rune agar tetap UTF-8 valid.
	for maxBytes > 0 && s[maxBytes]&0xC0 == 0x80 {
		maxBytes--
	}
	return s[:maxBytes]
}
