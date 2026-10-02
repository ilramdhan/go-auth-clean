package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

func (f *fixture) rule(t *testing.T, acc *domain.Account, typ, amount, freq string, start time.Time) *domain.RecurringRule {
	t.Helper()
	cat := f.expense.ID()
	if typ == "income" {
		cat = f.income.ID()
	}
	r, err := f.svc.CreateRecurringRule(context.Background(), CreateRecurringInput{UserID: f.user, AccountID: acc.ID(),
		CategoryID: cat, Type: typ, Amount: amount, Frequency: freq, StartDate: start, Note: "langganan"})
	if err != nil {
		t.Fatalf("CreateRecurringRule: %v", err)
	}
	return r
}

func (f *fixture) countRecurringTx(ruleID uuid.UUID) int {
	n := 0
	for _, tx := range f.db.txs { //nolint:gocritic // test
		if tx.RecurringRuleID() != nil && *tx.RecurringRuleID() == ruleID {
			n++
		}
	}
	return n
}

func TestRecurring_CRUD(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.account(t, "bank", "1000")
	r := f.rule(t, acc, "expense", "10", "monthly", today)
	if r.NextRunDate() != today || r.Status() != domain.RuleActive {
		t.Fatalf("rule %+v", r)
	}
	cases := []struct {
		name string
		in   CreateRecurringInput
		want error
	}{
		{"bad type", CreateRecurringInput{AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "x", Amount: "1", Frequency: "daily", StartDate: today}, domain.ErrInvalidTxType},
		{"no acc", CreateRecurringInput{AccountID: uuid.New(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Frequency: "daily", StartDate: today}, domain.ErrAccountNotFound},
		{"no cat", CreateRecurringInput{AccountID: acc.ID(), CategoryID: uuid.New(), Type: "expense", Amount: "1", Frequency: "daily", StartDate: today}, domain.ErrCategoryNotFound},
		{"bad amount", CreateRecurringInput{AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "x", Frequency: "daily", StartDate: today}, money.ErrInvalidAmount},
		{"bad freq", CreateRecurringInput{AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Frequency: "hourly", StartDate: today}, domain.ErrInvalidFrequency},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.UserID = f.user
			_, err := f.svc.CreateRecurringRule(ctx, c.in)
			wantErr(t, err, c.want)
		})
	}
	f.ids.err = errBoom
	_, err := f.svc.CreateRecurringRule(ctx, CreateRecurringInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Frequency: "daily", StartDate: today})
	wantErr(t, err, errBoom)
	f.ids.err = nil

	got, err := f.svc.GetRecurringRule(ctx, f.user, r.ID())
	if err != nil || got.ID() != r.ID() {
		t.Fatal(err)
	}
	_, err = f.svc.GetRecurringRule(ctx, uuid.New(), r.ID())
	wantErr(t, err, domain.ErrRecurringNotFound)
	if l, err := f.svc.ListRecurringRules(ctx, f.user, 20, nil); err != nil || len(l) != 1 {
		t.Fatalf("list %v", err)
	}

	upd, err := f.svc.UpdateRecurringRule(ctx, UpdateRecurringInput{UserID: f.user, ID: r.ID(), ExpectedVersion: ptr(1),
		Amount: ptr("20"), Type: ptr("income"), CategoryID: ptr(f.income.ID()), Frequency: ptr("weekly"), Count: ptr(3)})
	if err != nil || upd.Amount().Amount() != 20 || upd.Frequency() != domain.FreqWeekly || upd.EndDate() == nil ||
		!upd.EndDate().Equal(today.AddDate(0, 0, 14)) {
		t.Fatalf("update %+v err %v", upd, err)
	}
	_, err = f.svc.UpdateRecurringRule(ctx, UpdateRecurringInput{UserID: f.user, ID: r.ID(), ExpectedVersion: ptr(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	_, err = f.svc.UpdateRecurringRule(ctx, UpdateRecurringInput{UserID: f.user, ID: r.ID(), Type: ptr("bad")})
	wantErr(t, err, domain.ErrInvalidTxType)
	_, err = f.svc.UpdateRecurringRule(ctx, UpdateRecurringInput{UserID: f.user, ID: r.ID(), Amount: ptr("bad")})
	wantErr(t, err, money.ErrInvalidAmount)
	_, err = f.svc.UpdateRecurringRule(ctx, UpdateRecurringInput{UserID: f.user, ID: r.ID(), AccountID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.UpdateRecurringRule(ctx, UpdateRecurringInput{UserID: f.user, ID: r.ID(), CategoryID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrCategoryNotFound)
	_, err = f.svc.UpdateRecurringRule(ctx, UpdateRecurringInput{UserID: uuid.New(), ID: r.ID()})
	wantErr(t, err, domain.ErrRecurringNotFound)
	// ganti ke akun USD tanpa amount baru -> currency mismatch
	usdAcc, err := f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "USD", Type: "bank", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateRecurringRule(ctx, UpdateRecurringInput{UserID: f.user, ID: r.ID(), AccountID: ptr(usdAcc.ID())})
	wantErr(t, err, money.ErrCurrencyMismatch)

	p, err := f.svc.PauseRecurringRule(ctx, f.user, r.ID(), "cuti")
	if err != nil || p.Status() != domain.RulePaused || p.PauseReason() != "cuti" {
		t.Fatal(err)
	}
	_, err = f.svc.PauseRecurringRule(ctx, f.user, r.ID(), strings.Repeat("x", 300))
	wantErr(t, err, &domain.ValidationError{Field: "reason"})
	res, err := f.svc.ResumeRecurringRule(ctx, f.user, r.ID())
	if err != nil || res.Status() != domain.RuleActive || res.PauseReason() != "" {
		t.Fatal(err)
	}
	_, err = f.svc.ResumeRecurringRule(ctx, uuid.New(), r.ID())
	wantErr(t, err, domain.ErrRecurringNotFound)
	f.db.failOn["recurring.update"] = errBoom
	_, err = f.svc.PauseRecurringRule(ctx, f.user, r.ID(), "")
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "recurring.update")

	wantErr(t, f.svc.DeleteRecurringRule(ctx, uuid.New(), r.ID()), domain.ErrRecurringNotFound)
	if err := f.svc.DeleteRecurringRule(ctx, f.user, r.ID()); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.GetRecurringRule(ctx, f.user, r.ID())
	wantErr(t, err, domain.ErrRecurringNotFound)
}

func TestGenerateDue_CatchUpAndIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.account(t, "bank", "0")
	start := today.AddDate(0, 0, -40) // 41 kemunculan harian s/d hari ini
	r := f.rule(t, acc, "income", "10", "daily", start)
	future := f.rule(t, acc, "income", "10", "daily", today.AddDate(0, 0, 3))

	n, err := f.svc.GenerateDue(ctx, testNow, 0)
	if err != nil || n != domain.MaxCatchUp {
		t.Fatalf("n = %d err %v", n, err)
	}
	before := f.db.rules[r.ID()]
	n, err = f.svc.GenerateDue(ctx, testNow, 10)
	if err != nil || n != 41-domain.MaxCatchUp {
		t.Fatalf("catch-up kedua n = %d err %v", n, err)
	}
	if got := f.balance(t, acc.ID()); got != 410 {
		t.Fatalf("balance = %d", got)
	}
	got := f.db.rules[r.ID()]
	if !got.NextRunDate().Equal(today.AddDate(0, 0, 1)) || !got.LastRunDate().Equal(today) {
		t.Fatalf("next %v last %v", got.NextRunDate(), got.LastRunDate())
	}
	if f.countRecurringTx(future.ID()) != 0 {
		t.Fatal("rule masa depan tidak boleh jalan")
	}

	// Simulasi crash setelah insert tapi sebelum next_run tersimpan: rule
	// dikembalikan ke state lama -> occurrence tidak dobel, saldo tidak berubah.
	before.SyncVersion(got.Version())
	f.db.rules[r.ID()] = before
	n, err = f.svc.GenerateDue(ctx, testNow, 10)
	if err != nil || n != 0 || f.countRecurringTx(r.ID()) != 41 || f.balance(t, acc.ID()) != 410 {
		t.Fatalf("idempotent n=%d count=%d bal=%d err %v", n, f.countRecurringTx(r.ID()), f.balance(t, acc.ID()), err)
	}
	if n, _ := f.svc.GenerateDue(ctx, testNow, 10); n != 0 {
		t.Fatal("tidak ada yang jatuh tempo")
	}
}

func TestGenerateDue_PauseOnBusinessError(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cash := f.account(t, "cash", "25")
	r := f.rule(t, cash, "expense", "10", "daily", today.AddDate(0, 0, -4)) // 5 x 10 > 25
	n, err := f.svc.GenerateDue(ctx, testNow, 10)
	if err != nil || n != 2 {
		t.Fatalf("n = %d err %v", n, err)
	}
	got := f.db.rules[r.ID()]
	if got.Status() != domain.RulePaused || !strings.Contains(got.PauseReason(), "insufficient") || f.balance(t, cash.ID()) != 5 {
		t.Fatalf("status %s reason %q bal %d", got.Status(), got.PauseReason(), f.balance(t, cash.ID()))
	}
	if !got.NextRunDate().Equal(today.AddDate(0, 0, -2)) {
		t.Fatalf("next = %v", got.NextRunDate())
	}

	// akun diarsip -> rule di-pause, tidak ada transaksi.
	bank := f.account(t, "bank", "0")
	r2 := f.rule(t, bank, "income", "1", "monthly", today)
	if _, err := f.svc.ArchiveAccount(ctx, f.user, bank.ID()); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.GenerateDue(ctx, testNow, 10); err != nil || n != 0 {
		t.Fatalf("n = %d err %v", n, err)
	}
	if got := f.db.rules[r2.ID()]; got.Status() != domain.RulePaused || !strings.Contains(got.PauseReason(), "archived") {
		t.Fatalf("status %s reason %q", got.Status(), got.PauseReason())
	}
}

func TestGenerateDue_InfraErrorsRetry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.account(t, "bank", "0")
	r := f.rule(t, acc, "income", "10", "daily", today.AddDate(0, 0, -1))

	for _, op := range []string{"transactions.occurrence", "accounts.update", "recurring.update"} {
		f.db.failOn[op] = errBoom
		n, err := f.svc.GenerateDue(ctx, testNow, 10)
		wantErr(t, err, errBoom)
		delete(f.db.failOn, op)
		cur := f.db.rules[r.ID()]
		if n != 0 || f.countRecurringTx(r.ID()) != 0 || cur.Status() != domain.RuleActive {
			t.Fatalf("%s: rollback gagal n=%d", op, n)
		}
	}
	f.ids.err = errBoom
	_, err := f.svc.GenerateDue(ctx, testNow, 10)
	wantErr(t, err, errBoom)
	f.ids.err = nil
	f.db.failOn["recurring.claim"] = errBoom
	_, err = f.svc.GenerateDue(ctx, testNow, 10)
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "recurring.claim")

	// context dibatalkan -> berhenti tanpa memproses.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if n, _ := f.svc.GenerateDue(cctx, testNow, 10); n != 0 {
		t.Fatal("ctx batal")
	}
	if n, err := f.svc.GenerateDue(ctx, testNow, 10); err != nil || n != 2 {
		t.Fatalf("retry n=%d err %v", n, err)
	}
}

