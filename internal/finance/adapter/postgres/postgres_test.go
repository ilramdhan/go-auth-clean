package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/adapter/postgres"
	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/database"
	"go-auth-clean/internal/platform/database/dbtest"
	"go-auth-clean/internal/shared/money"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// Kategori system hasil seed migration 000011.
var (
	catFood   = uuid.MustParse("01920000-0000-7000-8000-000000000101")
	catSalary = uuid.MustParse("01920000-0000-7000-8000-000000000001")
)

// financeTables: tabel milik user; currencies & kategori system tidak di-truncate.
var financeTables = []string{"goal_contributions", "savings_goals", "debt_payments", "debts", "bills", "account_members", "finance_audit_logs", "exchange_rates", "transaction_tags", "tags", "budgets", "idempotency_keys", "transfers", "transactions", "recurring_rules", "accounts", "user_settings"}

type env struct {
	pool *pgxpool.Pool
	svc  *app.Service
	idem *postgres.IdempotencyStore
	accs *postgres.AccountRepository
	// P2
	notices *noticeSink
	users   userDir
}

func setup(t *testing.T) env {
	t.Helper()
	pool := dbtest.New(t)
	dbtest.Truncate(t, pool, financeTables...)
	// Kategori custom user dihapus tanpa menyentuh kategori system.
	if _, err := pool.Exec(t.Context(), `DELETE FROM categories WHERE user_id IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	idem := postgres.NewIdempotencyStore(pool)
	accs := postgres.NewAccountRepository(pool)
	notices := &noticeSink{}
	users := userDir{}
	svc := app.NewService(app.Deps{
		Tx: database.NewTxManager(pool), Clock: sysClock{},
		Settings: postgres.NewSettingsRepository(pool), Accounts: accs,
		Categories: postgres.NewCategoryRepository(pool), Transactions: postgres.NewTransactionRepository(pool),
		Transfers: postgres.NewTransferRepository(pool), Reports: postgres.NewReportRepository(pool),
		Budgets: postgres.NewBudgetRepository(pool), Tags: postgres.NewTagRepository(pool),
		Recurring: postgres.NewRecurringRepository(pool),
		Rates:     postgres.NewExchangeRateRepository(pool), Goals: postgres.NewGoalRepository(pool),
		Debts: postgres.NewDebtRepository(pool), Bills: postgres.NewBillRepository(pool),
		Members: postgres.NewMemberRepository(pool), Audit: postgres.NewAuditRepository(pool),
		BillNotices: notices, Users: users,
		Idempotency: idem, Defaults: app.Defaults{Currency: "IDR", Timezone: "Asia/Jakarta"},
	})
	return env{pool: pool, svc: svc, idem: idem, accs: accs, notices: notices, users: users}
}

type sysClock struct{}

func (sysClock) Now() time.Time { return time.Now() }

func today() time.Time {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	y, m, d := time.Now().In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// must dipakai sebagai must(call())(t)(t): gagal via t.Fatal bila err.
func must[T any](v T, err error) func(t *testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return v
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want %v", err, target)
	}
}

func newAccount(t *testing.T, e env, user uuid.UUID, name, typ, cur, initial string) *domain.Account {
	t.Helper()
	return must(e.svc.CreateAccount(t.Context(), app.CreateAccountInput{UserID: user, Name: name, Type: typ, Currency: cur, InitialBalance: initial}))(t)
}

func balance(t *testing.T, e env, user, id uuid.UUID) money.Money {
	t.Helper()
	return must(e.svc.GetAccount(t.Context(), user, id))(t).Balance()
}

func TestSettingsAndCurrencies(t *testing.T) {
	e := setup(t)
	ctx, user := t.Context(), uuid.New()
	st := must(e.svc.GetSettings(ctx, user))(t)
	if st.BaseCurrency() != money.IDR || st.Timezone() != "Asia/Jakarta" {
		t.Fatalf("defaults = %+v", st)
	}
	st = must(e.svc.UpdateSettings(ctx, app.UpdateSettingsInput{UserID: user, BaseCurrency: "USD", Timezone: "UTC", WeekStart: 0}))(t)
	if again := must(e.svc.GetSettings(ctx, user))(t); again.BaseCurrency() != money.USD || again.WeekStart() != 0 || st.Timezone() != "UTC" {
		t.Fatalf("updated = %+v", again)
	}
	cs := must(e.svc.ListCurrencies(ctx))(t)
	if len(cs) < 5 {
		t.Fatalf("currencies = %d", len(cs))
	}
}

func TestAccountRepository_CRUDAndIDOR(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	alice, bob := uuid.New(), uuid.New()
	a := newAccount(t, e, alice, "BCA", "bank", "", "1000000")
	if a.Balance().Amount() != 1000000 || a.Version() != 1 {
		t.Fatalf("created = %+v", a)
	}
	// IDOR: akun alice tidak terlihat oleh bob (not found, bukan forbidden).
	_, err := e.svc.GetAccount(ctx, bob, a.ID())
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = e.svc.UpdateAccount(ctx, app.UpdateAccountInput{UserID: bob, ID: a.ID(), Name: new("x")})
	wantErr(t, err, domain.ErrAccountNotFound)
	wantErr(t, e.svc.DeleteAccount(ctx, bob, a.ID()), domain.ErrAccountNotFound)

	// Nama unik per user (case-insensitive), user lain boleh sama.
	_, err = e.svc.CreateAccount(ctx, app.CreateAccountInput{UserID: alice, Name: "bca", Type: "cash"})
	wantErr(t, err, domain.ErrDuplicateName)
	newAccount(t, e, bob, "BCA", "cash", "", "")

	v := 1
	up := must(e.svc.UpdateAccount(ctx, app.UpdateAccountInput{UserID: alice, ID: a.ID(), ExpectedVersion: &v, Name: new("BCA Utama"), InitialBalance: new("1500000")}))(t)
	if up.Balance().Amount() != 1500000 || up.Version() != 2 {
		t.Fatalf("updated = %+v", up)
	}
	_, err = e.svc.UpdateAccount(ctx, app.UpdateAccountInput{UserID: alice, ID: a.ID(), ExpectedVersion: &v, Name: new("y")})
	wantErr(t, err, domain.ErrVersionConflict)

	must(e.svc.ArchiveAccount(ctx, alice, a.ID()))(t)
	if list := must(e.svc.ListAccounts(ctx, alice, false))(t); len(list) != 0 {
		t.Fatalf("archived listed: %d", len(list))
	}
	if list := must(e.svc.ListAccounts(ctx, alice, true))(t); len(list) != 1 || list[0].ArchivedAt() == nil {
		t.Fatalf("with archived: %+v", list)
	}
	must(e.svc.UnarchiveAccount(ctx, alice, a.ID()))(t)

	// Akun tanpa aktivitas boleh dihapus; nama bisa dipakai lagi.
	if err := e.svc.DeleteAccount(ctx, alice, a.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.DeleteAccount(ctx, alice, a.ID()), domain.ErrAccountNotFound)
	newAccount(t, e, alice, "BCA Utama", "bank", "USD", "12.50")
}

func TestCategoryRepository(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	alice, bob := uuid.New(), uuid.New()
	parent := must(e.svc.CreateCategory(ctx, app.CreateCategoryInput{UserID: alice, Type: "expense", Name: "Hobi", Color: "#A0522D"}))(t)
	pid := parent.ID()
	child := must(e.svc.CreateCategory(ctx, app.CreateCategoryInput{UserID: alice, Type: "expense", Name: "Kopi", ParentID: &pid}))(t)
	_, err := e.svc.CreateCategory(ctx, app.CreateCategoryInput{UserID: alice, Type: "expense", Name: "hobi"})
	wantErr(t, err, domain.ErrDuplicateName)

	tree := must(e.svc.ListCategories(ctx, alice, "expense"))(t)
	found := false
	for _, n := range tree {
		if n.Category.ID() == pid && len(n.Children) == 1 && n.Children[0].ID() == child.ID() {
			found = true
		}
	}
	if !found {
		t.Fatal("custom tree not listed")
	}
	// Bob hanya melihat kategori system.
	for _, n := range must(e.svc.ListCategories(ctx, bob, ""))(t) {
		if !n.Category.IsSystem() {
			t.Fatalf("bob sees %s", n.Category.Name())
		}
	}
	_, err = e.svc.GetCategory(ctx, bob, pid)
	wantErr(t, err, domain.ErrCategoryNotFound)
	_, err = e.svc.UpdateCategory(ctx, app.UpdateCategoryInput{UserID: alice, ID: catFood, Name: new("x")})
	wantErr(t, err, domain.ErrCategoryReadOnly)

	wantErr(t, e.svc.DeleteCategory(ctx, alice, pid, nil), domain.ErrCategoryHasChildren)

	// Kategori dipakai transaksi -> wajib reassign.
	acc := newAccount(t, e, alice, "Cash", "cash", "", "100000")
	tx := createTx(t, e, alice, acc.ID(), child.ID(), "expense", "5000", "key-cat-0001")
	wantErr(t, e.svc.DeleteCategory(ctx, alice, child.ID(), nil), domain.ErrCategoryInUse)
	food := catFood
	if err := e.svc.DeleteCategory(ctx, alice, child.ID(), &food); err != nil {
		t.Fatal(err)
	}
	if got := must(e.svc.GetTransaction(ctx, alice, tx.ID()))(t); got.CategoryID() != catFood {
		t.Fatalf("not reassigned: %v", got.CategoryID())
	}
	if err := e.svc.DeleteCategory(ctx, alice, pid, nil); err != nil {
		t.Fatal(err)
	}
}

func createTx(t *testing.T, e env, user, acc, cat uuid.UUID, typ, amount, _ string) *domain.Transaction {
	t.Helper()
	return must(e.svc.CreateTransaction(t.Context(), app.CreateTransactionInput{UserID: user, AccountID: acc, CategoryID: cat,
		Type: typ, Amount: amount, Date: today(), Note: "makan siang " + amount}))(t)
}

func TestTransactions_BalanceListAndIDOR(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	alice, bob := uuid.New(), uuid.New()
	cash := newAccount(t, e, alice, "Cash", "cash", "", "100000")
	bank := newAccount(t, e, alice, "Bank", "bank", "", "0")

	t1 := createTx(t, e, alice, cash.ID(), catFood, "expense", "30000", "")
	createTx(t, e, alice, cash.ID(), catSalary, "income", "50000", "")
	createTx(t, e, alice, bank.ID(), catFood, "expense", "1000", "")
	if b := balance(t, e, alice, cash.ID()); b.Amount() != 120000 {
		t.Fatalf("cash = %v", b)
	}
	// Cash tidak boleh negatif.
	_, err := e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: alice, AccountID: cash.ID(), CategoryID: catFood,
		Type: "expense", Amount: "999999", Date: today()})
	wantErr(t, err, domain.ErrInsufficientBalance)
	// IDOR: bob tidak bisa memakai akun/transaksi alice.
	_, err = e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: bob, AccountID: cash.ID(), CategoryID: catFood,
		Type: "expense", Amount: "1", Date: today()})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = e.svc.GetTransaction(ctx, bob, t1.ID())
	wantErr(t, err, domain.ErrTransactionNotFound)
	wantErr(t, e.svc.DeleteTransaction(ctx, bob, t1.ID()), domain.ErrTransactionNotFound)

	// Pindah akun + ubah nominal: saldo kedua akun ikut terkoreksi.
	v := t1.Version()
	up := must(e.svc.UpdateTransaction(ctx, app.UpdateTransactionInput{UserID: alice, ID: t1.ID(), ExpectedVersion: &v,
		AccountID: new(bank.ID()), Amount: new("40000")}))(t)
	if up.Version() != v+1 {
		t.Fatalf("version = %d", up.Version())
	}
	if c, b := balance(t, e, alice, cash.ID()), balance(t, e, alice, bank.ID()); c.Amount() != 150000 || b.Amount() != -41000 {
		t.Fatalf("cash=%v bank=%v", c, b)
	}
	_, err = e.svc.UpdateTransaction(ctx, app.UpdateTransactionInput{UserID: alice, ID: t1.ID(), ExpectedVersion: &v, Note: new("x")})
	wantErr(t, err, domain.ErrVersionConflict)

	// Filter + keyset pagination.
	all := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: alice, Limit: 10}))(t)
	if len(all) != 3 {
		t.Fatalf("list = %d", len(all))
	}
	page := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: alice, Limit: 1}))(t)
	if len(page) != 2 {
		t.Fatalf("limit+1 = %d", len(page))
	}
	next := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: alice, Limit: 10,
		After: &domain.PageKey{Date: page[0].Date(), ID: page[0].ID()}}))(t)
	if len(next) != 2 || next[0].ID() == page[0].ID() {
		t.Fatalf("after = %d", len(next))
	}
	exp := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: alice, Limit: 10, Type: "expense",
		AccountIDs: []uuid.UUID{bank.ID()}, MinAmount: "1000", MaxAmount: "40000", Query: "MAKAN"}))(t)
	if len(exp) != 2 {
		t.Fatalf("filtered = %d", len(exp))
	}
	from, to := today(), today()
	cat := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: alice, Limit: 10, From: &from, To: &to,
		CategoryIDs: []uuid.UUID{catSalary}, IncludeChildren: true}))(t)
	if len(cat) != 1 {
		t.Fatalf("by category = %d", len(cat))
	}
	// Wildcard LIKE di-escape: "%" tidak mencocokkan semua baris.
	if got := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: alice, Limit: 10, Query: "%"}))(t); len(got) != 0 {
		t.Fatalf("like escape = %d", len(got))
	}
	if got := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: bob, Limit: 10}))(t); len(got) != 0 {
		t.Fatalf("bob sees %d", len(got))
	}

	if err := e.svc.DeleteTransaction(ctx, alice, t1.ID()); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, e, alice, bank.ID()); b.Amount() != -1000 {
		t.Fatalf("bank after delete = %v", b)
	}
	// Akun yang pernah punya transaksi tidak bisa dihapus.
	wantErr(t, e.svc.DeleteAccount(ctx, alice, cash.ID()), domain.ErrAccountHasTransactions)
	assertNoDrift(t, e, nil)
}

func TestTransfers(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	alice, bob := uuid.New(), uuid.New()
	bank := newAccount(t, e, alice, "Bank", "bank", "", "1000000")
	wallet := newAccount(t, e, alice, "GoPay", "ewallet", "", "0")
	usd := newAccount(t, e, alice, "USD", "bank", "USD", "0")

	tr := must(e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: alice, FromAccountID: bank.ID(), ToAccountID: wallet.ID(),
		Amount: "500000", Fee: "6500", Date: today(), Note: "top up"}))(t)
	if tr.FeeTransactionID() == nil {
		t.Fatal("fee tx missing")
	}
	if b, w := balance(t, e, alice, bank.ID()), balance(t, e, alice, wallet.ID()); b.Amount() != 493500 || w.Amount() != 500000 {
		t.Fatalf("bank=%v wallet=%v", b, w)
	}
	// Transaksi fee dikelola transfer.
	_, err := e.svc.UpdateTransaction(ctx, app.UpdateTransactionInput{UserID: alice, ID: *tr.FeeTransactionID(), Note: new("x")})
	wantErr(t, err, domain.ErrManagedByTransfer)

	_, err = e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: alice, FromAccountID: bank.ID(), ToAccountID: usd.ID(), Amount: "1", Date: today()})
	// Lintas currency tanpa to_amount ditolak sebagai validation error.
	if ve, ok := errors.AsType[*domain.ValidationError](err); !ok || ve.Field != "to_amount" {
		t.Fatalf("err = %v, want to_amount validation", err)
	}
	_, err = e.svc.GetTransfer(ctx, bob, tr.ID())
	wantErr(t, err, domain.ErrTransferNotFound)

	v := tr.Version()
	up := must(e.svc.UpdateTransfer(ctx, app.UpdateTransferInput{UserID: alice, ID: tr.ID(), ExpectedVersion: &v, Amount: new("200000"), Fee: new("0")}))(t)
	if up.FeeTransactionID() != nil {
		t.Fatal("fee tx should be removed")
	}
	if b, w := balance(t, e, alice, bank.ID()), balance(t, e, alice, wallet.ID()); b.Amount() != 800000 || w.Amount() != 200000 {
		t.Fatalf("bank=%v wallet=%v", b, w)
	}
	wallID := wallet.ID()
	list := must(e.svc.ListTransfers(ctx, app.ListTransfersInput{UserID: alice, AccountID: &wallID, Limit: 10}))(t)
	if len(list) != 1 {
		t.Fatalf("transfers = %d", len(list))
	}
	if got := must(e.svc.ListTransfers(ctx, app.ListTransfersInput{UserID: alice, Limit: 10,
		After: &domain.PageKey{Date: list[0].Date(), ID: list[0].ID()}}))(t); len(got) != 0 {
		t.Fatalf("after = %d", len(got))
	}
	if err := e.svc.DeleteTransfer(ctx, alice, tr.ID()); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, e, alice, bank.ID()); b.Amount() != 1000000 {
		t.Fatalf("bank after delete = %v", b)
	}
	assertNoDrift(t, e, &alice)
}

func TestReports(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	alice := uuid.New()
	bank := newAccount(t, e, alice, "Bank", "bank", "", "1000000")
	newAccount(t, e, alice, "USD", "bank", "USD", "10.50")
	wallet := newAccount(t, e, alice, "Wallet", "ewallet", "", "0")
	createTx(t, e, alice, bank.ID(), catSalary, "income", "300000", "")
	createTx(t, e, alice, bank.ID(), catFood, "expense", "75000", "")
	createTx(t, e, alice, bank.ID(), catFood, "expense", "25000", "")
	must(e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: alice, FromAccountID: bank.ID(), ToAccountID: wallet.ID(),
		Amount: "100000", Date: today()}))(t)

	s := must(e.svc.Summary(ctx, alice, ""))(t)
	var idrTotals *app.CurrencySummary
	for i := range s.Totals {
		if s.Totals[i].Currency == money.IDR {
			idrTotals = &s.Totals[i]
		}
	}
	// Transfer tidak dihitung sebagai income/expense.
	if idrTotals == nil || idrTotals.Income.Amount() != 300000 || idrTotals.Expense.Amount() != 100000 || idrTotals.Net.Amount() != 200000 {
		t.Fatalf("totals = %+v", s.Totals)
	}
	if len(s.TotalBalance) != 2 {
		t.Fatalf("balances = %+v", s.TotalBalance)
	}
	if len(s.ByCategory) != 1 || s.ByCategory[0].Total.Amount() != 100000 || s.ByCategory[0].Percent != "100.00" {
		t.Fatalf("by category = %+v", s.ByCategory)
	}
	from := today().AddDate(0, 0, -3)
	items, cur := mustCashflow(t, e, app.CashflowInput{UserID: alice, From: &from, Granularity: "day"})
	if cur != money.IDR || len(items) != 4 || items[3].Net.Amount() != 200000 {
		t.Fatalf("cashflow = %+v", items)
	}
	shares, _, err := e.svc.CategoryReport(ctx, app.CategoryReportInput{UserID: alice, Type: "income"})
	if err != nil || len(shares) != 1 || shares[0].Total.Amount() != 300000 {
		t.Fatalf("category report = %+v err %v", shares, err)
	}
	assertNoDrift(t, e, &alice)
}

func mustCashflow(t *testing.T, e env, in app.CashflowInput) ([]app.CashflowItem, money.Currency) {
	t.Helper()
	items, cur, err := e.svc.Cashflow(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return items, cur
}

func assertNoDrift(t *testing.T, e env, user *uuid.UUID) {
	t.Helper()
	if d := must(e.svc.ReconcileBalances(t.Context(), user))(t); len(d) != 0 {
		t.Fatalf("drifts = %+v", d)
	}
}

func TestBalanceDrifts_DetectsTamperedCache(t *testing.T) {
	e := setup(t)
	alice := uuid.New()
	acc := newAccount(t, e, alice, "Bank", "bank", "", "1000")
	createTx(t, e, alice, acc.ID(), catFood, "expense", "100", "")
	if _, err := e.pool.Exec(t.Context(), `UPDATE accounts SET current_balance = current_balance + 7 WHERE id = $1`, acc.ID()); err != nil {
		t.Fatal(err)
	}
	drifts := must(e.svc.ReconcileBalances(t.Context(), nil))(t)
	if len(drifts) != 1 || drifts[0].AccountID != acc.ID() || drifts[0].Cached != 907 || drifts[0].Expected != 900 {
		t.Fatalf("drifts = %+v", drifts)
	}
	other := uuid.New()
	if d := must(e.svc.ReconcileBalances(t.Context(), &other))(t); len(d) != 0 {
		t.Fatalf("scoped drifts = %+v", d)
	}
}

func TestIdempotencyStore(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	user := uuid.New()
	now := time.Now().UTC()
	rec := app.IdempotencyRecord{UserID: user, Key: "key-00000001", Method: "POST", Path: "/api/v1/transactions",
		RequestHash: []byte("hash-a"), Now: now, ExpiresAt: now.Add(time.Hour)}

	if got := must(e.idem.Claim(ctx, rec))(t); got != nil {
		t.Fatalf("first claim = %+v", got)
	}
	_, err := e.idem.Claim(ctx, rec)
	wantErr(t, err, app.ErrIdempotencyInProgress)
	other := rec
	other.RequestHash = []byte("hash-b")
	_, err = e.idem.Claim(ctx, other)
	wantErr(t, err, app.ErrIdempotencyKeyReused)

	if err := e.idem.Complete(ctx, user, rec.Key, app.StoredResponse{Status: 201, Body: []byte(`{"data":1}`)}); err != nil {
		t.Fatal(err)
	}
	got := must(e.idem.Claim(ctx, rec))(t)
	if got == nil || got.Status != 201 || string(got.Body) != `{"data":1}` {
		t.Fatalf("replay = %+v", got)
	}
	// Key sama milik user lain adalah klaim baru (scoped per user).
	bobRec := rec
	bobRec.UserID = uuid.New()
	if got := must(e.idem.Claim(ctx, bobRec))(t); got != nil {
		t.Fatal("key leaked across users")
	}
	if err := e.idem.Complete(ctx, uuid.New(), "missing-key", app.StoredResponse{Status: 200}); err == nil {
		t.Fatal("complete unknown key should fail")
	}
	// Key kedaluwarsa bisa diklaim ulang dan dibersihkan purge.
	later := rec
	later.Now = now.Add(2 * time.Hour)
	later.ExpiresAt = later.Now.Add(time.Hour)
	later.RequestHash = []byte("hash-c")
	if got := must(e.idem.Claim(ctx, later))(t); got != nil {
		t.Fatal("expired key should be reclaimable")
	}
	n := must(e.idem.DeleteExpired(ctx, now.Add(10*time.Hour)))(t)
	if n != 2 {
		t.Fatalf("purged = %d", n)
	}
}

func TestIdempotent_ServiceReplay(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	user := uuid.New()
	acc := newAccount(t, e, user, "Cash", "cash", "", "1000")
	req := app.IdempotentRequest{UserID: user, Key: "key-replay-01", Method: "POST", Path: "/api/v1/transactions", Body: []byte(`{"a":1}`)}
	var calls atomic.Int32
	run := func(ctx context.Context) (app.StoredResponse, error) {
		calls.Add(1)
		if _, err := e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: user, AccountID: acc.ID(), CategoryID: catFood,
			Type: "expense", Amount: "100", Date: today()}); err != nil {
			return app.StoredResponse{}, err
		}
		return app.StoredResponse{Status: 201, Body: []byte(`{"ok":true}`)}, nil
	}
	_, replayed := mustIdem(t, e, req, run)
	if replayed {
		t.Fatal("first call replayed")
	}
	resp, replayed := mustIdem(t, e, req, run)
	if !replayed || resp.Status != 201 || calls.Load() != 1 {
		t.Fatalf("replay=%v status=%d calls=%d", replayed, resp.Status, calls.Load())
	}
	if b := balance(t, e, user, acc.ID()); b.Amount() != 900 {
		t.Fatalf("balance = %v (double charge?)", b)
	}
	// Use case gagal -> klaim ikut di-rollback, key bisa dipakai ulang.
	failReq := req
	failReq.Key = "key-fail-0001"
	_, _, err := e.svc.Idempotent(ctx, failReq, func(context.Context) (app.StoredResponse, error) {
		return app.StoredResponse{}, domain.ErrInsufficientBalance
	})
	wantErr(t, err, domain.ErrInsufficientBalance)
	if _, replayed := mustIdem(t, e, failReq, run); replayed {
		t.Fatal("failed request must not be stored")
	}
}

func mustIdem(t *testing.T, e env, req app.IdempotentRequest, fn func(context.Context) (app.StoredResponse, error)) (app.StoredResponse, bool) {
	t.Helper()
	resp, replayed, err := e.svc.Idempotent(t.Context(), req, fn)
	if err != nil {
		t.Fatal(err)
	}
	return resp, replayed
}

// TestConcurrency_NoLostUpdates: banyak goroutine menulis ke akun yang sama
// (transaksi + transfer dua arah). FOR UPDATE berurutan mencegah lost update
// dan deadlock; saldo akhir harus tepat dan rekonsiliasi bersih.
func TestConcurrency_NoLostUpdates(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	user := uuid.New()
	a := newAccount(t, e, user, "A", "cash", "", "1000000")
	b := newAccount(t, e, user, "B", "cash", "", "1000000")

	const workers, perWorker = 8, 10
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := range workers {
		wg.Go(func() {
			for i := range perWorker {
				var err error
				switch (w + i) % 3 {
				case 0:
					_, err = e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: user, AccountID: a.ID(), CategoryID: catFood,
						Type: "expense", Amount: "100", Date: today()})
				case 1:
					_, err = e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: user, FromAccountID: a.ID(), ToAccountID: b.ID(),
						Amount: "50", Fee: "1", Date: today()})
				default:
					_, err = e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: user, FromAccountID: b.ID(), ToAccountID: a.ID(),
						Amount: "30", Date: today()})
				}
				if err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent op failed: %v", err)
	}
	// Hitung ekspektasi dari jumlah tiap jenis operasi.
	var nTx, nAB, nBA int64
	for w := range workers {
		for i := range perWorker {
			switch (w + i) % 3 {
			case 0:
				nTx++
			case 1:
				nAB++
			default:
				nBA++
			}
		}
	}
	wantA := 1000000 - nTx*100 - nAB*51 + nBA*30
	wantB := 1000000 + nAB*50 - nBA*30
	if got := balance(t, e, user, a.ID()); got.Amount() != wantA {
		t.Errorf("A = %d, want %d", got.Amount(), wantA)
	}
	if got := balance(t, e, user, b.ID()); got.Amount() != wantB {
		t.Errorf("B = %d, want %d", got.Amount(), wantB)
	}
	// Satu operasi = satu kenaikan version (fee digabung via BalanceChanges).
	accA := must(e.accs.Get(ctx, user, a.ID()))(t)
	if want := int(1 + nTx + nAB + nBA); accA.Version() != want {
		t.Errorf("A version = %d, want %d", accA.Version(), want)
	}
	assertNoDrift(t, e, &user)
}

// TestConcurrency_IdempotentCreate: request paralel dengan key sama hanya
// menghasilkan satu transaksi; sisanya replay atau IN_PROGRESS.
func TestConcurrency_IdempotentCreate(t *testing.T) {
	e := setup(t)
	user := uuid.New()
	acc := newAccount(t, e, user, "Cash", "cash", "", "1000")
	req := app.IdempotentRequest{UserID: user, Key: "key-parallel-1", Method: "POST", Path: "/api/v1/transactions", Body: []byte(`{}`)}
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_, _, err := e.svc.Idempotent(t.Context(), req, func(ctx context.Context) (app.StoredResponse, error) {
				if _, err := e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: user, AccountID: acc.ID(), CategoryID: catFood,
					Type: "expense", Amount: "100", Date: today()}); err != nil {
					return app.StoredResponse{}, err
				}
				created.Add(1)
				return app.StoredResponse{Status: 201, Body: []byte(`{}`)}, nil
			})
			if err != nil && !errors.Is(err, app.ErrIdempotencyInProgress) {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("created = %d", created.Load())
	}
	if b := balance(t, e, user, acc.ID()); b.Amount() != 900 {
		t.Fatalf("balance = %v", b)
	}
}

// TestRepositories_StaleVersionAndMissing menguji jalur optimistic lock repo
// secara langsung (tanpa service): version basi = conflict, baris hilang = not found.
func TestRepositories_StaleVersionAndMissing(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	user := uuid.New()
	txRepo := postgres.NewTransactionRepository(e.pool)
	trRepo := postgres.NewTransferRepository(e.pool)
	catRepo := postgres.NewCategoryRepository(e.pool)

	acc := newAccount(t, e, user, "Cash", "cash", "", "1000")
	other := newAccount(t, e, user, "Bank", "bank", "", "0")
	stale := must(e.accs.Get(ctx, user, acc.ID()))(t)
	createTx(t, e, user, acc.ID(), catFood, "expense", "10", "")
	wantErr(t, e.accs.Update(ctx, stale), domain.ErrVersionConflict)

	// CHECK accounts_non_negative di DB tetap menjaga invariant walau domain dilewati.
	fresh := must(e.accs.Get(ctx, user, acc.ID()))(t)
	tampered := domain.RehydrateAccount(domain.AccountState{ID: fresh.ID(), UserID: user, Name: fresh.Name(), Type: fresh.Type(),
		InitialBalance: fresh.InitialBalance(), Balance: money.New(-1, money.IDR), Version: fresh.Version(),
		CreatedAt: fresh.CreatedAt(), UpdatedAt: fresh.UpdatedAt()})
	wantErr(t, e.accs.Update(ctx, tampered), domain.ErrInsufficientBalance)
	ghost := domain.RehydrateAccount(domain.AccountState{ID: uuid.New(), UserID: user, Name: "x", Type: domain.AccountCash,
		InitialBalance: money.Zero(money.IDR), Balance: money.Zero(money.IDR), Version: 1})
	wantErr(t, e.accs.Update(ctx, ghost), domain.ErrAccountNotFound)

	tx := createTx(t, e, user, acc.ID(), catFood, "expense", "20", "")
	staleTx := must(txRepo.Get(ctx, user, tx.ID()))(t)
	must(e.svc.UpdateTransaction(ctx, app.UpdateTransactionInput{UserID: user, ID: tx.ID(), Note: new("baru")}))(t)
	wantErr(t, txRepo.Update(ctx, staleTx), domain.ErrVersionConflict)
	wantErr(t, txRepo.SoftDelete(ctx, staleTx, time.Now()), domain.ErrVersionConflict)
	ghostTx := domain.RehydrateTransaction(domain.TransactionState{ID: uuid.New(), UserID: user, AccountID: acc.ID(), CategoryID: catFood,
		Type: domain.TxExpense, Amount: money.New(1, money.IDR), Date: today(), Source: domain.SourceManual, Version: 1})
	wantErr(t, txRepo.Update(ctx, ghostTx), domain.ErrTransactionNotFound)

	tr := must(e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: user, FromAccountID: acc.ID(), ToAccountID: other.ID(),
		Amount: "5", Date: today()}))(t)
	staleTr := must(trRepo.Get(ctx, user, tr.ID()))(t)
	must(e.svc.UpdateTransfer(ctx, app.UpdateTransferInput{UserID: user, ID: tr.ID(), Note: new("x")}))(t)
	wantErr(t, trRepo.Update(ctx, staleTr), domain.ErrVersionConflict)
	wantErr(t, trRepo.SoftDelete(ctx, staleTr, time.Now()), domain.ErrVersionConflict)
	if err := e.svc.DeleteTransfer(ctx, user, tr.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, trRepo.Update(ctx, staleTr), domain.ErrTransferNotFound)

	// Category update: rename + duplikat.
	c1 := must(e.svc.CreateCategory(ctx, app.CreateCategoryInput{UserID: user, Type: "expense", Name: "A"}))(t)
	must(e.svc.CreateCategory(ctx, app.CreateCategoryInput{UserID: user, Type: "expense", Name: "B"}))(t)
	up := must(e.svc.UpdateCategory(ctx, app.UpdateCategoryInput{UserID: user, ID: c1.ID(), Name: new("C"), Color: new("#00FF00")}))(t)
	if got := must(catRepo.Get(ctx, user, c1.ID()))(t); got.Name() != "C" || got.Color() != up.Color() {
		t.Fatalf("category = %s %s", got.Name(), got.Color())
	}
	_, err := e.svc.UpdateCategory(ctx, app.UpdateCategoryInput{UserID: user, ID: c1.ID(), Name: new("b")})
	wantErr(t, err, domain.ErrDuplicateName)
}
