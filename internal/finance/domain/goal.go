package domain

import (
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// GoalStatus adalah status target tabungan.
type GoalStatus string

const (
	GoalActive   GoalStatus = "active"
	GoalAchieved GoalStatus = "achieved"
	GoalArchived GoalStatus = "archived"
)

// SavingsGoal adalah target tabungan. Progress = jumlah kontribusi (bisa
// negatif untuk penarikan); opsional terkait satu akun tabungan.
type SavingsGoal struct {
	id         uuid.UUID
	userID     uuid.UUID
	name       string
	target     money.Money
	targetDate *time.Time
	accountID  *uuid.UUID
	status     GoalStatus
	version    int
	createdAt  time.Time
	updatedAt  time.Time
}

type NewGoalParams struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	Target     money.Money
	TargetDate *time.Time
	Account    *Account // opsional
	Now        time.Time
}

func NewSavingsGoal(p NewGoalParams) (*SavingsGoal, error) {
	name, err := normalizeName("name", p.Name)
	if err != nil {
		return nil, err
	}
	if err := validateAmount(p.Target.Amount()); err != nil {
		return nil, err
	}
	g := &SavingsGoal{id: p.ID, userID: p.UserID, name: name, target: p.Target, status: GoalActive, version: 1,
		createdAt: p.Now.UTC(), updatedAt: p.Now.UTC()}
	if err := g.setTargetDate(p.TargetDate, p.Now); err != nil {
		return nil, err
	}
	if err := g.setAccount(p.Account); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *SavingsGoal) setTargetDate(d *time.Time, now time.Time) error {
	if d == nil {
		g.targetDate = nil
		return nil
	}
	v, err := validatePlanDate("target_date", *d, now)
	if err != nil {
		return err
	}
	g.targetDate = &v
	return nil
}

func (g *SavingsGoal) setAccount(a *Account) error {
	if a == nil {
		g.accountID = nil
		return nil
	}
	if a.UserID() != g.userID {
		return ErrAccountNotFound
	}
	if a.Currency() != g.target.Currency() {
		return money.ErrCurrencyMismatch
	}
	id := a.ID()
	g.accountID = &id
	return nil
}

// GoalChange: nil = tidak diubah. ClearTargetDate/ClearAccount menghapus nilai.
type GoalChange struct {
	Name            *string
	Target          *money.Money
	TargetDate      *time.Time
	ClearTargetDate bool
	Account         *Account
	ClearAccount    bool
	Archived        *bool
	Now             time.Time
}

func (g *SavingsGoal) Update(c GoalChange) error {
	next := *g
	if c.Name != nil {
		n, err := normalizeName("name", *c.Name)
		if err != nil {
			return err
		}
		next.name = n
	}
	if c.Target != nil {
		if c.Target.Currency() != g.target.Currency() {
			return money.ErrCurrencyMismatch
		}
		if err := validateAmount(c.Target.Amount()); err != nil {
			return err
		}
		next.target = *c.Target
	}
	if c.ClearTargetDate {
		next.targetDate = nil
	} else if c.TargetDate != nil {
		if err := next.setTargetDate(c.TargetDate, c.Now); err != nil {
			return err
		}
	}
	if c.ClearAccount {
		next.accountID = nil
	} else if c.Account != nil {
		if err := next.setAccount(c.Account); err != nil {
			return err
		}
	}
	if c.Archived != nil {
		if *c.Archived {
			next.status = GoalArchived
		} else if next.status == GoalArchived {
			next.status = GoalActive
		}
	}
	next.updatedAt = c.Now.UTC()
	*g = next
	return nil
}

// Refresh menyelaraskan status achieved/active dengan total kontribusi.
// Mengembalikan true bila status berubah.
func (g *SavingsGoal) Refresh(saved int64, now time.Time) bool {
	if g.status == GoalArchived {
		return false
	}
	want := GoalActive
	if saved >= g.target.Amount() {
		want = GoalAchieved
	}
	if want == g.status {
		return false
	}
	g.status, g.updatedAt = want, now.UTC()
	return true
}

// GoalProgress adalah ringkasan pencapaian target.
type GoalProgress struct {
	Saved     int64
	Remaining int64 // tidak pernah negatif
	Percent   string
	// MonthlyNeeded adalah setoran per bulan agar target tercapai tepat waktu (0 bila tanpa target_date).
	MonthlyNeeded int64
}

func (g *SavingsGoal) Progress(saved int64, today time.Time) GoalProgress {
	p := GoalProgress{Saved: saved, Percent: Percent(saved, g.target.Amount())}
	p.Remaining = max(g.target.Amount()-saved, 0)
	if g.targetDate != nil && p.Remaining > 0 {
		t := DateOf(today)
		months := int64((g.targetDate.Year()-t.Year())*12 + int(g.targetDate.Month()-t.Month()))
		if months < 1 {
			months = 1
		}
		p.MonthlyNeeded = (p.Remaining + months - 1) / months
	}
	return p
}

