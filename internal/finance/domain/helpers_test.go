package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

var (
	idr     = money.MustCurrency("IDR")
	usd     = money.MustCurrency("USD")
	tNow    = time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	jakarta = mustLoc("Asia/Jakarta")
)

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func newAcc(t *testing.T, user uuid.UUID, typ AccountType, initial int64, cur money.Currency) *Account {
	t.Helper()
	a, err := NewAccount(NewAccountParams{ID: uuid.New(), UserID: user, Name: "Dompet " + string(typ),
		Type: typ, InitialBalance: money.New(initial, cur), Now: tNow})
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	return a
}

func sysCat(typ TxType) *Category {
	return RehydrateCategory(CategoryState{ID: uuid.New(), Type: typ, Name: "Sys " + string(typ)})
}

func newTx(t *testing.T, user uuid.UUID, acc *Account, cat *Category, typ TxType, amount int64) *Transaction {
	t.Helper()
	tx, err := NewTransaction(NewTransactionParams{ID: uuid.New(), UserID: user, Account: acc, Category: cat,
		Type: typ, Amount: money.New(amount, acc.Currency()), Date: tNow, Now: tNow, Location: jakarta})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	return tx
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func wantValidation(t *testing.T, err error, field string) {
	t.Helper()
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok || ve.Field != field {
		t.Fatalf("err = %v, want ValidationError on %q", err, field)
	}
}
