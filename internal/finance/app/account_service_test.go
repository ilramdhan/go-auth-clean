package app

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

func TestSettingsService(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	st, err := f.svc.GetSettings(ctx, f.user)
	if err != nil || st.BaseCurrency() != idr || st.Timezone() != "Asia/Jakarta" || st.WeekStart() != 1 {
		t.Fatalf("lazy default: %v %+v", err, st)
	}
	up, err := f.svc.UpdateSettings(ctx, UpdateSettingsInput{UserID: f.user, BaseCurrency: "USD", Timezone: "UTC", WeekStart: 0})
	if err != nil || up.BaseCurrency() != usd {
		t.Fatalf("update: %v", err)
	}
	if _, err := f.svc.UpdateSettings(ctx, UpdateSettingsInput{UserID: f.user, BaseCurrency: "USD", Timezone: "Nope/Zone"}); err == nil {
		t.Fatal("tz invalid harus ditolak")
	}
	f.db.failOn["settings.upsert"] = errBoom
	_, err = f.svc.UpdateSettings(ctx, UpdateSettingsInput{UserID: f.user, BaseCurrency: "USD", Timezone: "UTC"})
	wantErr(t, err, errBoom)
	f.db.failOn["settings.get"] = errBoom
	_, err = f.svc.GetSettings(ctx, uuid.New())
	wantErr(t, err, errBoom)
	cs, err := f.svc.ListCurrencies(ctx)
	if err != nil || len(cs) != 1 {
		t.Fatal(err)
	}
	f.db.failOn["currencies"] = errBoom
	_, err = f.svc.ListCurrencies(ctx)
	wantErr(t, err, errBoom)
	// default config rusak
	bad := NewService(Deps{Clock: &fixedClock{testNow}, Settings: fakeSettings{newMemDB()}, Defaults: Defaults{Currency: "XXX", Timezone: "UTC"}})
	if _, err := bad.GetSettings(ctx, f.user); err == nil {
		t.Fatal("default invalid harus error")
	}
}

func TestCreateAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, err := f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "Cash", Type: "cash", InitialBalance: "50000"})
	if err != nil || a.Currency() != idr || a.Balance().Amount() != 50000 {
		t.Fatalf("%v", err)
	}
	u, err := f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "USD", Type: "bank", Currency: "USD", InitialBalance: "12.34"})
	if err != nil || u.Balance().Amount() != 1234 {
		t.Fatalf("%v", err)
	}
	_, err = f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "Cash", Type: "cash"})
	wantErr(t, err, domain.ErrDuplicateName)
	_, err = f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "x", Type: "gold"})
	wantErr(t, err, domain.ErrInvalidAccountType)
	_, err = f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "x", Type: "cash", Currency: "ZZZ"})
	wantErr(t, err, money.ErrUnknownCurrency)
	_, err = f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "x", Type: "cash", InitialBalance: "1.5"})
	wantErr(t, err, money.ErrTooManyDecimals)
	_, err = f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "x", Type: "cash", InitialBalance: "-5"})
	wantErr(t, err, domain.ErrInsufficientBalance)
	f.ids.err = errBoom
	_, err = f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "y", Type: "cash"})
	wantErr(t, err, errBoom)
	f.ids.err = nil
	f.db.failOn["settings.get"] = errBoom
	_, err = f.svc.CreateAccount(ctx, CreateAccountInput{UserID: f.user, Name: "y", Type: "cash"})
	wantErr(t, err, errBoom)
	list, _ := f.svc.ListAccounts(ctx, f.user, false)
	if len(list) != 2 {
		t.Fatalf("list = %d", len(list))
	}
}

func TestUpdateArchiveDeleteAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.account(t, "cash", "1000")
	upd, err := f.svc.UpdateAccount(ctx, UpdateAccountInput{UserID: f.user, ID: a.ID(), ExpectedVersion: ptr(1),
		Name: ptr("Dompet"), InitialBalance: ptr("1500")})
	if err != nil || upd.Balance().Amount() != 1500 || upd.Version() != 2 || upd.Name() != "Dompet" {
		t.Fatalf("update: %v", err)
	}
	_, err = f.svc.UpdateAccount(ctx, UpdateAccountInput{UserID: f.user, ID: a.ID(), ExpectedVersion: ptr(1)})
	wantErr(t, err, domain.ErrVersionConflict)
	_, err = f.svc.UpdateAccount(ctx, UpdateAccountInput{UserID: f.user, ID: a.ID(), InitialBalance: ptr("abc")})
	wantErr(t, err, money.ErrInvalidAmount)
	_, err = f.svc.UpdateAccount(ctx, UpdateAccountInput{UserID: f.user, ID: a.ID(), InitialBalance: ptr("-1")})
	wantErr(t, err, domain.ErrInsufficientBalance)
	_, err = f.svc.UpdateAccount(ctx, UpdateAccountInput{UserID: uuid.New(), ID: a.ID()})
	wantErr(t, err, domain.ErrAccountNotFound)
	f.db.failOn["accounts.update"] = errBoom
	_, err = f.svc.UpdateAccount(ctx, UpdateAccountInput{UserID: f.user, ID: a.ID(), Name: ptr("z")})
	wantErr(t, err, errBoom)
	_, err = f.svc.ArchiveAccount(ctx, f.user, a.ID())
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "accounts.update")

	arch, err := f.svc.ArchiveAccount(ctx, f.user, a.ID())
	if err != nil || !arch.IsArchived() {
		t.Fatalf("archive: %v", err)
	}
	again, err := f.svc.ArchiveAccount(ctx, f.user, a.ID())
	if err != nil || again.Version() != arch.Version() {
		t.Fatal("archive ulang harus no-op")
	}
	if list, _ := f.svc.ListAccounts(ctx, f.user, false); len(list) != 0 {
		t.Fatal("arsip tidak boleh tampil")
	}
	if _, err := f.svc.UnarchiveAccount(ctx, f.user, a.ID()); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.ArchiveAccount(ctx, uuid.New(), a.ID())
	wantErr(t, err, domain.ErrAccountNotFound)

	// delete diblok bila ada transaksi
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: a.ID(), CategoryID: f.expense.ID(),
		Type: "expense", Amount: "10", Date: today})
	if err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteAccount(ctx, f.user, a.ID()), domain.ErrAccountHasTransactions)
	b := f.account(t, "bank", "0")
	if err := f.svc.DeleteAccount(ctx, f.user, b.ID()); err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteAccount(ctx, f.user, b.ID()), domain.ErrAccountNotFound)
	f.db.failOn["accounts.activity"] = errBoom
	wantErr(t, f.svc.DeleteAccount(ctx, f.user, a.ID()), errBoom)
}
