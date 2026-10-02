package app

import (
	"context"
	"log/slog"
	"time"

	"go-auth-clean/internal/platform/logger"
)

// Cleaner menjalankan Service.Cleanup secara periodik sampai ctx dibatalkan.
type Cleaner struct {
	run      func(ctx context.Context) (CleanupResult, error)
	interval time.Duration
	log      *slog.Logger
}

func NewCleaner(svc *Service, interval time.Duration, log *slog.Logger) *Cleaner {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Cleaner{run: svc.Cleanup, interval: interval, log: log}
}

// Run memblok sampai ctx selesai. Satu putaran langsung saat start, lalu tiap interval.
func (c *Cleaner) Run(ctx context.Context) error {
	ctx = logger.WithContext(ctx, c.log)
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		c.tick(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (c *Cleaner) tick(ctx context.Context) {
	// Batasi durasi satu putaran supaya tidak menumpuk.
	ctx, cancel := context.WithTimeout(ctx, max(c.interval/2, 10*time.Second))
	defer cancel()
	res, err := c.run(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.log.ErrorContext(ctx, "auth cleanup failed", slog.Any("error", err))
		}
		return
	}
	c.log.InfoContext(ctx, "auth cleanup done",
		slog.Int64("sessions", res.Sessions), slog.Int64("otps", res.OTPs),
		slog.Int64("unverified_users", res.Unverified), slog.Int64("audit_anonymized", res.AuditAnonymized))
}
