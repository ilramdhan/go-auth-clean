// Package money adalah shared kernel untuk uang: int64 minor unit + Currency.
// Tidak pernah memakai float. JSON amount dikirim sebagai string major unit
// ("35000" untuk IDR, "12.34" untuk USD) agar presisi aman di JavaScript.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var (
	ErrCurrencyMismatch = errors.New("currency mismatch")
	ErrUnknownCurrency  = errors.New("unknown currency")
	ErrAmountOverflow   = errors.New("amount overflow")
	ErrInvalidAmount    = errors.New("invalid amount format")
	ErrTooManyDecimals  = errors.New("too many decimal places for currency")
)

// Currency adalah value object: kode ISO 4217 + exponent minor unit.
type Currency struct {
	code     string
	exponent uint8
}

// Registry harus SAMA dengan seed tabel `currencies` (migration finance).
// Keputusan PRD-02 §5.1: IDR memakai exponent 0 (Rp35.000 -> 35000).
var currencies = map[string]Currency{
	"IDR": {"IDR", 0},
	"USD": {"USD", 2},
	"SGD": {"SGD", 2},
	"JPY": {"JPY", 0},
	"EUR": {"EUR", 2},
}

// Predefined currency untuk kemudahan.
var (
	IDR = currencies["IDR"]
	USD = currencies["USD"]
	SGD = currencies["SGD"]
	JPY = currencies["JPY"]
	EUR = currencies["EUR"]
)

// ParseCurrency mencari currency berdasarkan kode (case-insensitive).
func ParseCurrency(code string) (Currency, error) {
	c, ok := currencies[strings.ToUpper(strings.TrimSpace(code))]
	if !ok {
		return Currency{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, code)
	}
	return c, nil
}

// MustCurrency seperti ParseCurrency tapi panic; hanya untuk konstanta/test.
func MustCurrency(code string) Currency {
	c, err := ParseCurrency(code)
	if err != nil {
		panic(err)
	}
	return c
}

// Codes mengembalikan semua kode currency yang didukung.
func Codes() []string {
	out := make([]string, 0, len(currencies))
	for k := range currencies {
		out = append(out, k)
	}
	return out
}

func (c Currency) Code() string    { return c.code }
func (c Currency) Exponent() uint8 { return c.exponent }
func (c Currency) IsZero() bool    { return c.code == "" }
func (c Currency) String() string  { return c.code }

// MarshalJSON menulis kode currency sebagai string.
func (c Currency) MarshalJSON() ([]byte, error) { return json.Marshal(c.code) }

// UnmarshalJSON membaca kode currency dan memvalidasinya.
func (c *Currency) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("%w: currency harus string", ErrUnknownCurrency)
	}
	parsed, err := ParseCurrency(s)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

// Money adalah immutable value object. Zero value tidak valid (currency kosong).
type Money struct {
	amount   int64 // minor unit
	currency Currency
}

// New membuat Money dari minor unit.
func New(minor int64, c Currency) Money { return Money{amount: minor, currency: c} }

// Zero membuat Money bernilai 0 dalam currency c.
func Zero(c Currency) Money { return Money{currency: c} }

func (m Money) Amount() int64      { return m.amount }
func (m Money) Currency() Currency { return m.currency }
func (m Money) IsZero() bool       { return m.amount == 0 }
func (m Money) IsNegative() bool   { return m.amount < 0 }
func (m Money) IsPositive() bool   { return m.amount > 0 }
func (m Money) Equal(o Money) bool { return m == o }

// Add menjumlahkan dua Money dengan currency sama, aman dari overflow.
func (m Money) Add(o Money) (Money, error) {
	if m.currency != o.currency {
		return Money{}, fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency.code, o.currency.code)
	}
	if (o.amount > 0 && m.amount > math.MaxInt64-o.amount) ||
		(o.amount < 0 && m.amount < math.MinInt64-o.amount) {
		return Money{}, ErrAmountOverflow
	}
	return Money{amount: m.amount + o.amount, currency: m.currency}, nil
}

