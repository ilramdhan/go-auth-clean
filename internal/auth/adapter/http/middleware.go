package http

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type TokenVerifier interface {
	Verify(token string) (userID, sessionID uuid.UUID, err error)
}

type SessionChecker interface {
	AuthenticateSession(ctx context.Context, sessionID uuid.UUID) error
}

// RequireAuth memverifikasi Bearer token dan memastikan session belum di-revoke,
// lalu menaruh identitas user ke context. Dipertahankan untuk modul lain;
// modul auth memakai Authenticator (roles + API key).
func RequireAuth(verifier TokenVerifier, sessions SessionChecker) func(http.Handler) http.Handler {
	return NewAuthenticator(tokenOnlyVerifier{verifier}, sessions, nil, nil).User
}
