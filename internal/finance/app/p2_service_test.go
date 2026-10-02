package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

func TestGoalLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bank := f.account(t, "bank", "0")
	cash := f.account(t, "cash", "100000")

	g, err := f.svc.CreateGoal(ctx, CreateGoalInput{UserID: f.user, Name: "Laptop", Target: "30000",
		TargetDate: ptr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)), AccountID: ptr(bank.ID())})
	if err != nil {
		t.Fatal(err)
	}
	if g.Goal.Target().Currency() != idr || g.Progress.MonthlyNeeded != 10000 {
		t.Fatalf("progress = %+v", g.Progress)
	}
	_, err = f.svc.CreateGoal(ctx, CreateGoalInput{UserID: f.user, Name: "laptop", Target: "1"})
	wantErr(t, err, domain.ErrDuplicateName)
	_, err = f.svc.CreateGoal(ctx, CreateGoalInput{UserID: f.user, Name: "x", Target: "abc"})
	wantErr(t, err, &domain.ValidationError{Field: "target_amount"})
	_, err = f.svc.CreateGoal(ctx, CreateGoalInput{UserID: f.user, Name: "x", Target: "1", AccountID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.CreateGoal(ctx, CreateGoalInput{UserID: f.user, Name: "x", Target: "1", Currency: "XXX"})
	if err == nil {
		t.Fatal("currency invalid harus gagal")
	}
	_, err = f.svc.CreateGoal(ctx, CreateGoalInput{UserID: f.user, Name: "", Target: "1"})
	wantErr(t, err, &domain.ValidationError{Field: "name"})
	f.ids.err = errBoom
	_, err = f.svc.CreateGoal(ctx, CreateGoalInput{UserID: f.user, Name: "y", Target: "1"})
	wantErr(t, err, errBoom)
	f.ids.err = nil

	// setoran manual
	c, got, err := f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), Amount: "10000", Date: today})
	if err != nil || c.Amount.Amount() != 10000 || got.Progress.Saved != 10000 {
		t.Fatalf("contribute: %v %+v", err, got)
	}
	// setoran lewat transfer ke akun goal
	tr, err := f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: cash.ID(), ToAccountID: bank.ID(), Amount: "25000", Date: today})
	if err != nil {
		t.Fatal(err)
	}
	_, got, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), TransferID: ptr(tr.ID())})
	if err != nil || got.Goal.Status() != domain.GoalAchieved || got.Progress.Saved != 35000 {
		t.Fatalf("achieved: %v %+v", err, got)
	}
	_, _, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), TransferID: ptr(tr.ID())})
	wantErr(t, err, domain.ErrDuplicateLink)
	_, _, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), TransferID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrTransferNotFound)
	// penarikan melebihi saldo goal
	_, _, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), Amount: "99999", Withdraw: true, Date: today})
	wantErr(t, err, &domain.ValidationError{Field: "amount"})
	// penarikan menurunkan status kembali active
	w, got, err := f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), Amount: "10000", Withdraw: true, Date: today})
	if err != nil || w.Amount.Amount() != -10000 || got.Goal.Status() != domain.GoalActive {
		t.Fatalf("withdraw: %v %+v", err, got)
	}
	_, _, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), Amount: "x", Date: today})
	wantErr(t, err, &domain.ValidationError{Field: "amount"})
	_, _, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: uuid.New(), Amount: "1", Date: today})
	wantErr(t, err, domain.ErrGoalNotFound)
	for _, op := range []string{"goals.saved", "goals.contribute", "settings.get"} {
		f.db.failOn[op] = errBoom
		_, _, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), Amount: "1", Date: today})
		wantErr(t, err, errBoom)
		delete(f.db.failOn, op)
	}
	f.ids.err = errBoom
	_, _, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), Amount: "1", Date: today})
	wantErr(t, err, errBoom)
	f.ids.err = nil
	f.db.failOn["goals.update"] = errBoom
	_, _, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), Amount: "20000", Date: today})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "goals.update")

	list, err := f.svc.ListContributions(ctx, f.user, g.Goal.ID())
	if err != nil || len(list) != 3 {
		t.Fatalf("contributions = %d, %v", len(list), err)
	}
	_, err = f.svc.ListContributions(ctx, uuid.New(), g.Goal.ID())
	wantErr(t, err, domain.ErrGoalNotFound)

	// update
	up, err := f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: g.Goal.ID(), ExpectedVersion: ptr(got.Goal.Version()),
		Name: ptr("MacBook"), Target: ptr("20000"), ClearTargetDate: true, ClearAccount: true})
	if err != nil || up.Goal.Status() != domain.GoalAchieved || up.Goal.AccountID() != nil {
		t.Fatalf("update: %v", err)
	}
	_, err = f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: g.Goal.ID(), ExpectedVersion: ptr(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	_, err = f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: g.Goal.ID(), Target: ptr("x")})
	wantErr(t, err, &domain.ValidationError{Field: "target_amount"})
	_, err = f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: g.Goal.ID(), AccountID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: g.Goal.ID(), Name: ptr("")})
	wantErr(t, err, &domain.ValidationError{Field: "name"})
	_, err = f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: uuid.New()})
	wantErr(t, err, domain.ErrGoalNotFound)
	f.db.failOn["goals.saved"] = errBoom
	_, err = f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: g.Goal.ID()})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "goals.saved")
	f.db.failOn["goals.update"] = errBoom
	_, err = f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: g.Goal.ID()})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "goals.update")
	if _, err := f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: g.Goal.ID(), AccountID: ptr(bank.ID()), Archived: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.svc.Contribute(ctx, ContributeInput{UserID: f.user, GoalID: g.Goal.ID(), Amount: "1", Date: today})
	wantErr(t, err, domain.ErrGoalArchived)
	if _, err := f.svc.UpdateGoal(ctx, UpdateGoalInput{UserID: f.user, ID: g.Goal.ID(), Archived: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	// hapus kontribusi -> status diselaraskan
	if err := f.svc.DeleteContribution(ctx, f.user, g.Goal.ID(), c.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteContribution(ctx, f.user, g.Goal.ID(), c.ID), domain.ErrContributionNotFound)
	wantErr(t, f.svc.DeleteContribution(ctx, f.user, uuid.New(), c.ID), domain.ErrGoalNotFound)
	f.db.failOn["goals.saved"] = errBoom
	wantErr(t, f.svc.DeleteContribution(ctx, f.user, g.Goal.ID(), w.ID), errBoom)
	delete(f.db.failOn, "goals.saved")
	if err := f.svc.DeleteContribution(ctx, f.user, g.Goal.ID(), w.ID); err != nil {
		t.Fatal(err)
	}
	gg, err := f.svc.GetGoal(ctx, f.user, g.Goal.ID())
	if err != nil || gg.Progress.Saved != 25000 || gg.Goal.Status() != domain.GoalAchieved {
		t.Fatalf("get: %v %+v", err, gg)
	}
	_, err = f.svc.GetGoal(ctx, uuid.New(), g.Goal.ID())
	wantErr(t, err, domain.ErrGoalNotFound)
	f.db.failOn["goals.saved"] = errBoom
	_, err = f.svc.GetGoal(ctx, f.user, g.Goal.ID())
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "goals.saved")

	goals, err := f.svc.ListGoals(ctx, f.user)
	if err != nil || len(goals) != 1 || goals[0].Progress.Saved != 25000 {
		t.Fatalf("list: %v", err)
	}
	f.db.failOn["goals.list"] = errBoom
	_, err = f.svc.ListGoals(ctx, f.user)
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "goals.list")
	f.db.failOn["settings.get"] = errBoom
	_, err = f.svc.ListGoals(ctx, f.user)
	wantErr(t, err, errBoom)
	_, err = f.svc.CreateGoal(ctx, CreateGoalInput{UserID: f.user, Name: "z", Target: "1"})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "settings.get")
	f.db.failOn["goals.create"] = errBoom
	_, err = f.svc.CreateGoal(ctx, CreateGoalInput{UserID: f.user, Name: "z", Target: "1"})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "goals.create")

	if err := f.svc.DeleteGoal(ctx, f.user, g.Goal.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteGoal(ctx, f.user, g.Goal.ID()), domain.ErrGoalNotFound)
}

func TestDebtLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cash := f.account(t, "cash", "100000")

	d, err := f.svc.CreateDebt(ctx, CreateDebtInput{UserID: f.user, Direction: "payable", Counterparty: "Budi",
		Principal: "50000", StartDate: today, DueDate: ptr(today.AddDate(0, 1, 0))})
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.CreateDebt(ctx, CreateDebtInput{UserID: f.user, Direction: "receivable", Counterparty: "Ani", Principal: "20000", StartDate: today})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateDebt(ctx, CreateDebtInput{UserID: f.user, Direction: "x", Counterparty: "A", Principal: "1", StartDate: today})
	wantErr(t, err, &domain.ValidationError{Field: "direction"})
	_, err = f.svc.CreateDebt(ctx, CreateDebtInput{UserID: f.user, Direction: "payable", Counterparty: "A", Principal: "x", StartDate: today})
	wantErr(t, err, &domain.ValidationError{Field: "principal"})
	_, err = f.svc.CreateDebt(ctx, CreateDebtInput{UserID: f.user, Direction: "payable", Counterparty: "A", Principal: "1", Currency: "ZZZ", StartDate: today})
	if err == nil {
		t.Fatal("currency")
	}
	for _, op := range []string{"debts.create", "settings.get"} {
		f.db.failOn[op] = errBoom
		_, err = f.svc.CreateDebt(ctx, CreateDebtInput{UserID: f.user, Direction: "payable", Counterparty: "A", Principal: "1", Currency: "IDR", StartDate: today})
		wantErr(t, err, errBoom)
		delete(f.db.failOn, op)
	}
	f.ids.err = errBoom
	_, err = f.svc.CreateDebt(ctx, CreateDebtInput{UserID: f.user, Direction: "payable", Counterparty: "A", Principal: "1", StartDate: today})
	wantErr(t, err, errBoom)
	f.ids.err = nil

	// bayar dengan membuat transaksi expense baru
	p1, got, err := f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "20000", Date: today,
		AccountID: ptr(cash.ID()), CategoryID: ptr(f.expense.ID())})
	if err != nil || p1.TransactionID == nil || got.Remaining().Amount() != 30000 {
		t.Fatalf("pay: %v", err)
	}
	if b := f.balance(t, cash.ID()); b != 80000 {
		t.Fatalf("balance = %d", b)
	}
	// tautkan transaksi yang sudah ada (amount diambil dari transaksi)
	tx, _ := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: cash.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "10000", Date: today})
	if _, _, err := f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Date: today, TransactionID: ptr(tx.ID())}); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Date: today, TransactionID: ptr(tx.ID())})
	wantErr(t, err, domain.ErrDuplicateLink)
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "30001", Date: today})
	wantErr(t, err, domain.ErrOverpayment)
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Date: today})
	wantErr(t, err, &domain.ValidationError{Field: "amount"})
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "x", Date: today})
	wantErr(t, err, &domain.ValidationError{Field: "amount"})
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "1", Date: today, AccountID: ptr(cash.ID())})
	wantErr(t, err, &domain.ValidationError{Field: "category_id"})
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "1", Date: today, AccountID: ptr(cash.ID()), TransactionID: ptr(tx.ID())})
	wantErr(t, err, &domain.ValidationError{Field: "transaction_id"})
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Date: today, TransactionID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrTransactionNotFound)
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: uuid.New(), Amount: "1", Date: today})
	wantErr(t, err, domain.ErrDebtNotFound)
	// pembuatan transaksi gagal -> rollback seluruhnya
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "1", Date: today, AccountID: ptr(uuid.New()), CategoryID: ptr(f.expense.ID())})
	wantErr(t, err, domain.ErrAccountNotFound)
	for _, op := range []string{"debts.paid", "debts.pay", "settings.get"} {
		f.db.failOn[op] = errBoom
		_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "1", Date: today})
		wantErr(t, err, errBoom)
		delete(f.db.failOn, op)
	}
	f.ids.err = errBoom
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "1", Date: today})
	wantErr(t, err, errBoom)
	f.ids.err = nil
	f.db.failOn["debts.update"] = errBoom
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "20000", Date: today})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "debts.update")
	last, got, err := f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "20000", Date: today})
	if err != nil || got.Debt.Status() != domain.DebtSettled {
		t.Fatalf("settle: %v", err)
	}
	_, _, err = f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: d.Debt.ID(), Amount: "1", Date: today})
	wantErr(t, err, domain.ErrDebtSettled)

	// receivable: income baru
	if _, _, err := f.svc.PayDebt(ctx, PayDebtInput{UserID: f.user, DebtID: r.Debt.ID(), Amount: "5000", Date: today, Note: "cicil",
		AccountID: ptr(cash.ID()), CategoryID: ptr(f.income.ID())}); err != nil {
		t.Fatal(err)
	}

	settled, err := f.svc.ListDebts(ctx, f.user, "settled")
	if err != nil || len(settled) != 1 {
		t.Fatalf("list settled = %d %v", len(settled), err)
	}
	all, _ := f.svc.ListDebts(ctx, f.user, "")
	if len(all) != 2 {
		t.Fatalf("list all = %d", len(all))
	}
	_, err = f.svc.ListDebts(ctx, f.user, "bogus")
	wantErr(t, err, &domain.ValidationError{Field: "status"})
	f.db.failOn["debts.list"] = errBoom
	_, err = f.svc.ListDebts(ctx, f.user, "open")
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "debts.list")

	pays, err := f.svc.ListDebtPayments(ctx, f.user, d.Debt.ID())
	if err != nil || len(pays) != 3 {
		t.Fatalf("payments = %d %v", len(pays), err)
	}
	_, err = f.svc.ListDebtPayments(ctx, uuid.New(), d.Debt.ID())
	wantErr(t, err, domain.ErrDebtNotFound)

	// hapus cicilan terakhir -> kembali open
	f.db.failOn["debts.paid"] = errBoom
	wantErr(t, f.svc.DeleteDebtPayment(ctx, f.user, d.Debt.ID(), last.ID), errBoom)
	delete(f.db.failOn, "debts.paid")
	if err := f.svc.DeleteDebtPayment(ctx, f.user, d.Debt.ID(), last.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteDebtPayment(ctx, f.user, d.Debt.ID(), last.ID), domain.ErrDebtNotFound)
	wantErr(t, f.svc.DeleteDebtPayment(ctx, f.user, uuid.New(), last.ID), domain.ErrDebtNotFound)
	gd, err := f.svc.GetDebt(ctx, f.user, d.Debt.ID())
	if err != nil || gd.Debt.Status() != domain.DebtOpen || gd.Paid != 30000 {
		t.Fatalf("get: %v %+v", err, gd)
	}
	_, err = f.svc.GetDebt(ctx, uuid.New(), d.Debt.ID())
	wantErr(t, err, domain.ErrDebtNotFound)
	f.db.failOn["debts.paid"] = errBoom
	_, err = f.svc.GetDebt(ctx, f.user, d.Debt.ID())
	wantErr(t, err, errBoom)
	_, err = f.svc.UpdateDebt(ctx, UpdateDebtInput{UserID: f.user, ID: d.Debt.ID()})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "debts.paid")

	// update: principal tidak boleh di bawah terbayar
	_, err = f.svc.UpdateDebt(ctx, UpdateDebtInput{UserID: f.user, ID: d.Debt.ID(), Principal: ptr("1000")})
	wantErr(t, err, &domain.ValidationError{Field: "principal"})
	_, err = f.svc.UpdateDebt(ctx, UpdateDebtInput{UserID: f.user, ID: d.Debt.ID(), Principal: ptr("x")})
	wantErr(t, err, &domain.ValidationError{Field: "principal"})
	_, err = f.svc.UpdateDebt(ctx, UpdateDebtInput{UserID: f.user, ID: d.Debt.ID(), ExpectedVersion: ptr(99)})
	wantErr(t, err, domain.ErrVersionConflict)
	_, err = f.svc.UpdateDebt(ctx, UpdateDebtInput{UserID: f.user, ID: uuid.New()})
	wantErr(t, err, domain.ErrDebtNotFound)
	f.db.failOn["debts.update"] = errBoom
	_, err = f.svc.UpdateDebt(ctx, UpdateDebtInput{UserID: f.user, ID: d.Debt.ID()})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "debts.update")
	up, err := f.svc.UpdateDebt(ctx, UpdateDebtInput{UserID: f.user, ID: d.Debt.ID(), Principal: ptr("30000"), Counterparty: ptr("Budi S"), ClearDueDate: true, Note: ptr("ok")})
	if err != nil || up.Debt.Status() != domain.DebtSettled {
		t.Fatalf("update: %v", err)
	}

	if err := f.svc.DeleteDebt(ctx, f.user, d.Debt.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteDebt(ctx, f.user, d.Debt.ID()), domain.ErrDebtNotFound)
}

