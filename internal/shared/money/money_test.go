package money_test

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"testing"

	"go-auth-clean/internal/shared/money"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		cur     money.Currency
		want    int64
		wantErr error
	}{
		{"35000", money.IDR, 35000, nil},
		{"  35000 ", money.IDR, 35000, nil},
		{"0", money.IDR, 0, nil},
		{"-0", money.IDR, 0, nil},
		{"12.34", money.USD, 1234, nil},
		{"12.3", money.USD, 1230, nil},
		{"12", money.USD, 1200, nil},
		{"-0.05", money.USD, -5, nil},
		{"007", money.IDR, 7, nil},
		{"9223372036854775807", money.IDR, math.MaxInt64, nil},
		{"-9223372036854775808", money.IDR, math.MinInt64, nil},
		{"9223372036854775808", money.IDR, 0, money.ErrAmountOverflow},
		{"-9223372036854775809", money.IDR, 0, money.ErrAmountOverflow},
		{"99999999999999999999", money.IDR, 0, money.ErrAmountOverflow},
		{"92233720368547758.08", money.USD, 0, money.ErrAmountOverflow},
		{"12.345", money.USD, 0, money.ErrTooManyDecimals},
		{"1.5", money.IDR, 0, money.ErrTooManyDecimals},
		{"", money.IDR, 0, money.ErrInvalidAmount},
		{"-", money.IDR, 0, money.ErrInvalidAmount},
		{"+5", money.IDR, 0, money.ErrInvalidAmount},
		{"1e3", money.IDR, 0, money.ErrInvalidAmount},
		{"1,000", money.IDR, 0, money.ErrInvalidAmount},
		{"1.", money.USD, 0, money.ErrInvalidAmount},
		{".5", money.USD, 0, money.ErrInvalidAmount},
		{"--5", money.IDR, 0, money.ErrInvalidAmount},
		{"5", money.Currency{}, 0, money.ErrUnknownCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.in+"/"+tt.cur.Code(), func(t *testing.T) {
			got, err := money.Parse(tt.in, tt.cur)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil || got.Amount() != tt.want || got.Currency() != tt.cur {
				t.Fatalf("got %v (%d), err %v; want %d", got, got.Amount(), err, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		m    money.Money
		want string
	}{
		{money.New(35000, money.IDR), "35000"},
		{money.New(-35000, money.IDR), "-35000"},
		{money.New(1234, money.USD), "12.34"},
		{money.New(5, money.USD), "0.05"},
		{money.New(-5, money.USD), "-0.05"},
		{money.Zero(money.EUR), "0.00"},
		{money.New(math.MinInt64, money.IDR), "-9223372036854775808"},
		{money.New(math.MinInt64, money.USD), "-92233720368547758.08"},
	}
	for _, tt := range tests {
		if got := tt.m.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestArithmetic(t *testing.T) {
	a, b := money.New(100, money.USD), money.New(30, money.USD)
	if s, err := a.Add(b); err != nil || s.Amount() != 130 {
		t.Fatalf("Add = %v %v", s, err)
	}
	if s, err := a.Sub(b); err != nil || s.Amount() != 70 {
		t.Fatalf("Sub = %v %v", s, err)
	}
	if _, err := a.Add(money.New(1, money.IDR)); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("mismatch Add err = %v", err)
	}
	if _, err := a.Sub(money.New(1, money.IDR)); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("mismatch Sub err = %v", err)
	}
	overflow := []func() error{
		func() error { _, err := money.New(math.MaxInt64, money.IDR).Add(money.New(1, money.IDR)); return err },
		func() error { _, err := money.New(math.MinInt64, money.IDR).Add(money.New(-1, money.IDR)); return err },
		func() error { _, err := money.New(0, money.IDR).Sub(money.New(math.MinInt64, money.IDR)); return err },
		func() error { _, err := money.New(math.MinInt64, money.IDR).Neg(); return err },
		func() error { _, err := money.New(math.MinInt64, money.IDR).Abs(); return err },
	}
	for i, f := range overflow {
		if err := f(); !errors.Is(err, money.ErrAmountOverflow) {
			t.Errorf("case %d err = %v", i, err)
		}
	}
	if n, _ := money.New(-5, money.IDR).Neg(); n.Amount() != 5 {
		t.Fatal("Neg")
	}
	if n, _ := money.New(-5, money.IDR).Abs(); n.Amount() != 5 {
		t.Fatal("Abs neg")
	}
	if n, _ := money.New(5, money.IDR).Abs(); n.Amount() != 5 {
		t.Fatal("Abs pos")
	}
}

func TestPredicatesAndCmp(t *testing.T) {
	m := money.New(-1, money.IDR)
	if !m.IsNegative() || m.IsPositive() || m.IsZero() {
		t.Fatal("predikat negatif salah")
	}
	if !money.Zero(money.IDR).IsZero() || !money.New(1, money.IDR).IsPositive() {
		t.Fatal("predikat zero/positif salah")
	}
	if !money.New(1, money.IDR).Equal(money.New(1, money.IDR)) || money.New(1, money.IDR).Equal(money.New(1, money.JPY)) {
		t.Fatal("Equal salah")
	}
	cases := []struct {
		a, b int64
		want int
	}{{1, 2, -1}, {2, 1, 1}, {2, 2, 0}}
	for _, c := range cases {
		got, err := money.New(c.a, money.IDR).Cmp(money.New(c.b, money.IDR))
		if err != nil || got != c.want {
			t.Errorf("Cmp(%d,%d) = %d %v", c.a, c.b, got, err)
		}
	}
	if _, err := money.New(1, money.IDR).Cmp(money.New(1, money.USD)); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatal("Cmp mismatch harus error")
	}
}

func TestCurrency(t *testing.T) {
	c, err := money.ParseCurrency(" usd ")
	if err != nil || c != money.USD || c.Exponent() != 2 || c.String() != "USD" {
		t.Fatalf("ParseCurrency = %v %v", c, err)
	}
	if _, err := money.ParseCurrency("XXX"); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatal("XXX harus unknown")
	}
	if money.MustCurrency("IDR") != money.IDR {
		t.Fatal("MustCurrency")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("MustCurrency harus panic")
			}
		}()
		money.MustCurrency("XXX")
	}()
	codes := money.Codes()
	slices.Sort(codes)
	if !slices.Equal(codes, []string{"EUR", "IDR", "JPY", "SGD", "USD"}) {
		t.Fatalf("Codes = %v", codes)
	}
	if !(money.Currency{}).IsZero() {
		t.Fatal("zero currency")
	}
}

