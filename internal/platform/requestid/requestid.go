// Package requestid menyimpan request ID di context. Dipisah dari middleware
// supaya httpx dan middleware bisa sama-sama memakainya tanpa import cycle.
package requestid

import "context"

type ctxKey struct{}

// WithContext menyimpan request ID ke context.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext mengambil request ID; string kosong jika tidak ada.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
