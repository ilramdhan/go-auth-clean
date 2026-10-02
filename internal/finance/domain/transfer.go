package domain

import (
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// Transfer adalah perpindahan uang antar akun milik user yang sama: satu
// aggregate dengan dua leg (from, to). Transfer TIDAK dihitung sebagai
// income/expense di laporan. Biaya admin opsional dicatat sebagai transaksi
// expense terpisah (feeTransactionID).
type Transfer struct {
	id               uuid.UUID
	userID           uuid.UUID
	fromAccountID    uuid.UUID
	toAccountID      uuid.UUID
	amount           money.Money
	toAmount         money.Money // = amount bila satu currency
	fee              money.Money // zero = tanpa biaya
	feeTransactionID *uuid.UUID
	date             time.Time
	note             string
	version          int
	createdAt        time.Time
	updatedAt        time.Time
}

// NewTransferParams adalah input pembuatan transfer.
type NewTransferParams struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	From     *Account
	To       *Account
	Amount   money.Money
	ToAmount money.Money // kosong = sama dengan Amount (wajib bila beda currency)
	Fee      money.Money // Amount 0 = tanpa biaya
	Date     time.Time
	Note     string
	Now      time.Time
	Location *time.Location
}

// NewTransfer membuat transfer yang valid (belum mengubah saldo; panggil Apply).
func NewTransfer(p NewTransferParams) (*Transfer, error) {
	v, err := validateTransfer(p.UserID, p.From, p.To, transferAmounts{p.Amount, p.ToAmount, p.Fee}, p.Date, p.Note, p.Now, p.Location)
	if err != nil {
		return nil, err
	}
	now := p.Now.UTC()
	return &Transfer{
		id: p.ID, userID: p.UserID, fromAccountID: p.From.ID(), toAccountID: p.To.ID(),
		amount: p.Amount, toAmount: v.toAmount, fee: v.fee, date: v.date, note: v.note,
		version: 1, createdAt: now, updatedAt: now,
	}, nil
}

// ChangeTransferParams adalah nilai baru (lengkap) untuk transfer yang diedit.
type ChangeTransferParams struct {
	From     *Account
	To       *Account
	Amount   money.Money
	ToAmount money.Money
	Fee      money.Money
	Date     time.Time
	Note     string
	Now      time.Time
	Location *time.Location
}

// Change mengembalikan salinan transfer dengan nilai baru (feeTransactionID
// dipertahankan; app layer mengatur transaksi biayanya).
func (t *Transfer) Change(p ChangeTransferParams) (*Transfer, error) {
	v, err := validateTransfer(t.userID, p.From, p.To, transferAmounts{p.Amount, p.ToAmount, p.Fee}, p.Date, p.Note, p.Now, p.Location)
	if err != nil {
		return nil, err
	}
	next := *t
	next.fromAccountID, next.toAccountID = p.From.ID(), p.To.ID()
	next.amount, next.toAmount, next.fee, next.date, next.note = p.Amount, v.toAmount, v.fee, v.date, v.note
	next.updatedAt = p.Now.UTC()
	return &next, nil
}

type validTransfer struct {
	toAmount money.Money
	fee      money.Money
	date     time.Time
	note     string
}

type transferAmounts struct {
	amount, toAmount, fee money.Money
}

func validateTransfer(userID uuid.UUID, from, to *Account, am transferAmounts,
	date time.Time, note string, now time.Time, loc *time.Location,
) (validTransfer, error) {
	amount, fee := am.amount, am.fee
	if from == nil || to == nil || from.UserID() != userID || to.UserID() != userID {
		return validTransfer{}, ErrAccountNotFound
	}
	if from.ID() == to.ID() {
		return validTransfer{}, ErrSameAccountTransfer
	}
	if from.IsArchived() || to.IsArchived() {
		return validTransfer{}, ErrAccountArchived
	}
	if err := validateAmount(amount.Amount()); err != nil {
		return validTransfer{}, err
	}
	if amount.Currency() != from.Currency() {
		return validTransfer{}, money.ErrCurrencyMismatch
	}
	toAmount, err := validateToAmount(amount, am.toAmount, to.Currency())
	if err != nil {
		return validTransfer{}, err
	}
	if fee.Currency().IsZero() {
		fee = money.Zero(from.Currency())
	}
	if fee.IsNegative() {
		return validTransfer{}, &ValidationError{Field: "fee", Reason: "must not be negative"}
	}
	if !fee.IsZero() {
		if err := validateAmount(fee.Amount()); err != nil {
			return validTransfer{}, &ValidationError{Field: "fee", Reason: err.Error()}
		}
		if fee.Currency() != from.Currency() {
			return validTransfer{}, money.ErrCurrencyMismatch
		}
	}
	d, err := validateDate(date, now, loc)
	if err != nil {
		return validTransfer{}, err
	}
	n, err := normalizeNote(note)
	if err != nil {
		return validTransfer{}, err
	}
	return validTransfer{toAmount: toAmount, fee: fee, date: d, note: n}, nil
}

