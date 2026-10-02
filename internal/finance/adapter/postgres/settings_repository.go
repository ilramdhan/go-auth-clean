package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/domain"
)

var _ domain.SettingsRepository = (*SettingsRepository)(nil)

type SettingsRepository struct{ base }

func NewSettingsRepository(db *pgxpool.Pool) *SettingsRepository {
	return &SettingsRepository{base{db}}
}

func scanSettings(row interface{ Scan(...any) error }) (*domain.UserSettings, error) {
	var (
		uid              uuid.UUID
		cur, tz          string
		week             int16
		created, updated time.Time
	)
	if err := row.Scan(&uid, &cur, &tz, &week, &created, &updated); err != nil {
		return nil, err
	}
	c, err := currency(cur)
	if err != nil {
		return nil, err
	}
	return domain.RehydrateUserSettings(uid, c, tz, int(week), created, updated), nil
}

const settingsCols = `user_id, base_currency, timezone, week_start, created_at, updated_at`

// GetOrCreate: INSERT ... ON CONFLICT DO NOTHING lalu SELECT, aman untuk request paralel.
func (r *SettingsRepository) GetOrCreate(ctx context.Context, def *domain.UserSettings) (*domain.UserSettings, error) {
	const ins = `INSERT INTO user_settings (` + settingsCols + `) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (user_id) DO NOTHING`
	if _, err := r.conn(ctx).Exec(ctx, ins, def.UserID(), def.BaseCurrency().Code(), def.Timezone(),
		def.WeekStart(), def.CreatedAt(), def.UpdatedAt()); err != nil {
		return nil, fmt.Errorf("settingsRepo.GetOrCreate insert: %w", err)
	}
	s, err := scanSettings(r.conn(ctx).QueryRow(ctx, `SELECT `+settingsCols+` FROM user_settings WHERE user_id = $1`, def.UserID()))
	if err != nil {
		return nil, fmt.Errorf("settingsRepo.GetOrCreate select: %w", err)
	}
	return s, nil
}

func (r *SettingsRepository) Upsert(ctx context.Context, s *domain.UserSettings) (*domain.UserSettings, error) {
	const q = `INSERT INTO user_settings (` + settingsCols + `) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (user_id) DO UPDATE SET base_currency = EXCLUDED.base_currency,
			timezone = EXCLUDED.timezone, week_start = EXCLUDED.week_start, updated_at = EXCLUDED.updated_at
		RETURNING ` + settingsCols
	out, err := scanSettings(r.conn(ctx).QueryRow(ctx, q, s.UserID(), s.BaseCurrency().Code(), s.Timezone(),
		s.WeekStart(), s.CreatedAt(), s.UpdatedAt()))
	if err != nil {
		return nil, fmt.Errorf("settingsRepo.Upsert: %w", err)
	}
	return out, nil
}

func (r *SettingsRepository) ListCurrencies(ctx context.Context) ([]domain.CurrencyInfo, error) {
	rows, err := r.conn(ctx).Query(ctx, `SELECT code, name, minor_unit, symbol FROM currencies ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("settingsRepo.ListCurrencies: %w", err)
	}
	defer rows.Close()
	var out []domain.CurrencyInfo
	for rows.Next() {
		var c domain.CurrencyInfo
		var mu int16
		if err := rows.Scan(&c.Code, &c.Name, &mu, &c.Symbol); err != nil {
			return nil, fmt.Errorf("settingsRepo.ListCurrencies scan: %w", err)
		}
		c.MinorUnit = int(mu)
		out = append(out, c)
	}
	return out, rows.Err()
}
