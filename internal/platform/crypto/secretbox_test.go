package crypto_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"go-auth-clean/internal/platform/crypto"
)

func newBox(t *testing.T) *crypto.SecretBox {
	t.Helper()
	b, err := crypto.NewSecretBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNewSecretBox_KeyValidation(t *testing.T) {
	tests := []struct {
		name string
		b64  string
		ok   bool
	}{
		{"valid 32 byte", base64.StdEncoding.EncodeToString(make([]byte, 32)), true},
		{"16 byte", base64.StdEncoding.EncodeToString(make([]byte, 16)), false},
		{"not base64", "%%%", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := crypto.NewSecretBoxBase64(tt.b64)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	b := newBox(t)
	aad := []byte("user-1")
	for _, pt := range []string{"", "JBSWY3DPEHPK3PXP", "unicode ✓"} {
		ct, err := b.EncryptString(pt, aad)
		if err != nil {
			t.Fatal(err)
		}
		got, err := b.DecryptString(ct, aad)
		if err != nil || got != pt {
			t.Fatalf("got %q, %v", got, err)
		}
	}
}

func TestEncrypt_NonceIsRandom(t *testing.T) {
	b := newBox(t)
	c1, _ := b.Encrypt([]byte("x"), nil)
	c2, _ := b.Encrypt([]byte("x"), nil)
	if bytes.Equal(c1, c2) {
		t.Fatal("ciphertext identik: nonce tidak acak")
	}
}

func TestDecrypt_Rejects(t *testing.T) {
	b := newBox(t)
	ct, _ := b.Encrypt([]byte("secret"), []byte("user-1"))

	tampered := bytes.Clone(ct)
	tampered[len(tampered)-1] ^= 0xff
	badVersion := bytes.Clone(ct)
	badVersion[0] = 9
	other, _ := crypto.NewSecretBox(bytes.Repeat([]byte{8}, 32))

	tests := []struct {
		name string
		box  *crypto.SecretBox
		ct   []byte
		aad  []byte
	}{
		{"wrong aad", b, ct, []byte("user-2")},
		{"tampered", b, tampered, []byte("user-1")},
		{"bad version", b, badVersion, []byte("user-1")},
		{"too short", b, ct[:5], []byte("user-1")},
		{"wrong key", other, ct, []byte("user-1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.box.Decrypt(tt.ct, tt.aad); !errors.Is(err, crypto.ErrInvalidCiphertext) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if _, err := b.DecryptString("%%%", nil); !errors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("bad base64 err = %v", err)
	}
}
