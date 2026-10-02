package app

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

func TestCreateTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.account(t, "cash", "1000")
	tx, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.expense.ID(),
		Type: "expense", Amount: "250", Date: today, Note: "makan"})
	if err != nil || tx.Amount().Amount() != 250 {
		t.Fatal(err)
	}
	if got := f.balance(t, acc.ID()); got != 750 {
		t.Fatalf("balance = %d", got)
	}
	if _, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.income.ID(),
		Type: "income", Amount: "50", Date: today}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   CreateTransactionInput
		want error
	}{
		{"overspend", CreateTransactionInput{AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "801", Date: today}, domain.ErrInsufficientBalance},
		{"bad type", CreateTransactionInput{AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "x", Amount: "1", Date: today}, domain.ErrInvalidTxType},
		{"cat mismatch", CreateTransactionInput{AccountID: acc.ID(), CategoryID: f.income.ID(), Type: "expense", Amount: "1", Date: today}, domain.ErrCategoryTypeMismatch},
		{"no cat", CreateTransactionInput{AccountID: acc.ID(), CategoryID: uuid.New(), Type: "expense", Amount: "1", Date: today}, domain.ErrCategoryNotFound},
		{"no acc", CreateTransactionInput{AccountID: uuid.New(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Date: today}, domain.ErrAccountNotFound},
		{"bad amount", CreateTransactionInput{AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1,5", Date: today}, money.ErrInvalidAmount},
		{"future", CreateTransactionInput{AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Date: today.AddDate(0, 0, 5)}, domain.ErrDateInFuture},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.UserID = f.user
			_, err := f.svc.CreateTransaction(ctx, c.in)
			wantErr(t, err, c.want)
		})
	}
	if got := f.balance(t, acc.ID()); got != 800 {
		t.Fatalf("balance berubah walau gagal: %d", got)
	}
	// rollback: gagal simpan akun -> transaksi tidak tersimpan
	f.db.failOn["accounts.update"] = errBoom
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Date: today})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "accounts.update")
	if list, _ := f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: f.user}); len(list) != 2 {
		t.Fatalf("rollback gagal: %d", len(list))
	}
	f.db.failOn["transactions.create"] = errBoom
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Date: today})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "transactions.create")
	f.ids.err = errBoom
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Date: today})
	wantErr(t, err, errBoom)
	f.ids.err = nil
	f.db.failOn["settings.get"] = errBoom
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), Type: "expense"})
	wantErr(t, err, errBoom)
}

func TestUpdateDeleteTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.account(t, "cash", "1000")
	b := f.account(t, "cash", "0")
	tx, _ := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: a.ID(), CategoryID: f.expense.ID(),
		Type: "expense", Amount: "1000", Date: today})
	// 1000 -> 400: state antara tidak dicek, akhir 600
	up, err := f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID(), ExpectedVersion: ptr(1), Amount: ptr("400"), Note: ptr("n")})
	if err != nil || up.Version() != 2 {
		t.Fatal(err)
	}
	if got := f.balance(t, a.ID()); got != 600 {
		t.Fatalf("a = %d", got)
	}
	// pindah akun + jadi income
	if _, err := f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID(), AccountID: ptr(b.ID()),
		CategoryID: ptr(f.income.ID()), Type: ptr("income"), Date: ptr(today.AddDate(0, 0, -1))}); err != nil {
		t.Fatal(err)
	}
	if f.balance(t, a.ID()) != 1000 || f.balance(t, b.ID()) != 400 {
		t.Fatalf("a=%d b=%d", f.balance(t, a.ID()), f.balance(t, b.ID()))
	}
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID(), ExpectedVersion: ptr(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID(), Type: ptr("x")})
	wantErr(t, err, domain.ErrInvalidTxType)
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID(), Amount: ptr("x")})
	wantErr(t, err, money.ErrInvalidAmount)
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID(), Type: ptr("expense")})
	wantErr(t, err, domain.ErrCategoryTypeMismatch)
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID(), CategoryID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrCategoryNotFound)
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID(), AccountID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: uuid.New(), ID: tx.ID()})
	wantErr(t, err, domain.ErrTransactionNotFound)
	// b sudah dibelanjakan -> revert income ditolak
	spend, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: b.ID(), CategoryID: f.expense.ID(),
		Type: "expense", Amount: "300", Date: today})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID(), Amount: ptr("100")})
	wantErr(t, err, domain.ErrInsufficientBalance)
	wantErr(t, f.svc.DeleteTransaction(ctx, f.user, tx.ID()), domain.ErrInsufficientBalance)
	if err := f.svc.DeleteTransaction(ctx, f.user, spend.ID()); err != nil {
		t.Fatal(err)
	}
	f.db.failOn["transactions.delete"] = errBoom
	wantErr(t, f.svc.DeleteTransaction(ctx, f.user, tx.ID()), errBoom)
	delete(f.db.failOn, "transactions.delete")
	if f.balance(t, b.ID()) != 400 {
		t.Fatalf("rollback: %d", f.balance(t, b.ID()))
	}
	f.db.failOn["settings.get"] = errBoom
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: tx.ID()})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "settings.get")
	// arsip -> delete ditolak
	if _, err := f.svc.ArchiveAccount(ctx, f.user, a.ID()); err != nil {
		t.Fatal(err)
	}
	ta, _ := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: b.ID(), CategoryID: f.income.ID(), Type: "income", Amount: "5", Date: today})
	wantErr(t, f.svc.DeleteTransaction(ctx, uuid.New(), ta.ID()), domain.ErrTransactionNotFound)
	if err := f.svc.DeleteTransaction(ctx, f.user, ta.ID()); err != nil {
		t.Fatal(err)
	}
	// fee tx read-only
	fee := domain.RehydrateTransaction(domain.TransactionState{ID: uuid.New(), UserID: f.user, AccountID: b.ID(), CategoryID: f.fee.ID(),
		Type: domain.TxExpense, Amount: money.New(1, idr), Source: domain.SourceTransferFee, Version: 1})
	f.db.txs[fee.ID()] = *fee
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: f.user, ID: fee.ID()})
	wantErr(t, err, domain.ErrManagedByTransfer)
	wantErr(t, f.svc.DeleteTransaction(ctx, f.user, fee.ID()), domain.ErrManagedByTransfer)
}

func TestListTransactions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.account(t, "cash", "1000")
	for _, amt := range []string{"10", "200"} {
		if _, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: a.ID(), CategoryID: f.expense.ID(),
			Type: "expense", Amount: amt, Date: today}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: f.user, Type: "expense", MinAmount: "100", MaxAmount: "500"})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %d", err, len(got))
	}
	from, to := today, today.AddDate(0, 0, -1)
	_, err = f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: f.user, From: &from, To: &to})
	wantErr(t, err, domain.ErrInvalidRange)
	_, err = f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: f.user, Type: "x"})
	wantErr(t, err, domain.ErrInvalidTxType)
	for _, in := range []ListTransactionsInput{
		{MinAmount: "abc"}, {MaxAmount: "abc"}, {MinAmount: "10", MaxAmount: "5"},
	} {
		in.UserID = f.user
		_, err := f.svc.ListTransactions(ctx, in)
		if _, ok := errors.AsType[*domain.ValidationError](err); !ok {
			t.Fatalf("%+v: err = %v", in, err)
		}
	}
	_, err = f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: f.user, Currency: "ZZZ"})
	wantErr(t, err, money.ErrUnknownCurrency)
}
