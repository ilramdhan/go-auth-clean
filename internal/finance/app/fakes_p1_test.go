package app

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

// ---- transactions (P1) ----

func (m *memDB) hasTagName(txID uuid.UUID, name string) bool {
	for _, id := range m.txTags[txID] {
		if t, ok := m.tags[id]; ok && strings.EqualFold(t.Name(), name) {
			return true
		}
	}
	return false
}

func (r fakeTransactions) CreateOccurrence(_ context.Context, t *domain.Transaction) (bool, error) {
	if err := r.db.fail("transactions.occurrence"); err != nil {
		return false, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for _, x := range r.db.txs { //nolint:gocritic // fake in-memory
		if x.RecurringRuleID() != nil && *x.RecurringRuleID() == *t.RecurringRuleID() &&
			x.OccurrenceDate().Equal(*t.OccurrenceDate()) {
			return false, nil
		}
	}
	r.db.txs[t.ID()] = *t
	return true, nil
}

func (r fakeTransactions) ExistingImportHashes(_ context.Context, userID uuid.UUID, hashes [][]byte) (map[string]bool, error) {
	if err := r.db.fail("transactions.hashes"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	want := map[string]bool{}
	for _, h := range hashes {
		want[hex.EncodeToString(h)] = true
	}
	out := map[string]bool{}
	for _, t := range r.db.txs { //nolint:gocritic // fake in-memory
		if k := hex.EncodeToString(t.ImportHash()); t.UserID() == userID && want[k] {
			out[k] = true
		}
	}
	return out, nil
}

func (r fakeTransactions) Export(ctx context.Context, userID uuid.UUID, f domain.TransactionFilter, fn func(domain.ExportRow) error) error {
	if err := r.db.fail("transactions.export"); err != nil {
		return err
	}
	txs, err := r.List(ctx, userID, f)
	if err != nil {
		return err
	}
	slices.SortFunc(txs, func(a, b *domain.Transaction) int {
		if c := a.Date().Compare(b.Date()); c != 0 {
			return c
		}
		return strings.Compare(a.ID().String(), b.ID().String())
	})
	for _, t := range txs {
		r.db.mu.Lock()
		acc, cat := r.db.accounts[t.AccountID()], r.db.cats[t.CategoryID()]
		var tags []string
		for _, id := range r.db.txTags[t.ID()] {
			tg := r.db.tags[id]
			tags = append(tags, tg.Name())
		}
		r.db.mu.Unlock()
		if err := fn(domain.ExportRow{Transaction: t, AccountName: acc.Name(), CategoryName: cat.Name(), Tags: tags}); err != nil {
			return err
		}
	}
	return nil
}

// ---- budgets ----

type fakeBudgets struct{ db *memDB }

func (r fakeBudgets) Create(_ context.Context, b *domain.Budget) error {
	if err := r.db.fail("budgets.create"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for id, x := range r.db.budgets { //nolint:gocritic // fake in-memory
		if !r.db.deleted[id] && x.UserID() == b.UserID() && x.CategoryID() == b.CategoryID() && x.Month().Equal(b.Month()) {
			return domain.ErrBudgetExists
		}
	}
	r.db.budgets[b.ID()] = *b
	return nil
}

func (r fakeBudgets) Get(_ context.Context, userID, id uuid.UUID) (*domain.Budget, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	b, ok := r.db.budgets[id]
	if !ok || r.db.deleted[id] || b.UserID() != userID {
		return nil, domain.ErrBudgetNotFound
	}
	return &b, nil
}

func (r fakeBudgets) Update(_ context.Context, b *domain.Budget) error {
	if err := r.db.fail("budgets.update"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if cur := r.db.budgets[b.ID()]; cur.Version() != b.Version() {
		return domain.ErrVersionConflict
	}
	b.SyncVersion(b.Version() + 1)
	r.db.budgets[b.ID()] = *b
	return nil
}

func (r fakeBudgets) SoftDelete(_ context.Context, b *domain.Budget, _ time.Time) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.deleted[b.ID()] = true
	return nil
}

func (r fakeBudgets) spent(b *domain.Budget) int64 {
	var sum int64
	for id, t := range r.db.txs { //nolint:gocritic // fake in-memory
		if r.db.deleted[id] || t.UserID() != b.UserID() || t.Type() != domain.TxExpense ||
			t.Amount().Currency() != b.Amount().Currency() || t.Date().Before(b.Month()) || !t.Date().Before(b.MonthEnd()) {
			continue
		}
		cat := r.db.cats[t.CategoryID()]
		if t.CategoryID() == b.CategoryID() || (cat.ParentID() != nil && *cat.ParentID() == b.CategoryID()) {
			sum += t.Amount().Amount()
		}
	}
	return sum
}

func (r fakeBudgets) ListWithSpent(_ context.Context, userID uuid.UUID, month time.Time) ([]domain.BudgetSpent, error) {
	if err := r.db.fail("budgets.list"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	m := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := []domain.BudgetSpent{}
	for id, b := range r.db.budgets { //nolint:gocritic // fake in-memory
		if r.db.deleted[id] || b.UserID() != userID || !b.Month().Equal(m) {
			continue
		}
		out = append(out, domain.BudgetSpent{Budget: &b, Spent: r.spent(&b)})
	}
	slices.SortFunc(out, func(a, b domain.BudgetSpent) int { return a.Budget.CreatedAt().Compare(b.Budget.CreatedAt()) })
	return out, nil
}

func (r fakeBudgets) Spent(_ context.Context, b *domain.Budget) (int64, error) {
	if err := r.db.fail("budgets.spent"); err != nil {
		return 0, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	return r.spent(b), nil
}

// ---- tags ----

type fakeTags struct{ db *memDB }

func (r fakeTags) dup(t *domain.Tag) bool {
	for id, x := range r.db.tags { //nolint:gocritic // fake in-memory
		if id != t.ID() && x.UserID() == t.UserID() && strings.EqualFold(x.Name(), t.Name()) {
			return true
		}
	}
	return false
}

func (r fakeTags) Create(_ context.Context, t *domain.Tag) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if r.dup(t) {
		return domain.ErrDuplicateName
	}
	r.db.tags[t.ID()] = *t
	return nil
}

func (r fakeTags) Get(_ context.Context, userID, id uuid.UUID) (*domain.Tag, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	t, ok := r.db.tags[id]
	if !ok || t.UserID() != userID {
		return nil, domain.ErrTagNotFound
	}
	return &t, nil
}

func (r fakeTags) List(_ context.Context, userID uuid.UUID) ([]*domain.Tag, error) {
	if err := r.db.fail("tags.list"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.Tag{}
	for _, t := range r.db.tags { //nolint:gocritic // fake in-memory
		if t.UserID() == userID {
			out = append(out, &t)
		}
	}
	slices.SortFunc(out, func(a, b *domain.Tag) int { return strings.Compare(a.Name(), b.Name()) })
	return out, nil
}

func (r fakeTags) Update(_ context.Context, t *domain.Tag) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if r.dup(t) {
		return domain.ErrDuplicateName
	}
	r.db.tags[t.ID()] = *t
	return nil
}

func (r fakeTags) Delete(_ context.Context, userID, id uuid.UUID) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if t, ok := r.db.tags[id]; !ok || t.UserID() != userID {
		return domain.ErrTagNotFound
	}
	delete(r.db.tags, id)
	for tx, ids := range r.db.txTags {
		r.db.txTags[tx] = slices.DeleteFunc(slices.Clone(ids), func(x uuid.UUID) bool { return x == id })
	}
	return nil
}

func (r fakeTags) GetMany(_ context.Context, userID uuid.UUID, ids []uuid.UUID) ([]*domain.Tag, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.Tag{}
	for _, id := range ids {
		if t, ok := r.db.tags[id]; ok && t.UserID() == userID {
			out = append(out, &t)
		}
	}
	return out, nil
}

func (r fakeTags) SetForTransaction(_ context.Context, txID uuid.UUID, tagIDs []uuid.UUID) error {
	if err := r.db.fail("tags.set"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.txTags[txID] = slices.Clone(tagIDs)
	return nil
}

func (r fakeTags) ListForTransactions(_ context.Context, userID uuid.UUID, txIDs []uuid.UUID) (map[uuid.UUID][]*domain.Tag, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := map[uuid.UUID][]*domain.Tag{}
	for _, tx := range txIDs {
		for _, id := range r.db.txTags[tx] {
			if t, ok := r.db.tags[id]; ok && t.UserID() == userID {
				out[tx] = append(out[tx], &t)
			}
		}
	}
	return out, nil
}

// ---- recurring ----

type fakeRecurring struct{ db *memDB }

func (r fakeRecurring) Create(_ context.Context, x *domain.RecurringRule) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.rules[x.ID()] = *x
	return nil
}

func (r fakeRecurring) Get(_ context.Context, userID, id uuid.UUID) (*domain.RecurringRule, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	x, ok := r.db.rules[id]
	if !ok || r.db.deleted[id] || x.UserID() != userID {
		return nil, domain.ErrRecurringNotFound
	}
	return &x, nil
}

func (r fakeRecurring) Update(_ context.Context, x *domain.RecurringRule) error {
	if err := r.db.fail("recurring.update"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if cur := r.db.rules[x.ID()]; cur.Version() != x.Version() {
		return domain.ErrVersionConflict
	}
	x.SyncVersion(x.Version() + 1)
	r.db.rules[x.ID()] = *x
	return nil
}

func (r fakeRecurring) SoftDelete(_ context.Context, x *domain.RecurringRule, _ time.Time) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.deleted[x.ID()] = true
	return nil
}

func (r fakeRecurring) List(_ context.Context, userID uuid.UUID, limit int, _ *domain.PageKey) ([]*domain.RecurringRule, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.RecurringRule{}
	for id, x := range r.db.rules { //nolint:gocritic // fake in-memory
		if !r.db.deleted[id] && x.UserID() == userID {
			out = append(out, &x)
		}
	}
	if len(out) > limit+1 {
		out = out[:limit+1]
	}
	return out, nil
}

func (r fakeRecurring) ClaimDue(_ context.Context, before time.Time, limit int, exclude []uuid.UUID) ([]*domain.RecurringRule, error) {
	if err := r.db.fail("recurring.claim"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.RecurringRule{}
	for id, x := range r.db.rules { //nolint:gocritic // fake in-memory
		if r.db.deleted[id] || x.Status() != domain.RuleActive || x.NextRunDate().After(before) || slices.Contains(exclude, id) {
			continue
		}
		out = append(out, &x)
	}
	slices.SortFunc(out, func(a, b *domain.RecurringRule) int {
		if c := a.NextRunDate().Compare(b.NextRunDate()); c != 0 {
			return c
		}
		return strings.Compare(a.ID().String(), b.ID().String())
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---- notifier ----

type recordingNotifier struct {
	mu     sync.Mutex
	alerts []BudgetAlert
}

func (n *recordingNotifier) BudgetThresholdReached(_ context.Context, a BudgetAlert) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.alerts = append(n.alerts, a)
}
