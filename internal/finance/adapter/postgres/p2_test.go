package postgres_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

type noticeSink struct {
	mu sync.Mutex
	ns []app.BillNotice
}

func (n *noticeSink) BillNotice(_ context.Context, b app.BillNotice) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ns = append(n.ns, b)
}

func (n *noticeSink) kinds(user uuid.UUID) map[string]string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := map[string]string{}
	for _, x := range n.ns {
		if x.UserID == user {
			out[x.Name] = x.Kind
		}
	}
	return out
}

type userDir map[string]uuid.UUID

func (u userDir) LookupByEmail(_ context.Context, email string) (uuid.UUID, error) {
	if id, ok := u[email]; ok {
		return id, nil
	}
	return uuid.Nil, domain.ErrUserNotFound
}

func TestBudgetsTagsAndExport(t *testing.T) {
	e := setup(t)
	ctx, user, bob := t.Context(), uuid.New(), uuid.New()
	acc := newAccount(t, e, user, "Cash", "cash", "", "1000000")
	d := today()

	b := must(e.svc.CreateBudget(ctx, app.CreateBudgetInput{UserID: user, CategoryID: catFood, Amount: "100000", Threshold: 50}))(t)
	_, err := e.svc.CreateBudget(ctx, app.CreateBudgetInput{UserID: user, CategoryID: catFood, Amount: "1"})
	wantErr(t, err, domain.ErrBudgetExists)
	_, err = e.svc.GetBudget(ctx, bob, b.Budget.ID())
	wantErr(t, err, domain.ErrBudgetNotFound)

	food := must(e.svc.CreateTag(ctx, user, "Makan", "#FF0000"))(t)
	_, err = e.svc.CreateTag(ctx, user, "makan", "")
	wantErr(t, err, domain.ErrDuplicateName)
	other := must(e.svc.CreateTag(ctx, user, "Kantor", ""))(t)
	bobTag := must(e.svc.CreateTag(ctx, bob, "Makan", ""))(t)

	tx1 := must(e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: user, AccountID: acc.ID(), CategoryID: catFood,
		Type: "expense", Amount: "60000", Date: d, Note: "=HYPERLINK()", TagIDs: []uuid.UUID{food.ID(), other.ID()}}))(t)
	must(e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: user, AccountID: acc.ID(), CategoryID: catFood,
		Type: "expense", Amount: "1000", Date: d}))(t)
	_, err = e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: user, AccountID: acc.ID(), CategoryID: catFood,
		Type: "expense", Amount: "1", Date: d, TagIDs: []uuid.UUID{bobTag.ID()}})
	wantErr(t, err, domain.ErrTagNotFound)

	// budget spent mengikuti transaksi
	got := must(e.svc.GetBudget(ctx, user, b.Budget.ID()))(t)
	if got.Progress.Spent != 61000 {
		t.Fatalf("spent = %d", got.Progress.Spent)
	}
	list := must(e.svc.ListBudgets(ctx, user, ""))(t)
	if len(list) != 1 || list[0].Progress.Spent != 61000 {
		t.Fatalf("budgets = %+v", list)
	}
	up := must(e.svc.UpdateBudget(ctx, app.UpdateBudgetInput{UserID: user, ID: b.Budget.ID(), Amount: new("200000"), ExpectedVersion: new(got.Budget.Version())}))(t)
	if up.Budget.Version() != got.Budget.Version()+1 {
		t.Fatal("version")
	}
	_, err = e.svc.UpdateBudget(ctx, app.UpdateBudgetInput{UserID: user, ID: b.Budget.ID(), ExpectedVersion: new(got.Budget.Version())})
	wantErr(t, err, domain.ErrVersionConflict)

	// ?tag= filter (case-insensitive), tag milik user lain tidak bocor
	tagged := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: user, Tag: "MAKAN"}))(t)
	if len(tagged) != 1 || tagged[0].ID() != tx1.ID() {
		t.Fatalf("tag filter = %d", len(tagged))
	}
	if l := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: bob, Tag: "makan"}))(t); len(l) != 0 {
		t.Fatal("tag IDOR")
	}
	tags := must(e.svc.TransactionTags(ctx, user, []uuid.UUID{tx1.ID()}))(t)
	if len(tags[tx1.ID()]) != 2 {
		t.Fatalf("tags = %v", tags)
	}
	must(e.svc.SetTransactionTags(ctx, user, tx1.ID(), []uuid.UUID{other.ID()}))(t)
	if l := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: user, Tag: "makan"}))(t); len(l) != 0 {
		t.Fatal("tag diganti")
	}
	must(e.svc.UpdateTag(ctx, app.UpdateTagInput{UserID: user, ID: other.ID(), Name: new("Kerja")}))(t)
	_, err = e.svc.UpdateTag(ctx, app.UpdateTagInput{UserID: user, ID: other.ID(), Name: new("makan")})
	wantErr(t, err, domain.ErrDuplicateName)
	if l := must(e.svc.ListTags(ctx, user))(t); len(l) != 2 {
		t.Fatalf("tags = %d", len(l))
	}

	// export: header, formula injection disanitasi, tag ikut
	var buf bytes.Buffer
	if err := e.svc.ExportTransactions(ctx, app.ListTransactionsInput{UserID: user}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if lines := strings.Count(out, "\n"); lines != 3 || !strings.Contains(out, "'=HYPERLINK()") || !strings.Contains(out, "Kerja") || !strings.Contains(out, "Cash") {
		t.Fatalf("export =\n%s", out)
	}
	buf.Reset()
	if err := e.svc.ExportTransactions(ctx, app.ListTransactionsInput{UserID: bob}, &buf); err != nil || strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("export bob = %q %v", buf.String(), err)
	}

	wantErr(t, e.svc.DeleteTag(ctx, bob, other.ID()), domain.ErrTagNotFound)
	if err := e.svc.DeleteTag(ctx, user, other.ID()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteBudget(ctx, user, b.Budget.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.DeleteBudget(ctx, user, b.Budget.ID()), domain.ErrBudgetNotFound)
}

