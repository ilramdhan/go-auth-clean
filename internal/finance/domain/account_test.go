package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

func TestParseAccountType(t *testing.T) {
	for _, s := range []string{"cash", "bank", "ewallet", "credit_card"} {
		if _, err := ParseAccountType(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	_, err := ParseAccountType("crypto")
	wantErr(t, err, ErrInvalidAccountType)
	if AccountCash.DefaultAllowNegative() || !AccountBank.DefaultAllowNegative() || !AccountCreditCard.DefaultAllowNegative() {
		t.Fatal("default allow negative salah")
	}
}

func TestNewAccount(t *testing.T) {
	user := uuid.New()
	yes, no := true, false
	tests := []struct {
		name    string
		p       NewAccountParams
		wantErr error
		field   string
	}{
		{"ok", NewAccountParams{Name: "  Dompet   Utama ", Type: AccountCash, InitialBalance: money.New(100, idr)}, nil, ""},
		{"empty name", NewAccountParams{Name: "  ", Type: AccountCash, InitialBalance: money.New(0, idr)}, nil, "name"},
		{"long name", NewAccountParams{Name: strings.Repeat("a", 51), Type: AccountCash, InitialBalance: money.New(0, idr)}, nil, "name"},
		{"bad type", NewAccountParams{Name: "x", Type: "x", InitialBalance: money.New(0, idr)}, ErrInvalidAccountType, ""},
		{"no currency", NewAccountParams{Name: "x", Type: AccountCash}, money.ErrUnknownCurrency, ""},
		{"too large", NewAccountParams{Name: "x", Type: AccountCash, InitialBalance: money.New(MaxAmount+1, idr)}, ErrAmountTooLarge, ""},
		{"negative cash", NewAccountParams{Name: "x", Type: AccountCash, InitialBalance: money.New(-1, idr)}, ErrInsufficientBalance, ""},
		{"negative bank ok", NewAccountParams{Name: "x", Type: AccountBank, InitialBalance: money.New(-1, idr)}, nil, ""},
		{"negative cash override", NewAccountParams{Name: "x", Type: AccountCash, AllowNegative: &yes, InitialBalance: money.New(-1, idr)}, nil, ""},
		{"negative bank disabled", NewAccountParams{Name: "x", Type: AccountBank, AllowNegative: &no, InitialBalance: money.New(-1, idr)}, ErrInsufficientBalance, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.p.ID, tt.p.UserID, tt.p.Now = uuid.New(), user, tNow
			a, err := NewAccount(tt.p)
			switch {
			case tt.field != "":
				wantValidation(t, err, tt.field)
			case tt.wantErr != nil:
				wantErr(t, err, tt.wantErr)
			case err != nil:
				t.Fatalf("unexpected: %v", err)
			default:
				if a.Version() != 1 || a.Balance() != a.InitialBalance() || a.UserID() != user {
					t.Fatalf("state salah: %+v", a)
				}
			}
		})
	}
	a, _ := NewAccount(NewAccountParams{ID: uuid.New(), UserID: user, Name: "  Dompet   Utama ", Type: AccountCash, InitialBalance: money.New(5, idr), Now: tNow})
	if a.Name() != "Dompet Utama" || a.Type() != AccountCash || a.Currency() != idr || a.AllowNegative() ||
		!a.CreatedAt().Equal(tNow) || !a.UpdatedAt().Equal(tNow) || a.ID() == uuid.Nil || a.ArchivedAt() != nil {
		t.Fatalf("getter salah")
	}
}

func TestAccountUpdate(t *testing.T) {
	user := uuid.New()
	a := newAcc(t, user, AccountCash, 1000, idr)
	cat := sysCat(TxExpense)
	tx := newTx(t, user, a, cat, TxExpense, 300)
	if err := a.Apply(tx); err != nil {
		t.Fatal(err)
	}
	// saldo 700; ubah initial 1000 -> 500 => saldo 200
	nb := money.New(500, idr)
	name := "Baru"
	later := tNow.Add(time.Hour)
	if err := a.Update(AccountUpdate{Name: &name, InitialBalance: &nb, Now: later}); err != nil {
		t.Fatal(err)
	}
	if a.Balance().Amount() != 200 || a.Name() != "Baru" || !a.UpdatedAt().Equal(later) {
		t.Fatalf("balance=%d name=%s", a.Balance().Amount(), a.Name())
	}
	// initial 0 => saldo -300 ditolak, state tidak berubah
	zero := money.New(0, idr)
	wantErr(t, a.Update(AccountUpdate{InitialBalance: &zero}), ErrInsufficientBalance)
	if a.Balance().Amount() != 200 {
		t.Fatal("state berubah walau gagal")
	}
	yes := true
	if err := a.Update(AccountUpdate{InitialBalance: &zero, AllowNegative: &yes}); err != nil {
		t.Fatal(err)
	}
	empty := ""
	wantValidation(t, a.Update(AccountUpdate{Name: &empty}), "name")
	big := money.New(MaxAmount+1, idr)
	wantErr(t, a.Update(AccountUpdate{InitialBalance: &big}), ErrAmountTooLarge)
	other := money.New(1, usd)
	wantErr(t, a.Update(AccountUpdate{InitialBalance: &other}), money.ErrCurrencyMismatch)
	a.Archive(tNow)
	wantErr(t, a.Update(AccountUpdate{InitialBalance: &zero}), ErrAccountArchived)
	if err := a.Update(AccountUpdate{Name: &name}); err != nil {
		t.Fatalf("rename arsip harus boleh: %v", err)
	}
}

func TestAccountArchiveAndBalance(t *testing.T) {
	user := uuid.New()
	a := newAcc(t, user, AccountCash, 100, idr)
	a.Archive(tNow)
	first := *a.ArchivedAt()
	a.Archive(tNow.Add(time.Hour))
	if !a.IsArchived() || !a.ArchivedAt().Equal(first) {
		t.Fatal("archive harus idempotent")
	}
	wantErr(t, a.Credit(money.New(1, idr)), ErrAccountArchived)
	a.Unarchive(tNow)
	a.Unarchive(tNow)
	if a.IsArchived() {
		t.Fatal("unarchive gagal")
	}
	wantErr(t, a.Debit(money.New(0, idr)), ErrInvalidAmount)
	wantErr(t, a.Credit(money.New(-1, idr)), ErrInvalidAmount)
	wantErr(t, a.Debit(money.New(101, idr)), ErrInsufficientBalance)
	wantErr(t, a.Debit(money.New(1, usd)), money.ErrCurrencyMismatch)
	if err := a.Debit(money.New(100, idr)); err != nil || !a.Balance().IsZero() {
		t.Fatalf("debit: %v", err)
	}
	if err := a.Credit(money.New(5, idr)); err != nil || a.Balance().Amount() != 5 {
		t.Fatalf("credit: %v", err)
	}
	a.Touch(tNow.Add(time.Minute))
	a.SyncVersion(9)
	if a.Version() != 9 || !a.UpdatedAt().Equal(tNow.Add(time.Minute)) {
		t.Fatal("touch/sync")
	}
}

func TestAccountApplyRevert(t *testing.T) {
	user := uuid.New()
	a := newAcc(t, user, AccountCash, 100, idr)
	b := newAcc(t, user, AccountCash, 100, idr)
	inc := newTx(t, user, a, sysCat(TxIncome), TxIncome, 50)
	if err := a.Apply(inc); err != nil || a.Balance().Amount() != 150 {
		t.Fatalf("apply: %v", err)
	}
	if err := a.Revert(inc); err != nil || a.Balance().Amount() != 100 {
		t.Fatalf("revert: %v", err)
	}
	wantValidation(t, b.Apply(inc), "account_id")
	wantValidation(t, b.Revert(inc), "account_id")
	if err := a.Apply(inc); err != nil {
		t.Fatal(err)
	}
	if err := a.Debit(money.New(120, idr)); err != nil {
		t.Fatal(err)
	}
	// saldo 30; revert income 50 -> -20 ditolak
	wantErr(t, a.Revert(inc), ErrInsufficientBalance)
}

func TestRehydrateAccount(t *testing.T) {
	at := tNow
	a := RehydrateAccount(AccountState{ID: uuid.New(), Name: "x", Type: AccountBank,
		InitialBalance: money.New(1, idr), Balance: money.New(2, idr), Version: 3, ArchivedAt: &at})
	if a.Version() != 3 || a.Balance().Amount() != 2 || !a.IsArchived() {
		t.Fatal("rehydrate")
	}
}
