package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

func TestBalanceChanges(t *testing.T) {
	user := uuid.New()
	a := newAcc(t, user, AccountCash, 100, idr)
	b := newAcc(t, user, AccountCash, 0, idr)
	exp := sysCat(TxExpense)
	old := newTx(t, user, a, exp, TxExpense, 100) // saldo efektif a: 0 setelah apply
	if err := a.Apply(old); err != nil {
		t.Fatal(err)
	}
	// Edit 100 -> 60: state antara (revert dulu) tidak dicek, hanya hasil akhir.
	n, err := old.Change(ChangeTransactionParams{Account: a, Category: exp, Type: TxExpense,
		Amount: money.New(60, idr), Date: tNow, Now: tNow})
	if err != nil {
		t.Fatal(err)
	}
	ch := NewBalanceChanges()
	if err := ch.RevertTransaction(old); err != nil {
		t.Fatal(err)
	}
	if err := ch.AddTransaction(n); err != nil {
		t.Fatal(err)
	}
	if d, ok := ch.Delta(a.ID()); !ok || d.Amount() != 40 {
		t.Fatalf("delta = %v", d)
	}
	changed, err := ch.ApplyTo(map[uuid.UUID]*Account{a.ID(): a})
	if err != nil || len(changed) != 1 || a.Balance().Amount() != 40 {
		t.Fatalf("apply: %v %d", err, a.Balance().Amount())
	}
	// delta nol -> tidak ada akun yang disimpan
	same := NewBalanceChanges()
	_ = same.AddTransaction(n)
	_ = same.RevertTransaction(n)
	if changed, _ := same.ApplyTo(map[uuid.UUID]*Account{}); len(changed) != 0 {
		t.Fatal("delta nol harus di-skip")
	}
	// akun tidak dikunci
	miss := NewBalanceChanges()
	_ = miss.AddTransaction(n)
	if _, err := miss.ApplyTo(map[uuid.UUID]*Account{}); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("err = %v", err)
	}
	// transfer a->b 40 lalu revert
	tr, _ := NewTransfer(NewTransferParams{ID: uuid.New(), UserID: user, From: a, To: b, Amount: money.New(40, idr), Date: tNow, Now: tNow})
	tc := NewBalanceChanges()
	if err := tc.AddTransfer(tr); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.ApplyTo(map[uuid.UUID]*Account{a.ID(): a, b.ID(): b}); err != nil || b.Balance().Amount() != 40 {
		t.Fatalf("transfer: %v", err)
	}
	rc := NewBalanceChanges()
	_ = rc.RevertTransfer(tr)
	if _, err := rc.ApplyTo(map[uuid.UUID]*Account{a.ID(): a, b.ID(): b}); err != nil || a.Balance().Amount() != 40 {
		t.Fatalf("revert: %v", err)
	}
	// overspend ditolak
	over := NewBalanceChanges()
	big := newTx(t, user, b, sysCat(TxIncome), TxIncome, 1)
	_ = over.RevertTransaction(big)
	if _, err := over.ApplyTo(map[uuid.UUID]*Account{b.ID(): b}); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("err = %v", err)
	}
	// mismatch currency
	u := newAcc(t, user, AccountCash, 10, usd)
	mix := NewBalanceChanges()
	_ = mix.AddTransaction(n)
	ut := newTx(t, user, u, exp, TxExpense, 1)
	ut2 := RehydrateTransaction(TransactionState{ID: uuid.New(), AccountID: a.ID(), Type: TxExpense, Amount: ut.Amount()})
	if err := mix.AddTransaction(ut2); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("err = %v", err)
	}
}