type countingGen struct {
	calls atomic.Int32
	err   error
}

func (g *countingGen) GenerateDue(context.Context, time.Time, int) (int, error) {
	g.calls.Add(1)
	return 1, g.err
}

func TestRecurringWorker(t *testing.T) {
	g := &countingGen{}
	w := NewRecurringWorker(g, &fixedClock{t: testNow}, 0, nil)
	if w.interval != time.Minute || w.batch != 100 {
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

	w2 := NewRecurringWorker(g, &fixedClock{t: testNow}, time.Millisecond, nil)
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

// ---- CSV ----

func TestSanitizeCSVCell(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "kopi": "kopi", "=SUM(A1)": "'=SUM(A1)", "+62": "'+62", "-5": "'-5", "@x": "'@x", "\tx": "'\tx", "\rx": "'\rx",
	} {
		if got := SanitizeCSVCell(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
		if in != "" && unsanitizeCSVCell(SanitizeCSVCell(in)) != in {
			t.Errorf("round trip %q", in)
		}
	}
	if unsanitizeCSVCell("'abc") != "'abc" {
		t.Fatal("apostrof biasa harus tetap")
	}
}

func TestExportTransactions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.account(t, "cash", "1000")
	tag, _ := f.svc.CreateTag(ctx, f.user, "kantor", "")
	if _, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.expense.ID(),
		Type: "expense", Amount: "250", Date: today, Note: "=HYPERLINK(\"evil\")", TagIDs: []uuid.UUID{tag.ID()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.income.ID(),
		Type: "income", Amount: "5", Date: today.AddDate(0, 0, -1)}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := f.svc.ExportTransactions(ctx, ListTransactionsInput{UserID: f.user}, &buf); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(recs) != 3 {
		t.Fatalf("recs %v err %v", recs, err)
	}
	if strings.Join(recs[0], ",") != strings.Join(ExportHeader, ",") {
		t.Fatalf("header %v", recs[0])
	}
	if recs[1][1] != "income" || recs[2][6] != `'=HYPERLINK("evil")` || recs[2][7] != "kantor" || recs[2][5] != "Makan" || recs[2][2] != "250" {
		t.Fatalf("rows %v", recs[1:])
	}
	// filter ikut diterapkan
	buf.Reset()
	if err := f.svc.ExportTransactions(ctx, ListTransactionsInput{UserID: f.user, Type: "income"}, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "\n") != 2 {
		t.Fatalf("filtered %q", buf.String())
	}
	err = f.svc.ExportTransactions(ctx, ListTransactionsInput{UserID: f.user, Type: "bad"}, &buf)
	wantErr(t, err, domain.ErrInvalidTxType)
	f.db.failOn["transactions.export"] = errBoom
	wantErr(t, f.svc.ExportTransactions(ctx, ListTransactionsInput{UserID: f.user}, &buf), errBoom)
	delete(f.db.failOn, "transactions.export")
	wantErr(t, f.svc.ExportTransactions(ctx, ListTransactionsInput{UserID: f.user}, failWriter{}), errBoom)

	// > 500 baris: flush periodik
	for i := range 600 {
		if _, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.income.ID(),
			Type: "income", Amount: fmt.Sprint(i + 1), Date: today}); err != nil {
			t.Fatal(err)
		}
	}
	buf.Reset()
	if err := f.svc.ExportTransactions(ctx, ListTransactionsInput{UserID: f.user}, &buf); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "\n"); n != 603 {
		t.Fatalf("lines = %d", n)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errBoom }