// validateToAmount: satu currency -> toAmount = amount; beda currency -> toAmount
// wajib (hasil konversi kurs) dalam currency akun tujuan.
func validateToAmount(amount, toAmount money.Money, toCur money.Currency) (money.Money, error) {
	if amount.Currency() == toCur {
		if !toAmount.Currency().IsZero() && toAmount != amount {
			return money.Money{}, &ValidationError{Field: "to_amount", Reason: "must equal amount for same-currency transfer"}
		}
		return amount, nil
	}
	if toAmount.Currency().IsZero() {
		return money.Money{}, &ValidationError{Field: "to_amount", Reason: "required for cross-currency transfer"}
	}
	if toAmount.Currency() != toCur {
		return money.Money{}, money.ErrCurrencyMismatch
	}
	if err := validateAmount(toAmount.Amount()); err != nil {
		return money.Money{}, &ValidationError{Field: "to_amount", Reason: err.Error()}
	}
	return toAmount, nil
}

// Apply memindahkan amount dari akun asal ke akun tujuan (biaya diterapkan
// terpisah lewat transaksi fee).
func (t *Transfer) Apply(from, to *Account) error {
	if from.ID() != t.fromAccountID || to.ID() != t.toAccountID {
		return &ValidationError{Field: "account_id", Reason: "accounts do not match transfer"}
	}
	if err := from.Debit(t.amount); err != nil {
		return err
	}
	return to.Credit(t.toAmount)
}

// Revert membatalkan efek Apply.
func (t *Transfer) Revert(from, to *Account) error {
	if from.ID() != t.fromAccountID || to.ID() != t.toAccountID {
		return &ValidationError{Field: "account_id", Reason: "accounts do not match transfer"}
	}
	if err := to.Debit(t.toAmount); err != nil {
		return err
	}
	return from.Credit(t.amount)
}

// TransferState adalah snapshot untuk rehydrate dari database.
type TransferState struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	FromAccountID    uuid.UUID
	ToAccountID      uuid.UUID
	Amount           money.Money
	ToAmount         money.Money // kosong = sama dengan Amount
	Fee              money.Money
	FeeTransactionID *uuid.UUID
	Date             time.Time
	Note             string
	Version          int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// RehydrateTransfer hanya dipakai repository.
func RehydrateTransfer(s TransferState) *Transfer {
	fee := s.Fee
	if fee.Currency().IsZero() {
		fee = money.Zero(s.Amount.Currency())
	}
	toAmount := s.ToAmount
	if toAmount.Currency().IsZero() {
		toAmount = s.Amount
	}
	return &Transfer{
		id: s.ID, userID: s.UserID, fromAccountID: s.FromAccountID, toAccountID: s.ToAccountID,
		amount: s.Amount, toAmount: toAmount, fee: fee, feeTransactionID: s.FeeTransactionID, date: DateOf(s.Date),
		note: s.Note, version: s.Version, createdAt: s.CreatedAt, updatedAt: s.UpdatedAt,
	}
}

func (t *Transfer) ID() uuid.UUID                { return t.id }
func (t *Transfer) UserID() uuid.UUID            { return t.userID }
func (t *Transfer) FromAccountID() uuid.UUID     { return t.fromAccountID }
func (t *Transfer) ToAccountID() uuid.UUID       { return t.toAccountID }
func (t *Transfer) Amount() money.Money          { return t.amount }
func (t *Transfer) ToAmount() money.Money        { return t.toAmount }
func (t *Transfer) IsCrossCurrency() bool        { return t.toAmount.Currency() != t.amount.Currency() }
func (t *Transfer) Fee() money.Money             { return t.fee }
func (t *Transfer) HasFee() bool                 { return t.fee.IsPositive() }
func (t *Transfer) FeeTransactionID() *uuid.UUID { return t.feeTransactionID }
func (t *Transfer) Date() time.Time              { return t.date }
func (t *Transfer) Note() string                 { return t.note }
func (t *Transfer) Version() int                 { return t.version }
func (t *Transfer) CreatedAt() time.Time         { return t.createdAt }
func (t *Transfer) UpdatedAt() time.Time         { return t.updatedAt }
func (t *Transfer) SyncVersion(v int)            { t.version = v }

// SetFeeTransaction menghubungkan/melepas transaksi biaya (nil = tanpa biaya).
func (t *Transfer) SetFeeTransaction(id *uuid.UUID) { t.feeTransactionID = id }
