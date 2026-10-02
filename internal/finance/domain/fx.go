package domain

import (
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// RateScale adalah jumlah digit desimal kurs (NUMERIC(20,10)).
const RateScale = 10

var maxRate = new(big.Rat).SetInt64(1_000_000_000) // < 10^10 sesuai NUMERIC(20,10)

// ExchangeRate adalah kurs manual: 1 unit major Base = Rate unit major Quote,
// berlaku mulai AsOf. Kurs disimpan sebagai big.Rat (tanpa float).
type ExchangeRate struct {
	id        uuid.UUID
	userID    uuid.UUID
	base      money.Currency
	quote     money.Currency
	rate      *big.Rat
	asOf      time.Time
	createdAt time.Time
}

// ParseRate memvalidasi string desimal kurs ("15800", "0.0000633").
func ParseRate(s string) (*big.Rat, error) {
	s = strings.TrimSpace(s)
	bad := &ValidationError{Field: "rate", Reason: "must be a positive decimal with max 10 fraction digits"}
	if s == "" || strings.ContainsAny(s, "eE/+-") {
		return nil, bad
	}
	if _, frac, ok := strings.Cut(s, "."); ok && (len(frac) == 0 || len(frac) > RateScale) {
		return nil, bad
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() <= 0 || r.Cmp(maxRate) >= 0 {
		return nil, bad
	}
	return r, nil
}

func NewExchangeRate(id, userID uuid.UUID, base, quote money.Currency, rate string, asOf, now time.Time) (*ExchangeRate, error) {
	if base.IsZero() || quote.IsZero() {
		return nil, money.ErrUnknownCurrency
	}
	if base == quote {
		return nil, &ValidationError{Field: "quote", Reason: "must differ from base"}
	}
	r, err := ParseRate(rate)
	if err != nil {
		return nil, err
	}
	d, err := validatePlanDate("as_of", asOf, now)
	if err != nil {
		return nil, err
	}
	return &ExchangeRate{id: id, userID: userID, base: base, quote: quote, rate: r, asOf: d, createdAt: now.UTC()}, nil
}

// Convert mengubah m (currency Base) ke Quote dengan pembulatan half-up
// (menjauhi nol) di minor unit tujuan.
func (e *ExchangeRate) Convert(m money.Money) (money.Money, error) {
	if m.Currency() != e.base {
		return money.Money{}, money.ErrCurrencyMismatch
	}
	return ConvertAmount(m, e.rate, e.quote)
}

// ConvertAmount: hasil = m * rate * 10^(exp tujuan - exp asal), half-up.
func ConvertAmount(m money.Money, rate *big.Rat, to money.Currency) (money.Money, error) {
	v := new(big.Rat).Mul(new(big.Rat).SetInt64(m.Amount()), rate)
	diff := int(to.Exponent()) - int(m.Currency().Exponent())
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(diff))), nil))
	if diff >= 0 {
		v.Mul(v, scale)
	} else {
		v.Quo(v, scale)
	}
	out := roundHalfUp(v)
	if !out.IsInt64() {
		return money.Money{}, money.ErrAmountOverflow
	}
	return money.New(out.Int64(), to), nil
}

func roundHalfUp(v *big.Rat) *big.Int {
	num, den := new(big.Int).Abs(v.Num()), v.Denom()
	// (2*num + den) / (2*den)
	q := new(big.Int).Mul(num, big.NewInt(2))
	q.Add(q, den)
	q.Quo(q, new(big.Int).Mul(den, big.NewInt(2)))
	if v.Sign() < 0 {
		q.Neg(q)
	}
	return q
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// RateString memformat kurs dengan maksimal 10 desimal tanpa trailing zero.
func RateString(r *big.Rat) string {
	s := r.FloatString(RateScale)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// Invert mengembalikan 1/rate (dipakai bila hanya kurs kebalikan yang tersedia).
func Invert(r *big.Rat) *big.Rat { return new(big.Rat).Inv(r) }

// SetRate mengganti nilai kurs (divalidasi seperti ParseRate).
func (e *ExchangeRate) SetRate(s string) error {
	r, err := ParseRate(s)
	if err != nil {
		return err
	}
	e.rate = r
	return nil
}

type ExchangeRateState struct {
	ID, UserID  uuid.UUID
	Base, Quote money.Currency
	Rate        *big.Rat
	AsOf        time.Time
	CreatedAt   time.Time
}

func RehydrateExchangeRate(s ExchangeRateState) *ExchangeRate {
	return &ExchangeRate{id: s.ID, userID: s.UserID, base: s.Base, quote: s.Quote, rate: s.Rate,
		asOf: DateOf(s.AsOf), createdAt: s.CreatedAt}
}

func (e *ExchangeRate) ID() uuid.UUID         { return e.id }
func (e *ExchangeRate) UserID() uuid.UUID     { return e.userID }
func (e *ExchangeRate) Base() money.Currency  { return e.base }
func (e *ExchangeRate) Quote() money.Currency { return e.quote }
func (e *ExchangeRate) Rate() *big.Rat        { return new(big.Rat).Set(e.rate) }
func (e *ExchangeRate) RateString() string    { return RateString(e.rate) }
func (e *ExchangeRate) AsOf() time.Time       { return e.asOf }
func (e *ExchangeRate) CreatedAt() time.Time  { return e.createdAt }
