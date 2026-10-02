package app

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

func (f *fixture) spend(t *testing.T, acc *domain.Account, cat uuid.UUID, amount string, tags ...uuid.UUID) *domain.Transaction {
	t.Helper()
	tx, err := f.svc.CreateTransaction(context.Background(), CreateTransactionInput{UserID: f.user, AccountID: acc.ID(),
		CategoryID: cat, Type: "expense", Amount: amount, Date: today, TagIDs: tags})
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	return tx
}

func TestBudget_CRUDAndProgress(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.account(t, "cash", "1000000")
	child, err := f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "Kopi", ParentID: ptr(f.expense.ID())})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.svc.CreateBudget(ctx, CreateBudgetInput{UserID: f.user, CategoryID: f.expense.ID(), Amount: "1000"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Budget.Month().Month() != 3 || b.Budget.Threshold() != 80 || b.Budget.Amount().Currency() != idr || b.Progress.Spent != 0 {
		t.Fatalf("budget = %+v", b)
	}
	// duplikat (kategori, bulan) -> 409
	_, err = f.svc.CreateBudget(ctx, CreateBudgetInput{UserID: f.user, CategoryID: f.expense.ID(), Month: "2026-03", Amount: "5"})
	wantErr(t, err, domain.ErrBudgetExists)

	// spent mencakup sub kategori; alert warning lalu exceeded masing-masing sekali.
	f.spend(t, acc, f.expense.ID(), "500")
	if len(f.alerts.alerts) != 0 {
		t.Fatal("belum lewat threshold")
	}
	f.spend(t, acc, child.ID(), "350")
	f.spend(t, acc, child.ID(), "10")
	if len(f.alerts.alerts) != 1 || f.alerts.alerts[0].Level != "warning" || f.alerts.alerts[0].Spent != 850 {
		t.Fatalf("alerts = %+v", f.alerts.alerts)
	}
	f.spend(t, acc, f.expense.ID(), "200")
	f.spend(t, acc, f.expense.ID(), "1")
	if len(f.alerts.alerts) != 2 || f.alerts.alerts[1].Level != "exceeded" {
		t.Fatalf("alerts = %+v", f.alerts.alerts)
	}
	got, err := f.svc.GetBudget(ctx, f.user, b.Budget.ID())
	if err != nil || got.Progress.Spent != 1061 || !got.Progress.Overspent || got.Progress.Remaining != -61 {
		t.Fatalf("progress = %+v err %v", got.Progress, err)
	}
	list, err := f.svc.ListBudgets(ctx, f.user, "")
	if err != nil || len(list) != 1 || list[0].Progress.Spent != 1061 {
		t.Fatalf("list = %+v", list)
	}
	if l, _ := f.svc.ListBudgets(ctx, f.user, "2026-02"); len(l) != 0 {
		t.Fatal("bulan lain harus kosong")
	}
	_, err = f.svc.ListBudgets(ctx, f.user, "bad")
	if err == nil {
		t.Fatal("month invalid")
	}

	// update nominal: level dievaluasi ulang tanpa notifikasi.
	upd, err := f.svc.UpdateBudget(ctx, UpdateBudgetInput{UserID: f.user, ID: b.Budget.ID(), Amount: ptr("5000"), Threshold: ptr(90),
		ExpectedVersion: ptr(got.Budget.Version())})
	if err != nil || upd.Progress.Overspent || upd.Budget.LastAlert() != domain.AlertNone || len(f.alerts.alerts) != 2 {
		t.Fatalf("update = %+v err %v", upd, err)
	}
	_, err = f.svc.UpdateBudget(ctx, UpdateBudgetInput{UserID: f.user, ID: b.Budget.ID(), ExpectedVersion: ptr(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	_, err = f.svc.UpdateBudget(ctx, UpdateBudgetInput{UserID: f.user, ID: b.Budget.ID(), Amount: ptr("x")})
	wantErr(t, err, money.ErrInvalidAmount)
	_, err = f.svc.UpdateBudget(ctx, UpdateBudgetInput{UserID: f.user, ID: b.Budget.ID(), Threshold: ptr(101)})
	wantErr(t, err, &domain.ValidationError{Field: "alert_threshold_pct"})

	// IDOR: user lain -> not found.
	other := uuid.New()
	_, err = f.svc.GetBudget(ctx, other, b.Budget.ID())
	wantErr(t, err, domain.ErrBudgetNotFound)
	wantErr(t, f.svc.DeleteBudget(ctx, other, b.Budget.ID()), domain.ErrBudgetNotFound)
	_, err = f.svc.UpdateBudget(ctx, UpdateBudgetInput{UserID: other, ID: b.Budget.ID()})
	wantErr(t, err, domain.ErrBudgetNotFound)

	if err := f.svc.DeleteBudget(ctx, f.user, b.Budget.ID()); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.GetBudget(ctx, f.user, b.Budget.ID())
	wantErr(t, err, domain.ErrBudgetNotFound)
}

func TestBudget_CreateErrorsAndAlertScope(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cases := []struct {
		name string
		in   CreateBudgetInput
		want error
	}{
		{"income cat", CreateBudgetInput{CategoryID: f.income.ID(), Amount: "1"}, domain.ErrCategoryTypeMismatch},
		{"no cat", CreateBudgetInput{CategoryID: uuid.New(), Amount: "1"}, domain.ErrCategoryNotFound},
		{"bad amount", CreateBudgetInput{CategoryID: f.expense.ID(), Amount: "-1"}, domain.ErrInvalidAmount},
		{"bad currency", CreateBudgetInput{CategoryID: f.expense.ID(), Amount: "1", Currency: "XXX"}, money.ErrUnknownCurrency},
		{"bad month", CreateBudgetInput{CategoryID: f.expense.ID(), Amount: "1", Month: "2026-13"}, nil},
		{"bad threshold", CreateBudgetInput{CategoryID: f.expense.ID(), Amount: "1", Threshold: 200}, &domain.ValidationError{Field: "alert_threshold_pct"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.UserID = f.user
			_, err := f.svc.CreateBudget(ctx, c.in)
			if c.want == nil {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			wantErr(t, err, c.want)
		})
	}
	f.ids.err = errBoom
	_, err := f.svc.CreateBudget(ctx, CreateBudgetInput{UserID: f.user, CategoryID: f.expense.ID(), Amount: "1"})
	wantErr(t, err, errBoom)
	f.ids.err = nil
	f.db.failOn["budgets.spent"] = errBoom
	_, err = f.svc.CreateBudget(ctx, CreateBudgetInput{UserID: f.user, CategoryID: f.expense.ID(), Amount: "1"})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "budgets.spent")

	// Budget USD & bulan lalu tidak memicu alert dari expense IDR bulan ini.
	if _, err := f.svc.CreateBudget(ctx, CreateBudgetInput{UserID: f.user, CategoryID: f.expense.ID(), Amount: "1", Currency: "USD"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateBudget(ctx, CreateBudgetInput{UserID: f.user, CategoryID: f.expense.ID(), Month: "2026-02", Amount: "1"}); err != nil {
		t.Fatal(err)
	}
	acc := f.account(t, "cash", "1000")
	f.spend(t, acc, f.expense.ID(), "500")
	if _, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.expense.ID(),
		Type: "expense", Amount: "1", Date: today.AddDate(0, -1, 0)}); err != nil {
		t.Fatal(err)
	}
	if len(f.alerts.alerts) != 0 {
		t.Fatalf("alerts = %+v", f.alerts.alerts)
	}
	// Gagal membaca budget -> transaksi di-rollback.
	f.db.failOn["budgets.list"] = errBoom
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.expense.ID(),
		Type: "expense", Amount: "1", Date: today})
	wantErr(t, err, errBoom)
	if got := f.balance(t, acc.ID()); got != 499 {
		t.Fatalf("balance = %d", got)
	}
	delete(f.db.failOn, "budgets.list")

	// Budget IDR yang melewati threshold tapi gagal update -> error.
	child, err := f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "Jajan", ParentID: ptr(f.expense.ID())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateBudget(ctx, CreateBudgetInput{UserID: f.user, CategoryID: child.ID(), Amount: "1"}); err != nil {
		t.Fatal(err)
	}
	f.db.failOn["budgets.update"] = errBoom
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: child.ID(),
		Type: "expense", Amount: "1", Date: today})
	wantErr(t, err, errBoom)
}