func TestImportTransactions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.account(t, "cash", "100")
	if _, err := f.svc.CreateTag(ctx, f.user, "kantor", ""); err != nil {
		t.Fatal(err)
	}
	name := strings.ToUpper(acc.Name())
	body := "\xef\xbb\xbfDate,Type,Amount,Currency,Account,Category,Note,Tags\n" +
		"2026-03-14,income,50,IDR," + name + ",gaji,'=bonus,#kantor\n" + // 2 ok (case-insensitive, unsanitize)
		"2026-03-14,expense,30,," + acc.ID().String() + "," + f.expense.ID().String() + ",makan,\n" + // 3 ok (by ID)
		"2026-03-14,expense,30,,X," + "makan" + ",makan,\n" + // 4 acc
		"bad,expense,1,," + name + ",makan,,\n" + // 5 date
		"2026-03-14,transfer,1,," + name + ",makan,,\n" + // 6 type
		"2026-03-14,expense,1,USD," + name + ",makan,,\n" + // 7 currency
		"2026-03-14,expense,1.5,," + name + ",makan,,\n" + // 8 amount
		"2026-03-14,expense,1,," + name + ",nope,,\n" + // 9 category
		"2026-03-14,expense,1,," + name + ",gaji,,\n" + // 10 category type mismatch
		"2026-03-14,expense,1,," + name + ",makan,,ghost\n" + // 11 tag
		"2099-01-01,expense,1,," + name + ",makan,,\n" + // 12 future
		"2026-03-14,expense,30,," + name + ",makan,makan,\n" + // 13 duplikat baris 3 di file
		"2026-03-14,expense,500,," + name + ",makan,besar,\n" // 14 saldo kurang

	res, err := f.svc.ImportTransactions(ctx, ImportInput{UserID: f.user, File: strings.NewReader(body), DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[int]string{}
	for _, e := range res.Errors {
		fields[e.Row] = e.Field
	}
	want := map[int]string{4: "account", 5: "date", 6: "type", 7: "currency", 8: "amount", 9: "category", 10: "row", 11: "tags", 12: "transaction_date", 14: "amount"}
	for row, fl := range want {
		if fields[row] == "" {
			t.Errorf("row %d: no error (errors %+v)", row, res.Errors)
		} else if fl != "row" && fl != "transaction_date" && fields[row] != fl {
			t.Errorf("row %d field %q, want %q", row, fields[row], fl)
		}
	}
	if res.Imported != 2 || res.Skipped != 1 || res.Duplicates[0] != 13 || !res.DryRun {
		t.Fatalf("res %+v", res)
	}
	if f.balance(t, acc.ID()) != 100 || len(f.db.txs) != 0 {
		t.Fatal("dry run tidak boleh menulis")
	}

	res, err = f.svc.ImportTransactions(ctx, ImportInput{UserID: f.user, File: strings.NewReader(body)})
	if err != nil || res.Imported != 2 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if f.balance(t, acc.ID()) != 120 {
		t.Fatalf("balance %d", f.balance(t, acc.ID()))
	}
	list, _ := f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: f.user, Tag: "kantor"})
	if len(list) != 1 || list[0].Note() != "=bonus" || list[0].Source() != domain.SourceImport || len(list[0].ImportHash()) != 32 {
		t.Fatalf("imported %+v", list)
	}
	// import ulang -> semua baris valid terdeteksi duplikat.
	res, err = f.svc.ImportTransactions(ctx, ImportInput{UserID: f.user, File: strings.NewReader(body)})
	if err != nil || res.Imported != 0 || res.Skipped != 3 {
		t.Fatalf("reimport %+v err %v", res, err)
	}
}

