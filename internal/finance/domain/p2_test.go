package domain

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestParseRateAndConvert(t *testing.T) {
	for _, bad := range []string{"", "0", "-1", "+1", "1e3", "1/2", "1.", "1.12345678901", "abc", "1000000000"} {
		if _, err := ParseRate(bad); err == nil {
			t.Errorf("ParseRate(%q) harus gagal", bad)
		} else {
			wantValidation(t, err, "rate")
		}
	}
	r, err := ParseRate(" 15800.5 ")
	if err != nil || RateString(r) != "15800.5" {
		t.Fatalf("rate = %v %v", r, err)
	}
	if RateString(big.NewRat(3, 1)) != "3" || RateString(Invert(big.NewRat(16000, 1))) != "0.0000625" {
		t.Fatal("RateString")
	}

	user := uuid.New()
	_, err = NewExchangeRate(uuid.New(), user, money.Currency{}, idr, "1", tNow, tNow)
	wantErr(t, err, money.ErrUnknownCurrency)
	_, err = NewExchangeRate(uuid.New(), user, idr, idr, "1", tNow, tNow)
	wantValidation(t, err, "quote")
	_, err = NewExchangeRate(uuid.New(), user, usd, idr, "x", tNow, tNow)
	wantValidation(t, err, "rate")
	_, err = NewExchangeRate(uuid.New(), user, usd, idr, "1", day(1960, 1, 1), tNow)
	wantErr(t, err, ErrDateTooOld)

	e, err := NewExchangeRate(uuid.New(), user, usd, idr, "15800.5", tNow, tNow)
	if err != nil {
		t.Fatal(err)
	}
	// 1.23 USD = 123 sen -> 1.23*15800.5 = 19434.615 -> 19435 (half-up)
	got, err := e.Convert(money.New(123, usd))
	if err != nil || got.Amount() != 19435 || got.Currency() != idr {
		t.Fatalf("convert = %v %v", got, err)
	}
	neg, _ := e.Convert(money.New(-123, usd))
	if neg.Amount() != -19435 {
		t.Fatalf("neg = %v", neg)
	}
	_, err = e.Convert(money.New(1, idr))
	wantErr(t, err, money.ErrCurrencyMismatch)
	// IDR -> USD (eksponen turun): 16000 IDR * 1/16000 = 1 USD = 100 sen
	back, err := ConvertAmount(money.New(16000, idr), Invert(big.NewRat(16000, 1)), usd)
	if err != nil || back.Amount() != 100 {
		t.Fatalf("back = %v %v", back, err)
	}
	half, _ := ConvertAmount(money.New(1, usd), big.NewRat(1, 2), idr) // 0.005 IDR -> 0
	if half.Amount() != 0 {
		t.Fatalf("half = %v", half)
	}
	_, err = ConvertAmount(money.New(1<<62, idr), big.NewRat(999999999, 1), usd)
	wantErr(t, err, money.ErrAmountOverflow)

	if err := e.SetRate("16000"); err != nil || e.RateString() != "16000" {
		t.Fatal(err)
	}
	wantValidation(t, e.SetRate("0"), "rate")
	cp := e.Rate()
	cp.SetInt64(1)
	if e.RateString() != "16000" {
		t.Fatal("Rate harus copy")
	}
	re := RehydrateExchangeRate(ExchangeRateState{ID: e.ID(), UserID: e.UserID(), Base: e.Base(), Quote: e.Quote(),
		Rate: e.Rate(), AsOf: tNow, CreatedAt: e.CreatedAt()})
	if re.AsOf() != day(2026, 3, 15) || re.Base() != usd || re.Quote() != idr {
		t.Fatal("rehydrate")
	}
}

