package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// UserSettings adalah preferensi finance per user. Finance tidak tahu kapan
// user registrasi di auth, jadi settings dibuat lazily dengan default config.
type UserSettings struct {
	userID       uuid.UUID
	baseCurrency money.Currency
	timezone     string
	location     *time.Location
	weekStart    int
	createdAt    time.Time
	updatedAt    time.Time
}

// NewUserSettings memvalidasi currency, timezone IANA dan awal minggu (0=Minggu).
func NewUserSettings(userID uuid.UUID, currency, timezone string, weekStart int, now time.Time) (*UserSettings, error) {
	cur, err := money.ParseCurrency(currency)
	if err != nil {
		return nil, err
	}
	loc, err := LoadTimezone(timezone)
	if err != nil {
		return nil, err
	}
	if weekStart < 0 || weekStart > 6 {
		return nil, ErrInvalidWeekStart
	}
	now = now.UTC()
	return &UserSettings{userID: userID, baseCurrency: cur, timezone: loc.String(), location: loc,
		weekStart: weekStart, createdAt: now, updatedAt: now}, nil
}

// LoadTimezone memvalidasi nama timezone IANA. "Local" dan string kosong
// ditolak karena maknanya bergantung pada server.
func LoadTimezone(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "Local" || len(name) > 64 {
		return nil, ErrInvalidTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrInvalidTimezone
	}
	return loc, nil
}

// RehydrateUserSettings membangun ulang dari database. Timezone tak dikenal
// (mis. tzdata berubah) jatuh ke UTC daripada gagal total.
func RehydrateUserSettings(userID uuid.UUID, currency money.Currency, timezone string, weekStart int, createdAt, updatedAt time.Time) *UserSettings {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return &UserSettings{userID: userID, baseCurrency: currency, timezone: timezone, location: loc,
		weekStart: weekStart, createdAt: createdAt, updatedAt: updatedAt}
}

func (s *UserSettings) UserID() uuid.UUID            { return s.userID }
func (s *UserSettings) BaseCurrency() money.Currency { return s.baseCurrency }
func (s *UserSettings) Timezone() string             { return s.timezone }
func (s *UserSettings) Location() *time.Location     { return s.location }
func (s *UserSettings) WeekStart() int               { return s.weekStart }
func (s *UserSettings) CreatedAt() time.Time         { return s.createdAt }
func (s *UserSettings) UpdatedAt() time.Time         { return s.updatedAt }