// Sub mengurangkan o dari m.
func (m Money) Sub(o Money) (Money, error) {
	if m.currency != o.currency {
		return Money{}, fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency.code, o.currency.code)
	}
	if o.amount == math.MinInt64 {
		return Money{}, ErrAmountOverflow
	}
	return m.Add(Money{amount: -o.amount, currency: o.currency})
}

// Neg mengembalikan -m. Error bila m = MinInt64 (tidak punya negasi di int64).
func (m Money) Neg() (Money, error) {
	if m.amount == math.MinInt64 {
		return Money{}, ErrAmountOverflow
	}
	return Money{amount: -m.amount, currency: m.currency}, nil
}

// Abs mengembalikan |m|.
func (m Money) Abs() (Money, error) {
	if m.amount < 0 {
		return m.Neg()
	}
	return m, nil
}

// Cmp membandingkan: -1 bila m<o, 0 bila sama, 1 bila m>o. Error bila beda currency.
func (m Money) Cmp(o Money) (int, error) {
	if m.currency != o.currency {
		return 0, fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency.code, o.currency.code)
	}
	switch {
	case m.amount < o.amount:
		return -1, nil
	case m.amount > o.amount:
		return 1, nil
	default:
		return 0, nil
	}
}

// Parse mengubah string major unit ("12.34", "-35000") menjadi minor unit
// tanpa melewati float. Menolak notasi ilmiah, pemisah ribuan, dan tanda '+'.
func Parse(s string, c Currency) (Money, error) {
	if c.IsZero() {
		return Money{}, ErrUnknownCurrency
	}
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if intPart == "" || (hasDot && fracPart == "") {
		return Money{}, ErrInvalidAmount
	}
	if !allDigits(intPart) || !allDigits(fracPart) {
		return Money{}, ErrInvalidAmount
	}
	if len(fracPart) > int(c.exponent) {
		return Money{}, fmt.Errorf("%w: %s allows %d", ErrTooManyDecimals, c.code, c.exponent)
	}
	digits := strings.TrimLeft(intPart+fracPart+strings.Repeat("0", int(c.exponent)-len(fracPart)), "0")
	if digits == "" {
		return Money{currency: c}, nil
	}
	if len(digits) > 19 {
		return Money{}, ErrAmountOverflow
	}
	// Parse sebagai uint64 agar MinInt64 (magnitudo 2^63) bisa diterima saat negatif.
	u, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return Money{}, ErrAmountOverflow
	}
	if neg {
		if u > uint64(math.MaxInt64)+1 {
			return Money{}, ErrAmountOverflow
		}
		return Money{amount: int64(-u), currency: c}, nil //nolint:gosec // range sudah dicek di atas
	}
	if u > math.MaxInt64 {
		return Money{}, ErrAmountOverflow
	}
	return Money{amount: int64(u), currency: c}, nil
}

// ParseCode seperti Parse, tapi currency diberikan sebagai kode.
func ParseCode(amount, code string) (Money, error) {
	c, err := ParseCurrency(code)
	if err != nil {
		return Money{}, err
	}
	return Parse(amount, c)
}

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String mengembalikan representasi major unit: "12.34" / "35000" / "-0.05".
func (m Money) String() string {
	u := uint64(m.amount) //nolint:gosec // magnitudo dihitung di bawah
	sign := ""
	if m.amount < 0 {
		sign = "-"
		u = uint64(-(m.amount + 1)) + 1 //nolint:gosec // aman untuk MinInt64
	}
	if m.currency.exponent == 0 {
		return sign + strconv.FormatUint(u, 10)
	}
	pow := uint64(1)
	for range m.currency.exponent {
		pow *= 10
	}
	return fmt.Sprintf("%s%d.%0*d", sign, u/pow, int(m.currency.exponent), u%pow)
}

// MarshalJSON menulis amount sebagai string major unit (bukan number).
// Untuk JSON {amount, currency}, pakai DTO terpisah di adapter http.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// MarshalText dipakai encoding (mis. CSV/log) - sama dengan String.
func (m Money) MarshalText() ([]byte, error) { return []byte(m.String()), nil }