func TestParseCode(t *testing.T) {
	m, err := money.ParseCode("1.50", "eur")
	if err != nil || m.Amount() != 150 || m.Currency() != money.EUR {
		t.Fatalf("ParseCode = %v %v", m, err)
	}
	if _, err := money.ParseCode("1", "XXX"); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatal("unknown code harus error")
	}
}

func TestJSON(t *testing.T) {
	type dto struct {
		Amount   money.Money    `json:"amount"`
		Currency money.Currency `json:"currency"`
	}
	b, err := json.Marshal(dto{money.New(1234, money.USD), money.USD})
	if err != nil || string(b) != `{"amount":"12.34","currency":"USD"}` {
		t.Fatalf("marshal = %s %v", b, err)
	}
	txt, _ := money.New(35000, money.IDR).MarshalText()
	if string(txt) != "35000" {
		t.Fatalf("MarshalText = %s", txt)
	}

	var c money.Currency
	if err := json.Unmarshal([]byte(`"sgd"`), &c); err != nil || c != money.SGD {
		t.Fatalf("unmarshal = %v %v", c, err)
	}
	for _, bad := range []string{`"XXX"`, `123`} {
		if err := json.Unmarshal([]byte(bad), &c); !errors.Is(err, money.ErrUnknownCurrency) {
			t.Errorf("%s err = %v", bad, err)
		}
	}
}

// FuzzParse memastikan Parse tidak pernah panic dan Parse(String(m)) == m.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"0", "12.34", "-0.05", "35000", "9223372036854775807", "-9223372036854775808", "1e3", "+1", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, c := range []money.Currency{money.IDR, money.USD} {
			m, err := money.Parse(s, c)
			if err != nil {
				continue
			}
			back, err := money.Parse(m.String(), c)
			if err != nil || !back.Equal(m) {
				t.Fatalf("round trip %q -> %q -> %v (%v)", s, m.String(), back, err)
			}
		}
	})
}
