// Package crypto berisi helper enkripsi untuk secret at rest (mis. TOTP secret).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize adalah panjang key AES-256.
const KeySize = 32

// version byte di depan ciphertext: memudahkan rotasi algoritma/key di masa depan.
const version byte = 1

var (
	ErrInvalidKey        = errors.New("crypto: key harus 32 byte")
	ErrInvalidCiphertext = errors.New("crypto: ciphertext tidak valid")
)

// SecretBox mengenkripsi data dengan AES-256-GCM. Format output:
// version(1) || nonce(12) || ciphertext+tag. Aman dipakai concurrent.
type SecretBox struct {
	aead cipher.AEAD
}

// NewSecretBox membuat SecretBox dari key 32 byte.
func NewSecretBox(key []byte) (*SecretBox, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto.NewSecretBox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto.NewSecretBox: %w", err)
	}
	return &SecretBox{aead: aead}, nil
}

// NewSecretBoxBase64 membuat SecretBox dari key base64 (std, mis. hasil `openssl rand -base64 32`).
func NewSecretBoxBase64(b64 string) (*SecretBox, error) {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, ErrInvalidKey
	}
	return NewSecretBox(key)
}

// Encrypt mengenkripsi plaintext. aad (additional data, boleh nil) mengikat
// ciphertext ke konteks, mis. user_id, sehingga tidak bisa dipindah ke user lain.
func (b *SecretBox) Encrypt(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto.Encrypt nonce: %w", err)
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+b.aead.Overhead())
	out = append(out, version)
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plaintext, aad), nil
}

// Decrypt membuka ciphertext hasil Encrypt dengan aad yang sama.
func (b *SecretBox) Decrypt(ciphertext, aad []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < 1+ns+b.aead.Overhead() || ciphertext[0] != version {
		return nil, ErrInvalidCiphertext
	}
	nonce := ciphertext[1 : 1+ns]
	pt, err := b.aead.Open(nil, nonce, ciphertext[1+ns:], aad)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	return pt, nil
}

// EncryptString mengenkripsi string dan mengembalikan base64 (untuk kolom TEXT).
func (b *SecretBox) EncryptString(plaintext string, aad []byte) (string, error) {
	ct, err := b.Encrypt([]byte(plaintext), aad)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// DecryptString kebalikan EncryptString.
func (b *SecretBox) DecryptString(encoded string, aad []byte) (string, error) {
	ct, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	pt, err := b.Decrypt(ct, aad)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
