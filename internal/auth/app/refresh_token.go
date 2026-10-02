package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// newRefreshToken membuat token opaque 32 byte yang aman secara kriptografi.
// Yang dikirim ke client adalah token, yang disimpan di DB hanya hash-nya.
func newRefreshToken() (token string, hash []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read tidak pernah mengembalikan error sejak Go 1.24
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashRefreshToken(token)
}

func hashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
