package app

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

var errBoom = errors.New("boom")

// memDB adalah penyimpanan in-memory bersama untuk semua fake repository.
// fakeTx men-snapshot state dan mengembalikannya bila fn gagal (rollback).
type memDB struct {
	mu        sync.Mutex
	settings  map[uuid.UUID]domain.UserSettings
	accounts  map[uuid.UUID]domain.Account
	cats      map[uuid.UUID]domain.Category
	txs       map[uuid.UUID]domain.Transaction
	trs       map[uuid.UUID]domain.Transfer
	deleted   map[uuid.UUID]bool
	idem      map[string]idemRow
	budgets   map[uuid.UUID]domain.Budget
	tags      map[uuid.UUID]domain.Tag
	txTags    map[uuid.UUID][]uuid.UUID
	rules     map[uuid.UUID]domain.RecurringRule
	p2        p2State
	failOn    map[string]error // nama operasi -> error yang dipaksa
	drifts    []domain.BalanceDrift
	reportErr error
}

type idemRow struct {
	hash []byte
	resp *StoredResponse
}

func newMemDB() *memDB {
	return &memDB{
		settings: map[uuid.UUID]domain.UserSettings{}, accounts: map[uuid.UUID]domain.Account{},
		cats: map[uuid.UUID]domain.Category{}, txs: map[uuid.UUID]domain.Transaction{},
		trs: map[uuid.UUID]domain.Transfer{}, deleted: map[uuid.UUID]bool{},
		idem: map[string]idemRow{}, failOn: map[string]error{},
		budgets: map[uuid.UUID]domain.Budget{}, tags: map[uuid.UUID]domain.Tag{},
		txTags: map[uuid.UUID][]uuid.UUID{}, rules: map[uuid.UUID]domain.RecurringRule{},
		p2: newP2State(),
	}
}

func (m *memDB) fail(op string) error { return m.failOn[op] }

type snapshot struct {
	settings map[uuid.UUID]domain.UserSettings
	accounts map[uuid.UUID]domain.Account
	cats     map[uuid.UUID]domain.Category
	txs      map[uuid.UUID]domain.Transaction
	trs      map[uuid.UUID]domain.Transfer
	deleted  map[uuid.UUID]bool
	idem     map[string]idemRow
	budgets  map[uuid.UUID]domain.Budget
	tags     map[uuid.UUID]domain.Tag
	txTags   map[uuid.UUID][]uuid.UUID
	rules    map[uuid.UUID]domain.RecurringRule
	p2       p2State
}

type fakeTx struct {
	db    *memDB
	calls int
}

type inTxKey struct{}

func (f *fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(inTxKey{}) != nil {
		return fn(ctx)
	}
	f.calls++
	m := f.db
	m.mu.Lock()
	snap := snapshot{maps.Clone(m.settings), maps.Clone(m.accounts), maps.Clone(m.cats),
		maps.Clone(m.txs), maps.Clone(m.trs), maps.Clone(m.deleted), maps.Clone(m.idem),
		maps.Clone(m.budgets), maps.Clone(m.tags), maps.Clone(m.txTags), maps.Clone(m.rules), m.p2.clone()}
	m.mu.Unlock()
	err := fn(context.WithValue(ctx, inTxKey{}, true))
	if err != nil {
		m.mu.Lock()
		m.settings, m.accounts, m.cats, m.txs, m.trs, m.deleted, m.idem =
			snap.settings, snap.accounts, snap.cats, snap.txs, snap.trs, snap.deleted, snap.idem
		m.budgets, m.tags, m.txTags, m.rules, m.p2 = snap.budgets, snap.tags, snap.txTags, snap.rules, snap.p2
		m.mu.Unlock()
	}
	return err
}

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

type seqIDs struct{ err error }

func (g *seqIDs) NewID() (uuid.UUID, error) {
	if g.err != nil {
		return uuid.Nil, g.err
	}
	return uuid.NewV7()
}

