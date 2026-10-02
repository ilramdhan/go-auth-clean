package app

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

// p2State menyimpan data fitur P2; clone dipakai fakeTx untuk rollback.
type p2State struct {
	rates    map[uuid.UUID]domain.ExchangeRate
	goals    map[uuid.UUID]domain.SavingsGoal
	contribs map[uuid.UUID]domain.GoalContribution
	debts    map[uuid.UUID]domain.Debt
	payments map[uuid.UUID]domain.DebtPayment
	bills    map[uuid.UUID]domain.Bill
	members  map[[2]uuid.UUID]domain.AccountMember // (account, member)
	audit    []domain.AuditEntry
}

func newP2State() p2State {
	return p2State{rates: map[uuid.UUID]domain.ExchangeRate{}, goals: map[uuid.UUID]domain.SavingsGoal{},
		contribs: map[uuid.UUID]domain.GoalContribution{}, debts: map[uuid.UUID]domain.Debt{},
		payments: map[uuid.UUID]domain.DebtPayment{}, bills: map[uuid.UUID]domain.Bill{},
		members: map[[2]uuid.UUID]domain.AccountMember{}}
}

func (p p2State) clone() p2State {
	return p2State{rates: maps.Clone(p.rates), goals: maps.Clone(p.goals), contribs: maps.Clone(p.contribs),
		debts: maps.Clone(p.debts), payments: maps.Clone(p.payments), bills: maps.Clone(p.bills),
		members: maps.Clone(p.members), audit: slices.Clone(p.audit)}
}

func mustTx(ctx context.Context) {
	if ctx.Value(inTxKey{}) == nil {
		panic("GetForUpdate di luar tx")
	}
}

// ---- rates ----

type fakeRates struct{ db *memDB }

