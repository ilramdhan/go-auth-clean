package app

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

// Defaults adalah nilai awal user_settings (dari config).
type Defaults struct {
	Currency string
	Timezone string
}

// IdempotencyTTL adalah umur penyimpanan Idempotency-Key.
const IdempotencyTTL = 24 * time.Hour

// Deps berisi semua dependency Service.
type Deps struct {
	Tx           TxManager
	Clock        Clock
	IDs          IDGenerator
	Settings     domain.SettingsRepository
	Accounts     domain.AccountRepository
	Categories   domain.CategoryRepository
	Transactions domain.TransactionRepository
	Transfers    domain.TransferRepository
	Reports      domain.ReportRepository
	Budgets      domain.BudgetRepository
	Tags         domain.TagRepository
	Recurring    domain.RecurringRepository
	// BudgetAlerts menerima notifikasi budget melewati threshold (nil = diabaikan).
	BudgetAlerts BudgetNotifier
	Idempotency  IdempotencyStore
	Defaults     Defaults

	// P2. Members nil = tanpa shared wallet (akses hanya pemilik); Audit nil =
	// audit trail tidak dicatat; BillNotices nil = pengingat diabaikan;
	// Users nil = undangan via email tidak tersedia.
	Rates       domain.ExchangeRateRepository
	Goals       domain.GoalRepository
	Debts       domain.DebtRepository
	Bills       domain.BillRepository
	Members     domain.MemberRepository
	Audit       domain.AuditRepository
	BillNotices BillNotifier
	Users       UserDirectory
}

// Service adalah application service finance. Method dibagi per file sesuai aggregate.
type Service struct {
	tx           TxManager
	clock        Clock
	ids          IDGenerator
	settings     domain.SettingsRepository
	accounts     domain.AccountRepository
	categories   domain.CategoryRepository
	transactions domain.TransactionRepository
	transfers    domain.TransferRepository
	reports      domain.ReportRepository
	budgets      domain.BudgetRepository
	tags         domain.TagRepository
	recurring    domain.RecurringRepository
	alerts       BudgetNotifier
	idem         IdempotencyStore
	defaults     Defaults
	rates        domain.ExchangeRateRepository
	goals        domain.GoalRepository
	debts        domain.DebtRepository
	bills        domain.BillRepository
	members      domain.MemberRepository
	audit        domain.AuditRepository
	billNotices  BillNotifier
	users        UserDirectory
}

// NewService merakit Service. IDs nil = UUIDv7.
func NewService(d Deps) *Service {
	ids := d.IDs
	if ids == nil {
		ids = UUIDv7{}
	}
	alerts := d.BudgetAlerts
	if alerts == nil {
		alerts = noopNotifier{}
	}
	notices := d.BillNotices
	if notices == nil {
		notices = noopNotifier{}
	}
	return &Service{
		rates: d.Rates, goals: d.Goals, debts: d.Debts, bills: d.Bills, members: d.Members, audit: d.Audit,
		billNotices: notices, users: d.Users,
		budgets: d.Budgets, tags: d.Tags, recurring: d.Recurring, alerts: alerts,
		tx: d.Tx, clock: d.Clock, ids: ids, settings: d.Settings, accounts: d.Accounts,
		categories: d.Categories, transactions: d.Transactions, transfers: d.Transfers,
		reports: d.Reports, idem: d.Idempotency, defaults: d.Defaults,
	}
}

func (s *Service) now() time.Time { return s.clock.Now().UTC() }

func (s *Service) newID() (uuid.UUID, error) {
	id, err := s.ids.NewID()
	if err != nil {
		return uuid.Nil, fmt.Errorf("finance.newID: %w", err)
	}
	return id, nil
}

// lockAccounts mengunci akun (FOR UPDATE) dengan urutan ID ascending dan
// tanpa duplikat, supaya dua request yang mengunci akun yang sama dengan arah
// berlawanan tidak deadlock (jebakan #13). Wajib dipanggil di dalam tx.
func (s *Service) lockAccounts(ctx context.Context, userID uuid.UUID, ids ...uuid.UUID) (map[uuid.UUID]*domain.Account, error) {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, compareUUID)
	sorted = slices.Compact(sorted)
	out := make(map[uuid.UUID]*domain.Account, len(sorted))
	for _, id := range sorted {
		a, err := s.accounts.GetForUpdate(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		out[id] = a
	}
	return out, nil
}

// saveAccounts menyimpan akun yang saldonya berubah (version naik).
func (s *Service) saveAccounts(ctx context.Context, accs []*domain.Account, now time.Time) error {
	for _, a := range accs {
		a.Touch(now)
		if err := s.accounts.Update(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

func compareUUID(a, b uuid.UUID) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// checkVersion: expected nil = tidak dicek (If-Match opsional).
func checkVersion(expected *int, actual int) error {
	if expected != nil && *expected != actual {
		return domain.ErrVersionConflict
	}
	return nil
}
