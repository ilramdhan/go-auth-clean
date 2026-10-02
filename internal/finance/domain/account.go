package domain

import (
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// AccountType adalah jenis dompet/rekening.
type AccountType string

const (
	AccountCash       AccountType = "cash"
	AccountBank       AccountType = "bank"
	AccountEWallet    AccountType = "ewallet"
	AccountCreditCard AccountType = "credit_card"
)

// ParseAccountType memvalidasi string menjadi AccountType.
func ParseAccountType(s string) (AccountType, error) {
	switch t := AccountType(s); t {
	case AccountCash, AccountBank, AccountEWallet, AccountCreditCard:
		return t, nil
	default:
		return "", ErrInvalidAccountType
	}
}

// DefaultAllowNegative: keputusan MVP, bank & kartu kredit boleh saldo negatif,
// cash & e-wallet tidak.
func (t AccountType) DefaultAllowNegative() bool {
	return t == AccountBank || t == AccountCreditCard
}

// Account adalah aggregate dompet. Field unexported supaya saldo hanya bisa
// berubah lewat method yang menjaga invariant.
type Account struct {
	id            uuid.UUID
	userID        uuid.UUID
	name          string
	typ           AccountType
	initial       money.Money
	balance       money.Money
	allowNegative bool
	version       int
	archivedAt    *time.Time
	createdAt     time.Time
	updatedAt     time.Time
}

// NewAccountParams adalah input pembuatan akun. ID dibuat di app layer (UUIDv7).
type NewAccountParams struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Name           string
	Type           AccountType
	InitialBalance money.Money
	// AllowNegative nil = pakai default dari tipe akun.
	AllowNegative *bool
	Now           time.Time
}

// NewAccount membuat akun baru dengan saldo = saldo awal dan version 1.
func NewAccount(p NewAccountParams) (*Account, error) {
	name, err := normalizeName("name", p.Name)
	if err != nil {
		return nil, err
	}
	if _, err := ParseAccountType(string(p.Type)); err != nil {
		return nil, err
	}
	if p.InitialBalance.Currency().IsZero() {
		return nil, money.ErrUnknownCurrency
	}
	if err := validateBalanceBound(p.InitialBalance); err != nil {
		return nil, err
	}
	allowNeg := p.Type.DefaultAllowNegative()
	if p.AllowNegative != nil {
		allowNeg = *p.AllowNegative
	}
	if p.InitialBalance.IsNegative() && !allowNeg {
		return nil, ErrInsufficientBalance
	}
	now := p.Now.UTC()
	return &Account{
		id: p.ID, userID: p.UserID, name: name, typ: p.Type,
		initial: p.InitialBalance, balance: p.InitialBalance, allowNegative: allowNeg,
		version: 1, createdAt: now, updatedAt: now,
	}, nil
}

// AccountState adalah snapshot untuk membangun ulang Account dari database
// tanpa validasi ulang (data di DB dianggap sudah valid).
type AccountState struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Name           string
	Type           AccountType
	InitialBalance money.Money
	Balance        money.Money
	AllowNegative  bool
	Version        int
	ArchivedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// RehydrateAccount hanya dipakai repository.
func RehydrateAccount(s AccountState) *Account {
	return &Account{
		id: s.ID, userID: s.UserID, name: s.Name, typ: s.Type,
		initial: s.InitialBalance, balance: s.Balance, allowNegative: s.AllowNegative,
		version: s.Version, archivedAt: s.ArchivedAt, createdAt: s.CreatedAt, updatedAt: s.UpdatedAt,
	}
}

func (a *Account) ID() uuid.UUID               { return a.id }
func (a *Account) UserID() uuid.UUID           { return a.userID }
func (a *Account) Name() string                { return a.name }
func (a *Account) Type() AccountType           { return a.typ }
func (a *Account) Currency() money.Currency    { return a.balance.Currency() }
func (a *Account) InitialBalance() money.Money { return a.initial }
func (a *Account) Balance() money.Money        { return a.balance }
func (a *Account) AllowNegative() bool         { return a.allowNegative }
func (a *Account) Version() int                { return a.version }
func (a *Account) ArchivedAt() *time.Time      { return a.archivedAt }
func (a *Account) IsArchived() bool            { return a.archivedAt != nil }
func (a *Account) CreatedAt() time.Time        { return a.createdAt }
func (a *Account) UpdatedAt() time.Time        { return a.updatedAt }

