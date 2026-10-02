package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

func TestCreateTransfer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.account(t, "cash", "1000")
	b := f.account(t, "cash", "0")
	tr, err := f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(),
		Amount: "300", Fee: "5", Date: today, Note: "topup"})
	if err != nil {
		t.Fatal(err)
	}
	if f.balance(t, a.ID()) != 695 || f.balance(t, b.ID()) != 300 {
		t.Fatalf("a=%d b=%d", f.balance(t, a.ID()), f.balance(t, b.ID()))
	}
	fee, err := f.svc.GetTransaction(ctx, f.user, *tr.FeeTransactionID())
	if err != nil || fee.Source() != domain.SourceTransferFee || fee.CategoryID() != domain.FeeCategoryID || fee.Note() != "Biaya transfer: topup" {
		t.Fatalf("fee tx: %v", err)
	}
	// tanpa fee
	tr2, err := f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: b.ID(), ToAccountID: a.ID(), Amount: "100", Date: today})
	if err != nil || tr2.FeeTransactionID() != nil {
		t.Fatal(err)
	}
	u, _ := f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "usd", Type: "bank", Currency: "USD"})
	cases := []struct {
		name string
		in   CreateTransferInput
		want error
	}{
		{"same", CreateTransferInput{FromAccountID: a.ID(), ToAccountID: a.ID(), Amount: "1"}, domain.ErrSameAccountTransfer},
		{"overspend", CreateTransferInput{FromAccountID: b.ID(), ToAccountID: a.ID(), Amount: "201"}, domain.ErrInsufficientBalance},
		{"fee overspend", CreateTransferInput{FromAccountID: b.ID(), ToAccountID: a.ID(), Amount: "200", Fee: "1"}, domain.ErrInsufficientBalance},
		{"currency without to_amount", CreateTransferInput{FromAccountID: a.ID(), ToAccountID: u.ID(), Amount: "1"}, &domain.ValidationError{Field: "to_amount"}},
		{"bad to_amount", CreateTransferInput{FromAccountID: a.ID(), ToAccountID: u.ID(), Amount: "1", ToAmount: "x"}, &domain.ValidationError{Field: "to_amount"}},
		{"missing", CreateTransferInput{FromAccountID: a.ID(), ToAccountID: uuid.New(), Amount: "1"}, domain.ErrAccountNotFound},
		{"amount", CreateTransferInput{FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "x"}, money.ErrInvalidAmount},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.UserID, c.in.Date = f.user, today
			_, err := f.svc.CreateTransfer(ctx, c.in)
			wantErr(t, err, c.want)
		})
	}
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "1", Fee: "x", Date: today})
	if ve, ok := errors.AsType[*domain.ValidationError](err); !ok || ve.Field != "fee" {
		t.Fatalf("fee err = %v", err)
	}
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "1", Date: today.AddDate(1, 0, 0)})
	wantErr(t, err, domain.ErrDateInFuture)
	// kategori fee hilang -> rollback total
	delete(f.db.cats, domain.FeeCategoryID)
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "1", Fee: "1", Date: today})
	wantErr(t, err, domain.ErrCategoryNotFound)
	if f.balance(t, a.ID()) != 795 {
		t.Fatalf("rollback: %d", f.balance(t, a.ID()))
	}
	f.ids.err = errBoom
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "1", Date: today})
	wantErr(t, err, errBoom)
	f.ids.err = nil
	f.db.failOn["transactions.create"] = errBoom
	f.db.cats[domain.FeeCategoryID] = *f.fee
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "1", Fee: "1", Date: today})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "transactions.create")
	f.db.failOn["accounts.update"] = errBoom
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "1", Date: today})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "accounts.update")
	f.db.failOn["settings.get"] = errBoom
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "1"})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "settings.get")

	list, err := f.svc.ListTransfers(ctx, ListTransfersInput{UserID: f.user})
	if err != nil || len(list) != 2 {
		t.Fatalf("list %d", len(list))
	}
	from, to := today, today.AddDate(0, 0, -1)
	_, err = f.svc.ListTransfers(ctx, ListTransfersInput{UserID: f.user, From: &from, To: &to})
	wantErr(t, err, domain.ErrInvalidRange)
	if got, err := f.svc.GetTransfer(ctx, f.user, tr.ID()); err != nil || got.ID() != tr.ID() {
		t.Fatal(err)
	}
	if feeNote(strings.Repeat("x", 300)) == "" || len([]rune(feeNote(strings.Repeat("x", 300)))) != domain.MaxNoteLength {
		t.Fatal("feeNote harus dipotong")
	}
}