func TestSavingsGoal(t *testing.T) {
	user := uuid.New()
	acc := newAcc(t, user, AccountBank, 0, idr)
	other := newAcc(t, uuid.New(), AccountBank, 0, idr)
	usdAcc := newAcc(t, user, AccountBank, 0, usd)
	target := money.New(12000, idr)
	td := day(2026, 7, 1)

	_, err := NewSavingsGoal(NewGoalParams{ID: uuid.New(), UserID: user, Name: " ", Target: target, Now: tNow})
	wantValidation(t, err, "name")
	_, err = NewSavingsGoal(NewGoalParams{ID: uuid.New(), UserID: user, Name: "x", Target: money.New(0, idr), Now: tNow})
	if err == nil {
		t.Fatal("target 0")
	}
	_, err = NewSavingsGoal(NewGoalParams{ID: uuid.New(), UserID: user, Name: "x", Target: target, TargetDate: ptrT(day(1900, 1, 1)), Now: tNow})
	wantErr(t, err, ErrDateTooOld)
	_, err = NewSavingsGoal(NewGoalParams{ID: uuid.New(), UserID: user, Name: "x", Target: target, Account: other, Now: tNow})
	wantErr(t, err, ErrAccountNotFound)
	_, err = NewSavingsGoal(NewGoalParams{ID: uuid.New(), UserID: user, Name: "x", Target: target, Account: usdAcc, Now: tNow})
	wantErr(t, err, money.ErrCurrencyMismatch)

	g, err := NewSavingsGoal(NewGoalParams{ID: uuid.New(), UserID: user, Name: " Rumah ", Target: target, TargetDate: &td, Account: acc, Now: tNow})
	if err != nil || g.Name() != "Rumah" || g.Status() != GoalActive || *g.AccountID() != acc.ID() || g.Version() != 1 {
		t.Fatalf("goal: %v", err)
	}
	p := g.Progress(3000, tNow)
	if p.Remaining != 9000 || p.MonthlyNeeded != 2250 || p.Percent != "25.00" {
		t.Fatalf("progress = %+v", p)
	}
	if p := g.Progress(1000, day(2026, 8, 1)); p.MonthlyNeeded != 11000 {
		t.Fatalf("lewat target = %+v", p)
	}
	if p := g.Progress(13000, tNow); p.Remaining != 0 || p.MonthlyNeeded != 0 {
		t.Fatalf("lebih = %+v", p)
	}
	if !g.Refresh(12000, tNow) || g.Status() != GoalAchieved || g.Refresh(12000, tNow) {
		t.Fatal("refresh achieved")
	}
	if !g.Refresh(10, tNow) || g.Status() != GoalActive {
		t.Fatal("refresh active")
	}

	// contribution
	from := newAcc(t, user, AccountCash, 50000, idr)
	tr, err := NewTransfer(NewTransferParams{ID: uuid.New(), UserID: user, From: from, To: acc, Amount: money.New(5000, idr), Date: tNow, Now: tNow, Location: jakarta})
	if err != nil {
		t.Fatal(err)
	}
	back, _ := NewTransfer(NewTransferParams{ID: uuid.New(), UserID: user, From: acc, To: from, Amount: money.New(2000, idr), Date: tNow, Now: tNow, Location: jakarta})
	base := NewContributionParams{ID: uuid.New(), Goal: g, Date: tNow, Now: tNow, Location: jakarta}
	c := base
	c.Transfer = tr
	got, err := NewGoalContribution(c)
	if err != nil || got.Amount.Amount() != 5000 || *got.TransferID != tr.ID() {
		t.Fatalf("contrib transfer: %v", err)
	}
	c.Transfer, c.Withdraw = back, true
	if got, err := NewGoalContribution(c); err != nil || got.Amount.Amount() != -2000 {
		t.Fatalf("withdraw transfer: %v", err)
	}
	c.Transfer = tr
	_, err = NewGoalContribution(c)
	wantValidation(t, err, "transfer_id")
	c.Withdraw, c.Transfer = false, back
	_, err = NewGoalContribution(c)
	wantValidation(t, err, "transfer_id")
	foreign, _ := NewTransfer(NewTransferParams{ID: uuid.New(), UserID: other.UserID(), From: other, To: newAcc(t, other.UserID(), AccountCash, 0, idr),
		Amount: money.New(1, idr), Date: tNow, Now: tNow, Location: jakarta})
	c.Transfer = foreign
	_, err = NewGoalContribution(c)
	wantErr(t, err, ErrTransferNotFound)

	c = base
	c.Amount = money.New(100, idr)
	c.Note = "  gajian "
	if got, err := NewGoalContribution(c); err != nil || got.Note != "gajian" || got.GoalID != g.ID() {
		t.Fatalf("manual: %v", err)
	}
	c.Withdraw = true
	_, err = NewGoalContribution(c)
	wantErr(t, err, ErrInvalidAmount)
	c.Amount = money.New(-100, idr)
	if _, err := NewGoalContribution(c); err != nil {
		t.Fatal(err)
	}
	c.Withdraw, c.Amount = false, money.New(0, idr)
	_, err = NewGoalContribution(c)
	wantErr(t, err, ErrInvalidAmount)
	c.Amount = money.New(MaxAmount+1, idr)
	_, err = NewGoalContribution(c)
	wantErr(t, err, ErrAmountTooLarge)
	c.Amount = money.New(1, usd)
	_, err = NewGoalContribution(c)
	wantErr(t, err, money.ErrCurrencyMismatch)
	c.Amount, c.Date = money.New(1, idr), day(2027, 1, 1)
	_, err = NewGoalContribution(c)
	if err == nil {
		t.Fatal("tanggal masa depan")
	}
	c.Date, c.Note = tNow, strings.Repeat("n", 1000)
	_, err = NewGoalContribution(c)
	if err == nil {
		t.Fatal("note panjang")
	}

	// update
	wantValidation(t, g.Update(GoalChange{Name: ptrS(""), Now: tNow}), "name")
	wantErr(t, g.Update(GoalChange{Target: ptrM(money.New(1, usd)), Now: tNow}), money.ErrCurrencyMismatch)
	if g.Update(GoalChange{Target: ptrM(money.New(-1, idr)), Now: tNow}) == nil {
		t.Fatal("target negatif")
	}
	wantValidation(t, g.Update(GoalChange{TargetDate: ptrT(day(2200, 1, 1)), Now: tNow}), "target_date")
	wantErr(t, g.Update(GoalChange{Account: other, Now: tNow}), ErrAccountNotFound)
	if err := g.Update(GoalChange{Name: ptrS("Rumah 2"), Target: ptrM(money.New(20000, idr)), ClearTargetDate: true, ClearAccount: true, Now: tNow}); err != nil {
		t.Fatal(err)
	}
	if g.TargetDate() != nil || g.AccountID() != nil || g.Target().Amount() != 20000 || g.Name() != "Rumah 2" {
		t.Fatal("update tidak diterapkan")
	}
	if err := g.Update(GoalChange{TargetDate: &td, Account: acc, Archived: ptrB(true), Now: tNow}); err != nil || g.Status() != GoalArchived {
		t.Fatal(err)
	}
	if g.Refresh(99999, tNow) {
		t.Fatal("archived tidak di-refresh")
	}
	wantErr(t, g.EnsureContributable(), ErrGoalArchived)
	c = base
	c.Amount = money.New(1, idr)
	_, err = NewGoalContribution(c)
	wantErr(t, err, ErrGoalArchived)
	if err := g.Update(GoalChange{Archived: ptrB(false), Now: tNow}); err != nil || g.Status() != GoalActive {
		t.Fatal(err)
	}
	if err := g.Update(GoalChange{Archived: ptrB(false), Now: tNow}); err != nil || g.Status() != GoalActive {
		t.Fatal(err)
	}
	g.SyncVersion(4)
	r := RehydrateSavingsGoal(SavingsGoalState{ID: g.ID(), UserID: g.UserID(), Name: g.Name(), Target: g.Target(),
		TargetDate: g.TargetDate(), AccountID: g.AccountID(), Status: g.Status(), Version: g.Version(), CreatedAt: g.CreatedAt(), UpdatedAt: g.UpdatedAt()})
	if r.Version() != 4 || !r.TargetDate().Equal(td) || r.UpdatedAt() != g.UpdatedAt() {
		t.Fatal("rehydrate")
	}
}

