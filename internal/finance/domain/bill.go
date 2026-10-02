package domain

import (
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

type BillStatus string

const (
	BillActive BillStatus = "active"
	BillPaused BillStatus = "paused"
	BillDone   BillStatus = "done"
)

// MaxRemindDays adalah batas pengingat sebelum jatuh tempo.
const MaxRemindDays = 30

// Bill adalah tagihan berkala dengan pengingat sebelum jatuh tempo.
type Bill struct {
	id           uuid.UUID
	userID       uuid.UUID
	name         string
	amount       money.Money
	accountID    *uuid.UUID
	categoryID   *uuid.UUID
	freq         Frequency
	monthDay     int
	nextDue      time.Time
	remindDays   int
	lastReminded *time.Time
	overdueFor   *time.Time // jatuh tempo yang sudah ditandai/dinotifikasi overdue
	status       BillStatus
	version      int
	createdAt    time.Time
	updatedAt    time.Time
}

type NewBillParams struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	Amount     money.Money
	Account    *Account  // opsional
	Category   *Category // opsional, harus expense
	Frequency  string
	DueDate    time.Time
	RemindDays *int // nil = 3
	Now        time.Time
}

func NewBill(p NewBillParams) (*Bill, error) {
	b := &Bill{id: p.ID, userID: p.UserID, status: BillActive, version: 1, remindDays: 3,
		createdAt: p.Now.UTC(), updatedAt: p.Now.UTC(), amount: p.Amount}
	name, err := normalizeName("name", p.Name)
	if err != nil {
		return nil, err
	}
	b.name = name
	if err := validateAmount(p.Amount.Amount()); err != nil {
		return nil, err
	}
	if b.freq, err = ParseFrequency(p.Frequency, FreqOnce, FreqWeekly, FreqMonthly, FreqYearly); err != nil {
		return nil, err
	}
	if b.nextDue, err = validatePlanDate("due_date", p.DueDate, p.Now); err != nil {
		return nil, err
	}
	b.monthDay = b.nextDue.Day()
	if p.RemindDays != nil {
		if err := b.setRemind(*p.RemindDays); err != nil {
			return nil, err
		}
	}
	if err := b.setLinks(p.Account, p.Category); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Bill) setRemind(n int) error {
	if n < 0 || n > MaxRemindDays {
		return &ValidationError{Field: "remind_days_before", Reason: "must be between 0 and 30"}
	}
	b.remindDays = n
	return nil
}

func (b *Bill) setLinks(acc *Account, cat *Category) error {
	if acc != nil {
		if acc.UserID() != b.userID {
			return ErrAccountNotFound
		}
		if acc.Currency() != b.amount.Currency() {
			return money.ErrCurrencyMismatch
		}
		id := acc.ID()
		b.accountID = &id
	}
	if cat != nil {
		if !cat.VisibleTo(b.userID) {
			return ErrCategoryNotFound
		}
		if cat.Type() != TxExpense {
			return ErrCategoryTypeMismatch
		}
		id := cat.ID()
		b.categoryID = &id
	}
	return nil
}

// BillChange: nil = tidak diubah.
type BillChange struct {
	Name          *string
	Amount        *money.Money
	Account       *Account
	ClearAccount  bool
	Category      *Category
	ClearCategory bool
	Frequency     *string
	DueDate       *time.Time
	RemindDays    *int
	Paused        *bool
	Now           time.Time
}

func (b *Bill) Update(c BillChange) error {
	if b.status == BillDone {
		return ErrBillDone
	}
	next := *b
	var err error
	if c.Name != nil {
		if next.name, err = normalizeName("name", *c.Name); err != nil {
			return err
		}
	}
	if c.Amount != nil {
		if c.Amount.Currency() != b.amount.Currency() {
			return money.ErrCurrencyMismatch
		}
		if err := validateAmount(c.Amount.Amount()); err != nil {
			return err
		}
		next.amount = *c.Amount
	}
	if c.ClearAccount {
		next.accountID = nil
	}
	if c.ClearCategory {
		next.categoryID = nil
	}
	if err := next.setLinks(c.Account, c.Category); err != nil {
		return err
	}
	if c.Frequency != nil {
		if next.freq, err = ParseFrequency(*c.Frequency, FreqOnce, FreqWeekly, FreqMonthly, FreqYearly); err != nil {
			return err
		}
	}
	if c.DueDate != nil {
		if next.nextDue, err = validatePlanDate("due_date", *c.DueDate, c.Now); err != nil {
			return err
		}
		next.monthDay, next.lastReminded, next.overdueFor = next.nextDue.Day(), nil, nil
	}
	if c.RemindDays != nil {
		if err := next.setRemind(*c.RemindDays); err != nil {
			return err
		}
	}
	if c.Paused != nil {
		next.status = BillActive
		if *c.Paused {
			next.status = BillPaused
		}
	}
	next.updatedAt = c.Now.UTC()
	*b = next
	return nil
}

