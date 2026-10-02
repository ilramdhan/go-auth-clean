package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

// UpdateSettingsInput adalah input PUT /settings.
type UpdateSettingsInput struct {
	UserID       uuid.UUID
	BaseCurrency string
	Timezone     string
	WeekStart    int
}

// GetSettings mengembalikan settings user, dibuat lazily dengan default config.
func (s *Service) GetSettings(ctx context.Context, userID uuid.UUID) (*domain.UserSettings, error) {
	def, err := domain.NewUserSettings(userID, s.defaults.Currency, s.defaults.Timezone, 1, s.now())
	if err != nil {
		return nil, fmt.Errorf("finance.GetSettings default: %w", err)
	}
	st, err := s.settings.GetOrCreate(ctx, def)
	if err != nil {
		return nil, fmt.Errorf("finance.GetSettings: %w", err)
	}
	return st, nil
}

// UpdateSettings memvalidasi lalu menyimpan settings user.
func (s *Service) UpdateSettings(ctx context.Context, in UpdateSettingsInput) (*domain.UserSettings, error) {
	st, err := domain.NewUserSettings(in.UserID, in.BaseCurrency, in.Timezone, in.WeekStart, s.now())
	if err != nil {
		return nil, err
	}
	saved, err := s.settings.Upsert(ctx, st)
	if err != nil {
		return nil, fmt.Errorf("finance.UpdateSettings: %w", err)
	}
	return saved, nil
}

// ListCurrencies mengembalikan currency yang didukung.
func (s *Service) ListCurrencies(ctx context.Context) ([]domain.CurrencyInfo, error) {
	out, err := s.settings.ListCurrencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("finance.ListCurrencies: %w", err)
	}
	return out, nil
}
