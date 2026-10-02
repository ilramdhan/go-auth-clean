package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

// HMACOTPCodec membuat OTP 6 digit (crypto/rand) dan menyimpannya sebagai
// HMAC-SHA256(pepper, code). Pepper mencegah brute force offline ruang 10^6
// jika isi DB bocor.
type HMACOTPCodec struct {
	pepper []byte
}

const otpPepperLabel = "go-auth-clean/otp-pepper/v1"

// NewHMACOTPCodec menurunkan pepper dari master key (mis. ENCRYPTION_KEY) via
// HMAC dengan label, sehingga key yang sama tidak dipakai langsung untuk dua tujuan.
func NewHMACOTPCodec(masterKey []byte) (*HMACOTPCodec, error) {
	if len(masterKey) < 32 {
		return nil, errors.New("otp codec: master key minimal 32 byte")
	}
	m := hmac.New(sha256.New, masterKey)
	m.Write([]byte(otpPepperLabel))
	return &HMACOTPCodec{pepper: m.Sum(nil)}, nil
}

var otpMax = big.NewInt(1_000_000)

func (c *HMACOTPCodec) Generate() (string, error) {
	n, err := rand.Int(rand.Reader, otpMax)
	if err != nil {
		return "", fmt.Errorf("otp generate: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func (c *HMACOTPCodec) Hash(code string) []byte {
	m := hmac.New(sha256.New, c.pepper)
	m.Write([]byte(code))
	return m.Sum(nil)
}

// Verify membandingkan secara constant-time (hmac.Equal).
func (c *HMACOTPCodec) Verify(hash []byte, code string) bool {
	return hmac.Equal(hash, c.Hash(code))
}