// Touch memperbarui updatedAt (dipanggil sebelum disimpan setelah saldo berubah).
func (a *Account) Touch(now time.Time) { a.updatedAt = now.UTC() }

// SyncVersion dipanggil repository setelah UPDATE ... WHERE version = old berhasil.
func (a *Account) SyncVersion(v int) { a.version = v }

// AccountUpdate berisi perubahan opsional (nil = tidak diubah).
type AccountUpdate struct {
	Name           *string
	InitialBalance *money.Money
	AllowNegative  *bool
	Now            time.Time
}

// Update menerapkan perubahan metadata. Mengubah saldo awal menggeser saldo
// berjalan sebesar selisihnya. Semua-atau-tidak: bila gagal, state tidak berubah.
func (a *Account) Update(u AccountUpdate) error {
	next := *a
	if u.Name != nil {
		name, err := normalizeName("name", *u.Name)
		if err != nil {
			return err
		}
		next.name = name
	}
	if u.AllowNegative != nil {
		next.allowNegative = *u.AllowNegative
	}
	if u.InitialBalance != nil {
		if next.IsArchived() {
			return ErrAccountArchived
		}
		if err := validateBalanceBound(*u.InitialBalance); err != nil {
			return err
		}
		delta, err := u.InitialBalance.Sub(next.initial)
		if err != nil {
			return err
		}
		nb, err := next.balance.Add(delta)
		if err != nil {
			return err
		}
		next.initial, next.balance = *u.InitialBalance, nb
	}
	if next.balance.IsNegative() && !next.allowNegative {
		return ErrInsufficientBalance
	}
	next.updatedAt = u.Now.UTC()
	*a = next
	return nil
}

// Archive menandai akun tidak aktif (idempotent).
func (a *Account) Archive(now time.Time) {
	if a.archivedAt != nil {
		return
	}
	t := now.UTC()
	a.archivedAt, a.updatedAt = &t, t
}

// Unarchive mengaktifkan kembali akun (idempotent).
func (a *Account) Unarchive(now time.Time) {
	if a.archivedAt == nil {
		return
	}
	a.archivedAt, a.updatedAt = nil, now.UTC()
}

// Apply menerapkan efek transaksi ke saldo.
func (a *Account) Apply(t *Transaction) error {
	if t.AccountID() != a.id {
		return &ValidationError{Field: "account_id", Reason: "transaction does not belong to account"}
	}
	delta, err := t.SignedAmount()
	if err != nil {
		return err
	}
	return a.adjust(delta)
}

// Revert membatalkan efek transaksi (untuk update/delete).
func (a *Account) Revert(t *Transaction) error {
	if t.AccountID() != a.id {
		return &ValidationError{Field: "account_id", Reason: "transaction does not belong to account"}
	}
	delta, err := t.SignedAmount()
	if err != nil {
		return err
	}
	neg, err := delta.Neg()
	if err != nil {
		return err
	}
	return a.adjust(neg)
}

// Debit mengurangi saldo sebesar m (m harus positif).
func (a *Account) Debit(m money.Money) error {
	if !m.IsPositive() {
		return ErrInvalidAmount
	}
	neg, err := m.Neg()
	if err != nil {
		return err
	}
	return a.adjust(neg)
}

// Credit menambah saldo sebesar m (m harus positif).
func (a *Account) Credit(m money.Money) error {
	if !m.IsPositive() {
		return ErrInvalidAmount
	}
	return a.adjust(m)
}

func (a *Account) adjust(delta money.Money) error {
	if a.IsArchived() {
		return ErrAccountArchived
	}
	nb, err := a.balance.Add(delta)
	if err != nil {
		return err
	}
	if nb.IsNegative() && !a.allowNegative {
		return ErrInsufficientBalance
	}
	a.balance = nb
	return nil
}

func validateBalanceBound(m money.Money) error {
	if m.Amount() > MaxAmount || m.Amount() < -MaxAmount {
		return ErrAmountTooLarge
	}
	return nil
}
