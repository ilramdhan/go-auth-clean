package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// DebtDirection: payable = user berhutang, receivable = orang lain berhutang ke user.
type DebtDirection string

const (
	DebtPayable    DebtDirection = "payable"
	DebtReceivable DebtDirection = "receivable"
)

type DebtStatus string

const (
	DebtOpen    DebtStatus = "open"
	DebtSettled DebtStatus = "settled"
)

// MaxCounterpartyLength adalah panjang maksimal nama pihak lawan.
const MaxCounterpartyLength = 100

// Debt adalah hutang/piutang dengan cicilan. Sisa = principal - total pembayaran.
type Debt struct {
	id           uuid.UUID
	userID       uuid.UUID
	direction    DebtDirection
	counterparty string
	principal    money.Money
	startDate    time.Time
	dueDate      *time.Time
	note         string
	status       DebtStatus
	version      int
	createdAt    time.Time
	updatedAt    time.Time
}

type NewDebtParams struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	Direction    string
	Counterparty string
	Principal    money.Money
	StartDate    time.Time
	DueDate      *time.Time
	Note         string
	Now          time.Time
	Location     *time.Location
}

func NewDebt(p NewDebtParams) (*Debt, error) {
	dir := DebtDirection(p.Direction)
	if dir != DebtPayable && dir != DebtReceivable {
		return nil, &ValidationError{Field: "direction", Reason: "must be payable or receivable"}
	}
	cp, err := normalizeCounterparty(p.Counterparty)
	if err != nil {
		return nil, err
	}
	if err := validateAmount(p.Principal.Amount()); err != nil {
		return nil, err
	}
	start, err := validateDate(p.StartDate, p.Now, p.Location)
	if err != nil {
		return nil, err
	}
	due, err := validateDue(p.DueDate, start, p.Now)
	if err != nil {
		return nil, err
	}
	note, err := normalizeNote(p.Note)
	if err != nil {
		return nil, err
	}
	now := p.Now.UTC()
	return &Debt{id: p.ID, userID: p.UserID, direction: dir, counterparty: cp, principal: p.Principal,
		startDate: start, dueDate: due, note: note, status: DebtOpen, version: 1, createdAt: now, updatedAt: now}, nil
}

func normalizeCounterparty(s string) (string, error) {
	n := strings.Join(strings.Fields(s), " ")
	l := utf8.RuneCountInString(n)
	if l == 0 {
		return "", &ValidationError{Field: "counterparty", Reason: "must not be empty"}
	}
	if l > MaxCounterpartyLength {
		return "", &ValidationError{Field: "counterparty", Reason: "max 100 characters"}
	}
	return n, nil
}

func validateDue(d *time.Time, start, now time.Time) (*time.Time, error) {
	if d == nil {
		return nil, nil
	}
	v, err := validatePlanDate("due_date", *d, now)
	if err != nil {
		return nil, err
	}
	if v.Before(start) {
		return nil, &ValidationError{Field: "due_date", Reason: "must not be before start_date"}
	}
	return &v, nil
}

// DebtChange: nil = tidak diubah. Principal tidak boleh di bawah total pembayaran.
type DebtChange struct {
	Counterparty *string
	Principal    *money.Money
	DueDate      *time.Time
	ClearDueDate bool
	Note         *string
	Paid         int64
	Now          time.Time
}

func (d *Debt) Update(c DebtChange) error {
	next := *d
	var err error
	if c.Counterparty != nil {
		if next.counterparty, err = normalizeCounterparty(*c.Counterparty); err != nil {
			return err
		}
	}
	if c.Principal != nil {
		if c.Principal.Currency() != d.principal.Currency() {
			return money.ErrCurrencyMismatch
		}
		if err := validateAmount(c.Principal.Amount()); err != nil {
			return err
		}
		if c.Principal.Amount() < c.Paid {
			return &ValidationError{Field: "principal", Reason: "must not be below total paid"}
		}
		next.principal = *c.Principal
	}
	if c.ClearDueDate {
		next.dueDate = nil
	} else if c.DueDate != nil {
		if next.dueDate, err = validateDue(c.DueDate, d.startDate, c.Now); err != nil {
			return err
		}
	}
	if c.Note != nil {
		if next.note, err = normalizeNote(*c.Note); err != nil {
			return err
		}
	}
	next.updatedAt = c.Now.UTC()
	next.syncStatus(c.Paid)
	*d = next
	return nil
}

func (d *Debt) syncStatus(paid int64) bool {
	want := DebtOpen
	if paid >= d.principal.Amount() {
		want = DebtSettled
	}
	changed := want != d.status
	d.status = want
	return changed
}