func TestUpdateDeleteTransfer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.account(t, "cash", "1000")
	b := f.account(t, "cash", "0")
	c := f.account(t, "cash", "0")
	tr, _ := f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "300", Fee: "5", Date: today})
	oldFee := *tr.FeeTransactionID()
	// pindah tujuan ke c, amount 200, fee 10
	up, err := f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), ExpectedVersion: ptr(1),
		ToAccountID: ptr(c.ID()), Amount: ptr("200"), Fee: ptr("10"), Note: ptr("n"), Date: ptr(today)})
	if err != nil {
		t.Fatal(err)
	}
	if f.balance(t, a.ID()) != 790 || f.balance(t, b.ID()) != 0 || f.balance(t, c.ID()) != 200 {
		t.Fatalf("a=%d b=%d c=%d", f.balance(t, a.ID()), f.balance(t, b.ID()), f.balance(t, c.ID()))
	}
	if up.FeeTransactionID() == nil || *up.FeeTransactionID() == oldFee || up.Version() != 2 {
		t.Fatal("fee tx harus diganti")
	}
	if _, err := f.svc.GetTransaction(ctx, f.user, oldFee); err == nil {
		t.Fatal("fee lama harus terhapus")
	}
	// hapus fee
	up, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), Fee: ptr("0")})
	if err != nil || up.FeeTransactionID() != nil || f.balance(t, a.ID()) != 800 {
		t.Fatalf("hapus fee: %v %d", err, f.balance(t, a.ID()))
	}
	// swap arah (from=c, to=a)
	if _, err := f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), FromAccountID: ptr(c.ID()), ToAccountID: ptr(a.ID()), Amount: ptr("200")}); err == nil {
		// c saldo 200 dari transfer sendiri: revert (c-200=0) lalu c->a 200 => c=-200 ditolak
		t.Fatal("harus insufficient")
	}
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), ExpectedVersion: ptr(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), ToAccountID: ptr(a.ID())})
	wantErr(t, err, domain.ErrSameAccountTransfer)
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), ToAccountID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), Amount: ptr("abc")})
	wantErr(t, err, money.ErrInvalidAmount)
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), Date: ptr(today.AddDate(0, 1, 0))})
	wantErr(t, err, domain.ErrDateInFuture)
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: uuid.New(), ID: tr.ID()})
	wantErr(t, err, domain.ErrTransferNotFound)
	f.db.failOn["settings.get"] = errBoom
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID()})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "settings.get")
	// tambah fee lagi lalu delete: semua kembali
	up, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), Fee: ptr("7")})
	if err != nil {
		t.Fatal(err)
	}
	f.db.failOn["transactions.delete"] = errBoom
	wantErr(t, f.svc.DeleteTransfer(ctx, f.user, tr.ID()), errBoom)
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), Fee: ptr("8")})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "transactions.delete")
	f.db.failOn["transactions.create"] = errBoom
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), Fee: ptr("8")})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "transactions.create")
	if err := f.svc.DeleteTransfer(ctx, f.user, tr.ID()); err != nil {
		t.Fatal(err)
	}
	if f.balance(t, a.ID()) != 1000 || f.balance(t, c.ID()) != 0 {
		t.Fatalf("a=%d c=%d", f.balance(t, a.ID()), f.balance(t, c.ID()))
	}
	if _, err := f.svc.GetTransaction(ctx, f.user, *up.FeeTransactionID()); err == nil {
		t.Fatal("fee harus ikut terhapus")
	}
	wantErr(t, f.svc.DeleteTransfer(ctx, f.user, tr.ID()), domain.ErrTransferNotFound)
	// delete diblok bila tujuan sudah dibelanjakan
	tr3, _ := f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: b.ID(), Amount: "100", Date: today})
	if _, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: b.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "50", Date: today}); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteTransfer(ctx, f.user, tr3.ID()), domain.ErrInsufficientBalance)
	// fee tx hilang (data korup) -> error dikembalikan
	tr4, _ := f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: c.ID(), Amount: "1", Fee: "1", Date: today})
	f.db.deleted[*tr4.FeeTransactionID()] = true
	wantErr(t, f.svc.DeleteTransfer(ctx, f.user, tr4.ID()), domain.ErrTransactionNotFound)
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr4.ID()})
	wantErr(t, err, domain.ErrTransactionNotFound)
}

func TestCrossCurrencyTransfer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.account(t, "cash", "1000000")
	u, err := f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "usd", Type: "bank", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: a.ID(), ToAccountID: u.ID(),
		Amount: "160000", ToAmount: "10.50", Date: today})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.IsCrossCurrency() || f.balance(t, u.ID()) != 1050 || f.balance(t, a.ID()) != 840000 {
		t.Fatalf("a=%d u=%d", f.balance(t, a.ID()), f.balance(t, u.ID()))
	}
	// edit amount saja: to_amount lama dipertahankan
	up, err := f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), Amount: ptr("170000")})
	if err != nil || up.ToAmount().Amount() != 1050 || f.balance(t, a.ID()) != 830000 {
		t.Fatalf("update: %v", err)
	}
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), ToAmount: ptr("11")})
	if err != nil || f.balance(t, u.ID()) != 1100 {
		t.Fatalf("update to_amount: %v", err)
	}
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), ToAmount: ptr("bad")})
	wantErr(t, err, &domain.ValidationError{Field: "to_amount"})
	// pindah ke akun IDR: to_amount lama (USD) dibuang, kembali satu currency
	b := f.account(t, "cash", "0")
	up, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: f.user, ID: tr.ID(), ToAccountID: ptr(b.ID())})
	if err != nil || up.IsCrossCurrency() || f.balance(t, b.ID()) != 170000 || f.balance(t, u.ID()) != 0 {
		t.Fatalf("to idr: %v", err)
	}
	if up.Version() != 4 {
		t.Fatalf("version %d", up.Version())
	}
}