func TestBillLifecycleAndWorker(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cash := f.account(t, "cash", "1000000")

	b, err := f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "Listrik", Amount: "250000", AccountID: ptr(cash.ID()),
		CategoryID: ptr(f.expense.ID()), Frequency: "monthly", DueDate: today.AddDate(0, 0, 2)})
	if err != nil {
		t.Fatal(err)
	}
	once, err := f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "Pajak", Amount: "100", Frequency: "once", DueDate: today.AddDate(0, 0, -1), RemindDays: ptr(0)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "x", Amount: "x", Frequency: "once", DueDate: today})
	wantErr(t, err, &domain.ValidationError{Field: "amount"})
	_, err = f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "x", Amount: "1", Frequency: "hourly", DueDate: today})
	wantErr(t, err, domain.ErrInvalidFrequency)
	_, err = f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "x", Amount: "1", Frequency: "once", DueDate: today, AccountID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "x", Amount: "1", Frequency: "once", DueDate: today, CategoryID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrCategoryNotFound)
	_, err = f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "x", Amount: "1", Currency: "QQQ", Frequency: "once", DueDate: today})
	if err == nil {
		t.Fatal("currency")
	}
	f.db.failOn["bills.create"] = errBoom
	_, err = f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "x", Amount: "1", Frequency: "once", DueDate: today})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "bills.create")
	f.ids.err = errBoom
	_, err = f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "x", Amount: "1", Frequency: "once", DueDate: today})
	wantErr(t, err, errBoom)
	f.ids.err = nil

	// worker: Listrik -> due_soon, Pajak -> overdue; putaran kedua tidak mengirim ulang
	n, err := f.svc.ProcessBills(ctx, testNow, 0)
	if err != nil || n != 2 {
		t.Fatalf("process = %d, %v", n, err)
	}
	kinds := map[string]string{}
	for _, x := range f.notices.notices {
		kinds[x.Name] = x.Kind
	}
	if kinds["Listrik"] != BillDueSoon || kinds["Pajak"] != BillOverdue {
		t.Fatalf("kinds = %v", kinds)
	}
	if n, _ := f.svc.ProcessBills(ctx, testNow, 10); n != 0 {
		t.Fatalf("second run = %d", n)
	}
	// klaim gagal / settings gagal dilaporkan
	f.db.failOn["bills.claim"] = errBoom
	_, err = f.svc.ProcessBills(ctx, testNow, 10)
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "bills.claim")
	b2, _ := f.svc.CreateBill(ctx, CreateBillInput{UserID: f.user, Name: "Air", Amount: "1", Frequency: "weekly", DueDate: today})
	f.db.failOn["bills.update"] = errBoom
	_, err = f.svc.ProcessBills(ctx, testNow, 10)
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "bills.update")
	if got, _ := f.svc.GetBill(ctx, f.user, b2.ID()); got.LastRemindedFor() != nil {
		t.Fatal("rollback gagal")
	}

	// bayar dengan transaksi
	paid, tx, err := f.svc.PayBill(ctx, PayBillInput{UserID: f.user, ID: b.ID(), CreateTransaction: true})
	if err != nil || tx == nil || tx.Amount().Amount() != 250000 || !paid.NextDueDate().Equal(today.AddDate(0, 1, 2)) {
		t.Fatalf("pay: %v", err)
	}
	if bal := f.balance(t, cash.ID()); bal != 750000 {
		t.Fatalf("balance = %d", bal)
	}
	_, _, err = f.svc.PayBill(ctx, PayBillInput{UserID: f.user, ID: once.ID(), CreateTransaction: true})
	wantErr(t, err, &domain.ValidationError{Field: "account_id"})
	_, _, err = f.svc.PayBill(ctx, PayBillInput{UserID: f.user, ID: once.ID(), CreateTransaction: true, AccountID: ptr(cash.ID())})
	wantErr(t, err, &domain.ValidationError{Field: "category_id"})
	f.db.failOn["settings.get"] = errBoom
	_, _, err = f.svc.PayBill(ctx, PayBillInput{UserID: f.user, ID: once.ID(), CreateTransaction: true, AccountID: ptr(cash.ID()), CategoryID: ptr(f.expense.ID())})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "settings.get")
	f.db.failOn["bills.update"] = errBoom
	_, _, err = f.svc.PayBill(ctx, PayBillInput{UserID: f.user, ID: once.ID()})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "bills.update")
	done, tx, err := f.svc.PayBill(ctx, PayBillInput{UserID: f.user, ID: once.ID(), CreateTransaction: true, AccountID: ptr(cash.ID()),
		CategoryID: ptr(f.expense.ID()), Amount: "150", Date: ptr(today), Note: "lunas"})
	if err != nil || done.Status() != domain.BillDone || tx.Amount().Amount() != 150 {
		t.Fatalf("pay once: %v", err)
	}
	_, _, err = f.svc.PayBill(ctx, PayBillInput{UserID: f.user, ID: once.ID()})
	wantErr(t, err, domain.ErrBillDone)
	_, _, err = f.svc.PayBill(ctx, PayBillInput{UserID: f.user, ID: uuid.New()})
	wantErr(t, err, domain.ErrBillNotFound)

	// update
	up, err := f.svc.UpdateBill(ctx, UpdateBillInput{UserID: f.user, ID: b.ID(), Name: ptr("PLN"), Amount: ptr("300000"),
		ClearAccount: true, ClearCategory: true, Frequency: ptr("yearly"), DueDate: ptr(today.AddDate(0, 0, 5)), RemindDays: ptr(1), Paused: ptr(true)})
	if err != nil || up.Status() != domain.BillPaused || up.AccountID() != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := f.svc.UpdateBill(ctx, UpdateBillInput{UserID: f.user, ID: b.ID(), AccountID: ptr(cash.ID()), CategoryID: ptr(f.expense.ID())}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateBill(ctx, UpdateBillInput{UserID: f.user, ID: b.ID(), Amount: ptr("x")})
	wantErr(t, err, &domain.ValidationError{Field: "amount"})
	_, err = f.svc.UpdateBill(ctx, UpdateBillInput{UserID: f.user, ID: b.ID(), AccountID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.UpdateBill(ctx, UpdateBillInput{UserID: f.user, ID: b.ID(), ExpectedVersion: ptr(99)})
	wantErr(t, err, domain.ErrVersionConflict)
	_, err = f.svc.UpdateBill(ctx, UpdateBillInput{UserID: f.user, ID: once.ID(), Name: ptr("x")})
	wantErr(t, err, domain.ErrBillDone)
	_, err = f.svc.UpdateBill(ctx, UpdateBillInput{UserID: f.user, ID: uuid.New()})
	wantErr(t, err, domain.ErrBillNotFound)
	f.db.failOn["bills.update"] = errBoom
	_, err = f.svc.UpdateBill(ctx, UpdateBillInput{UserID: f.user, ID: b.ID()})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "bills.update")

	if l, err := f.svc.ListBills(ctx, f.user); err != nil || len(l) != 3 {
		t.Fatalf("list = %d %v", len(l), err)
	}
	if err := f.svc.DeleteBill(ctx, f.user, b.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteBill(ctx, f.user, b.ID()), domain.ErrBillNotFound)
	_, err = f.svc.GetBill(ctx, f.user, b.ID())
	wantErr(t, err, domain.ErrBillNotFound)
}

type countingBills struct {
	calls atomic.Int32
	err   error
}

func (g *countingBills) ProcessBills(context.Context, time.Time, int) (int, error) {
	g.calls.Add(1)
	return 1, g.err
}

func TestBillWorker(t *testing.T) {
	g := &countingBills{}
	w := NewBillWorker(g, &fixedClock{t: testNow}, 0, nil)
	if w.interval != time.Hour || w.batch != 500 {
		t.Fatal("default")
	}
	w.RunOnce(context.Background())
	g.err = errBoom
	w.RunOnce(context.Background())
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.RunOnce(cctx)
	if g.calls.Load() != 2 {
		t.Fatalf("calls = %d", g.calls.Load())
	}
	w2 := NewBillWorker(g, &fixedClock{t: testNow}, time.Millisecond, nil)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w2.Run(ctx) }()
	time.Sleep(5 * time.Millisecond)
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker tidak berhenti")
	}
}