// MarkPaid memajukan jatuh tempo ke periode berikutnya; tagihan sekali bayar menjadi done.
func (b *Bill) MarkPaid(now time.Time) error {
	if b.status == BillDone {
		return ErrBillDone
	}
	if b.freq == FreqOnce {
		b.status = BillDone
	} else {
		b.nextDue = NextDate(b.freq, 1, b.monthDay, b.nextDue)
	}
	b.lastReminded, b.overdueFor, b.updatedAt = nil, nil, now.UTC()
	return nil
}

// RemindAt adalah tanggal pengingat untuk jatuh tempo berikutnya.
func (b *Bill) RemindAt() time.Time { return b.nextDue.AddDate(0, 0, -b.remindDays) }

// ShouldRemind: aktif, sudah masuk jendela pengingat, dan belum diingatkan
// untuk jatuh tempo ini (idempotent per next_due_date).
func (b *Bill) ShouldRemind(today time.Time) bool {
	t := DateOf(today)
	return b.status == BillActive && !t.Before(b.RemindAt()) &&
		(b.lastReminded == nil || !b.lastReminded.Equal(b.nextDue))
}

// MarkReminded mencatat pengingat sudah dikirim untuk jatuh tempo sekarang.
func (b *Bill) MarkReminded(now time.Time) {
	d := b.nextDue
	b.lastReminded, b.updatedAt = &d, now.UTC()
}

// IsOverdue: belum dibayar dan jatuh tempo sudah lewat.
func (b *Bill) IsOverdue(today time.Time) bool {
	return b.status == BillActive && DateOf(today).After(b.nextDue)
}

// MarkOverdue menandai tagihan overdue untuk jatuh tempo saat ini; true bila
// baru ditandai (notifikasi overdue cukup sekali per jatuh tempo).
func (b *Bill) MarkOverdue(today time.Time, now time.Time) bool {
	if !b.IsOverdue(today) || (b.overdueFor != nil && b.overdueFor.Equal(b.nextDue)) {
		return false
	}
	d := b.nextDue
	b.overdueFor, b.updatedAt = &d, now.UTC()
	return true
}

type BillState struct {
	ID, UserID           uuid.UUID
	Name                 string
	Amount               money.Money
	AccountID            *uuid.UUID
	CategoryID           *uuid.UUID
	Frequency            Frequency
	MonthDay             int
	NextDue              time.Time
	RemindDays           int
	LastReminded         *time.Time
	OverdueFor           *time.Time
	Status               BillStatus
	Version              int
	CreatedAt, UpdatedAt time.Time
}

func RehydrateBill(s BillState) *Bill {
	return &Bill{id: s.ID, userID: s.UserID, name: s.Name, amount: s.Amount, accountID: s.AccountID,
		categoryID: s.CategoryID, freq: s.Frequency, monthDay: s.MonthDay, nextDue: DateOf(s.NextDue),
		remindDays: s.RemindDays, lastReminded: datePtr(s.LastReminded), overdueFor: datePtr(s.OverdueFor), status: s.Status, version: s.Version,
		createdAt: s.CreatedAt, updatedAt: s.UpdatedAt}
}

func (b *Bill) ID() uuid.UUID               { return b.id }
func (b *Bill) UserID() uuid.UUID           { return b.userID }
func (b *Bill) Name() string                { return b.name }
func (b *Bill) Amount() money.Money         { return b.amount }
func (b *Bill) AccountID() *uuid.UUID       { return b.accountID }
func (b *Bill) CategoryID() *uuid.UUID      { return b.categoryID }
func (b *Bill) Frequency() Frequency        { return b.freq }
func (b *Bill) MonthDay() int               { return b.monthDay }
func (b *Bill) NextDueDate() time.Time      { return b.nextDue }
func (b *Bill) RemindDays() int             { return b.remindDays }
func (b *Bill) LastRemindedFor() *time.Time { return b.lastReminded }
func (b *Bill) OverdueFor() *time.Time      { return b.overdueFor }
func (b *Bill) Status() BillStatus          { return b.status }
func (b *Bill) Version() int                { return b.version }
func (b *Bill) CreatedAt() time.Time        { return b.createdAt }
func (b *Bill) UpdatedAt() time.Time        { return b.updatedAt }
func (b *Bill) SyncVersion(v int)           { b.version = v }
