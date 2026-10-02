package http

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

var (
	budgetID = uuid.MustParse("01920000-0000-7000-8000-000000000401")
	tagID    = uuid.MustParse("01920000-0000-7000-8000-000000000501")
	ruleID   = uuid.MustParse("01920000-0000-7000-8000-000000000601")
)

func sampleBudgetView() *app.BudgetView {
	b := domain.RehydrateBudget(domain.BudgetState{ID: budgetID, UserID: testUser, CategoryID: catID,
		Month: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Amount: money.New(100000, money.IDR), Threshold: 80,
		Version: 1, CreatedAt: tNow, UpdatedAt: tNow})
	return &app.BudgetView{Budget: b, Progress: b.Progress(120000)}
}

func sampleTag() *domain.Tag {
	return domain.RehydrateTag(tagID, testUser, "liburan", "#ff0000", tNow, tNow)
}

func sampleRule(id uuid.UUID, created time.Time) *domain.RecurringRule {
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	return domain.RehydrateRecurringRule(domain.RecurringRuleState{ID: id, UserID: testUser, AccountID: accID, CategoryID: catID,
		Type: domain.TxExpense, Amount: money.New(50000, money.IDR), Note: "Netflix", Frequency: domain.FreqMonthly,
		Interval: 1, MonthDay: 31, StartDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), EndDate: &end,
		NextRunDate: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), Status: domain.RuleActive,
		Version: 2, CreatedAt: created, UpdatedAt: created})
}

func (f *fakeSvc) CreateBudget(_ context.Context, in app.CreateBudgetInput) (*app.BudgetView, error) {
	f.gotBudget = in
	return sampleBudgetView(), f.err
}

func (f *fakeSvc) GetBudget(context.Context, uuid.UUID, uuid.UUID) (*app.BudgetView, error) {
	return sampleBudgetView(), f.err
}

func (f *fakeSvc) ListBudgets(_ context.Context, _ uuid.UUID, month string) ([]app.BudgetView, error) {
	f.gotBudgetMonth = month
	return []app.BudgetView{*sampleBudgetView()}, f.err
}

func (f *fakeSvc) UpdateBudget(_ context.Context, in app.UpdateBudgetInput) (*app.BudgetView, error) {
	f.gotUpdBudget = in
	return sampleBudgetView(), f.err
}

func (f *fakeSvc) DeleteBudget(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}

func (f *fakeSvc) CreateTag(context.Context, uuid.UUID, string, string) (*domain.Tag, error) {
	return sampleTag(), f.err
}

func (f *fakeSvc) ListTags(context.Context, uuid.UUID) ([]*domain.Tag, error) {
	return []*domain.Tag{sampleTag()}, f.err
}

func (f *fakeSvc) UpdateTag(_ context.Context, in app.UpdateTagInput) (*domain.Tag, error) {
	f.gotUpdTag = in
	return sampleTag(), f.err
}

func (f *fakeSvc) DeleteTag(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}

func (f *fakeSvc) SetTransactionTags(_ context.Context, _, _ uuid.UUID, ids []uuid.UUID) ([]*domain.Tag, error) {
	f.gotTagIDs = ids
	return []*domain.Tag{sampleTag()}, f.err
}

// TransactionTags selalu memberi satu tag untuk txID supaya response terlihat.
func (f *fakeSvc) TransactionTags(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]*domain.Tag, error) {
	out := map[uuid.UUID][]*domain.Tag{}
	for _, id := range ids {
		if id == txID {
			out[id] = []*domain.Tag{sampleTag()}
		}
	}
	return out, f.err
}

func (f *fakeSvc) CreateRecurringRule(_ context.Context, in app.CreateRecurringInput) (*domain.RecurringRule, error) {
	f.gotRule = in
	return sampleRule(ruleID, tNow), f.err
}

func (f *fakeSvc) GetRecurringRule(context.Context, uuid.UUID, uuid.UUID) (*domain.RecurringRule, error) {
	return sampleRule(ruleID, tNow), f.err
}

func (f *fakeSvc) ListRecurringRules(_ context.Context, _ uuid.UUID, _ int, after *domain.PageKey) ([]*domain.RecurringRule, error) {
	f.gotRuleAfter = after
	return f.rules, f.err
}

func (f *fakeSvc) UpdateRecurringRule(_ context.Context, in app.UpdateRecurringInput) (*domain.RecurringRule, error) {
	f.gotUpdRule = in
	return sampleRule(ruleID, tNow), f.err
}

func (f *fakeSvc) DeleteRecurringRule(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}

func (f *fakeSvc) PauseRecurringRule(_ context.Context, _, _ uuid.UUID, reason string) (*domain.RecurringRule, error) {
	f.gotPauseReason = reason
	return sampleRule(ruleID, tNow), f.err
}

func (f *fakeSvc) ResumeRecurringRule(context.Context, uuid.UUID, uuid.UUID) (*domain.RecurringRule, error) {
	return sampleRule(ruleID, tNow), f.err
}

func (f *fakeSvc) ExportTransactions(_ context.Context, in app.ListTransactionsInput, w io.Writer) error {
	f.gotExport = in
	if f.err != nil && !f.exportErrAfterWrite {
		return f.err
	}
	if _, err := io.WriteString(w, "date,type\n2026-09-30,expense\n"); err != nil {
		return err
	}
	return f.err
}

func (f *fakeSvc) ImportTransactions(_ context.Context, in app.ImportInput) (*app.ImportResult, error) {
	f.gotImport = in
	if in.File != nil {
		b, _ := io.ReadAll(in.File)
		f.gotImportBody = string(b)
	}
	return &app.ImportResult{DryRun: in.DryRun, Imported: 2, Skipped: 1, Duplicates: []int{4},
		Errors: []app.ImportRowError{{Row: 3, Field: "amount", Message: "invalid"}}}, f.err
}