func TestDebt(t *testing.T) {
	user := uuid.New()
	base := NewDebtParams{ID: uuid.New(), UserID: user, Direction: "payable", Counterparty: "  Budi   Santoso ",
		Principal: money.New(10000, idr), StartDate: tNow, Now: tNow, Location: jakarta}
	bad := func(mut func(*NewDebtParams), field string) {
		t.Helper()
		p := base
		mut(&p)
		_, err := NewDebt(p)
		if field == "" {
			if err == nil {
				t.Fatal("harus gagal")
			}
			return
		}
		wantValidation(t, err, field)
	}
	bad(func(p *NewDebtParams) { p.Direction = "x" }, "direction")
	bad(func(p *NewDebtParams) { p.Counterparty = " " }, "counterparty")
	bad(func(p *NewDebtParams) { p.Counterparty = strings.Repeat("a", 101) }, "counterparty")
	bad(func(p *NewDebtParams) { p.Principal = money.New(0, idr) }, "")
	bad(func(p *NewDebtParams) { p.StartDate = day(2030, 1, 1) }, "")
	bad(func(p *NewDebtParams) { p.DueDate = ptrT(day(2026, 1, 1)) }, "due_date")
	bad(func(p *NewDebtParams) { p.DueDate = ptrT(day(2200, 1, 1)) }, "due_date")
	bad(func(p *NewDebtParams) { p.Note = strings.Repeat("n", 1000) }, "")

	due := day(2026, 4, 1)
	p := base
	p.DueDate = &due
	d, err := NewDebt(p)
	if err != nil || d.Counterparty() != "Budi Santoso" || d.Status() != DebtOpen || d.Direction() != DebtPayable {
		t.Fatalf("debt: %v", err)
	}
	if d.IsOverdue(tNow) || !d.IsOverdue(day(2026, 4, 2)) {
		t.Fatal("overdue")
	}
	if d.Remaining(3000) != 7000 || d.Remaining(20000) != 0 {
		t.Fatal("remaining")
	}

	cash := newAcc(t, user, AccountCash, 100000, idr)
	exp := newTx(t, user, cash, sysCat(TxExpense), TxExpense, 4000)
	inc := newTx(t, user, cash, sysCat(TxIncome), TxIncome, 4000)
	pp := NewDebtPaymentParams{ID: uuid.New(), Debt: d, Date: tNow, Now: tNow, Location: jakarta}
	q := pp
	q.Transaction = exp
	pay, err := NewDebtPayment(q)
	if err != nil || pay.Amount.Amount() != 4000 || *pay.TransactionID != exp.ID() {
		t.Fatalf("pay tx: %v", err)
	}
	q.Transaction = inc
	_, err = NewDebtPayment(q)
	wantValidation(t, err, "transaction_id")
	oc := newAcc(t, uuid.New(), AccountCash, 100000, idr)
	q.Transaction = newTx(t, oc.UserID(), oc, sysCat(TxExpense), TxExpense, 1)
	_, err = NewDebtPayment(q)
	wantErr(t, err, ErrTransactionNotFound)

	q = pp
	q.Amount, q.Paid = money.New(7000, idr), 4000
	_, err = NewDebtPayment(q)
	wantErr(t, err, ErrOverpayment)
	q.Amount = money.New(1, usd)
	_, err = NewDebtPayment(q)
	wantErr(t, err, money.ErrCurrencyMismatch)
	q.Amount = money.New(0, idr)
	if _, err = NewDebtPayment(q); err == nil {
		t.Fatal("amount 0")
	}
	q.Amount, q.Date = money.New(1, idr), day(2030, 1, 1)
	if _, err = NewDebtPayment(q); err == nil {
		t.Fatal("tanggal")
	}
	q.Date, q.Note = tNow, strings.Repeat("n", 1000)
	if _, err = NewDebtPayment(q); err == nil {
		t.Fatal("note")
	}

	if !d.ApplyPayments(10000, tNow) || d.Status() != DebtSettled || d.ApplyPayments(10000, tNow) {
		t.Fatal("settle")
	}
	_, err = NewDebtPayment(pp)
	wantErr(t, err, ErrDebtSettled)
	if d.IsOverdue(day(2027, 1, 1)) {
		t.Fatal("settled tidak overdue")
	}

	// update
	wantValidation(t, d.Update(DebtChange{Counterparty: ptrS(""), Now: tNow}), "counterparty")
	wantErr(t, d.Update(DebtChange{Principal: ptrM(money.New(1, usd)), Now: tNow}), money.ErrCurrencyMismatch)
	if d.Update(DebtChange{Principal: ptrM(money.New(0, idr)), Now: tNow}) == nil {
		t.Fatal("principal 0")
	}
	wantValidation(t, d.Update(DebtChange{Principal: ptrM(money.New(5, idr)), Paid: 10, Now: tNow}), "principal")
	wantValidation(t, d.Update(DebtChange{DueDate: ptrT(day(2026, 1, 1)), Now: tNow}), "due_date")
	if d.Update(DebtChange{Note: ptrS(strings.Repeat("n", 1000)), Now: tNow}) == nil {
		t.Fatal("note")
	}
	if err := d.Update(DebtChange{Counterparty: ptrS("Ani"), Principal: ptrM(money.New(20000, idr)), Paid: 10000,
		DueDate: ptrT(day(2026, 5, 1)), Note: ptrS("x"), Now: tNow}); err != nil {
		t.Fatal(err)
	}
	if d.Status() != DebtOpen || d.Note() != "x" || !d.DueDate().Equal(day(2026, 5, 1)) {
		t.Fatal("update")
	}
	if err := d.Update(DebtChange{ClearDueDate: true, Paid: 20000, Now: tNow}); err != nil || d.DueDate() != nil || d.Status() != DebtSettled {
		t.Fatal(err)
	}

	// receivable -> income
	p = base
	p.Direction = "receivable"
	r, _ := NewDebt(p)
	q = NewDebtPaymentParams{ID: uuid.New(), Debt: r, Transaction: inc, Date: tNow, Now: tNow, Location: jakarta}
	if _, err := NewDebtPayment(q); err != nil {
		t.Fatal(err)
	}
	d.SyncVersion(3)
	rh := RehydrateDebt(DebtState{ID: d.ID(), UserID: d.UserID(), Direction: d.Direction(), Counterparty: d.Counterparty(),
		Principal: d.Principal(), StartDate: d.StartDate(), DueDate: d.DueDate(), Note: d.Note(), Status: d.Status(),
		Version: d.Version(), CreatedAt: d.CreatedAt(), UpdatedAt: d.UpdatedAt()})
	if rh.Version() != 3 || rh.Principal() != d.Principal() || rh.UpdatedAt() != d.UpdatedAt() {
		t.Fatal("rehydrate")
	}
}