// EnsureContributable menolak kontribusi ke goal yang diarsip.
func (g *SavingsGoal) EnsureContributable() error {
	if g.status == GoalArchived {
		return ErrGoalArchived
	}
	return nil
}

type SavingsGoalState struct {
	ID, UserID           uuid.UUID
	Name                 string
	Target               money.Money
	TargetDate           *time.Time
	AccountID            *uuid.UUID
	Status               GoalStatus
	Version              int
	CreatedAt, UpdatedAt time.Time
}

func RehydrateSavingsGoal(s SavingsGoalState) *SavingsGoal {
	return &SavingsGoal{id: s.ID, userID: s.UserID, name: s.Name, target: s.Target, targetDate: datePtr(s.TargetDate),
		accountID: s.AccountID, status: s.Status, version: s.Version, createdAt: s.CreatedAt, updatedAt: s.UpdatedAt}
}

func (g *SavingsGoal) ID() uuid.UUID          { return g.id }
func (g *SavingsGoal) UserID() uuid.UUID      { return g.userID }
func (g *SavingsGoal) Name() string           { return g.name }
func (g *SavingsGoal) Target() money.Money    { return g.target }
func (g *SavingsGoal) TargetDate() *time.Time { return g.targetDate }
func (g *SavingsGoal) AccountID() *uuid.UUID  { return g.accountID }
func (g *SavingsGoal) Status() GoalStatus     { return g.status }
func (g *SavingsGoal) Version() int           { return g.version }
func (g *SavingsGoal) CreatedAt() time.Time   { return g.createdAt }
func (g *SavingsGoal) UpdatedAt() time.Time   { return g.updatedAt }
func (g *SavingsGoal) SyncVersion(v int)      { g.version = v }

// GoalContribution adalah setoran (+) atau penarikan (-) ke goal; opsional
// tertaut ke transfer (setoran ke akun tabungan).
type GoalContribution struct {
	ID         uuid.UUID
	GoalID     uuid.UUID
	UserID     uuid.UUID
	Amount     money.Money
	Date       time.Time
	TransferID *uuid.UUID
	Note       string
	CreatedAt  time.Time
}

type NewContributionParams struct {
	ID       uuid.UUID
	Goal     *SavingsGoal
	Amount   money.Money // negatif = penarikan
	Date     time.Time
	Transfer *Transfer // opsional; amount diambil dari transfer
	// Withdraw: penarikan. Dengan Transfer, transfer harus KELUAR dari akun goal
	// dan amount = -transfer.Amount; tanpa Transfer, Amount harus negatif.
	Withdraw bool
	Note     string
	Now      time.Time
	Location *time.Location
}

func NewGoalContribution(p NewContributionParams) (*GoalContribution, error) {
	if err := p.Goal.EnsureContributable(); err != nil {
		return nil, err
	}
	amount, date := p.Amount, p.Date
	var trID *uuid.UUID
	if p.Transfer != nil {
		if p.Transfer.UserID() != p.Goal.userID {
			return nil, ErrTransferNotFound
		}
		amount, date = p.Transfer.ToAmount(), p.Transfer.Date()
		if p.Withdraw {
			if p.Goal.accountID != nil && p.Transfer.FromAccountID() != *p.Goal.accountID {
				return nil, &ValidationError{Field: "transfer_id", Reason: "transfer must leave the goal account"}
			}
			amount = money.New(-p.Transfer.Amount().Amount(), p.Transfer.Amount().Currency())
		} else if p.Goal.accountID != nil && p.Transfer.ToAccountID() != *p.Goal.accountID {
			return nil, &ValidationError{Field: "transfer_id", Reason: "transfer must go to the goal account"}
		}
		id := p.Transfer.ID()
		trID = &id
	}
	if amount.Currency() != p.Goal.target.Currency() {
		return nil, money.ErrCurrencyMismatch
	}
	if amount.IsZero() || (p.Transfer == nil && p.Withdraw != amount.IsNegative()) {
		return nil, ErrInvalidAmount
	}
	if amount.Amount() > MaxAmount || amount.Amount() < -MaxAmount {
		return nil, ErrAmountTooLarge
	}
	d, err := validateDate(date, p.Now, p.Location)
	if err != nil {
		return nil, err
	}
	note, err := normalizeNote(p.Note)
	if err != nil {
		return nil, err
	}
	return &GoalContribution{ID: p.ID, GoalID: p.Goal.id, UserID: p.Goal.userID, Amount: amount, Date: d,
		TransferID: trID, Note: note, CreatedAt: p.Now.UTC()}, nil
}
