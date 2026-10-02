package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

func TestParseTxTypeAndDates(t *testing.T) {
	if _, err := ParseTxType("income"); err != nil {
		t.Fatal(err)
	}
	_, err := ParseTxType("refund")
	wantErr(t, err, ErrInvalidTxType)
	// 23:30 UTC = 06:30 WIB besok
	late := time.Date(2026, 3, 15, 23, 30, 0, 0, time.UTC)
	if got := Today(late, jakarta); got.Day() != 16 {
		t.Fatalf("today = %v", got)
	}
	if got := Today(late, nil); got.Day() != 15 {
		t.Fatalf("today utc = %v", got)
	}
}

func TestNewTransaction(t *testing.T) {
	user := uuid.New()
	acc := newAcc(t, user, AccountCash, 1000, idr)
	exp := sysCat(TxExpense)
	other := uuid.New()
	foreignCat := RehydrateCategory(CategoryState{ID: uuid.New(), UserID: &other, Type: TxExpense})
	foreignAcc := newAcc(t, other, AccountCash, 0, idr)
	archived := newAcc(t, user, AccountCash, 0, idr)
	archived.Archive(tNow)

	tx := newTx(t, user, acc, exp, TxExpense, 250)
	if tx.Source() != SourceManual || !tx.IsManual() || tx.Version() != 1 || tx.Note() != "" ||
		tx.AccountID() != acc.ID() || tx.CategoryID() != exp.ID() || tx.UserID() != user || tx.Type() != TxExpense ||
		tx.Amount().Amount() != 250 || tx.Date().Hour() != 0 || tx.ID() == uuid.Nil ||
		!tx.CreatedAt().Equal(tNow) || !tx.UpdatedAt().Equal(tNow) {
		t.Fatal("state salah")
	}
	if s, _ := tx.SignedAmount(); s.Amount() != -250 {
		t.Fatalf("signed = %d", s.Amount())
	}
	if err := tx.EnsureEditable(); err != nil {
		t.Fatal(err)
	}

	base := NewTransactionParams{ID: uuid.New(), UserID: user, Account: acc, Category: exp, Type: TxExpense,
		Amount: money.New(1, idr), Date: tNow, Now: tNow, Location: jakarta}
	cases := []struct {
		name string
		mod  func(p *NewTransactionParams)
		err  error
		fld  string
	}{
		{"bad type", func(p *NewTransactionParams) { p.Type = "x" }, ErrInvalidTxType, ""},
		{"zero", func(p *NewTransactionParams) { p.Amount = money.New(0, idr) }, ErrInvalidAmount, ""},
		{"negative", func(p *NewTransactionParams) { p.Amount = money.New(-5, idr) }, ErrInvalidAmount, ""},
		{"too large", func(p *NewTransactionParams) { p.Amount = money.New(MaxAmount+1, idr) }, ErrAmountTooLarge, ""},
		{"nil account", func(p *NewTransactionParams) { p.Account = nil }, ErrAccountNotFound, ""},
		{"foreign account", func(p *NewTransactionParams) { p.Account = foreignAcc }, ErrAccountNotFound, ""},
		{"foreign category", func(p *NewTransactionParams) { p.Category = foreignCat }, ErrCategoryNotFound, ""},
		{"archived", func(p *NewTransactionParams) { p.Account = archived }, ErrAccountArchived, ""},
		{"currency", func(p *NewTransactionParams) { p.Amount = money.New(1, usd) }, money.ErrCurrencyMismatch, ""},
		{"cat type", func(p *NewTransactionParams) { p.Type = TxIncome }, ErrCategoryTypeMismatch, ""},
		{"future", func(p *NewTransactionParams) { p.Date = tNow.AddDate(0, 0, 3) }, ErrDateInFuture, ""},
		{"old", func(p *NewTransactionParams) { p.Date = time.Date(1969, 12, 31, 0, 0, 0, 0, time.UTC) }, ErrDateTooOld, ""},
		{"note", func(p *NewTransactionParams) { p.Note = strings.Repeat("n", 256) }, nil, "note"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p := base
			tt.mod(&p)
			_, err := NewTransaction(p)
			if tt.fld != "" {
				wantValidation(t, err, tt.fld)
			} else {
				wantErr(t, err, tt.err)
			}
		})
	}
	// besok (toleransi timezone) diizinkan
	p := base
	p.Date = tNow.AddDate(0, 0, 1)
	p.Source = SourceTransferFee
	fee, err := NewTransaction(p)
	if err != nil {
		t.Fatal(err)
	}
	wantErr(t, fee.EnsureEditable(), ErrManagedByTransfer)
}

func TestTransactionChange(t *testing.T) {
	user := uuid.New()
	a := newAcc(t, user, AccountCash, 1000, idr)
	b := newAcc(t, user, AccountBank, 0, idr)
	tx := newTx(t, user, a, sysCat(TxExpense), TxExpense, 100)
	inc := sysCat(TxIncome)
	later := tNow.Add(time.Hour)
	n, err := tx.Change(ChangeTransactionParams{Account: b, Category: inc, Type: TxIncome,
		Amount: money.New(70, idr), Date: tNow, Note: " gaji ", Now: later, Location: jakarta})
	if err != nil {
		t.Fatal(err)
	}
	if n.AccountID() != b.ID() || n.Type() != TxIncome || n.Note() != "gaji" || n.ID() != tx.ID() || !n.UpdatedAt().Equal(later) {
		t.Fatal("change salah")
	}
	if tx.AccountID() != a.ID() {
		t.Fatal("objek lama tidak boleh berubah")
	}
	_, err = tx.Change(ChangeTransactionParams{Account: b, Category: inc, Type: TxExpense, Amount: money.New(1, idr), Date: tNow, Now: tNow})
	wantErr(t, err, ErrCategoryTypeMismatch)
	n.SyncVersion(4)
	n.SetUpdatedAt(tNow)
	if n.Version() != 4 || !n.UpdatedAt().Equal(tNow) {
		t.Fatal("sync")
	}
	r := RehydrateTransaction(TransactionState{ID: uuid.New(), Date: time.Date(2026, 1, 2, 7, 0, 0, 0, jakarta), Source: SourceManual})
	if r.Date().Hour() != 0 || r.Date().Day() != 2 {
		t.Fatalf("rehydrate date = %v", r.Date())
	}
}
