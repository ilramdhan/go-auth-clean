package domain

import "time"

// Frequency adalah pola pengulangan jadwal (recurring rule & tagihan).
type Frequency string

const (
	FreqOnce    Frequency = "once"
	FreqDaily   Frequency = "daily"
	FreqWeekly  Frequency = "weekly"
	FreqMonthly Frequency = "monthly"
	FreqYearly  Frequency = "yearly"
)

// MaxInterval membatasi interval pengulangan (mis. tiap 365 hari).
const MaxInterval = 365

// ParseFrequency memvalidasi frekuensi terhadap daftar yang diizinkan.
func ParseFrequency(s string, allowed ...Frequency) (Frequency, error) {
	for _, f := range allowed {
		if string(f) == s {
			return f, nil
		}
	}
	return "", ErrInvalidFrequency
}

// NextDate mengembalikan tanggal kemunculan berikutnya setelah d. Untuk
// bulanan/tahunan hari di-anchor ke anchorDay dan di-clamp ke akhir bulan
// (31 Jan -> 28/29 Feb -> 31 Mar). FreqOnce tidak punya kemunculan berikutnya
// sehingga mengembalikan zero time.
func NextDate(f Frequency, interval, anchorDay int, d time.Time) time.Time {
	if interval < 1 {
		interval = 1
	}
	d = DateOf(d)
	switch f {
	case FreqDaily:
		return d.AddDate(0, 0, interval)
	case FreqWeekly:
		return d.AddDate(0, 0, 7*interval)
	case FreqMonthly:
		return addMonthsClamped(d, interval, anchorDay)
	case FreqYearly:
		return addMonthsClamped(d, 12*interval, anchorDay)
	default:
		return time.Time{}
	}
}

func addMonthsClamped(d time.Time, n, day int) time.Time {
	first := time.Date(d.Year(), d.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	if day < 1 {
		day = d.Day()
	}
	return time.Date(first.Year(), first.Month(), min(day, last), 0, 0, 0, 0, time.UTC)
}

func validateInterval(n int) (int, error) {
	if n == 0 {
		return 1, nil
	}
	if n < 1 || n > MaxInterval {
		return 0, &ValidationError{Field: "interval", Reason: "must be between 1 and 365"}
	}
	return n, nil
}

// validatePlanDate: tanggal jadwal (boleh di masa depan) minimal 1970 dan
// maksimal 100 tahun ke depan.
func validatePlanDate(field string, d, now time.Time) (time.Time, error) {
	d = DateOf(d)
	if d.Before(minDate) {
		return time.Time{}, ErrDateTooOld
	}
	if d.After(DateOf(now).AddDate(100, 0, 0)) {
		return time.Time{}, &ValidationError{Field: field, Reason: "too far in the future"}
	}
	return d, nil
}
