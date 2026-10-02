package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

func TestNewTransfer(t *testing.T) {
	user := uuid.New()
	a := newAcc(t, user, AccountCash, 1000, idr)
	b := newAcc(t, user, AccountBank, 0, idr)
	u := newAcc(t, user, AccountBank, 0, usd)
	foreign := newAcc(t, uuid.New(), AccountBank, 0, idr)
	arch := newAcc(t, user, AccountBank, 0, idr)
	arch.Archive(tNow)

	base := NewTransferParams{ID: uuid.New(), UserID: user, From: a, To: b, Amount: money.New(100, idr),
		Date: tNow, Note: " pindah ", Now: tNow, Location: jakarta}
	tr, err := NewTransfer(base)
	if err != nil {
		t.Fatal(err)
	}
	if tr.HasFee() || tr.Fee().Currency() != idr || tr.Note() != "pindah" || tr.Version() != 1 ||
		tr.FromAccountID() != a.ID() || tr.ToAccountID() != b.ID() || tr.UserID() != user || tr.ID() == uuid.Nil ||
		tr.Amount().Amount() != 100 || tr.FeeTransactionID() != nil || tr.Date().Hour() != 0 ||
		!tr.CreatedAt().Equal(tNow) || !tr.UpdatedAt().Equal(tNow) {
		t.Fatal("state salah")
	}
	cases := []struct {
		name string
		mod  func(p *NewTransferParams)
		err  error
		fld  string
	}{
		{"nil", func(p *NewTransferParams) { p.To = nil }, ErrAccountNotFound, ""},
		{"foreign", func(p *NewTransferParams) { p.To = foreign }, ErrAccountNotFound, ""},
		{"same", func(p *NewTransferParams) { p.To = a }, ErrSameAccountTransfer, ""},
		{"archived", func(p *NewTransferParams) { p.To = arch }, ErrAccountArchived, ""},
		{"zero", func(p *NewTransferParams) { p.Amount = money.New(0, idr) }, ErrInvalidAmount, ""},
		{"cross currency no to_amount", func(p *NewTransferParams) { p.To = u }, nil, "to_amount"},
		{"cross currency wrong cur", func(p *NewTransferParams) { p.To = u; p.ToAmount = money.New(5, idr) }, money.ErrCurrencyMismatch, ""},
		{"cross currency zero", func(p *NewTransferParams) { p.To = u; p.ToAmount = money.New(0, usd) }, nil, "to_amount"},
		{"same currency diff to_amount", func(p *NewTransferParams) { p.ToAmount = money.New(1, idr) }, nil, "to_amount"},
		{"amount currency", func(p *NewTransferParams) { p.Amount = money.New(1, usd) }, money.ErrCurrencyMismatch, ""},
		{"neg fee", func(p *NewTransferParams) { p.Fee = money.New(-1, idr) }, nil, "fee"},
		{"huge fee", func(p *NewTransferParams) { p.Fee = money.New(MaxAmount+1, idr) }, nil, "fee"},
		{"fee currency", func(p *NewTransferParams) { p.Fee = money.New(1, usd) }, money.ErrCurrencyMismatch, ""},
		{"future", func(p *NewTransferParams) { p.Date = tNow.AddDate(0, 1, 0) }, ErrDateInFuture, ""},
		{"note", func(p *NewTransferParams) { p.Note = string(make([]byte, 300)) + "x" }, nil, "note"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p := base
			tt.mod(&p)
			_, err := NewTransfer(p)
			if tt.fld != "" {
				wantValidation(t, err, tt.fld)
			} else {
				wantErr(t, err, tt.err)
			}
		})
	}
}

func TestTransferApplyRevertChange(t *testing.T) {
	user := uuid.New()
	a := newAcc(t, user, AccountCash, 1000, idr)
	b := newAcc(t, user, AccountCash, 0, idr)
	c := newAcc(t, user, AccountCash, 0, idr)
	tr, err := NewTransfer(NewTransferParams{ID: uuid.New(), UserID: user, From: a, To: b,
		Amount: money.New(400, idr), Fee: money.New(5, idr), Date: tNow, Now: tNow})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.HasFee() {
		t.Fatal("fee")
	}
	if err := tr.Apply(a, b); err != nil || a.Balance().Amount() != 600 || b.Balance().Amount() != 400 {
		t.Fatalf("apply: %v", err)
	}
	wantValidation(t, tr.Apply(b, a), "account_id")
	wantValidation(t, tr.Revert(b, a), "account_id")
	if err := b.Debit(money.New(100, idr)); err != nil {
		t.Fatal(err)
	}
	wantErr(t, tr.Revert(a, b), ErrInsufficientBalance)
	if err := b.Credit(money.New(100, idr)); err != nil {
		t.Fatal(err)
	}
	if err := tr.Revert(a, b); err != nil || a.Balance().Amount() != 1000 || b.Balance().IsPositive() {
		t.Fatalf("revert: %v", err)
	}
	fid := uuid.New()
	tr.SetFeeTransaction(&fid)
	later := tNow.Add(time.Hour)
	n, err := tr.Change(ChangeTransferParams{From: a, To: c, Amount: money.New(10, idr), Date: tNow, Now: later})
	if err != nil {
		t.Fatal(err)
	}
	if n.ToAccountID() != c.ID() || *n.FeeTransactionID() != fid || n.HasFee() || !n.UpdatedAt().Equal(later) {
		t.Fatal("change salah")
	}
	if tr.ToAccountID() != b.ID() {
		t.Fatal("objek lama berubah")
	}
	_, err = tr.Change(ChangeTransferParams{From: a, To: a, Amount: money.New(10, idr), Date: tNow, Now: later})
	wantErr(t, err, ErrSameAccountTransfer)
	n.SyncVersion(3)
	r := RehydrateTransfer(TransferState{ID: uuid.New(), Version: 2, Fee: money.New(0, idr)})
	if n.Version() != 3 || r.Version() != 2 || r.HasFee() {
		t.Fatal("sync/rehydrate")
	}
}

func TestTransferCrossCurrency(t *testing.T) {
	user := uuid.New()
	a := newAcc(t, user, AccountCash, 100000, idr)
	b := newAcc(t, user, AccountCash, 0, usd)
	tr, err := NewTransfer(NewTransferParams{ID: uuid.New(), UserID: user, From: a, To: b,
		Amount: money.New(16000, idr), ToAmount: money.New(100, usd), Date: tNow, Now: tNow, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.IsCrossCurrency() || tr.ToAmount() != money.New(100, usd) {
		t.Fatal("to amount salah")
	}
	if err := tr.Apply(a, b); err != nil {
		t.Fatal(err)
	}
	if a.Balance().Amount() != 84000 || b.Balance().Amount() != 100 {
		t.Fatalf("saldo %d %d", a.Balance().Amount(), b.Balance().Amount())
	}
	bc := NewBalanceChanges()
	if err := bc.RevertTransfer(tr); err != nil {
		t.Fatal(err)
	}
	if d, _ := bc.Delta(b.ID()); d.Amount() != -100 {
		t.Fatalf("delta %v", d)
	}
	if err := tr.Revert(a, b); err != nil {
		t.Fatal(err)
	}
	if a.Balance().Amount() != 100000 || b.Balance().Amount() != 0 {
		t.Fatal("revert salah")
	}
	r := RehydrateTransfer(TransferState{Amount: money.New(5, idr)})
	if r.ToAmount() != money.New(5, idr) || r.IsCrossCurrency() {
		t.Fatal("rehydrate default")
	}
}
