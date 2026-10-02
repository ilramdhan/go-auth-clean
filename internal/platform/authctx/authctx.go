// Package authctx menyimpan identitas user yang sudah terautentikasi di context.
// Dipakai bersama oleh semua bounded context (auth, finance) tanpa saling import.
package authctx

import (
	"context"

	"github.com/google/uuid"
)

type Identity struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

type ctxKey struct{}

func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}