func TestBill(t *testing.T) {
	user := uuid.New()
	acc := newAcc(t, user, AccountCash, 0, idr)
	exp := RehydrateCategory(CategoryState{ID: uuid.New(), Type: TxExpense, Name: "Listrik"})
	inc := sysCat(TxIncome)
	base := NewBillParams{ID: uuid.New(), UserID: user, Name: "Listrik", Amount: money.New(250000, idr), Frequency: "monthly",
		DueDate: day(2026, 1, 31), Now: tNow}
	bad := func(mut func(*NewBillParams), want error) {
		t.Helper()
		p := base
		mut(&p)
		_, err := NewBill(p)
		if want == nil {
			if err == nil {
				t.Fatal("harus gagal")
			}
			return
		}
		wantErr(t, err, want)
	}
	bad(func(p *NewBillParams) { p.Name = "" }, nil)
	bad(func(p *NewBillParams) { p.Amount = money.New(0, idr) }, nil)
	bad(func(p *NewBillParams) { p.Frequency = "daily" }, ErrInvalidFrequency)
	bad(func(p *NewBillParams) { p.DueDate = day(1900, 1, 1) }, nil)
	bad(func(p *NewBillParams) { p.RemindDays = ptrI(31) }, nil)
	bad(func(p *NewBillParams) { p.Account = newAcc(t, uuid.New(), AccountCash, 0, idr) }, ErrAccountNotFound)
	bad(func(p *NewBillParams) { p.Account = newAcc(t, user, AccountCash, 0, usd) }, money.ErrCurrencyMismatch)
	bad(func(p *NewBillParams) { p.Category = inc }, ErrCategoryTypeMismatch)
	bad(func(p *NewBillParams) {
		p.Category = RehydrateCategory(CategoryState{ID: uuid.New(), UserID: ptrU(uuid.New()), Type: TxExpense, Name: "x"})
	}, ErrCategoryNotFound)

	p := base
	p.Account, p.Category = acc, exp
	b, err := NewBill(p)
	if err != nil || b.RemindDays() != 3 || b.MonthDay() != 31 || *b.AccountID() != acc.ID() || *b.CategoryID() != exp.ID() {
		t.Fatalf("bill: %v", err)
	}
	if !b.RemindAt().Equal(day(2026, 1, 28)) || !b.ShouldRemind(tNow) || !b.IsOverdue(tNow) {
		t.Fatal("remind/overdue")
	}
	if !b.MarkOverdue(tNow, tNow) || b.MarkOverdue(tNow, tNow) || !b.OverdueFor().Equal(day(2026, 1, 31)) {
		t.Fatal("mark overdue")
	}
	b.MarkReminded(tNow)
	if b.ShouldRemind(tNow) {
		t.Fatal("sudah diingatkan")
	}
	if err := b.MarkPaid(tNow); err != nil || !b.NextDueDate().Equal(day(2026, 2, 28)) || b.LastRemindedFor() != nil || b.OverdueFor() != nil {
		t.Fatalf("paid: %v %v", err, b.NextDueDate())
	}
	_ = b.MarkPaid(tNow)
	if !b.NextDueDate().Equal(day(2026, 3, 31)) {
		t.Fatalf("month day dipertahankan: %v", b.NextDueDate())
	}
	if b.IsOverdue(tNow) || b.MarkOverdue(tNow, tNow) || b.ShouldRemind(tNow) {
		t.Fatal("belum jatuh tempo")
	}

	// update
	wantErr(t, b.Update(BillChange{Amount: ptrM(money.New(1, usd)), Now: tNow}), money.ErrCurrencyMismatch)
	if b.Update(BillChange{Amount: ptrM(money.New(0, idr)), Now: tNow}) == nil ||
		b.Update(BillChange{Name: ptrS(""), Now: tNow}) == nil ||
		b.Update(BillChange{RemindDays: ptrI(-1), Now: tNow}) == nil ||
		b.Update(BillChange{DueDate: ptrT(day(1900, 1, 1)), Now: tNow}) == nil {
		t.Fatal("update invalid harus gagal")
	}
	wantErr(t, b.Update(BillChange{Frequency: ptrS("hourly"), Now: tNow}), ErrInvalidFrequency)
	wantErr(t, b.Update(BillChange{Category: inc, Now: tNow}), ErrCategoryTypeMismatch)
	if err := b.Update(BillChange{Name: ptrS("PLN"), Amount: ptrM(money.New(1, idr)), ClearAccount: true, ClearCategory: true,
		Frequency: ptrS("once"), DueDate: ptrT(day(2026, 3, 20)), RemindDays: ptrI(5), Paused: ptrB(true), Now: tNow}); err != nil {
		t.Fatal(err)
	}
	if b.Status() != BillPaused || b.AccountID() != nil || b.CategoryID() != nil || b.Frequency() != FreqOnce || b.MonthDay() != 20 || b.Name() != "PLN" {
		t.Fatal("update tidak diterapkan")
	}
	if b.ShouldRemind(tNow) || b.IsOverdue(day(2026, 4, 1)) {
		t.Fatal("paused")
	}
	_ = b.Update(BillChange{Paused: ptrB(false), Now: tNow})
	if !b.ShouldRemind(tNow) {
		t.Fatal("remind 5 hari")
	}
	if err := b.MarkPaid(tNow); err != nil || b.Status() != BillDone {
		t.Fatal(err)
	}
	wantErr(t, b.MarkPaid(tNow), ErrBillDone)
	wantErr(t, b.Update(BillChange{Now: tNow}), ErrBillDone)
	b.SyncVersion(7)
	rh := RehydrateBill(BillState{ID: b.ID(), UserID: b.UserID(), Name: b.Name(), Amount: b.Amount(), AccountID: b.AccountID(),
		CategoryID: b.CategoryID(), Frequency: b.Frequency(), MonthDay: b.MonthDay(), NextDue: b.NextDueDate(), RemindDays: b.RemindDays(),
		LastReminded: nil, OverdueFor: ptrT(tNow), Status: b.Status(), Version: b.Version(), CreatedAt: b.CreatedAt(), UpdatedAt: b.UpdatedAt()})
	if rh.Version() != 7 || !rh.OverdueFor().Equal(day(2026, 3, 15)) || rh.UpdatedAt() != b.UpdatedAt() {
		t.Fatal("rehydrate")
	}
}

