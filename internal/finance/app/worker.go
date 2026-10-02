package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

// maintenanceService adalah subset Service yang dipakai worker (mudah di-fake).
type maintenanceService interface {
	PurgeExpiredIdempotencyKeys(ctx context.Context) (int64, error)
	ReconcileBalances(ctx context.Context, userID *uuid.UUID) ([]domain.BalanceDrift, error)
}

// MaintenanceWorker berkala menghapus idempotency key kedaluwarsa dan
// mendeteksi drift saldo (hanya di-log sebagai alert, tidak auto-fix).
type MaintenanceWorker struct {
	svc      maintenanceService
	interval time.Duration
	log      *slog.Logger
}

// NewMaintenanceWorker: interval <= 0 diganti 1 jam.
func NewMaintenanceWorker(svc maintenanceService, interval time.Duration, log *slog.Logger) *MaintenanceWorker {
	if interval <= 0 {
		interval = time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &MaintenanceWorker{svc: svc, interval: interval, log: log}
}

// Run berjalan sampai ctx selesai; satu putaran langsung saat start.
func (w *MaintenanceWorker) Run(ctx context.Context) error {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		w.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// RunOnce menjalankan satu putaran; error di-log, tidak menghentikan worker.
func (w *MaintenanceWorker) RunOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if n, err := w.svc.PurgeExpiredIdempotencyKeys(ctx); err != nil {
		w.log.ErrorContext(ctx, "finance: purge idempotency keys failed", slog.Any("error", err))
	} else if n > 0 {
		w.log.InfoContext(ctx, "finance: purged idempotency keys", slog.Int64("count", n))
	}
	drifts, err := w.svc.ReconcileBalances(ctx, nil)
	if err != nil {
		w.log.ErrorContext(ctx, "finance: reconcile balances failed", slog.Any("error", err))
		return
	}
	// Detail per akun sudah di-log oleh ReconcileBalances.
	if len(drifts) > 0 {
		w.log.WarnContext(ctx, "finance: balance drift detected", slog.Int("accounts", len(drifts)))
	}
}

// recurringGenerator adalah subset Service yang dipakai RecurringWorker.
type recurringGenerator interface {
	GenerateDue(ctx context.Context, asOf time.Time, batch int) (int, error)
}

// RecurringWorker berkala membuat transaksi dari recurring rule yang jatuh
// tempo. Aman dijalankan di banyak instance (FOR UPDATE SKIP LOCKED) dan
// idempotent per (rule, occurrence_date).
type RecurringWorker struct {
	gen      recurringGenerator
	clock    Clock
	interval time.Duration
	batch    int
	log      *slog.Logger
}

// NewRecurringWorker: interval <= 0 diganti 1 menit, batch 100 rule per tick.
func NewRecurringWorker(gen recurringGenerator, clock Clock, interval time.Duration, log *slog.Logger) *RecurringWorker {
	if interval <= 0 {
		interval = time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	return &RecurringWorker{gen: gen, clock: clock, interval: interval, batch: 100,
		log: log.With(slog.String("component", "recurring_worker"))}
}

// Run berjalan sampai ctx selesai; satu putaran langsung saat start.
func (w *RecurringWorker) Run(ctx context.Context) error {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		w.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// RunOnce menjalankan satu putaran dengan timeout = interval.
func (w *RecurringWorker) RunOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, w.interval)
	defer cancel()
	n, err := w.gen.GenerateDue(ctx, w.clock.Now(), w.batch)
	if err != nil {
		w.log.ErrorContext(ctx, "finance: generate recurring failed", slog.Any("error", err), slog.Int("created", n))
		return
	}
	if n > 0 {
		w.log.InfoContext(ctx, "finance: recurring transactions created", slog.Int("count", n))
	}
}

// billProcessor adalah subset Service yang dipakai BillWorker.
type billProcessor interface {
	ProcessBills(ctx context.Context, asOf time.Time, batch int) (int, error)
}

// BillWorker berkala mengirim pengingat tagihan dan menandai tagihan overdue.
type BillWorker struct {
	proc     billProcessor
	clock    Clock
	interval time.Duration
	batch    int
	log      *slog.Logger
}

// NewBillWorker: interval <= 0 diganti 1 jam, batch 500 tagihan per tick.
func NewBillWorker(proc billProcessor, clock Clock, interval time.Duration, log *slog.Logger) *BillWorker {
	if interval <= 0 {
		interval = time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &BillWorker{proc: proc, clock: clock, interval: interval, batch: 500,
		log: log.With(slog.String("component", "bill_worker"))}
}

// Run berjalan sampai ctx selesai; satu putaran langsung saat start.
func (w *BillWorker) Run(ctx context.Context) error {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		w.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// RunOnce menjalankan satu putaran dengan timeout = interval.
func (w *BillWorker) RunOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, w.interval)
	defer cancel()
	n, err := w.proc.ProcessBills(ctx, w.clock.Now(), w.batch)
	if err != nil {
		w.log.ErrorContext(ctx, "finance: process bills failed", slog.Any("error", err), slog.Int("notices", n))
		return
	}
	if n > 0 {
		w.log.InfoContext(ctx, "finance: bill notices sent", slog.Int("count", n))
	}
}
