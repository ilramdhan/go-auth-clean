package domain

import (
	"math/big"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// AlertLevel adalah tingkat peringatan budget.
type AlertLevel string

const (
	AlertNone     AlertLevel = ""
	AlertWarning  AlertLevel = "warning"
	AlertExceeded AlertLevel = "exceeded"
)

func (l AlertLevel) rank() int {
	switch l {
	case AlertWarning:
		return 1
	case AlertExceeded:
		return 2
	default:
		return 0
	}
}

// DefaultAlertThreshold adalah persentase pemakaian default yang memicu warning.
const DefaultAlertThreshold = 80

// Budget adalah batas pengeluaran per kategori expense per bulan. Pemakaian
// (spent) dihitung dari transaksi saat dibaca, tidak disimpan.
type Budget struct {
	id         uuid.UUID
	userID     uuid.UUID
	categoryID uuid.UUID
	month      time.Time
	amount     money.Money
	threshold  int
	lastAlert  AlertLevel
	version    int
	createdAt  time.Time
	updatedAt  time.Time
}

type NewBudgetParams struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Category  *Category
	Month     time.Time // tanggal mana pun di bulan tsb
	Amount    money.Money
	Threshold int // 0 = default 80
	Now       time.Time
}

func NewBudget(p NewBudgetParams) (*Budget, error) {
	if p.Category == nil || !p.Category.VisibleTo(p.UserID) {
		return nil, ErrCategoryNotFound
	}
	if p.Category.Type() != TxExpense {
		return nil, ErrCategoryTypeMismatch
	}
	if err := validateAmount(p.Amount.Amount()); err != nil {
		return nil, err
	}
	th, err := validateThreshold(p.Threshold)
	if err != nil {
		return nil, err
	}
	m := DateOf(p.Month)
	if m.Before(minDate) {
		return nil, ErrDateTooOld
	}
	now := p.Now.UTC()
	return &Budget{
		id: p.ID, userID: p.UserID, categoryID: p.Category.ID(),
		month:  time.Date(m.Year(), m.Month(), 1, 0, 0, 0, 0, time.UTC),
		amount: p.Amount, threshold: th, version: 1, createdAt: now, updatedAt: now,
	}, nil
}

func validateThreshold(n int) (int, error) {
	if n == 0 {
		return DefaultAlertThreshold, nil
	}
	if n < 1 || n > 100 {
		return 0, &ValidationError{Field: "alert_threshold_pct", Reason: "must be between 1 and 100"}
	}
	return n, nil
}

// Update mengubah nominal dan/atau threshold (nil = tidak diubah).
func (b *Budget) Update(amount *money.Money, threshold *int, now time.Time) error {
	if amount != nil {
		if amount.Currency() != b.amount.Currency() {
			return money.ErrCurrencyMismatch
		}
		if err := validateAmount(amount.Amount()); err != nil {
			return err
		}
	}
	if threshold != nil {
		if *threshold == 0 {
			return &ValidationError{Field: "alert_threshold_pct", Reason: "must be between 1 and 100"}
		}
		if _, err := validateThreshold(*threshold); err != nil {
			return err
		}
	}
	if amount != nil {
		b.amount = *amount
	}
	if threshold != nil {
		b.threshold = *threshold
	}
	b.updatedAt = now.UTC()
	return nil
}

// BudgetProgress adalah status pemakaian budget. Remaining negatif = overspent.
type BudgetProgress struct {
	Spent     int64
	Remaining int64
	Percent   string
	Overspent bool
	Level     AlertLevel
}

// Progress menghitung status pemakaian dari total spent (minor unit).
func (b *Budget) Progress(spent int64) BudgetProgress {
	total := b.amount.Amount()
	p := BudgetProgress{Spent: spent, Remaining: total - spent, Percent: Percent(spent, total)}
	sp, tot := big.NewInt(spent), big.NewInt(total)
	switch {
	case sp.Cmp(tot) >= 0:
		p.Level = AlertExceeded
	case new(big.Int).Mul(sp, big.NewInt(100)).Cmp(new(big.Int).Mul(tot, big.NewInt(int64(b.threshold)))) >= 0:
		p.Level = AlertWarning
	}
	p.Overspent = spent > total
	return p
}

// EvaluateAlert membandingkan level sekarang dengan level terakhir yang sudah
// diberitahukan. notify = level baru yang harus dikirim (naik); dirty = state
// berubah dan perlu disimpan (naik atau turun karena transaksi dihapus).
func (b *Budget) EvaluateAlert(spent int64) (notify AlertLevel, dirty bool) {
	lvl := b.Progress(spent).Level
	if lvl == b.lastAlert {
		return AlertNone, false
	}
	up := lvl.rank() > b.lastAlert.rank()
	b.lastAlert = lvl
	if up {
		return lvl, true
	}
	return AlertNone, true
}

type BudgetState struct {
	ID, UserID, CategoryID uuid.UUID
	Month                  time.Time
	Amount                 money.Money
	Threshold              int
	LastAlert              AlertLevel
	Version                int
	CreatedAt, UpdatedAt   time.Time
}

func RehydrateBudget(s BudgetState) *Budget {
	return &Budget{id: s.ID, userID: s.UserID, categoryID: s.CategoryID, month: DateOf(s.Month),
		amount: s.Amount, threshold: s.Threshold, lastAlert: s.LastAlert, version: s.Version,
		createdAt: s.CreatedAt, updatedAt: s.UpdatedAt}
}

func (b *Budget) ID() uuid.UUID         { return b.id }
func (b *Budget) UserID() uuid.UUID     { return b.userID }
func (b *Budget) CategoryID() uuid.UUID { return b.categoryID }
func (b *Budget) Month() time.Time      { return b.month }
func (b *Budget) MonthEnd() time.Time   { return b.month.AddDate(0, 1, 0) }
func (b *Budget) Amount() money.Money   { return b.amount }
func (b *Budget) Threshold() int        { return b.threshold }
func (b *Budget) LastAlert() AlertLevel { return b.lastAlert }
func (b *Budget) Version() int          { return b.version }
func (b *Budget) CreatedAt() time.Time  { return b.createdAt }
func (b *Budget) UpdatedAt() time.Time  { return b.updatedAt }
func (b *Budget) SyncVersion(v int)     { b.version = v }

// BudgetSpent adalah budget beserta total pengeluaran bulannya.
type BudgetSpent struct {
	Budget *Budget
	Spent  int64
}
