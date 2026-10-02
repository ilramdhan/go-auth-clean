// Package finance adalah bounded context personal finance tracker
// (akun, kategori, transaksi, transfer, laporan). Entry point untuk wiring
// di cmd/api: NewModule -> RegisterRoutes -> StartWorkers.
package finance

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	fhttp "go-auth-clean/internal/finance/adapter/http"
	"go-auth-clean/internal/finance/adapter/postgres"
	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/platform/clock"
	"go-auth-clean/internal/platform/database"
	"go-auth-clean/internal/platform/validator"
)

// Config adalah konfigurasi module (diisi dari config.FinanceConfig).
type Config struct {
	DefaultCurrency string
	DefaultTimezone string
	// WorkerInterval adalah periode maintenance worker (default 1 jam).
	WorkerInterval time.Duration
	// RecurringInterval adalah periode worker recurring transaction (default 1 menit).
	RecurringInterval time.Duration
	// BillInterval adalah periode worker pengingat/overdue tagihan (default 1 jam).
	BillInterval time.Duration
}

// Deps adalah dependency module. TxManager & Clock opsional (default dibuat dari Pool / clock.System).
type Deps struct {
	Pool      *pgxpool.Pool
	TxManager app.TxManager
	Clock     app.Clock
	Logger    *slog.Logger
	Config    Config
	// UserDirectory dipakai undangan shared wallet via email (adapter ke auth,
	// dirakit di cmd/api). nil = undangan hanya via user_id.
	UserDirectory app.UserDirectory
	// BillNotifier opsional; default mencatat pengingat ke log.
	BillNotifier app.BillNotifier
}

// Module menyatukan service, handler, dan worker finance.
type Module struct {
	svc       *app.Service
	handler   *fhttp.Handler
	worker    *app.MaintenanceWorker
	recurring *app.RecurringWorker
	bills     *app.BillWorker
	wg        sync.WaitGroup
}

func NewModule(d Deps) *Module {
	if d.Clock == nil {
		d.Clock = clock.System{}
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.TxManager == nil {
		d.TxManager = database.NewTxManager(d.Pool)
	}
	if d.Config.DefaultCurrency == "" {
		d.Config.DefaultCurrency = "IDR"
	}
	if d.Config.DefaultTimezone == "" {
		d.Config.DefaultTimezone = "Asia/Jakarta"
	}
	log := d.Logger.With(slog.String("module", "finance"))
	if d.BillNotifier == nil {
		d.BillNotifier = billNoticeLogger{log: log}
	}
	svc := app.NewService(app.Deps{
		Tx:           d.TxManager,
		Clock:        d.Clock,
		IDs:          app.UUIDv7{},
		Settings:     postgres.NewSettingsRepository(d.Pool),
		Accounts:     postgres.NewAccountRepository(d.Pool),
		Categories:   postgres.NewCategoryRepository(d.Pool),
		Transactions: postgres.NewTransactionRepository(d.Pool),
		Transfers:    postgres.NewTransferRepository(d.Pool),
		Reports:      postgres.NewReportRepository(d.Pool),
		Budgets:      postgres.NewBudgetRepository(d.Pool),
		Tags:         postgres.NewTagRepository(d.Pool),
		Recurring:    postgres.NewRecurringRepository(d.Pool),
		BudgetAlerts: budgetAlertLogger{log: log},
		Idempotency:  postgres.NewIdempotencyStore(d.Pool),
		Defaults:     app.Defaults{Currency: d.Config.DefaultCurrency, Timezone: d.Config.DefaultTimezone},
		Rates:        postgres.NewExchangeRateRepository(d.Pool),
		Goals:        postgres.NewGoalRepository(d.Pool),
		Debts:        postgres.NewDebtRepository(d.Pool),
		Bills:        postgres.NewBillRepository(d.Pool),
		Members:      postgres.NewMemberRepository(d.Pool),
		Audit:        postgres.NewAuditRepository(d.Pool),
		BillNotices:  d.BillNotifier,
		Users:        d.UserDirectory,
	})
	return &Module{
		svc:       svc,
		handler:   fhttp.NewHandler(svc, validator.New()),
		worker:    app.NewMaintenanceWorker(svc, d.Config.WorkerInterval, log),
		recurring: app.NewRecurringWorker(svc, d.Clock, d.Config.RecurringInterval, log),
		bills:     app.NewBillWorker(svc, d.Clock, d.Config.BillInterval, log),
	}
}

// Service mengekspos application service (untuk test/integrasi lain).
func (m *Module) Service() *app.Service { return m.svc }

// RegisterRoutes mendaftarkan semua endpoint /api/v1/... finance; semuanya butuh auth.
func (m *Module) RegisterRoutes(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler) {
	m.handler.Routes(mux, requireAuth)
}

// Workers mengembalikan worker supaya pemanggil bisa mengatur lifecycle sendiri.
func (m *Module) Workers() []interface{ Run(context.Context) error } {
	return []interface{ Run(context.Context) error }{m.worker, m.recurring, m.bills}
}

// StartWorkers menjalankan worker di goroutine sampai ctx selesai; Wait menunggu semuanya berhenti.
func (m *Module) StartWorkers(ctx context.Context) {
	for _, w := range m.Workers() {
		m.wg.Go(func() { _ = w.Run(ctx) })
	}
}

// Wait menunggu worker berhenti (panggil setelah ctx StartWorkers dibatalkan).
func (m *Module) Wait() { m.wg.Wait() }

// budgetAlertLogger mencatat event BudgetThresholdReached ke log (belum ada
// kanal notifikasi; bisa diganti outbox/push tanpa mengubah app).
type budgetAlertLogger struct{ log *slog.Logger }

func (l budgetAlertLogger) BudgetThresholdReached(ctx context.Context, a app.BudgetAlert) {
	l.log.InfoContext(ctx, "finance: budget threshold reached",
		slog.String("budget_id", a.BudgetID.String()), slog.String("level", a.Level))
}

// billNoticeLogger mencatat pengingat tagihan ke log (kanal notifikasi
// sebenarnya bisa dipasang lewat Deps.BillNotifier).
type billNoticeLogger struct{ log *slog.Logger }

func (l billNoticeLogger) BillNotice(ctx context.Context, n app.BillNotice) {
	l.log.InfoContext(ctx, "finance: bill reminder",
		slog.String("bill_id", n.BillID.String()), slog.String("user_id", n.UserID.String()),
		slog.String("kind", n.Kind), slog.String("due_date", n.DueDate.Format(time.DateOnly)))
}
