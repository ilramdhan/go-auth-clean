package domain

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// Granularity adalah resolusi seri cashflow.
type Granularity string

const (
	GranularityDay   Granularity = "day"
	GranularityMonth Granularity = "month"
)

// Batas rentang laporan agar query tetap ringan.
const (
	MaxDayRange   = 366
	MaxMonthRange = 120
)

var ErrInvalidRange = errors.New("invalid date range")

// ParseGranularity memvalidasi granularity (kosong = month).
func ParseGranularity(s string) (Granularity, error) {
	switch g := Granularity(s); g {
	case "":
		return GranularityMonth, nil
	case GranularityDay, GranularityMonth:
		return g, nil
	default:
		return "", &ValidationError{Field: "granularity", Reason: "must be day or month"}
	}
}

// IncomeExpense adalah total income & expense untuk satu currency.
type IncomeExpense struct {
	Currency money.Currency
	Income   int64
	Expense  int64
}

// CategoryTotal adalah total per kategori (sudah di-roll-up ke parent).
type CategoryTotal struct {
	CategoryID uuid.UUID
	Name       string
	Total      int64
}

// CashflowPoint adalah satu titik seri (awal hari/bulan).
type CashflowPoint struct {
	Period  time.Time
	Income  int64
	Expense int64
}

// BalanceDrift adalah akun yang saldo cache-nya berbeda dengan hasil hitung ulang.
type BalanceDrift struct {
	AccountID uuid.UUID
	UserID    uuid.UUID
	Currency  money.Currency
	Cached    int64
	Expected  int64
}

// MonthRange mengembalikan [from, to) satu bulan kalender.
func MonthRange(year int, month time.Month) (from, to time.Time) {
	from = time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(0, 1, 0)
}

// ParseMonth membaca "YYYY-MM". String kosong = bulan berjalan di timezone user.
func ParseMonth(s string, now time.Time, loc *time.Location) (from, to time.Time, err error) {
	if s == "" {
		today := Today(now, loc)
		from, to = MonthRange(today.Year(), today.Month())
		return from, to, nil
	}
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, time.Time{}, &ValidationError{Field: "month", Reason: "must be YYYY-MM"}
	}
	from, to = MonthRange(t.Year(), t.Month())
	return from, to, nil
}

// ValidateRange memastikan from <= to (inklusif) dan rentang tidak melebihi batas.
func ValidateRange(from, to time.Time, g Granularity) error {
	if to.Before(from) {
		return ErrInvalidRange
	}
	switch g {
	case GranularityDay:
		if to.Sub(from) > (MaxDayRange-1)*24*time.Hour {
			return ErrInvalidRange
		}
	case GranularityMonth:
		months := (to.Year()-from.Year())*12 + int(to.Month()-from.Month())
		if months >= MaxMonthRange {
			return ErrInvalidRange
		}
	}
	return nil
}

// Percent menghitung bagian/total sebagai string 2 desimal ("12.34") tanpa
// float (pembulatan half-up). total <= 0 menghasilkan "0.00". Memakai big.Int
// supaya perkalian tidak overflow untuk nominal besar.
func Percent(part, total int64) string {
	if total <= 0 || part <= 0 {
		return "0.00"
	}
	// bp = round(part * 10000 / total) = (2*part*10000 + total) / (2*total)
	num := new(big.Int).Mul(big.NewInt(part), big.NewInt(20000))
	num.Add(num, big.NewInt(total))
	bp := num.Quo(num, new(big.Int).Mul(big.NewInt(total), big.NewInt(2))).Int64()
	return fmt.Sprintf("%d.%02d", bp/100, bp%100)
}
