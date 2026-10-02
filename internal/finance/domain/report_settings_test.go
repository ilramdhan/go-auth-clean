package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGranularityAndRange(t *testing.T) {
	if g, _ := ParseGranularity(""); g != GranularityMonth {
		t.Fatal("default")
	}
	if g, _ := ParseGranularity("day"); g != GranularityDay {
		t.Fatal("day")
	}
	_, err := ParseGranularity("week")
	wantValidation(t, err, "granularity")
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		from, to time.Time
		g        Granularity
		ok       bool
	}{
		{d(2026, 1, 1), d(2026, 1, 1), GranularityDay, true},
		{d(2026, 1, 2), d(2026, 1, 1), GranularityDay, false},
		{d(2025, 1, 1), d(2025, 12, 31), GranularityDay, true},
		{d(2024, 1, 1), d(2024, 12, 31), GranularityDay, true}, // kabisat 366 hari
		{d(2025, 1, 1), d(2026, 1, 2), GranularityDay, false},
		{d(2016, 1, 1), d(2025, 12, 1), GranularityMonth, true},
		{d(2016, 1, 1), d(2026, 1, 1), GranularityMonth, false},
	}
	for i, c := range cases {
		if err := ValidateRange(c.from, c.to, c.g); (err == nil) != c.ok {
			t.Errorf("case %d: err=%v", i, err)
		}
	}
}

func TestParseMonthAndPercent(t *testing.T) {
	from, to, err := ParseMonth("2024-02", tNow, jakarta)
	if err != nil || from.Day() != 1 || from.Month() != 2 || to.Month() != 3 {
		t.Fatalf("%v %v %v", from, to, err)
	}
	// 31 Maret 20:00 UTC = 1 April WIB
	from, _, _ = ParseMonth("", time.Date(2026, 3, 31, 20, 0, 0, 0, time.UTC), jakarta)
	if from.Month() != time.April {
		t.Fatalf("month = %v", from)
	}
	_, _, err = ParseMonth("2024/02", tNow, jakarta)
	wantValidation(t, err, "month")
	cases := map[[2]int64]string{
		{1, 3}: "33.33", {2, 3}: "66.67", {0, 5}: "0.00", {5, 0}: "0.00", {1, 1}: "100.00",
		{MaxAmount, MaxAmount * 2}: "50.00", {1, 200000}: "0.00", {1, 20000}: "0.01",
	}
	for in, want := range cases {
		if got := Percent(in[0], in[1]); got != want {
			t.Errorf("Percent(%d,%d) = %s, want %s", in[0], in[1], got, want)
		}
	}
}

func TestUserSettings(t *testing.T) {
	user := uuid.New()
	s, err := NewUserSettings(user, "IDR", "Asia/Jakarta", 1, tNow)
	if err != nil {
		t.Fatal(err)
	}
	if s.UserID() != user || s.BaseCurrency() != idr || s.Timezone() != "Asia/Jakarta" || s.Location() == nil ||
		s.WeekStart() != 1 || !s.CreatedAt().Equal(tNow) || !s.UpdatedAt().Equal(tNow) {
		t.Fatal("getter")
	}
	if _, err := NewUserSettings(user, "XXX", "Asia/Jakarta", 1, tNow); err == nil {
		t.Fatal("currency harus ditolak")
	}
	for _, tz := range []string{"", "Local", "Mars/Olympus"} {
		_, err := NewUserSettings(user, "IDR", tz, 1, tNow)
		wantErr(t, err, ErrInvalidTimezone)
	}
	_, err = NewUserSettings(user, "IDR", "UTC", 7, tNow)
	wantErr(t, err, ErrInvalidWeekStart)
	r := RehydrateUserSettings(user, idr, "Not/AZone", 0, tNow, tNow)
	if r.Location() != time.UTC {
		t.Fatal("fallback UTC")
	}
	if (&ValidationError{Field: "a", Reason: "b"}).Error() != "a: b" {
		t.Fatal("error string")
	}
}