func (r fakeRates) Create(_ context.Context, e *domain.ExchangeRate) error {
	if err := r.db.fail("rates.create"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for _, x := range r.db.p2.rates { //nolint:gocritic // fake in-memory
		if x.UserID() == e.UserID() && x.Base() == e.Base() && x.Quote() == e.Quote() && x.AsOf().Equal(e.AsOf()) {
			return domain.ErrRateExists
		}
	}
	r.db.p2.rates[e.ID()] = *e
	return nil
}

func (r fakeRates) Get(_ context.Context, userID, id uuid.UUID) (*domain.ExchangeRate, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	x, ok := r.db.p2.rates[id]
	if !ok || x.UserID() != userID {
		return nil, domain.ErrRateNotFound
	}
	return &x, nil
}

func (r fakeRates) Update(_ context.Context, e *domain.ExchangeRate) error {
	if err := r.db.fail("rates.update"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.p2.rates[e.ID()] = *e
	return nil
}

func (r fakeRates) Delete(_ context.Context, userID, id uuid.UUID) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	x, ok := r.db.p2.rates[id]
	if !ok || x.UserID() != userID {
		return domain.ErrRateNotFound
	}
	delete(r.db.p2.rates, id)
	return nil
}

func (r fakeRates) List(_ context.Context, userID uuid.UUID) ([]*domain.ExchangeRate, error) {
	if err := r.db.fail("rates.list"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.ExchangeRate{}
	for _, x := range r.db.p2.rates { //nolint:gocritic // fake in-memory
		if x.UserID() == userID {
			out = append(out, &x)
		}
	}
	return out, nil
}

// ---- goals ----

type fakeGoals struct{ db *memDB }

func (r fakeGoals) dup(g *domain.SavingsGoal) bool {
	for id, x := range r.db.p2.goals { //nolint:gocritic // fake in-memory
		if id != g.ID() && !r.db.deleted[id] && x.UserID() == g.UserID() && strings.EqualFold(x.Name(), g.Name()) {
			return true
		}
	}
	return false
}

func (r fakeGoals) Create(_ context.Context, g *domain.SavingsGoal) error {
	if err := r.db.fail("goals.create"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if r.dup(g) {
		return domain.ErrDuplicateName
	}
	r.db.p2.goals[g.ID()] = *g
	return nil
}

func (r fakeGoals) Get(_ context.Context, userID, id uuid.UUID) (*domain.SavingsGoal, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	x, ok := r.db.p2.goals[id]
	if !ok || r.db.deleted[id] || x.UserID() != userID {
		return nil, domain.ErrGoalNotFound
	}
	return &x, nil
}

func (r fakeGoals) GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*domain.SavingsGoal, error) {
	mustTx(ctx)
	return r.Get(ctx, userID, id)
}

func (r fakeGoals) Update(_ context.Context, g *domain.SavingsGoal) error {
	if err := r.db.fail("goals.update"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if r.dup(g) {
		return domain.ErrDuplicateName
	}
	if cur := r.db.p2.goals[g.ID()]; cur.Version() != g.Version() {
		return domain.ErrVersionConflict
	}
	g.SyncVersion(g.Version() + 1)
	r.db.p2.goals[g.ID()] = *g
	return nil
}

func (r fakeGoals) SoftDelete(_ context.Context, g *domain.SavingsGoal, _ time.Time) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.deleted[g.ID()] = true
	return nil
}

func (r fakeGoals) saved(goalID uuid.UUID) int64 {
	var n int64
	for _, c := range r.db.p2.contribs { //nolint:gocritic // fake in-memory
		if c.GoalID == goalID {
			n += c.Amount.Amount()
		}
	}
	return n
}

func (r fakeGoals) List(_ context.Context, userID uuid.UUID) ([]domain.GoalWithSaved, error) {
	if err := r.db.fail("goals.list"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []domain.GoalWithSaved{}
	for id, x := range r.db.p2.goals { //nolint:gocritic // fake in-memory
		if !r.db.deleted[id] && x.UserID() == userID {
			out = append(out, domain.GoalWithSaved{Goal: &x, Saved: r.saved(id)})
		}
	}
	return out, nil
}

func (r fakeGoals) Saved(_ context.Context, _, goalID uuid.UUID) (int64, error) {
	if err := r.db.fail("goals.saved"); err != nil {
		return 0, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	return r.saved(goalID), nil
}

func (r fakeGoals) AddContribution(_ context.Context, c *domain.GoalContribution) error {
	if err := r.db.fail("goals.contribute"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for _, x := range r.db.p2.contribs { //nolint:gocritic // fake in-memory
		if c.TransferID != nil && x.TransferID != nil && x.GoalID == c.GoalID && *x.TransferID == *c.TransferID {
			return domain.ErrDuplicateLink
		}
	}
	r.db.p2.contribs[c.ID] = *c
	return nil
}

func (r fakeGoals) ListContributions(_ context.Context, userID, goalID uuid.UUID) ([]*domain.GoalContribution, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.GoalContribution{}
	for _, x := range r.db.p2.contribs { //nolint:gocritic // fake in-memory
		if x.GoalID == goalID && x.UserID == userID {
			out = append(out, &x)
		}
	}
	return out, nil
}

func (r fakeGoals) DeleteContribution(_ context.Context, userID, goalID, id uuid.UUID) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	x, ok := r.db.p2.contribs[id]
	if !ok || x.GoalID != goalID || x.UserID != userID {
		return domain.ErrContributionNotFound
	}
	delete(r.db.p2.contribs, id)
	return nil
}

// ---- debts ----

type fakeDebts struct{ db *memDB }

func (r fakeDebts) Create(_ context.Context, d *domain.Debt) error {
	if err := r.db.fail("debts.create"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.p2.debts[d.ID()] = *d
	return nil
}

func (r fakeDebts) Get(_ context.Context, userID, id uuid.UUID) (*domain.Debt, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	x, ok := r.db.p2.debts[id]
	if !ok || r.db.deleted[id] || x.UserID() != userID {
		return nil, domain.ErrDebtNotFound
	}
	return &x, nil
}

func (r fakeDebts) GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*domain.Debt, error) {
	mustTx(ctx)
	return r.Get(ctx, userID, id)
}

func (r fakeDebts) Update(_ context.Context, d *domain.Debt) error {
	if err := r.db.fail("debts.update"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if cur := r.db.p2.debts[d.ID()]; cur.Version() != d.Version() {
		return domain.ErrVersionConflict
	}
	d.SyncVersion(d.Version() + 1)
	r.db.p2.debts[d.ID()] = *d
	return nil
}

func (r fakeDebts) SoftDelete(_ context.Context, d *domain.Debt, _ time.Time) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.deleted[d.ID()] = true
	return nil
}

func (r fakeDebts) paid(debtID uuid.UUID) int64 {
	var n int64
	for _, p := range r.db.p2.payments { //nolint:gocritic // fake in-memory
		if p.DebtID == debtID {
			n += p.Amount.Amount()
		}
	}
	return n
}

func (r fakeDebts) List(_ context.Context, userID uuid.UUID, st *domain.DebtStatus) ([]domain.DebtWithPaid, error) {
	if err := r.db.fail("debts.list"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []domain.DebtWithPaid{}
	for id, x := range r.db.p2.debts { //nolint:gocritic // fake in-memory
		if !r.db.deleted[id] && x.UserID() == userID && (st == nil || x.Status() == *st) {
			out = append(out, domain.DebtWithPaid{Debt: &x, Paid: r.paid(id)})
		}
	}
	return out, nil
}

func (r fakeDebts) Paid(_ context.Context, _, debtID uuid.UUID) (int64, error) {
	if err := r.db.fail("debts.paid"); err != nil {
		return 0, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	return r.paid(debtID), nil
}

func (r fakeDebts) AddPayment(_ context.Context, p *domain.DebtPayment) error {
	if err := r.db.fail("debts.pay"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for _, x := range r.db.p2.payments { //nolint:gocritic // fake in-memory
		if p.TransactionID != nil && x.TransactionID != nil && *x.TransactionID == *p.TransactionID {
			return domain.ErrDuplicateLink
		}
	}
	r.db.p2.payments[p.ID] = *p
	return nil
}

func (r fakeDebts) ListPayments(_ context.Context, userID, debtID uuid.UUID) ([]*domain.DebtPayment, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.DebtPayment{}
	for _, x := range r.db.p2.payments { //nolint:gocritic // fake in-memory
		if x.DebtID == debtID && x.UserID == userID {
			out = append(out, &x)
		}
	}
	return out, nil
}

func (r fakeDebts) DeletePayment(_ context.Context, userID, debtID, id uuid.UUID) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	x, ok := r.db.p2.payments[id]
	if !ok || x.DebtID != debtID || x.UserID != userID {
		return domain.ErrDebtNotFound
	}
	delete(r.db.p2.payments, id)
	return nil
}

// ---- bills ----

type fakeBills struct{ db *memDB }

func (r fakeBills) Create(_ context.Context, b *domain.Bill) error {
	if err := r.db.fail("bills.create"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.p2.bills[b.ID()] = *b
	return nil
}

func (r fakeBills) Get(_ context.Context, userID, id uuid.UUID) (*domain.Bill, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	x, ok := r.db.p2.bills[id]
	if !ok || r.db.deleted[id] || x.UserID() != userID {
		return nil, domain.ErrBillNotFound
	}
	return &x, nil
}

func (r fakeBills) GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*domain.Bill, error) {
	mustTx(ctx)
	return r.Get(ctx, userID, id)
}

func (r fakeBills) Update(_ context.Context, b *domain.Bill) error {
	if err := r.db.fail("bills.update"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if cur := r.db.p2.bills[b.ID()]; cur.Version() != b.Version() {
		return domain.ErrVersionConflict
	}
	b.SyncVersion(b.Version() + 1)
	r.db.p2.bills[b.ID()] = *b
	return nil
}

func (r fakeBills) SoftDelete(_ context.Context, b *domain.Bill, _ time.Time) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.deleted[b.ID()] = true
	return nil
}

func (r fakeBills) List(_ context.Context, userID uuid.UUID) ([]*domain.Bill, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.Bill{}
	for id, x := range r.db.p2.bills { //nolint:gocritic // fake in-memory
		if !r.db.deleted[id] && x.UserID() == userID {
			out = append(out, &x)
		}
	}
	return out, nil
}

func (r fakeBills) ClaimAttention(ctx context.Context, before time.Time, limit int, exclude []uuid.UUID) ([]*domain.Bill, error) {
	mustTx(ctx)
	if err := r.db.fail("bills.claim"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.Bill{}
	for id, x := range r.db.p2.bills { //nolint:gocritic // fake in-memory
		if r.db.deleted[id] || x.Status() != domain.BillActive || x.RemindAt().After(before) || slices.Contains(exclude, id) {
			continue
		}
		done := x.OverdueFor() != nil && x.OverdueFor().Equal(x.NextDueDate())
		reminded := x.LastRemindedFor() != nil && x.LastRemindedFor().Equal(x.NextDueDate())
		if done || (reminded && !x.NextDueDate().Before(before)) {
			continue
		}
		out = append(out, &x)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---- members ----

type fakeMembers struct{ db *memDB }

func (r fakeMembers) Add(_ context.Context, m *domain.AccountMember) error {
	if err := r.db.fail("members.add"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	k := [2]uuid.UUID{m.AccountID, m.MemberID}
	if _, ok := r.db.p2.members[k]; ok {
		return domain.ErrMemberExists
	}
	r.db.p2.members[k] = *m
	return nil
}

func (r fakeMembers) Get(_ context.Context, accountID, memberID uuid.UUID) (*domain.AccountMember, error) {
	if err := r.db.fail("members.get"); err != nil {
		return nil, err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	m, ok := r.db.p2.members[[2]uuid.UUID{accountID, memberID}]
	if !ok || r.db.deleted[accountID] {
		return nil, domain.ErrMemberNotFound
	}
	return &m, nil
}

func (r fakeMembers) UpdateRole(_ context.Context, m *domain.AccountMember) error {
	if err := r.db.fail("members.update"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.p2.members[[2]uuid.UUID{m.AccountID, m.MemberID}] = *m
	return nil
}

func (r fakeMembers) Remove(_ context.Context, accountID, memberID uuid.UUID) error {
	if err := r.db.fail("members.remove"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	delete(r.db.p2.members, [2]uuid.UUID{accountID, memberID})
	return nil
}

func (r fakeMembers) List(_ context.Context, ownerID, accountID uuid.UUID) ([]*domain.AccountMember, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.AccountMember{}
	for _, m := range r.db.p2.members {
		if m.AccountID == accountID && m.OwnerID == ownerID {
			out = append(out, &m)
		}
	}
	return out, nil
}

func (r fakeMembers) ListShared(_ context.Context, memberID uuid.UUID) ([]domain.SharedAccount, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []domain.SharedAccount{}
	for _, m := range r.db.p2.members {
		if a, ok := r.db.accounts[m.AccountID]; ok && m.MemberID == memberID && !r.db.deleted[m.AccountID] {
			out = append(out, domain.SharedAccount{Account: &a, Role: m.Role})
		}
	}
	return out, nil
}

func (r fakeMembers) isMember(memberID uuid.UUID, accountIDs ...uuid.UUID) bool {
	for _, id := range accountIDs {
		if _, ok := r.db.p2.members[[2]uuid.UUID{id, memberID}]; ok {
			return true
		}
	}
	return false
}

func (r fakeMembers) TransactionOwner(_ context.Context, memberID, txID uuid.UUID) (uuid.UUID, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	t, ok := r.db.txs[txID]
	if !ok || r.db.deleted[txID] || !r.isMember(memberID, t.AccountID()) {
		return uuid.Nil, domain.ErrTransactionNotFound
	}
	return t.UserID(), nil
}

func (r fakeMembers) TransferOwner(_ context.Context, memberID, transferID uuid.UUID) (uuid.UUID, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	t, ok := r.db.trs[transferID]
	if !ok || r.db.deleted[transferID] || !r.isMember(memberID, t.FromAccountID(), t.ToAccountID()) {
		return uuid.Nil, domain.ErrTransferNotFound
	}
	return t.UserID(), nil
}

// ---- audit ----

type fakeAudit struct{ db *memDB }

func (r fakeAudit) Create(_ context.Context, e *domain.AuditEntry) error {
	if err := r.db.fail("audit.create"); err != nil {
		return err
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	r.db.p2.audit = append(r.db.p2.audit, *e)
	return nil
}

func (r fakeAudit) List(_ context.Context, userID uuid.UUID, f domain.AuditFilter) ([]*domain.AuditEntry, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	out := []*domain.AuditEntry{}
	for _, e := range r.db.p2.audit { //nolint:gocritic // fake in-memory
		if e.UserID == userID && (f.Entity == "" || e.Entity == f.Entity) {
			out = append(out, &e)
		}
	}
	return out, nil
}

// ---- ports ----

type recordingBills struct {
	mu      sync.Mutex
	notices []BillNotice
}

func (n *recordingBills) BillNotice(_ context.Context, b BillNotice) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notices = append(n.notices, b)
}

type fakeUsers map[string]uuid.UUID

func (u fakeUsers) LookupByEmail(_ context.Context, email string) (uuid.UUID, error) {
	if email == "boom@x.io" {
		return uuid.Nil, errBoom
	}
	id, ok := u[strings.ToLower(email)]
	if !ok {
		return uuid.Nil, ErrUserNotFound
	}
	return id, nil
}