func TestRatesAndYearly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r1, err := f.svc.CreateRate(ctx, CreateRateInput{UserID: f.user, Base: "USD", Quote: "IDR", Rate: "15000", AsOf: today.AddDate(0, -2, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateRate(ctx, CreateRateInput{UserID: f.user, Base: "USD", Quote: "IDR", Rate: "16000", AsOf: today}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateRate(ctx, CreateRateInput{UserID: f.user, Base: "USD", Quote: "IDR", Rate: "1", AsOf: today})
	wantErr(t, err, domain.ErrRateExists)
	_, err = f.svc.CreateRate(ctx, CreateRateInput{UserID: f.user, Base: "ZZ", Quote: "IDR", Rate: "1", AsOf: today})
	wantErr(t, err, &domain.ValidationError{Field: "base"})
	_, err = f.svc.CreateRate(ctx, CreateRateInput{UserID: f.user, Base: "USD", Quote: "ZZ", Rate: "1", AsOf: today})
	wantErr(t, err, &domain.ValidationError{Field: "quote"})
	_, err = f.svc.CreateRate(ctx, CreateRateInput{UserID: f.user, Base: "USD", Quote: "IDR", Rate: "-1", AsOf: today})
	if err == nil {
		t.Fatal("rate negatif")
	}
	f.ids.err = errBoom
	_, err = f.svc.CreateRate(ctx, CreateRateInput{UserID: f.user, Base: "USD", Quote: "IDR", Rate: "1", AsOf: today})
	wantErr(t, err, errBoom)
	f.ids.err = nil

	got, err := f.svc.GetRate(ctx, f.user, r1.ID())
	if err != nil || got.RateString() != "15000" {
		t.Fatal(err)
	}
	_, err = f.svc.GetRate(ctx, uuid.New(), r1.ID())
	wantErr(t, err, domain.ErrRateNotFound)
	if up, err := f.svc.UpdateRate(ctx, f.user, r1.ID(), "15500"); err != nil || up.RateString() != "15500" {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateRate(ctx, f.user, r1.ID(), "abc")
	if err == nil {
		t.Fatal("rate invalid")
	}
	_, err = f.svc.UpdateRate(ctx, uuid.New(), r1.ID(), "1")
	wantErr(t, err, domain.ErrRateNotFound)
	f.db.failOn["rates.update"] = errBoom
	_, err = f.svc.UpdateRate(ctx, f.user, r1.ID(), "1")
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "rates.update")

	// Convert: kurs langsung, kebalikan, dan tanggal sebelum semua kurs
	_, out, err := f.svc.Convert(ctx, ConvertInput{UserID: f.user, Amount: "10.00", From: "USD", Date: ptr(today.AddDate(0, -1, 0))})
	if err != nil || out.Amount() != 155000 || out.Currency() != idr {
		t.Fatalf("convert = %v %v", out, err)
	}
	_, out, _ = f.svc.Convert(ctx, ConvertInput{UserID: f.user, Amount: "16000", From: "IDR", To: "USD"})
	if out.Amount() != 100 {
		t.Fatalf("inverse = %v", out)
	}
	_, out, _ = f.svc.Convert(ctx, ConvertInput{UserID: f.user, Amount: "1", From: "USD", Date: ptr(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))})
	if out.Amount() != 15500 {
		t.Fatalf("earliest = %v", out)
	}
	_, _, err = f.svc.Convert(ctx, ConvertInput{UserID: f.user, Amount: "1", From: "EUR"})
	wantErr(t, err, domain.ErrRateUnavailable)
	_, _, err = f.svc.Convert(ctx, ConvertInput{UserID: f.user, Amount: "1", From: "XX"})
	wantErr(t, err, &domain.ValidationError{Field: "from"})
	_, _, err = f.svc.Convert(ctx, ConvertInput{UserID: f.user, Amount: "1", From: "USD", To: "XX"})
	wantErr(t, err, &domain.ValidationError{Field: "to"})
	_, _, err = f.svc.Convert(ctx, ConvertInput{UserID: f.user, Amount: "x", From: "USD"})
	wantErr(t, err, &domain.ValidationError{Field: "amount"})
	f.db.failOn["rates.list"] = errBoom
	_, _, err = f.svc.Convert(ctx, ConvertInput{UserID: f.user, Amount: "1", From: "USD"})
	wantErr(t, err, errBoom)
	_, err = f.svc.Yearly(ctx, f.user, 2026)
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "rates.list")

	// transfer lintas currency tanpa to_amount diisi otomatis dari kurs
	usdAcc, _ := f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "USD", Type: "bank", Currency: "USD", InitialBalance: "100"})
	idrAcc := f.account(t, "cash", "0")
	tr, err := f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: usdAcc.ID(), ToAccountID: idrAcc.ID(), Amount: "1", Date: today})
	if err != nil || tr.ToAmount().Amount() != 16000 {
		t.Fatalf("auto to_amount: %v %v", tr, err)
	}
	eurAcc, _ := f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "EUR", Type: "bank", Currency: "EUR"})
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: idrAcc.ID(), ToAccountID: eurAcc.ID(), Amount: "1", Date: today})
	wantErr(t, err, &domain.ValidationError{Field: "to_amount"})
	f.db.failOn["rates.list"] = errBoom
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: usdAcc.ID(), ToAccountID: idrAcc.ID(), Amount: "1", Date: today})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "rates.list")

	// yearly
	eur := money.MustCurrency("EUR")
	f.reports.daily = []domain.DailyTotal{
		{Date: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC), Currency: idr, Income: 1000000, Expense: 200000},
		{Date: time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC), Currency: usd, Income: 100, Expense: 0}, // 1 USD @15500
		{Date: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), Currency: usd, Income: 0, Expense: 50},  // 0.5 USD @16000
		{Date: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), Currency: eur, Income: 500, Expense: 100},
	}
	rep, err := f.svc.Yearly(ctx, f.user, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Months) != 12 || rep.Months[0].Income.Amount() != 1015500 || rep.Months[2].Expense.Amount() != 8000 {
		t.Fatalf("months = %+v", rep.Months[:3])
	}
	if rep.Net.Amount() != 1015500-200000-8000 || rep.SavingsRate == "0.00" {
		t.Fatalf("totals = %v %s", rep.Net, rep.SavingsRate)
	}
	if len(rep.Unconverted) != 1 || rep.Unconverted[0].Currency != eur || rep.Unconverted[0].Net.Amount() != 400 {
		t.Fatalf("unconverted = %+v", rep.Unconverted)
	}
	if !f.reports.lastFrom.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) || !f.reports.lastTo.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("range")
	}
	_, err = f.svc.Yearly(ctx, f.user, 1999)
	wantErr(t, err, &domain.ValidationError{Field: "year"})
	f.db.failOn["reports.daily"] = errBoom
	_, err = f.svc.Yearly(ctx, f.user, 2026)
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "reports.daily")
	f.db.failOn["settings.get"] = errBoom
	_, err = f.svc.Yearly(ctx, f.user, 2026)
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "settings.get")

	if l, _ := f.svc.ListRates(ctx, f.user); len(l) != 2 {
		t.Fatalf("rates = %d", len(l))
	}
	if err := f.svc.DeleteRate(ctx, f.user, r1.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteRate(ctx, f.user, r1.ID()), domain.ErrRateNotFound)
	f.db.failOn["rates.create"] = errBoom
	_, err = f.svc.CreateRate(ctx, CreateRateInput{UserID: f.user, Base: "USD", Quote: "IDR", Rate: "1", AsOf: today.AddDate(0, 0, -9)})
	wantErr(t, err, errBoom)
}
