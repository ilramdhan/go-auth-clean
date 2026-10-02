package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Batas-batas invariant yang dipakai bersama oleh beberapa aggregate.
const (
	// MaxAmount membatasi nominal satu transaksi/transfer/saldo awal (minor unit)
	// supaya penjumlahan saldo jauh dari overflow int64 (jebakan #22).
	MaxAmount int64 = 1_000_000_000_000_000
	// MaxNoteLength adalah panjang maksimal catatan (rune).
	MaxNoteLength = 255
	// MaxNameLength adalah panjang maksimal nama akun/kategori (rune).
	MaxNameLength = 50
)

// minDate adalah tanggal transaksi paling awal yang diizinkan.
var minDate = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// TxType adalah arah uang: income menambah saldo, expense mengurangi.
type TxType string

const (
	TxIncome  TxType = "income"
	TxExpense TxType = "expense"
)

// ParseTxType memvalidasi string menjadi TxType.
func ParseTxType(s string) (TxType, error) {
	switch t := TxType(s); t {
	case TxIncome, TxExpense:
		return t, nil
	default:
		return "", ErrInvalidTxType
	}
}

// DateOf mengambil bagian tanggal (kalender) dari t lalu menormalkan ke
// tengah malam UTC. Kolom DATE di Postgres tidak punya timezone.
func DateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Today mengembalikan tanggal hari ini menurut timezone user.
func Today(now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	return DateOf(now.In(loc))
}

// validateDate memastikan tanggal tidak sebelum 1970 dan tidak lebih dari
// besok (toleransi perbedaan timezone client) menurut timezone user.
func validateDate(date, now time.Time, loc *time.Location) (time.Time, error) {
	d := DateOf(date)
	if d.Before(minDate) {
		return time.Time{}, ErrDateTooOld
	}
	if d.After(Today(now, loc).AddDate(0, 0, 1)) {
		return time.Time{}, ErrDateInFuture
	}
	return d, nil
}

func normalizeName(field, s string) (string, error) {
	name := strings.Join(strings.Fields(s), " ")
	n := utf8.RuneCountInString(name)
	if n == 0 {
		return "", &ValidationError{Field: field, Reason: "must not be empty"}
	}
	if n > MaxNameLength {
		return "", &ValidationError{Field: field, Reason: "max 50 characters"}
	}
	return name, nil
}

func normalizeNote(s string) (string, error) {
	note := strings.TrimSpace(s)
	if utf8.RuneCountInString(note) > MaxNoteLength {
		return "", &ValidationError{Field: "note", Reason: "max 255 characters"}
	}
	return note, nil
}

func validateAmount(minor int64) error {
	if minor <= 0 {
		return ErrInvalidAmount
	}
	if minor > MaxAmount {
		return ErrAmountTooLarge
	}
	return nil
}
