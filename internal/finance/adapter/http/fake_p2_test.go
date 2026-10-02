package http

import (
	"context"
	"encoding/json"
	"math/big"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

var (
	rateID = uuid.MustParse("01920000-0000-7000-8000-000000000701")
	goalID = uuid.MustParse("01920000-0000-7000-8000-000000000801")
	debtID = uuid.MustParse("01920000-0000-7000-8000-000000000901")
	billID = uuid.MustParse("01920000-0000-7000-8000-000000000a01")
	peerID = uuid.MustParse("01920000-0000-7000-8000-00000000bbbb")
	day1   = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
)

func sampleRate() *domain.ExchangeRate {
	return domain.RehydrateExchangeRate(domain.ExchangeRateState{ID: rateID, UserID: testUser, Base: money.USD, Quote: money.IDR,
		Rate: big.NewRat(16250, 1), AsOf: day1, CreatedAt: tNow})
}

func sampleGoal() *app.Goal {
	td, a := day1.AddDate(1, 0, 0), accID
	g := domain.RehydrateSavingsGoal(domain.SavingsGoalState{ID: goalID, UserID: testUser, Name: "Laptop",
		Target: money.New(1000000, money.IDR), TargetDate: &td, AccountID: &a, Status: domain.GoalActive, Version: 1,
		CreatedAt: tNow, UpdatedAt: tNow})
	return &app.Goal{Goal: g, Progress: g.Progress(250000, day1)}
}

func sampleContribution() *domain.GoalContribution {
	tr := trID
	return &domain.GoalContribution{ID: uuid.New(), GoalID: goalID, UserID: testUser, Amount: money.New(250000, money.IDR),
		Date: day1, TransferID: &tr, CreatedAt: tNow}
}

func sampleDebt() *app.Debt {
	due := day1.AddDate(0, 3, 0)
	d := domain.RehydrateDebt(domain.DebtState{ID: debtID, UserID: testUser, Direction: domain.DebtPayable, Counterparty: "Budi",
		Principal: money.New(100000, money.IDR), StartDate: day1, DueDate: &due, Status: domain.DebtOpen, Version: 2,
		CreatedAt: tNow, UpdatedAt: tNow})
	return &app.Debt{Debt: d, Paid: 40000}
}

func samplePayment() *domain.DebtPayment {
	t := txID
	return &domain.DebtPayment{ID: uuid.New(), DebtID: debtID, UserID: testUser, Amount: money.New(40000, money.IDR),
		Date: day1, TransactionID: &t, CreatedAt: tNow}
}

func sampleBill() *domain.Bill {
	a, od := accID, day1
	return domain.RehydrateBill(domain.BillState{ID: billID, UserID: testUser, Name: "Listrik", Amount: money.New(450000, money.IDR),
		AccountID: &a, Frequency: domain.FreqMonthly, MonthDay: 1, NextDue: day1, RemindDays: 3, OverdueFor: &od,
		Status: domain.BillActive, Version: 4, CreatedAt: tNow, UpdatedAt: tNow})
}

func sampleMember() *domain.AccountMember {
	return &domain.AccountMember{AccountID: accID, OwnerID: testUser, MemberID: peerID, Role: domain.RoleViewer, CreatedAt: tNow, UpdatedAt: tNow}
}

func sampleAudit(n int) []*domain.AuditEntry {
	out := make([]*domain.AuditEntry, 0, n)
	for i := range n {
		out = append(out, &domain.AuditEntry{ID: uuid.New(), UserID: testUser, ActorID: peerID, Entity: app.EntityTransaction,
			EntityID: txID, Action: domain.AuditUpdate, Before: json.RawMessage(`{"amount":"1"}`),
			CreatedAt: tNow.Add(-time.Duration(i) * time.Minute)})
	}
	return out
}

func (f *fakeSvc) CreateRate(_ context.Context, in app.CreateRateInput) (*domain.ExchangeRate, error) {
	f.p2In = in
	return sampleRate(), f.err
}
func (f *fakeSvc) GetRate(context.Context, uuid.UUID, uuid.UUID) (*domain.ExchangeRate, error) {
	return sampleRate(), f.err
}
func (f *fakeSvc) ListRates(context.Context, uuid.UUID) ([]*domain.ExchangeRate, error) {
	return []*domain.ExchangeRate{sampleRate()}, f.err
}
func (f *fakeSvc) UpdateRate(_ context.Context, _, _ uuid.UUID, rate string) (*domain.ExchangeRate, error) {
	f.p2In = rate
	return sampleRate(), f.err
}
func (f *fakeSvc) DeleteRate(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}
func (f *fakeSvc) Convert(_ context.Context, in app.ConvertInput) (money.Money, money.Money, error) {
	f.p2In = in
	return money.New(100, money.USD), money.New(16250, money.IDR), f.err
}
func (f *fakeSvc) Yearly(_ context.Context, _ uuid.UUID, year int) (*app.YearlyReport, error) {
	f.p2In = year
	z := money.Zero(money.IDR)
	return &app.YearlyReport{Year: year, Currency: money.IDR, Months: []app.YearMonth{{Month: day1, Income: z, Expense: z, Net: z}},
		Income: z, Expense: z, Net: z, SavingsRate: "0.00",
		Unconverted: []app.CurrencySummary{{Currency: money.EUR, Income: money.Zero(money.EUR), Expense: money.Zero(money.EUR), Net: money.Zero(money.EUR)}}}, f.err
}
func (f *fakeSvc) ListAuditLogs(_ context.Context, _ uuid.UUID, flt domain.AuditFilter) ([]*domain.AuditEntry, error) {
	f.p2In = flt
	return f.audit, f.err
}

func (f *fakeSvc) CreateGoal(_ context.Context, in app.CreateGoalInput) (*app.Goal, error) {
	f.p2In = in
	return sampleGoal(), f.err
}
func (f *fakeSvc) GetGoal(context.Context, uuid.UUID, uuid.UUID) (*app.Goal, error) {
	return sampleGoal(), f.err
}
func (f *fakeSvc) ListGoals(context.Context, uuid.UUID) ([]*app.Goal, error) {
	return []*app.Goal{sampleGoal()}, f.err
}
func (f *fakeSvc) UpdateGoal(_ context.Context, in app.UpdateGoalInput) (*app.Goal, error) {
	f.p2In = in
	return sampleGoal(), f.err
}
func (f *fakeSvc) DeleteGoal(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}
func (f *fakeSvc) Contribute(_ context.Context, in app.ContributeInput) (*domain.GoalContribution, *app.Goal, error) {
	f.p2In = in
	return sampleContribution(), sampleGoal(), f.err
}
func (f *fakeSvc) ListContributions(context.Context, uuid.UUID, uuid.UUID) ([]*domain.GoalContribution, error) {
	return []*domain.GoalContribution{sampleContribution()}, f.err
}
func (f *fakeSvc) DeleteContribution(_ context.Context, _, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}

func (f *fakeSvc) CreateDebt(_ context.Context, in app.CreateDebtInput) (*app.Debt, error) {
	f.p2In = in
	return sampleDebt(), f.err
}
func (f *fakeSvc) GetDebt(context.Context, uuid.UUID, uuid.UUID) (*app.Debt, error) {
	return sampleDebt(), f.err
}
func (f *fakeSvc) ListDebts(_ context.Context, _ uuid.UUID, status string) ([]*app.Debt, error) {
	f.p2In = status
	return []*app.Debt{sampleDebt()}, f.err
}
func (f *fakeSvc) UpdateDebt(_ context.Context, in app.UpdateDebtInput) (*app.Debt, error) {
	f.p2In = in
	return sampleDebt(), f.err
}
func (f *fakeSvc) DeleteDebt(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}
func (f *fakeSvc) PayDebt(_ context.Context, in app.PayDebtInput) (*domain.DebtPayment, *app.Debt, error) {
	f.p2In = in
	return samplePayment(), sampleDebt(), f.err
}
func (f *fakeSvc) ListDebtPayments(context.Context, uuid.UUID, uuid.UUID) ([]*domain.DebtPayment, error) {
	return []*domain.DebtPayment{samplePayment()}, f.err
}
func (f *fakeSvc) DeleteDebtPayment(_ context.Context, _, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}

func (f *fakeSvc) CreateBill(_ context.Context, in app.CreateBillInput) (*domain.Bill, error) {
	f.p2In = in
	return sampleBill(), f.err
}
func (f *fakeSvc) GetBill(context.Context, uuid.UUID, uuid.UUID) (*domain.Bill, error) {
	return sampleBill(), f.err
}
func (f *fakeSvc) ListBills(context.Context, uuid.UUID) ([]*domain.Bill, error) {
	return []*domain.Bill{sampleBill()}, f.err
}
func (f *fakeSvc) UpdateBill(_ context.Context, in app.UpdateBillInput) (*domain.Bill, error) {
	f.p2In = in
	return sampleBill(), f.err
}
func (f *fakeSvc) DeleteBill(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}
func (f *fakeSvc) PayBill(_ context.Context, in app.PayBillInput) (*domain.Bill, *domain.Transaction, error) {
	f.p2In = in
	if !in.CreateTransaction {
		return sampleBill(), nil, f.err
	}
	return sampleBill(), sampleTx(txID, 1), f.err
}

func (f *fakeSvc) AddMember(_ context.Context, in app.AddMemberInput) (*domain.AccountMember, error) {
	f.p2In = in
	if f.err != nil {
		return nil, f.err
	}
	return sampleMember(), nil
}
func (f *fakeSvc) ListMembers(context.Context, uuid.UUID, uuid.UUID) ([]*domain.AccountMember, error) {
	return []*domain.AccountMember{sampleMember()}, f.err
}
func (f *fakeSvc) UpdateMemberRole(_ context.Context, _, _, member uuid.UUID, role string) (*domain.AccountMember, error) {
	f.p2In = [2]string{member.String(), role}
	if f.err != nil {
		return nil, f.err
	}
	return sampleMember(), nil
}
func (f *fakeSvc) RemoveMember(_ context.Context, _, _, member uuid.UUID) error {
	f.gotDeleted = member
	return f.err
}
func (f *fakeSvc) ListSharedAccounts(context.Context, uuid.UUID) ([]domain.SharedAccount, error) {
	return []domain.SharedAccount{{Account: sampleAccount(), Role: domain.RoleEditor}}, f.err
}
