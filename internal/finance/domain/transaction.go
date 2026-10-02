package domain

import (
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// TxSource menandai asal transaksi. Transaksi non-manual dikelola aggregate
// lain (mis. biaya transfer) sehingga tidak boleh diubah langsung oleh user.
type TxSource string

const (
	SourceManual      TxSource = "manual"
	SourceTransferFee TxSource = "transfer_fee"
	SourceRecurring   TxSource = "recurring"
	SourceImport      TxSource = "import"
)

// Transaction adalah pemasukan/pengeluaran. Amount selalu positif; arah saldo
// diturunkan dari type lewat SignedAmount (jebakan #5).
type Transaction struct {
	id         uuid.UUID
	userID     uuid.UUID
	accountID  uuid.UUID
	categoryID uuid.UUID
	typ        TxType
	amount     money.Money
	date       time.Time
	note       string
	source     TxSource
	ruleID     *uuid.UUID // recurring rule asal (source recurring)
	occurrence *time.Time // tanggal kemunculan rule (idempotency worker)
	importHash []byte     // sidik import CSV (dedupe)
	version    int
	createdAt  time.Time
	updatedAt  time.Time
}

// NewTransactionParams adalah input pembuatan transaksi. Account & Category
// dipakai untuk validasi kepemilikan, status, currency dan tipe.
type NewTransactionParams struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Account  *Account
	Category *Category
	Type     TxType
	Amount   money.Money
	Date     time.Time
	Note     string
	Source   TxSource // kosong = manual
	// RecurringRuleID & OccurrenceDate diisi worker recurring (keduanya atau tidak sama sekali).
	RecurringRuleID *uuid.UUID
	OccurrenceDate  *time.Time
	ImportHash      []byte
	Now             time.Time
	// Location adalah timezone user untuk menentukan "hari ini".
	Location *time.Location
}

// NewTransaction membuat transaksi baru yang valid.
func NewTransaction(p NewTransactionParams) (*Transaction, error) {
	fields, err := validateTxFields(p.UserID, txFields{
		account: p.Account, category: p.Category, typ: p.Type, amount: p.Amount,
		date: p.Date, note: p.Note, now: p.Now, loc: p.Location,
	})
	if err != nil {
		return nil, err
	}
	src := p.Source
	if src == "" {
		src = SourceManual
	}
	if (p.RecurringRuleID == nil) != (p.OccurrenceDate == nil) {
		return nil, &ValidationError{Field: "recurring_rule_id", Reason: "rule and occurrence must be set together"}
	}
	var occ *time.Time
	if p.OccurrenceDate != nil {
		d := DateOf(*p.OccurrenceDate)
		occ = &d
	}
	now := p.Now.UTC()
	return &Transaction{
		id: p.ID, userID: p.UserID, accountID: p.Account.ID(), categoryID: p.Category.ID(),
		typ: p.Type, amount: p.Amount, date: fields.date, note: fields.note, source: src,
		ruleID: p.RecurringRuleID, occurrence: occ, importHash: p.ImportHash,
		version: 1, createdAt: now, updatedAt: now,
	}, nil
}

// ChangeTransactionParams adalah nilai baru (lengkap) untuk transaksi yang diedit.
type ChangeTransactionParams struct {
	Account  *Account
	Category *Category
	Type     TxType
	Amount   money.Money
	Date     time.Time
	Note     string
	Now      time.Time
	Location *time.Location
}

// Change mengembalikan salinan transaksi dengan nilai baru (divalidasi seperti
// NewTransaction). Objek lama tidak diubah supaya bisa di-Revert dari akun lama.
func (t *Transaction) Change(p ChangeTransactionParams) (*Transaction, error) {
	fields, err := validateTxFields(t.userID, txFields{
		account: p.Account, category: p.Category, typ: p.Type, amount: p.Amount,
		date: p.Date, note: p.Note, now: p.Now, loc: p.Location,
	})
	if err != nil {
		return nil, err
	}
	next := *t
	next.accountID, next.categoryID = p.Account.ID(), p.Category.ID()
	next.typ, next.amount, next.date, next.note = p.Type, p.Amount, fields.date, fields.note
	next.updatedAt = p.Now.UTC()
	return &next, nil
}