func TestTags(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, err := f.svc.CreateTag(ctx, f.user, " Liburan ", "#ff0000")
	if err != nil || a.Name() != "Liburan" || a.Color() != "#FF0000" {
		t.Fatalf("tag %+v err %v", a, err)
	}
	_, err = f.svc.CreateTag(ctx, f.user, "liburan", "")
	wantErr(t, err, domain.ErrDuplicateName)
	_, err = f.svc.CreateTag(ctx, f.user, "", "")
	wantErr(t, err, &domain.ValidationError{Field: "name"})
	f.ids.err = errBoom
	_, err = f.svc.CreateTag(ctx, f.user, "x", "")
	wantErr(t, err, errBoom)
	f.ids.err = nil
	b, _ := f.svc.CreateTag(ctx, f.user, "kantor", "")

	upd, err := f.svc.UpdateTag(ctx, UpdateTagInput{UserID: f.user, ID: b.ID(), Name: ptr("kerja")})
	if err != nil || upd.Name() != "kerja" {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateTag(ctx, UpdateTagInput{UserID: f.user, ID: b.ID(), Name: ptr("LIBURAN")})
	wantErr(t, err, domain.ErrDuplicateName)
	_, err = f.svc.UpdateTag(ctx, UpdateTagInput{UserID: f.user, ID: b.ID(), Color: ptr("red")})
	wantErr(t, err, &domain.ValidationError{Field: "color"})
	_, err = f.svc.UpdateTag(ctx, UpdateTagInput{UserID: uuid.New(), ID: b.ID()})
	wantErr(t, err, domain.ErrTagNotFound)

	acc := f.account(t, "cash", "1000")
	tx := f.spend(t, acc, f.expense.ID(), "1", a.ID(), a.ID())
	m, err := f.svc.TransactionTags(ctx, f.user, []uuid.UUID{tx.ID()})
	if err != nil || len(m[tx.ID()]) != 1 {
		t.Fatalf("tags = %v", m)
	}
	// filter ?tag= (case-insensitive)
	if l, _ := f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: f.user, Tag: "LIBURAN"}); len(l) != 1 {
		t.Fatalf("filter tag = %d", len(l))
	}
	if l, _ := f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: f.user, Tag: "kerja"}); len(l) != 0 {
		t.Fatal("filter tag lain harus kosong")
	}

	// tag milik user lain / tidak ada -> 404, transaksi tidak dibuat.
	foreign, _ := f.svc.CreateTag(ctx, uuid.New(), "asing", "")
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.expense.ID(),
		Type: "expense", Amount: "1", Date: today, TagIDs: []uuid.UUID{foreign.ID()}})
	wantErr(t, err, domain.ErrTagNotFound)
	if got := f.balance(t, acc.ID()); got != 999 {
		t.Fatalf("rollback: balance %d", got)
	}
	many := make([]uuid.UUID, 11)
	for i := range many {
		many[i] = uuid.New()
	}
	_, err = f.svc.SetTransactionTags(ctx, f.user, tx.ID(), many)
	wantErr(t, err, domain.ErrTooManyTags)
	_, err = f.svc.SetTransactionTags(ctx, uuid.New(), tx.ID(), nil)
	wantErr(t, err, domain.ErrTransactionNotFound)
	f.db.failOn["tags.set"] = errBoom
	_, err = f.svc.SetTransactionTags(ctx, f.user, tx.ID(), []uuid.UUID{b.ID()})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "tags.set")
	set, err := f.svc.SetTransactionTags(ctx, f.user, tx.ID(), []uuid.UUID{b.ID(), a.ID()})
	if err != nil || len(set) != 2 {
		t.Fatal(err)
	}
	if err := f.svc.DeleteTag(ctx, f.user, a.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteTag(ctx, f.user, a.ID()), domain.ErrTagNotFound)
	m, _ = f.svc.TransactionTags(ctx, f.user, []uuid.UUID{tx.ID()})
	if len(m[tx.ID()]) != 1 {
		t.Fatalf("relasi tag terhapus harus hilang: %v", m)
	}
	if l, err := f.svc.ListTags(ctx, f.user); err != nil || len(l) != 1 {
		t.Fatalf("list = %v", l)
	}
}