func TestRecurringRepository(t *testing.T) {
	e := setup(t)
	ctx, user := t.Context(), uuid.New()
	acc := newAccount(t, e, user, "Cash", "cash", "", "1000000")
	start := today().AddDate(0, 0, -2)
	r := must(e.svc.CreateRecurringRule(ctx, app.CreateRecurringInput{UserID: user, AccountID: acc.ID(), CategoryID: catFood,
		Type: "expense", Amount: "1000", Frequency: "daily", Interval: 1, StartDate: start}))(t)
	_, err := e.svc.GetRecurringRule(ctx, uuid.New(), r.ID())
	wantErr(t, err, domain.ErrRecurringNotFound)
	n := must(e.svc.GenerateDue(ctx, time.Now(), 50))(t)
	if n != 3 {
		t.Fatalf("generated = %d", n)
	}
	if again := must(e.svc.GenerateDue(ctx, time.Now(), 50))(t); again != 0 {
		t.Fatalf("idempotent = %d", again)
	}
	if b := balance(t, e, user, acc.ID()); b.Amount() != 997000 {
		t.Fatalf("balance = %v", b)
	}
	p := must(e.svc.PauseRecurringRule(ctx, user, r.ID(), "libur"))(t)
	if p.Status() != domain.RulePaused {
		t.Fatal("paused")
	}
	must(e.svc.ResumeRecurringRule(ctx, user, r.ID()))(t)
	up := must(e.svc.UpdateRecurringRule(ctx, app.UpdateRecurringInput{UserID: user, ID: r.ID(), Amount: new("2000"), Note: new("kopi")}))(t)
	if up.Amount().Amount() != 2000 {
		t.Fatal("update")
	}
	_, err = e.svc.UpdateRecurringRule(ctx, app.UpdateRecurringInput{UserID: user, ID: r.ID(), ExpectedVersion: new(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	page := must(e.svc.ListRecurringRules(ctx, user, 10, nil))(t)
	if len(page) != 1 {
		t.Fatal("list")
	}
	next := must(e.svc.ListRecurringRules(ctx, user, 10, &domain.PageKey{Date: page[0].CreatedAt(), ID: page[0].ID()}))(t)
	if len(next) != 0 {
		t.Fatal("keyset")
	}
	if err := e.svc.DeleteRecurringRule(ctx, user, r.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.DeleteRecurringRule(ctx, user, r.ID()), domain.ErrRecurringNotFound)
}

func TestRatesYearlyAndCrossCurrency(t *testing.T) {
	e := setup(t)
	ctx, user, bob := t.Context(), uuid.New(), uuid.New()
	d := today()
	r := must(e.svc.CreateRate(ctx, app.CreateRateInput{UserID: user, Base: "USD", Quote: "IDR", Rate: "16000.1234567891", AsOf: d.AddDate(0, 0, -30)}))(t)
	if r.RateString() != "16000.1234567891" {
		t.Fatalf("rate = %s", r.RateString())
	}
	_, err := e.svc.CreateRate(ctx, app.CreateRateInput{UserID: user, Base: "USD", Quote: "IDR", Rate: "1", AsOf: d.AddDate(0, 0, -30)})
	wantErr(t, err, domain.ErrRateExists)
	must(e.svc.CreateRate(ctx, app.CreateRateInput{UserID: bob, Base: "USD", Quote: "IDR", Rate: "1", AsOf: d}))(t)
	if got := must(e.svc.GetRate(ctx, user, r.ID()))(t); got.RateString() != "16000.1234567891" || !got.AsOf().Equal(d.AddDate(0, 0, -30)) {
		t.Fatalf("get = %s %v", got.RateString(), got.AsOf())
	}
	_, err = e.svc.GetRate(ctx, bob, r.ID())
	wantErr(t, err, domain.ErrRateNotFound)
	_, err = e.svc.UpdateRate(ctx, bob, r.ID(), "2")
	wantErr(t, err, domain.ErrRateNotFound)
	must(e.svc.UpdateRate(ctx, user, r.ID(), "16000"))(t)
	if l := must(e.svc.ListRates(ctx, user))(t); len(l) != 1 {
		t.Fatalf("rates = %d", len(l))
	}

	usd := newAccount(t, e, user, "USD", "bank", "USD", "100")
	idr := newAccount(t, e, user, "IDR", "cash", "", "0")
	tr := must(e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: user, FromAccountID: usd.ID(), ToAccountID: idr.ID(), Amount: "2.50", Date: d}))(t)
	if tr.ToAmount().Amount() != 40000 || balance(t, e, user, idr.ID()).Amount() != 40000 {
		t.Fatalf("to_amount = %v", tr.ToAmount())
	}
	must(e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: user, AccountID: idr.ID(), CategoryID: catSalary, Type: "income", Amount: "500000", Date: d}))(t)
	must(e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: user, AccountID: usd.ID(), CategoryID: catFood, Type: "expense", Amount: "1", Date: d}))(t)
	eur := newAccount(t, e, user, "EUR", "bank", "EUR", "0")
	must(e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: user, AccountID: eur.ID(), CategoryID: catSalary, Type: "income", Amount: "3", Date: d}))(t)

	rep := must(e.svc.Yearly(ctx, user, d.Year()))(t)
	m := rep.Months[d.Month()-1]
	if m.Income.Amount() != 500000 || m.Expense.Amount() != 16000 || rep.Net.Amount() != 484000 {
		t.Fatalf("yearly = %+v net %v", m, rep.Net)
	}
	if len(rep.Unconverted) != 1 || rep.Unconverted[0].Currency.Code() != "EUR" {
		t.Fatalf("unconverted = %+v", rep.Unconverted)
	}
	if err := e.svc.DeleteRate(ctx, user, r.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.DeleteRate(ctx, user, r.ID()), domain.ErrRateNotFound)
}