type txFields struct {
	account  *Account
	category *Category
	typ      TxType
	amount   money.Money
	date     time.Time
	note     string
	now      time.Time
	loc      *time.Location
}

type validTxFields struct {
	date time.Time
	note string
}

func validateTxFields(userID uuid.UUID, f txFields) (validTxFields, error) {
	if err := validateTxTarget(userID, f.account, f.category, f.typ, f.amount); err != nil {
		return validTxFields{}, err
	}
	date, err := validateDate(f.date, f.now, f.loc)
	if err != nil {
		return validTxFields{}, err
	}
	note, err := normalizeNote(f.note)
	if err != nil {
		return validTxFields{}, err
	}
	return validTxFields{date: date, note: note}, nil
}

// TransactionState adalah snapshot untuk rehydrate dari database.
type TransactionState struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	AccountID  uuid.UUID
	CategoryID uuid.UUID
	Type       TxType
	Amount     money.Money
	Date       time.Time
	Note       string
	Source     TxSource
	RuleID     *uuid.UUID
	Occurrence *time.Time
	ImportHash []byte
	Version    int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// RehydrateTransaction hanya dipakai repository.
func RehydrateTransaction(s TransactionState) *Transaction {
	return &Transaction{
		id: s.ID, userID: s.UserID, accountID: s.AccountID, categoryID: s.CategoryID,
		typ: s.Type, amount: s.Amount, date: DateOf(s.Date), note: s.Note, source: s.Source,
		ruleID: s.RuleID, occurrence: s.Occurrence, importHash: s.ImportHash, version: s.Version, createdAt: s.CreatedAt, updatedAt: s.UpdatedAt,
	}
}

func (t *Transaction) ID() uuid.UUID               { return t.id }
func (t *Transaction) UserID() uuid.UUID           { return t.userID }
func (t *Transaction) AccountID() uuid.UUID        { return t.accountID }
func (t *Transaction) CategoryID() uuid.UUID       { return t.categoryID }
func (t *Transaction) Type() TxType                { return t.typ }
func (t *Transaction) Amount() money.Money         { return t.amount }
func (t *Transaction) Date() time.Time             { return t.date }
func (t *Transaction) Note() string                { return t.note }
func (t *Transaction) Source() TxSource            { return t.source }
func (t *Transaction) Version() int                { return t.version }
func (t *Transaction) CreatedAt() time.Time        { return t.createdAt }
func (t *Transaction) UpdatedAt() time.Time        { return t.updatedAt }
func (t *Transaction) RecurringRuleID() *uuid.UUID { return t.ruleID }
func (t *Transaction) OccurrenceDate() *time.Time  { return t.occurrence }
func (t *Transaction) ImportHash() []byte          { return t.importHash }
func (t *Transaction) IsManual() bool              { return t.source == SourceManual }
func (t *Transaction) SyncVersion(v int)           { t.version = v }
func (t *Transaction) SetUpdatedAt(at time.Time)   { t.updatedAt = at.UTC() }

// EnsureEditable menolak edit/hapus langsung untuk transaksi yang dikelola
// aggregate lain (biaya transfer).
func (t *Transaction) EnsureEditable() error {
	if t.source == SourceTransferFee {
		return ErrManagedByTransfer
	}
	return nil
}

// SignedAmount adalah efek transaksi terhadap saldo: + untuk income, - untuk expense.
func (t *Transaction) SignedAmount() (money.Money, error) {
	if t.typ == TxExpense {
		return t.amount.Neg()
	}
	return t.amount, nil
}

// validateTxTarget memeriksa tipe, nominal, akun & kategori (dipakai juga oleh
// recurring rule dan import).
func validateTxTarget(userID uuid.UUID, account *Account, category *Category, typ TxType, amount money.Money) error {
	if _, err := ParseTxType(string(typ)); err != nil {
		return err
	}
	if err := validateAmount(amount.Amount()); err != nil {
		return err
	}
	if account == nil || account.UserID() != userID {
		return ErrAccountNotFound
	}
	if category == nil || !category.VisibleTo(userID) {
		return ErrCategoryNotFound
	}
	if account.IsArchived() {
		return ErrAccountArchived
	}
	if amount.Currency() != account.Currency() {
		return money.ErrCurrencyMismatch
	}
	if category.Type() != typ {
		return ErrCategoryTypeMismatch
	}
	return nil
}