func TestMemberRoles(t *testing.T) {
	if _, err := ParseMemberRole("owner"); err == nil {
		t.Fatal("owner tidak bisa diberikan")
	}
	r, err := ParseMemberRole("viewer")
	if err != nil || !r.CanRead() || r.CanWrite() || r.CanManage() {
		t.Fatal("viewer")
	}
	if !RoleEditor.CanWrite() || RoleEditor.CanManage() || !RoleOwner.CanManage() || MemberRole("x").CanRead() {
		t.Fatal("roles")
	}
	owner := uuid.New()
	acc := newAcc(t, owner, AccountCash, 0, idr)
	_, err = NewAccountMember(nil, owner, uuid.New(), "editor", tNow)
	wantErr(t, err, ErrAccountNotFound)
	_, err = NewAccountMember(acc, uuid.New(), uuid.New(), "editor", tNow)
	wantErr(t, err, ErrAccountNotFound)
	_, err = NewAccountMember(acc, owner, owner, "editor", tNow)
	wantValidation(t, err, "member_id")
	_, err = NewAccountMember(acc, owner, uuid.Nil, "editor", tNow)
	wantValidation(t, err, "member_id")
	_, err = NewAccountMember(acc, owner, uuid.New(), "admin", tNow)
	wantErr(t, err, ErrInvalidRole)
	m, err := NewAccountMember(acc, owner, uuid.New(), "editor", tNow)
	if err != nil || m.AccountID != acc.ID() || m.Role != RoleEditor {
		t.Fatal(err)
	}
}

func ptrT(v time.Time) *time.Time     { return &v }
func ptrS(v string) *string           { return &v }
func ptrB(v bool) *bool               { return &v }
func ptrI(v int) *int                 { return &v }
func ptrU(v uuid.UUID) *uuid.UUID     { return &v }
func ptrM(v money.Money) *money.Money { return &v }