func TestImportTransactions_FileErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.account(t, "cash", "0")
	header := "date,type,amount,account,category\n"
	var many strings.Builder
	many.WriteString(header)
	for range MaxImportRows + 1 {
		many.WriteString("2026-03-14,income,1,a,b\n")
	}
	cases := []struct {
		name, body string
		want       error
	}{
		{"too large", strings.Repeat("x", MaxImportBytes+1), ErrImportTooLarge},
		{"empty", "", ErrImportBadHeader},
		{"missing col", "date,type,amount,account\n", ErrImportBadHeader},
		{"not utf8", header + "\xff\xfe\n", &domain.ValidationError{Field: "file"}},
		{"bad quote", header + "\"a,b\n", &domain.ValidationError{Field: "file"}},
		{"too many rows", many.String(), ErrImportTooManyRow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.svc.ImportTransactions(ctx, ImportInput{UserID: f.user, File: strings.NewReader(c.body)})
			wantErr(t, err, c.want)
		})
	}
	ok := header + "2026-03-14,income,1," + acc.Name() + ",gaji\n"
	for _, op := range []string{"tags.list", "transactions.hashes", "transactions.create", "accounts.update"} {
		f.db.failOn[op] = errBoom
		_, err := f.svc.ImportTransactions(ctx, ImportInput{UserID: f.user, File: strings.NewReader(ok)})
		wantErr(t, err, errBoom)
		delete(f.db.failOn, op)
	}
	if f.balance(t, acc.ID()) != 0 {
		t.Fatal("rollback import")
	}
	// batas laporan error
	var bad strings.Builder
	bad.WriteString(header)
	for range maxRowErrors + 10 {
		bad.WriteString("x,income,1,a,b\n")
	}
	res, err := f.svc.ImportTransactions(ctx, ImportInput{UserID: f.user, File: strings.NewReader(bad.String())})
	if err != nil || len(res.Errors) != maxRowErrors {
		t.Fatalf("errors %d err %v", len(res.Errors), err)
	}
}
