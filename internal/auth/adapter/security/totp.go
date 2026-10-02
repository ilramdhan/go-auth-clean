package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"net/url"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
)

// TOTP mengimplementasikan RFC 6238 (SHA1, 6 digit, periode 30 detik, toleransi ±1 step)
// agar kompatibel dengan Google Authenticator/Authy. Validate mengembalikan step
// yang cocok sehingga use case dapat mencegah replay.
type TOTP struct {
	issuer string
	period int64
	skew   int64
}

func NewTOTP(issuer string) *TOTP {
	return &TOTP{issuer: issuer, period: 30, skew: 1}
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func (t *TOTP) GenerateSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("totp secret: %w", err)
	}
	return b32.EncodeToString(b), nil
}

func (t *TOTP) URI(secret, account string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", t.issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	u := url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + t.issuer + ":" + account, RawQuery: v.Encode()}
	return u.String()
}

func (t *TOTP) Validate(secret, code string, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	cur := now.Unix() / t.period
	opts := hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	var matched int64 = -1
	// Periksa semua step dalam jendela (tanpa early return) agar waktu konstan.
	for s := cur - t.skew; s <= cur+t.skew; s++ {
		if s < 0 {
			continue
		}
		want, err := hotp.GenerateCodeCustom(secret, uint64(s), opts)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 && matched < 0 {
			matched = s
		}
	}
	if matched < 0 {
		return 0, false
	}
	return matched, true
}
