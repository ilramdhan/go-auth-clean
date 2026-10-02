package app

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

func TestSummary(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c1, c2 := uuid.New(), uuid.New()
	f.reports.balances = []money.Money{money.New(1000, idr)}
	f.reports.ie = []domain.IncomeExpense{{Currency: idr, Income: 500, Expense: 300}}
	f.reports.cats = []domain.CategoryTotal{{CategoryID: c1, Name: "Makan", Total: 200}, {CategoryID: c2, Name: "Lain", Total: 100}}
	f.reports.points = []domain.CashflowPoint{{Period: today, Income: 500, Expense: 300}}
	s, err := f.svc.Summary(ctx, f.user, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Month.Month() != time.March || s.Currency != idr || len(s.TotalBalance) != 1 || s.Totals[0].Net.Amount() != 200 {
		t.Fatalf("%+v", s)
	}
	if s.ByCategory[0].Percent != "66.67" || s.ByCategory[1].Percent != "33.33" || s.Cashflow[0].Net.Amount() != 200 {
		t.Fatalf("shares %+v", s.ByCategory)
	}
	f.reports.balances = nil
	if s, _ := f.svc.Summary(ctx, f.user, "2026-01"); s.TotalBalance == nil || s.Month.Month() != time.January {
		t.Fatal("balances nil harus jadi []")
	}
	_, err = f.svc.Summary(ctx, f.user, "2026/01")
	if _, ok := errors.AsType[*domain.ValidationError](err); !ok {
		t.Fatalf("err = %v", err)
	}
	for _, op := range []string{"reports.balances", "reports.ie", "reports.cats", "reports.cashflow", "settings.get"} {
		f.db.failOn[op] = errBoom
		_, err := f.svc.Summary(ctx, f.user, "")
		wantErr(t, err, errBoom)
		delete(f.db.failOn, op)
	}
	f.reports.cats = []domain.CategoryTotal{{Total: math.MaxInt64}, {Total: 1}}
	if _, err := f.svc.Summary(ctx, f.user, ""); err == nil {
		t.Fatal("overflow harus error")
	}
}

func TestCashflowAndCategoryReport(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.reports.points = []domain.CashflowPoint{{Period: today, Income: 1, Expense: 2}}
	items, cur, err := f.svc.Cashflow(ctx, CashflowInput{UserID: f.user})
	if err != nil || cur != idr || len(items) != 1 || items[0].Net.Amount() != -1 {
		t.Fatal(err)
	}
	// default month: 12 bulan terakhir, to eksklusif = besok
	if f.reports.lastFrom != time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC) || f.reports.lastTo != today.AddDate(0, 0, 1) {
		t.Fatalf("range %v %v", f.reports.lastFrom, f.reports.lastTo)
	}
	from, to := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
	if _, cur, err := f.svc.Cashflow(ctx, CashflowInput{UserID: f.user, From: &from, To: &to, Currency: "USD"}); err != nil || cur != usd {
		t.Fatal(err)
	}
	if f.reports.lastFrom.Day() != 1 {
		t.Fatal("month granularity harus dimulai tanggal 1")
	}
	if _, _, err := f.svc.Cashflow(ctx, CashflowInput{UserID: f.user, Granularity: "day"}); err != nil || f.reports.lastFrom != time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) {
		t.Fatal("default day = bulan ini")
	}
	_, _, err = f.svc.Cashflow(ctx, CashflowInput{UserID: f.user, Granularity: "week"})
	if _, ok := errors.AsType[*domain.ValidationError](err); !ok {
		t.Fatal(err)
	}
	_, _, err = f.svc.Cashflow(ctx, CashflowInput{UserID: f.user, Currency: "ZZZ"})
	wantErr(t, err, money.ErrUnknownCurrency)
	_, _, err = f.svc.Cashflow(ctx, CashflowInput{UserID: f.user, From: &to, To: &from})
	wantErr(t, err, domain.ErrInvalidRange)
	f.db.failOn["settings.get"] = errBoom
	_, _, err = f.svc.Cashflow(ctx, CashflowInput{UserID: f.user})
	wantErr(t, err, errBoom)
	_, _, err = f.svc.CategoryReport(ctx, CategoryReportInput{UserID: f.user})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "settings.get")

	f.reports.cats = []domain.CategoryTotal{{CategoryID: uuid.New(), Name: "Gaji", Total: 10}}
	shares, cur, err := f.svc.CategoryReport(ctx, CategoryReportInput{UserID: f.user, Type: "income", From: &from, To: &to, Currency: "USD"})
	if err != nil || cur != usd || shares[0].Percent != "100.00" || shares[0].Total.Currency() != usd {
		t.Fatal(err)
	}
	if f.reports.lastTo != to.AddDate(0, 0, 1) {
		t.Fatal("to inklusif")
	}
	_, _, err = f.svc.CategoryReport(ctx, CategoryReportInput{UserID: f.user, Type: "x"})
	wantErr(t, err, domain.ErrInvalidTxType)
	_, _, err = f.svc.CategoryReport(ctx, CategoryReportInput{UserID: f.user, Currency: "ZZZ"})
	wantErr(t, err, money.ErrUnknownCurrency)
	_, _, err = f.svc.CategoryReport(ctx, CategoryReportInput{UserID: f.user, From: &to, To: &from})
	wantErr(t, err, domain.ErrInvalidRange)
	f.db.failOn["reports.cats"] = errBoom
	_, _, err = f.svc.CategoryReport(ctx, CategoryReportInput{UserID: f.user})
	wantErr(t, err, errBoom)
}

func TestReconcileAndWorker(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d, err := f.svc.ReconcileBalances(ctx, &f.user)
	if err != nil || d == nil || len(d) != 0 {
		t.Fatal("drift kosong harus []")
	}
	f.db.drifts = []domain.BalanceDrift{{AccountID: uuid.New(), Currency: idr, Cached: 1, Expected: 2}}
	if d, _ := f.svc.ReconcileBalances(ctx, nil); len(d) != 1 {
		t.Fatal("drift")
	}
	f.idem.purged = 3
	if n, err := f.svc.PurgeExpiredIdempotencyKeys(ctx); err != nil || n != 3 || !f.idem.purgeAt.Equal(testNow) {
		t.Fatal(err)
	}
	w := NewMaintenanceWorker(f.svc, 0, nil)
	if w.interval != time.Hour {
		t.Fatal("default interval")
	}
	w.RunOnce(ctx)
	f.db.failOn["idem.purge"] = errBoom
	f.db.reportErr = errBoom
	w.RunOnce(ctx)
	_, err = f.svc.ReconcileBalances(ctx, nil)
	wantErr(t, err, errBoom)
	_, err = f.svc.PurgeExpiredIdempotencyKeys(ctx)
	wantErr(t, err, errBoom)

	cctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	w2 := NewMaintenanceWorker(f.svc, time.Millisecond, nil)
	go func() { done <- w2.Run(cctx) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker tidak berhenti")
	}
	w2.RunOnce(cctx) // ctx selesai: no-op
}