func TestGoalsAndDebts(t *testing.T) {
	e := setup(t)
	ctx, user, bob := t.Context(), uuid.New(), uuid.New()
	d := today()
	cash := newAccount(t, e, user, "Cash", "cash", "", "1000000")
	save := newAccount(t, e, user, "Tabungan", "bank", "", "0")

	g := must(e.svc.CreateGoal(ctx, app.CreateGoalInput{UserID: user, Name: "Laptop", Target: "300000", AccountID: new(save.ID()), TargetDate: new(d.AddDate(0, 6, 0))}))(t)
	_, err := e.svc.CreateGoal(ctx, app.CreateGoalInput{UserID: user, Name: "LAPTOP", Target: "1"})
	wantErr(t, err, domain.ErrDuplicateName)
	_, err = e.svc.GetGoal(ctx, bob, g.Goal.ID())
	wantErr(t, err, domain.ErrGoalNotFound)
	tr := must(e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: user, FromAccountID: cash.ID(), ToAccountID: save.ID(), Amount: "200000", Date: d}))(t)
	trID := tr.ID()
	_, gg := must2(e.svc.Contribute(ctx, app.ContributeInput{UserID: user, GoalID: g.Goal.ID(), TransferID: &trID}))(t)
	if gg.Progress.Saved != 200000 {
		t.Fatalf("saved = %d", gg.Progress.Saved)
	}
	_, _, err = e.svc.Contribute(ctx, app.ContributeInput{UserID: user, GoalID: g.Goal.ID(), TransferID: &trID})
	wantErr(t, err, domain.ErrDuplicateLink)
	_, _, err = e.svc.Contribute(ctx, app.ContributeInput{UserID: bob, GoalID: g.Goal.ID(), Amount: "1", Date: d})
	wantErr(t, err, domain.ErrGoalNotFound)
	c, gg := must2(e.svc.Contribute(ctx, app.ContributeInput{UserID: user, GoalID: g.Goal.ID(), Amount: "100000", Date: d, Note: "bonus"}))(t)
	if gg.Goal.Status() != domain.GoalAchieved {
		t.Fatal("achieved")
	}
	must2(e.svc.Contribute(ctx, app.ContributeInput{UserID: user, GoalID: g.Goal.ID(), Amount: "50000", Date: d, Withdraw: true}))(t)
	if l := must(e.svc.ListContributions(ctx, user, g.Goal.ID()))(t); len(l) != 3 || l[0].Amount.Currency() != money.IDR {
		t.Fatalf("contributions = %d", len(l))
	}
	goals := must(e.svc.ListGoals(ctx, user))(t)
	if len(goals) != 1 || goals[0].Progress.Saved != 250000 || goals[0].Goal.Status() != domain.GoalActive {
		t.Fatalf("goals = %+v", goals[0].Progress)
	}
	up := must(e.svc.UpdateGoal(ctx, app.UpdateGoalInput{UserID: user, ID: g.Goal.ID(), Name: new("MacBook"), Target: new("250000"), ClearTargetDate: true}))(t)
	if up.Goal.Status() != domain.GoalAchieved || up.Goal.TargetDate() != nil {
		t.Fatal("update goal")
	}
	_, err = e.svc.UpdateGoal(ctx, app.UpdateGoalInput{UserID: user, ID: g.Goal.ID(), ExpectedVersion: new(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	wantErr(t, e.svc.DeleteContribution(ctx, bob, g.Goal.ID(), c.ID), domain.ErrGoalNotFound)
	must0(t, e.svc.DeleteContribution(ctx, user, g.Goal.ID(), c.ID))
	wantErr(t, e.svc.DeleteContribution(ctx, user, g.Goal.ID(), c.ID), domain.ErrContributionNotFound)
	must0(t, e.svc.DeleteGoal(ctx, user, g.Goal.ID()))
	wantErr(t, e.svc.DeleteGoal(ctx, user, g.Goal.ID()), domain.ErrGoalNotFound)

	// debts
	debt := must(e.svc.CreateDebt(ctx, app.CreateDebtInput{UserID: user, Direction: "payable", Counterparty: "Budi", Principal: "100000", StartDate: d, DueDate: new(d.AddDate(0, 1, 0))}))(t)
	_, err = e.svc.GetDebt(ctx, bob, debt.Debt.ID())
	wantErr(t, err, domain.ErrDebtNotFound)
	p1, _ := must2(e.svc.PayDebt(ctx, app.PayDebtInput{UserID: user, DebtID: debt.Debt.ID(), Amount: "40000", Date: d, AccountID: new(cash.ID()), CategoryID: new(catFood)}))(t)
	if p1.TransactionID == nil || balance(t, e, user, cash.ID()).Amount() != 760000 {
		t.Fatal("pay via tx")
	}
	_, _, err = e.svc.PayDebt(ctx, app.PayDebtInput{UserID: user, DebtID: debt.Debt.ID(), Date: d, TransactionID: p1.TransactionID})
	wantErr(t, err, domain.ErrDuplicateLink)
	_, _, err = e.svc.PayDebt(ctx, app.PayDebtInput{UserID: user, DebtID: debt.Debt.ID(), Amount: "60001", Date: d})
	wantErr(t, err, domain.ErrOverpayment)
	last, dd := must2(e.svc.PayDebt(ctx, app.PayDebtInput{UserID: user, DebtID: debt.Debt.ID(), Amount: "60000", Date: d}))(t)
	if dd.Debt.Status() != domain.DebtSettled {
		t.Fatal("settled")
	}
	if l := must(e.svc.ListDebts(ctx, user, "settled"))(t); len(l) != 1 || l[0].Paid != 100000 {
		t.Fatal("list settled")
	}
	if l := must(e.svc.ListDebts(ctx, user, "open"))(t); len(l) != 0 {
		t.Fatal("list open")
	}
	if l := must(e.svc.ListDebtPayments(ctx, user, debt.Debt.ID()))(t); len(l) != 2 {
		t.Fatal("payments")
	}
	must0(t, e.svc.DeleteDebtPayment(ctx, user, debt.Debt.ID(), last.ID))
	wantErr(t, e.svc.DeleteDebtPayment(ctx, user, debt.Debt.ID(), last.ID), domain.ErrDebtNotFound)
	upd := must(e.svc.UpdateDebt(ctx, app.UpdateDebtInput{UserID: user, ID: debt.Debt.ID(), Counterparty: new("Budi S"), ClearDueDate: true}))(t)
	if upd.Debt.Status() != domain.DebtOpen || upd.Debt.DueDate() != nil || upd.Paid != 40000 {
		t.Fatal("update debt")
	}
	_, err = e.svc.UpdateDebt(ctx, app.UpdateDebtInput{UserID: user, ID: debt.Debt.ID(), ExpectedVersion: new(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	must0(t, e.svc.DeleteDebt(ctx, user, debt.Debt.ID()))
	wantErr(t, e.svc.DeleteDebt(ctx, user, debt.Debt.ID()), domain.ErrDebtNotFound)
}

func TestBillsAndWorker(t *testing.T) {
	e := setup(t)
	ctx, user := t.Context(), uuid.New()
	d := today()
	cash := newAccount(t, e, user, "Cash", "cash", "", "1000000")
	cashID := cash.ID()
	soon := must(e.svc.CreateBill(ctx, app.CreateBillInput{UserID: user, Name: "Listrik", Amount: "200000", AccountID: &cashID,
		CategoryID: new(catFood), Frequency: "monthly", DueDate: d.AddDate(0, 0, 2)}))(t)
	late := must(e.svc.CreateBill(ctx, app.CreateBillInput{UserID: user, Name: "Pajak", Amount: "100", Frequency: "once", DueDate: d.AddDate(0, 0, -3)}))(t)
	must(e.svc.CreateBill(ctx, app.CreateBillInput{UserID: user, Name: "Nanti", Amount: "1", Frequency: "yearly", DueDate: d.AddDate(0, 2, 0)}))(t)
	_, err := e.svc.GetBill(ctx, uuid.New(), soon.ID())
	wantErr(t, err, domain.ErrBillNotFound)

	n := must(e.svc.ProcessBills(ctx, time.Now(), 50))(t)
	k := e.notices.kinds(user)
	if n < 2 || k["Listrik"] != app.BillDueSoon || k["Pajak"] != app.BillOverdue || k["Nanti"] != "" {
		t.Fatalf("notices = %d %v", n, k)
	}
	if again := must(e.svc.ProcessBills(ctx, time.Now(), 50))(t); again != 0 {
		t.Fatalf("again = %d", again)
	}
	got := must(e.svc.GetBill(ctx, user, late.ID()))(t)
	if got.OverdueFor() == nil || !got.OverdueFor().Equal(got.NextDueDate()) {
		t.Fatal("overdue_for")
	}

	paid, tx := must2(e.svc.PayBill(ctx, app.PayBillInput{UserID: user, ID: soon.ID(), CreateTransaction: true}))(t)
	if tx == nil || !paid.NextDueDate().Equal(soon.NextDueDate().AddDate(0, 1, 0)) || paid.LastRemindedFor() != nil {
		t.Fatalf("pay = %v", paid.NextDueDate())
	}
	done, _ := must2(e.svc.PayBill(ctx, app.PayBillInput{UserID: user, ID: late.ID()}))(t)
	if done.Status() != domain.BillDone {
		t.Fatal("done")
	}
	up := must(e.svc.UpdateBill(ctx, app.UpdateBillInput{UserID: user, ID: soon.ID(), Amount: new("250000"), ClearAccount: true, Paused: new(true)}))(t)
	if up.Status() != domain.BillPaused || up.AccountID() != nil {
		t.Fatal("update bill")
	}
	_, err = e.svc.UpdateBill(ctx, app.UpdateBillInput{UserID: user, ID: soon.ID(), ExpectedVersion: new(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	if l := must(e.svc.ListBills(ctx, user))(t); len(l) != 3 {
		t.Fatal("list")
	}
	must0(t, e.svc.DeleteBill(ctx, user, soon.ID()))
	wantErr(t, e.svc.DeleteBill(ctx, user, soon.ID()), domain.ErrBillNotFound)
}

// TestSharedWallet_IDOR: akses member diatur role; non-member selalu 404 dan
// semua perubahan tercatat di audit log (keyset pagination).
func TestSharedWallet_IDORAndAudit(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	owner, editor, viewer, outsider := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	e.users["viewer@x.io"] = viewer
	d := today()
	acc := newAccount(t, e, owner, "Keluarga", "cash", "", "1000000")
	private := newAccount(t, e, owner, "Pribadi", "bank", "", "0")
	edAcc := newAccount(t, e, editor, "Milik Editor", "cash", "", "0")

	must(e.svc.AddMember(ctx, app.AddMemberInput{OwnerID: owner, AccountID: acc.ID(), MemberID: new(editor), Role: "editor"}))(t)
	must(e.svc.AddMember(ctx, app.AddMemberInput{OwnerID: owner, AccountID: acc.ID(), Email: "viewer@x.io", Role: "viewer"}))(t)
	_, err := e.svc.AddMember(ctx, app.AddMemberInput{OwnerID: owner, AccountID: acc.ID(), MemberID: new(editor), Role: "viewer"})
	wantErr(t, err, domain.ErrMemberExists)
	_, err = e.svc.AddMember(ctx, app.AddMemberInput{OwnerID: owner, AccountID: acc.ID(), Email: "nobody@x.io", Role: "viewer"})
	wantErr(t, err, domain.ErrUserNotFound)
	_, err = e.svc.AddMember(ctx, app.AddMemberInput{OwnerID: editor, AccountID: acc.ID(), MemberID: new(outsider), Role: "viewer"})
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.AddMember(ctx, app.AddMemberInput{OwnerID: outsider, AccountID: acc.ID(), MemberID: new(uuid.New()), Role: "viewer"})
	wantErr(t, err, domain.ErrAccountNotFound)

	ownTx := must(e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: owner, AccountID: acc.ID(), CategoryID: catFood, Type: "expense", Amount: "1000", Date: d}))(t)
	privTx := must(e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: owner, AccountID: private.ID(), CategoryID: catSalary, Type: "income", Amount: "5000", Date: d}))(t)
	tr := must(e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: owner, FromAccountID: acc.ID(), ToAccountID: private.ID(), Amount: "100", Date: d}))(t)

	// outsider: 404 di semua titik
	_, err = e.svc.GetAccount(ctx, outsider, acc.ID())
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = e.svc.GetTransaction(ctx, outsider, ownTx.ID())
	wantErr(t, err, domain.ErrTransactionNotFound)
	_, err = e.svc.GetTransfer(ctx, outsider, tr.ID())
	wantErr(t, err, domain.ErrTransferNotFound)
	_, err = e.svc.ListMembers(ctx, outsider, acc.ID())
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: outsider, AccountID: acc.ID(), CategoryID: catFood, Type: "expense", Amount: "1", Date: d})
	wantErr(t, err, domain.ErrAccountNotFound)
	wantErr(t, e.svc.DeleteTransaction(ctx, outsider, ownTx.ID()), domain.ErrTransactionNotFound)

	// member tidak bisa melihat akun/transaksi yang tidak dibagikan
	_, err = e.svc.GetAccount(ctx, editor, private.ID())
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = e.svc.GetTransaction(ctx, editor, privTx.ID())
	wantErr(t, err, domain.ErrTransactionNotFound)

	// viewer: baca saja
	if a := must(e.svc.GetAccount(ctx, viewer, acc.ID()))(t); a.ID() != acc.ID() {
		t.Fatal("viewer read")
	}
	must(e.svc.GetTransaction(ctx, viewer, ownTx.ID()))(t)
	must(e.svc.GetTransfer(ctx, viewer, tr.ID()))(t)
	if l := must(e.svc.ListTransactions(ctx, app.ListTransactionsInput{UserID: viewer, AccountIDs: []uuid.UUID{acc.ID()}}))(t); len(l) != 1 {
		t.Fatalf("viewer list = %d", len(l))
	}
	_, err = e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: viewer, AccountID: acc.ID(), CategoryID: catFood, Type: "expense", Amount: "1", Date: d})
	wantErr(t, err, domain.ErrForbidden)
	wantErr(t, e.svc.DeleteTransaction(ctx, viewer, ownTx.ID()), domain.ErrForbidden)

	// editor: tulis atas nama owner; tidak boleh mencampur akun milik sendiri
	etx := must(e.svc.CreateTransaction(ctx, app.CreateTransactionInput{UserID: editor, AccountID: acc.ID(), CategoryID: catFood, Type: "expense", Amount: "2000", Date: d}))(t)
	if etx.UserID() != owner {
		t.Fatal("owner")
	}
	must(e.svc.UpdateTransaction(ctx, app.UpdateTransactionInput{UserID: editor, ID: ownTx.ID(), Amount: new("1500")}))(t)
	_, err = e.svc.CreateTransfer(ctx, app.CreateTransferInput{UserID: editor, FromAccountID: acc.ID(), ToAccountID: edAcc.ID(), Amount: "1", Date: d})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = e.svc.UpdateAccount(ctx, app.UpdateAccountInput{UserID: editor, ID: acc.ID(), Name: new("x")})
	wantErr(t, err, domain.ErrForbidden)
	if b := balance(t, e, owner, acc.ID()); b.Amount() != 1000000-1500-2000-100 {
		t.Fatalf("balance = %v", b)
	}

	shared := must(e.svc.ListSharedAccounts(ctx, viewer))(t)
	if len(shared) != 1 || shared[0].Role != domain.RoleViewer || shared[0].Account.ID() != acc.ID() {
		t.Fatalf("shared = %+v", shared)
	}
	must(e.svc.UpdateMemberRole(ctx, owner, acc.ID(), viewer, "editor"))(t)
	if l := must(e.svc.ListMembers(ctx, viewer, acc.ID()))(t); len(l) != 2 {
		t.Fatal("members")
	}
	must0(t, e.svc.RemoveMember(ctx, viewer, acc.ID(), viewer)) // leave
	_, err = e.svc.GetAccount(ctx, viewer, acc.ID())
	wantErr(t, err, domain.ErrAccountNotFound)
	wantErr(t, e.svc.RemoveMember(ctx, owner, acc.ID(), viewer), domain.ErrMemberNotFound)
	must0(t, e.svc.RemoveMember(ctx, owner, acc.ID(), editor))
	_, err = e.svc.GetTransaction(ctx, editor, etx.ID())
	wantErr(t, err, domain.ErrTransactionNotFound)

	// audit log: entri editor dicatat pada data owner, keyset DESC
	all := must(e.svc.ListAuditLogs(ctx, owner, domain.AuditFilter{Limit: 100}))(t)
	byEditor := 0
	for _, a := range all {
		if a.ActorID == editor {
			byEditor++
		}
		if strings.Contains(string(a.After), "password") {
			t.Fatal("secret")
		}
	}
	if byEditor != 2 || len(all) < 10 {
		t.Fatalf("audit = %d (editor %d)", len(all), byEditor)
	}
	page := must(e.svc.ListAuditLogs(ctx, owner, domain.AuditFilter{Limit: 3}))(t)
	if len(page) != 4 {
		t.Fatalf("limit+1 = %d", len(page))
	}
	next := must(e.svc.ListAuditLogs(ctx, owner, domain.AuditFilter{Limit: 3, After: &domain.AuditCursor{At: page[2].CreatedAt, ID: page[2].ID}}))(t)
	if next[0].ID != page[3].ID {
		t.Fatal("keyset")
	}
	ent := must(e.svc.ListAuditLogs(ctx, owner, domain.AuditFilter{Entity: app.EntityTransaction, EntityID: new(ownTx.ID())}))(t)
	if len(ent) != 2 || ent[0].Action != domain.AuditUpdate || len(ent[0].Before) == 0 {
		t.Fatalf("entity filter = %d", len(ent))
	}
	if l := must(e.svc.ListAuditLogs(ctx, outsider, domain.AuditFilter{}))(t); len(l) != 0 {
		t.Fatal("audit IDOR")
	}
}

func must2[A, B any](a A, b B, err error) func(t *testing.T) (A, B) {
	return func(t *testing.T) (A, B) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return a, b
	}
}

func must0(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
