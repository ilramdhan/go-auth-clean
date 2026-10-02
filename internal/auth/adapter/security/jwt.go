package security

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

const (
	audience    = "go-auth-clean-api"
	mfaAudience = "go-auth-clean-mfa"
)

type Claims struct {
	SessionID string   `json:"sid"`
	Roles     []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}

// AccessClaims adalah hasil verifikasi access token.
type AccessClaims struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Roles     []domain.Role
}

type JWTIssuer struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

func NewJWTIssuer(secret []byte, issuer string, ttl time.Duration) *JWTIssuer {
	return &JWTIssuer{secret: secret, issuer: issuer, ttl: ttl}
}

func (j *JWTIssuer) Issue(userID, sessionID uuid.UUID, roles []domain.Role, now time.Time) (string, time.Time, error) {
	exp := now.Add(j.ttl)
	claims := Claims{
		SessionID: sessionID.String(),
		Roles:     domain.RoleStrings(roles),
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   userID.String(),
			Issuer:    j.issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(j.secret)
	return token, exp, err
}

func (j *JWTIssuer) parse(token, aud string) (*Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return j.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(j.issuer),
		jwt.WithAudience(aud),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("verify jwt: %w", err)
	}
	return &claims, nil
}

// Verify memvalidasi signature, algoritma, issuer, audience dan expiry.
// Dipertahankan untuk consumer lain (mis. finance) yang hanya butuh identitas.
func (j *JWTIssuer) Verify(token string) (userID, sessionID uuid.UUID, err error) {
	c, err := j.VerifyAccess(token)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return c.UserID, c.SessionID, nil
}

// VerifyAccess sama dengan Verify tetapi juga mengembalikan roles (role tak dikenal diabaikan).
func (j *JWTIssuer) VerifyAccess(token string) (AccessClaims, error) {
	claims, err := j.parse(token, audience)
	if err != nil {
		return AccessClaims{}, err
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return AccessClaims{}, errors.New("verify jwt: invalid subject")
	}
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return AccessClaims{}, errors.New("verify jwt: invalid session id")
	}
	roles := make([]domain.Role, 0, len(claims.Roles))
	for _, r := range claims.Roles {
		if role, err := domain.ParseRole(r); err == nil {
			roles = append(roles, role)
		}
	}
	return AccessClaims{UserID: userID, SessionID: sessionID, Roles: roles}, nil
}

// MFATokenIssuer menerbitkan token sementara (audience terpisah) antara
// langkah password dan langkah kode 2FA. Tidak bisa dipakai sebagai access token.
type MFATokenIssuer struct {
	j *JWTIssuer
}

func NewMFATokenIssuer(secret []byte, issuer string, ttl time.Duration) *MFATokenIssuer {
	return &MFATokenIssuer{j: NewJWTIssuer(secret, issuer, ttl)}
}

func (m *MFATokenIssuer) IssueMFA(userID uuid.UUID, now time.Time) (string, time.Time, error) {
	exp := now.Add(m.j.ttl)
	claims := jwt.RegisteredClaims{
		ID:        uuid.NewString(),
		Subject:   userID.String(),
		Issuer:    m.j.issuer,
		Audience:  jwt.ClaimStrings{mfaAudience},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.j.secret)
	return token, exp, err
}

func (m *MFATokenIssuer) VerifyMFA(token string) (uuid.UUID, error) {
	claims, err := m.j.parse(token, mfaAudience)
	if err != nil {
		return uuid.Nil, err
	}
	if claims.SessionID != "" {
		return uuid.Nil, errors.New("verify mfa: unexpected session claim")
	}
	uid, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, errors.New("verify mfa: invalid subject")
	}
	return uid, nil
}

// ChallengeID memverifikasi mfa_token (signature, issuer, audience, expiry)
// lalu mengembalikan jti-nya. Dipakai adapter HTTP sebagai key limiter
// percobaan per challenge; token tidak valid -> error (tidak dihitung).
func (m *MFATokenIssuer) ChallengeID(token string) (string, error) {
	claims, err := m.j.parse(token, mfaAudience)
	if err != nil {
		return "", err
	}
	if claims.ID == "" {
		return "", errors.New("verify mfa: missing jti")
	}
	return claims.ID, nil
}

// VerifyRoles adalah bentuk tuple dari VerifyAccess (dipakai middleware HTTP via interface).
func (j *JWTIssuer) VerifyRoles(token string) (uuid.UUID, uuid.UUID, []domain.Role, error) {
	c, err := j.VerifyAccess(token)
	return c.UserID, c.SessionID, c.Roles, err
}