// ---- settings ----

type fakeSettings struct{ db *memDB }

func (r fakeSettings) GetOrCreate(_ context.Context, def *domain.UserSettings) (*domain.UserSettings, error) {
	if err := r.db.fail("settings.get"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if s, ok := r.db.settings[def.UserID()]; ok {
		return &s, nil
	}
	r.db.settings[def.UserID()] = *def
	return def, nil
}

func (r fakeSettings) Upsert(_ context.Context, s *domain.UserSettings) (*domain.UserSettings, error) {
	if err := r.db.fail("settings.upsert"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.settings[s.UserID()] = *s
	return s, nil
}

func (r fakeSettings) ListCurrencies(context.Context) ([]domain.CurrencyInfo, error) {
	if err := r.db.fail("currencies"); err != nil {
		return nil, err
	}
	return []domain.CurrencyInfo{{Code: "IDR", Name: "Rupiah", MinorUnit: 0, Symbol: "Rp"}}, nil
}

// ---- accounts ----

type fakeAccounts struct{ db *memDB }

func (r fakeAccounts) Create(_ context.Context, a *domain.Account) error {
	if err := r.db.fail("accounts.create"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for id, x := range r.db.accounts { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if !r.db.deleted[id] && x.UserID() == a.UserID() && x.Name() == a.Name() {
			return domain.ErrDuplicateName
		}
	}
	r.db.accounts[a.ID()] = *a
	return nil
}

func (r fakeAccounts) get(userID, id uuid.UUID) (*domain.Account, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	a, ok := r.db.accounts[id]
	if !ok || r.db.deleted[id] || a.UserID() != userID {
		return nil, domain.ErrAccountNotFound
	}
	return &a, nil
}

func (r fakeAccounts) Get(_ context.Context, userID, id uuid.UUID) (*domain.Account, error) {
	return r.get(userID, id)
}

func (r fakeAccounts) GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error) {
	if ctx.Value(inTxKey{}) == nil {
		panic("GetForUpdate di luar tx")
	}
	return r.get(userID, id)
}

func (r fakeAccounts) List(_ context.Context, userID uuid.UUID, includeArchived bool) ([]*domain.Account, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.Account{}
	for id, a := range r.db.accounts { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if r.db.deleted[id] || a.UserID() != userID || (a.IsArchived() && !includeArchived) {
			continue
		}
		out = append(out, &a)
	}
	return out, nil
}

func (r fakeAccounts) Update(_ context.Context, a *domain.Account) error {
	if err := r.db.fail("accounts.update"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	cur, ok := r.db.accounts[a.ID()]
	if !ok || r.db.deleted[a.ID()] {
		return domain.ErrAccountNotFound
	}
	if cur.Version() != a.Version() {
		return domain.ErrVersionConflict
	}
	a.SyncVersion(a.Version() + 1)
	r.db.accounts[a.ID()] = *a
	return nil
}

func (r fakeAccounts) HasActivity(_ context.Context, _, id uuid.UUID) (bool, error) {
	if err := r.db.fail("accounts.activity"); err != nil {
		return false, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for tid, t := range r.db.txs { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if !r.db.deleted[tid] && t.AccountID() == id {
			return true, nil
		}
	}
	for tid, t := range r.db.trs { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if !r.db.deleted[tid] && (t.FromAccountID() == id || t.ToAccountID() == id) {
			return true, nil
		}
	}
	return false, nil
}

func (r fakeAccounts) SoftDelete(_ context.Context, _, id uuid.UUID, _ time.Time) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.deleted[id] = true
	return nil
}

// ---- categories ----

type fakeCategories struct{ db *memDB }

func (r fakeCategories) Get(_ context.Context, userID, id uuid.UUID) (*domain.Category, error) {
	if err := r.db.fail("categories.get"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	c, ok := r.db.cats[id]
	if !ok || r.db.deleted[id] || !c.VisibleTo(userID) {
		return nil, domain.ErrCategoryNotFound
	}
	return &c, nil
}

func (r fakeCategories) List(_ context.Context, userID uuid.UUID, typ *domain.TxType) ([]*domain.Category, error) {
	if err := r.db.fail("categories.list"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.Category{}
	for id, c := range r.db.cats { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if r.db.deleted[id] || !c.VisibleTo(userID) || (typ != nil && c.Type() != *typ) {
			continue
		}
		out = append(out, &c)
	}
	return out, nil
}

func (r fakeCategories) Create(_ context.Context, c *domain.Category) error {
	if err := r.db.fail("categories.create"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.cats[c.ID()] = *c
	return nil
}

func (r fakeCategories) Update(_ context.Context, c *domain.Category) error {
	if err := r.db.fail("categories.update"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.cats[c.ID()] = *c
	return nil
}

func (r fakeCategories) HasChildren(_ context.Context, _, id uuid.UUID) (bool, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for cid, c := range r.db.cats { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if !r.db.deleted[cid] && c.ParentID() != nil && *c.ParentID() == id {
			return true, nil
		}
	}
	return false, nil
}

func (r fakeCategories) IsInUse(_ context.Context, userID, id uuid.UUID) (bool, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for tid, t := range r.db.txs { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if !r.db.deleted[tid] && t.UserID() == userID && t.CategoryID() == id {
			return true, nil
		}
	}
	return false, nil
}

func (r fakeCategories) Reassign(_ context.Context, userID, from, to uuid.UUID, _ time.Time) error {
	if err := r.db.fail("categories.reassign"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for tid, t := range r.db.txs { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if t.UserID() == userID && t.CategoryID() == from {
			s := domain.TransactionState{ID: t.ID(), UserID: t.UserID(), AccountID: t.AccountID(), CategoryID: to,
				Type: t.Type(), Amount: t.Amount(), Date: t.Date(), Note: t.Note(), Source: t.Source(),
				Version: t.Version() + 1, CreatedAt: t.CreatedAt(), UpdatedAt: t.UpdatedAt()}
			r.db.txs[tid] = *domain.RehydrateTransaction(s)
		}
	}
	return nil
}

func (r fakeCategories) SoftDelete(_ context.Context, _, id uuid.UUID, _ time.Time) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.deleted[id] = true
	return nil
}

// ---- transactions ----

type fakeTransactions struct{ db *memDB }

func (r fakeTransactions) Create(_ context.Context, t *domain.Transaction) error {
	if err := r.db.fail("transactions.create"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.txs[t.ID()] = *t
	return nil
}

func (r fakeTransactions) Get(_ context.Context, userID, id uuid.UUID) (*domain.Transaction, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	t, ok := r.db.txs[id]
	if !ok || r.db.deleted[id] || t.UserID() != userID {
		return nil, domain.ErrTransactionNotFound
	}
	return &t, nil
}

func (r fakeTransactions) Update(_ context.Context, t *domain.Transaction) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	cur := r.db.txs[t.ID()]
	if cur.Version() != t.Version() {
		return domain.ErrVersionConflict
	}
	t.SyncVersion(t.Version() + 1)
	r.db.txs[t.ID()] = *t
	return nil
}

func (r fakeTransactions) SoftDelete(_ context.Context, t *domain.Transaction, _ time.Time) error {
	if err := r.db.fail("transactions.delete"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.deleted[t.ID()] = true
	return nil
}

func (r fakeTransactions) List(_ context.Context, userID uuid.UUID, f domain.TransactionFilter) ([]*domain.Transaction, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.Transaction{}
	for id, t := range r.db.txs { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if r.db.deleted[id] || t.UserID() != userID {
			continue
		}
		if f.Type != nil && t.Type() != *f.Type {
			continue
		}
		if f.MinAmount != nil && t.Amount().Amount() < *f.MinAmount {
			continue
		}
		if f.TagName != "" && !r.db.hasTagName(id, f.TagName) {
			continue
		}
		out = append(out, &t)
	}
	return out, nil
}

// ---- transfers ----

type fakeTransfers struct{ db *memDB }

func (r fakeTransfers) Create(_ context.Context, t *domain.Transfer) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.trs[t.ID()] = *t
	return nil
}

func (r fakeTransfers) Get(_ context.Context, userID, id uuid.UUID) (*domain.Transfer, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	t, ok := r.db.trs[id]
	if !ok || r.db.deleted[id] || t.UserID() != userID {
		return nil, domain.ErrTransferNotFound
	}
	return &t, nil
}

func (r fakeTransfers) Update(_ context.Context, t *domain.Transfer) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	cur := r.db.trs[t.ID()]
	if cur.Version() != t.Version() {
		return domain.ErrVersionConflict
	}
	t.SyncVersion(t.Version() + 1)
	r.db.trs[t.ID()] = *t
	return nil
}

func (r fakeTransfers) SoftDelete(_ context.Context, t *domain.Transfer, _ time.Time) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.deleted[t.ID()] = true
	return nil
}

func (r fakeTransfers) List(_ context.Context, userID uuid.UUID, _ domain.TransferFilter) ([]*domain.Transfer, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.Transfer{}
	for id, t := range r.db.trs { //nolint:gocritic // fake in-memory: copy nilai map disengaja
		if !r.db.deleted[id] && t.UserID() == userID {
			out = append(out, &t)
		}
	}
	return out, nil
}

// ---- reports (stub dengan data tetap) ----

type fakeReports struct {
	db       *memDB
	balances []money.Money
	ie       []domain.IncomeExpense
	cats     []domain.CategoryTotal
	points   []domain.CashflowPoint
	daily    []domain.DailyTotal
	lastFrom time.Time
	lastTo   time.Time
}

func (r *fakeReports) TotalBalances(context.Context, uuid.UUID) ([]money.Money, error) {
	return r.balances, r.db.fail("reports.balances")
}

func (r *fakeReports) IncomeExpense(context.Context, uuid.UUID, time.Time, time.Time) ([]domain.IncomeExpense, error) {
	return r.ie, r.db.fail("reports.ie")
}

func (r *fakeReports) CategoryBreakdown(_ context.Context, _ uuid.UUID, _ domain.TxType, _ money.Currency, from, to time.Time) ([]domain.CategoryTotal, error) {
	r.lastFrom, r.lastTo = from, to
	return r.cats, r.db.fail("reports.cats")
}

func (r *fakeReports) Cashflow(_ context.Context, _ uuid.UUID, _ money.Currency, _ domain.Granularity, from, to time.Time) ([]domain.CashflowPoint, error) {
	r.lastFrom, r.lastTo = from, to
	return r.points, r.db.fail("reports.cashflow")
}

func (r *fakeReports) DailyTotals(_ context.Context, _ uuid.UUID, from, to time.Time) ([]domain.DailyTotal, error) {
	r.lastFrom, r.lastTo = from, to
	return r.daily, r.db.fail("reports.daily")
}

func (r *fakeReports) BalanceDrifts(context.Context, *uuid.UUID) ([]domain.BalanceDrift, error) {
	return r.db.drifts, r.db.reportErr
}

// ---- idempotency ----

type fakeIdem struct {
	db      *memDB
	purged  int64
	purgeAt time.Time
}

func (s *fakeIdem) Claim(_ context.Context, rec IdempotencyRecord) (*StoredResponse, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	k := rec.UserID.String() + "|" + rec.Key
	row, ok := s.db.idem[k]
	if !ok {
		s.db.idem[k] = idemRow{hash: rec.RequestHash}
		return nil, nil
	}
	if !MatchHash(row.hash, rec.RequestHash) {
		return nil, ErrIdempotencyKeyReused
	}
	if row.resp == nil {
		return nil, ErrIdempotencyInProgress
	}
	return row.resp, nil
}

func (s *fakeIdem) Complete(_ context.Context, userID uuid.UUID, key string, resp StoredResponse) error {
	if err := s.db.fail("idem.complete"); err != nil {
		return err
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	k := userID.String() + "|" + key
	row := s.db.idem[k]
	row.resp = &resp
	s.db.idem[k] = row
	return nil
}

func (s *fakeIdem) DeleteExpired(_ context.Context, now time.Time) (int64, error) {
	s.purgeAt = now
	return s.purged, s.db.fail("idem.purge")
}

// ---- fixture ----

var (
	idr     = money.MustCurrency("IDR")
	usd     = money.MustCurrency("USD")
	testNow = time.Date(2026, 3, 15, 3, 0, 0, 0, time.UTC)
	today   = time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
)

type fixture struct {
	svc     *Service
	db      *memDB
	tx      *fakeTx
	reports *fakeReports
	idem    *fakeIdem
	ids     *seqIDs
	alerts  *recordingNotifier
	notices *recordingBills
	users   fakeUsers
	user    uuid.UUID
	expense *domain.Category
	income  *domain.Category
	fee     *domain.Category
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := newMemDB()
	f := &fixture{db: db, tx: &fakeTx{db: db}, reports: &fakeReports{db: db}, idem: &fakeIdem{db: db},
		ids: &seqIDs{}, alerts: &recordingNotifier{}, notices: &recordingBills{}, users: fakeUsers{}, user: uuid.New()}
	f.svc = NewService(Deps{
		Tx: f.tx, Clock: &fixedClock{t: testNow}, IDs: f.ids,
		Settings: fakeSettings{db}, Accounts: fakeAccounts{db}, Categories: fakeCategories{db},
		Transactions: fakeTransactions{db}, Transfers: fakeTransfers{db}, Reports: f.reports,
		Budgets: fakeBudgets{db}, Tags: fakeTags{db}, Recurring: fakeRecurring{db}, BudgetAlerts: f.alerts,
		Idempotency: f.idem, Defaults: Defaults{Currency: "IDR", Timezone: "Asia/Jakarta"},
		Rates: fakeRates{db}, Goals: fakeGoals{db}, Debts: fakeDebts{db}, Bills: fakeBills{db},
		Members: fakeMembers{db}, Audit: fakeAudit{db}, BillNotices: f.notices, Users: f.users,
	})
	f.expense = f.sysCat(uuid.New(), domain.TxExpense, "Makan")
	f.income = f.sysCat(uuid.New(), domain.TxIncome, "Gaji")
	f.fee = f.sysCat(domain.FeeCategoryID, domain.TxExpense, "Biaya Admin")
	return f
}

func (f *fixture) sysCat(id uuid.UUID, typ domain.TxType, name string) *domain.Category {
	c := domain.RehydrateCategory(domain.CategoryState{ID: id, Type: typ, Name: name})
	f.db.cats[id] = *c
	return c
}

func (f *fixture) account(t *testing.T, typ, initial string) *domain.Account {
	t.Helper()
	a, err := f.svc.CreateAccount(context.Background(), CreateAccountInput{
		UserID: f.user, Name: "Akun " + uuid.NewString()[:8], Type: typ, InitialBalance: initial})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return a
}

func (f *fixture) balance(t *testing.T, id uuid.UUID) int64 {
	t.Helper()
	a, err := f.svc.GetAccount(context.Background(), f.user, id)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	return a.Balance().Amount()
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if wv, ok := errors.AsType[*domain.ValidationError](want); ok {
		if ve, ok := errors.AsType[*domain.ValidationError](got); !ok || ve.Field != wv.Field {
			t.Fatalf("err = %v, want validation error on %q", got, wv.Field)
		}
		return
	}
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func ptr[T any](v T) *T { return &v }