// ApplyPayments menyelaraskan status dengan total pembayaran (true = berubah).
func (d *Debt) ApplyPayments(paid int64, now time.Time) bool {
	if d.syncStatus(paid) {
		d.updatedAt = now.UTC()
		return true
	}
	return false
}

// Remaining adalah sisa yang belum dibayar (tidak negatif).
func (d *Debt) Remaining(paid int64) int64 { return max(d.principal.Amount()-paid, 0) }

// IsOverdue: belum lunas dan due_date sudah lewat.
func (d *Debt) IsOverdue(today time.Time) bool {
	return d.status == DebtOpen && d.dueDate != nil && DateOf(today).After(*d.dueDate)
}

// DebtPayment adalah satu cicilan, opsional tertaut ke transaksi.
type DebtPayment struct {
	ID            uuid.UUID
	DebtID        uuid.UUID
	UserID        uuid.UUID
	Amount        money.Money
	Date          time.Time
	TransactionID *uuid.UUID
	Note          string
	CreatedAt     time.Time
}

type NewDebtPaymentParams struct {
	ID          uuid.UUID
	Debt        *Debt
	Paid        int64 // total pembayaran sebelumnya
	Amount      money.Money
	Date        time.Time
	Transaction *Transaction // opsional
	Note        string
	Now         time.Time
	Location    *time.Location
}

func NewDebtPayment(p NewDebtPaymentParams) (*DebtPayment, error) {
	if p.Debt.status == DebtSettled {
		return nil, ErrDebtSettled
	}
	amount := p.Amount
	var txID *uuid.UUID
	if p.Transaction != nil {
		if p.Transaction.UserID() != p.Debt.userID {
			return nil, ErrTransactionNotFound
		}
		// payable dibayar dengan expense; receivable diterima sebagai income
		want := TxExpense
		if p.Debt.direction == DebtReceivable {
			want = TxIncome
		}
		if p.Transaction.Type() != want {
			return nil, &ValidationError{Field: "transaction_id", Reason: "transaction type must be " + string(want)}
		}
		if amount.Currency().IsZero() {
			amount = p.Transaction.Amount()
		}
		id := p.Transaction.ID()
		txID = &id
	}
	if amount.Currency() != p.Debt.principal.Currency() {
		return nil, money.ErrCurrencyMismatch
	}
	if err := validateAmount(amount.Amount()); err != nil {
		return nil, err
	}
	if amount.Amount() > p.Debt.Remaining(p.Paid) {
		return nil, ErrOverpayment
	}
	d, err := validateDate(p.Date, p.Now, p.Location)
	if err != nil {
		return nil, err
	}
	note, err := normalizeNote(p.Note)
	if err != nil {
		return nil, err
	}
	return &DebtPayment{ID: p.ID, DebtID: p.Debt.id, UserID: p.Debt.userID, Amount: amount, Date: d,
		TransactionID: txID, Note: note, CreatedAt: p.Now.UTC()}, nil
}

type DebtState struct {
	ID, UserID           uuid.UUID
	Direction            DebtDirection
	Counterparty         string
	Principal            money.Money
	StartDate            time.Time
	DueDate              *time.Time
	Note                 string
	Status               DebtStatus
	Version              int
	CreatedAt, UpdatedAt time.Time
}

func RehydrateDebt(s DebtState) *Debt {
	return &Debt{id: s.ID, userID: s.UserID, direction: s.Direction, counterparty: s.Counterparty,
		principal: s.Principal, startDate: DateOf(s.StartDate), dueDate: datePtr(s.DueDate), note: s.Note,
		status: s.Status, version: s.Version, createdAt: s.CreatedAt, updatedAt: s.UpdatedAt}
}

func (d *Debt) ID() uuid.UUID            { return d.id }
func (d *Debt) UserID() uuid.UUID        { return d.userID }
func (d *Debt) Direction() DebtDirection { return d.direction }
func (d *Debt) Counterparty() string     { return d.counterparty }
func (d *Debt) Principal() money.Money   { return d.principal }
func (d *Debt) StartDate() time.Time     { return d.startDate }
func (d *Debt) DueDate() *time.Time      { return d.dueDate }
func (d *Debt) Note() string             { return d.note }
func (d *Debt) Status() DebtStatus       { return d.status }
func (d *Debt) Version() int             { return d.version }
func (d *Debt) CreatedAt() time.Time     { return d.createdAt }
func (d *Debt) UpdatedAt() time.Time     { return d.updatedAt }
func (d *Debt) SyncVersion(v int)        { d.version = v }
