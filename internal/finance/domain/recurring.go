package domain

import (
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// RuleStatus adalah status recurring rule.
type RuleStatus string

const (
	RuleActive RuleStatus = "active"
	RulePaused RuleStatus = "paused"
	RuleEnded  RuleStatus = "ended"
)

// MaxCatchUp membatasi jumlah kemunculan yang dibuat per rule per putaran
// worker (catch-up setelah downtime); sisanya diproses putaran berikutnya.
const MaxCatchUp = 31

// RecurringRule adalah template transaksi yang dibuat otomatis oleh worker
// pada setiap tanggal kemunculan (next_run_date).
type RecurringRule struct {
	id          uuid.UUID
	userID      uuid.UUID
	accountID   uuid.UUID
	categoryID  uuid.UUID
	typ         TxType
	amount      money.Money
	note        string
	freq        Frequency
	interval    int
	monthDay    int
	start       time.Time
	end         *time.Time
	next        time.Time
	lastRun     *time.Time
	status      RuleStatus
	pauseReason string
	version     int
	createdAt   time.Time
	updatedAt   time.Time
}

type NewRecurringRuleParams struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Account   *Account
	Category  *Category
	Type      TxType
	Amount    money.Money
	Note      string
	Frequency string
	Interval  int // 0 = 1
	StartDate time.Time
	EndDate   *time.Time
	// Count (opsional, eksklusif dengan EndDate) = jumlah kemunculan; disimpan
	// sebagai end_date = tanggal kemunculan ke-Count.
	Count int
	Now   time.Time
}

// MaxOccurrenceCount membatasi field count.
const MaxOccurrenceCount = 1000

func NewRecurringRule(p NewRecurringRuleParams) (*RecurringRule, error) {
	if err := validateTxTarget(p.UserID, p.Account, p.Category, p.Type, p.Amount); err != nil {
		return nil, err
	}
	v, err := validateSchedule(p.Frequency, p.Interval, p.StartDate, p.EndDate, p.Count, p.Now)
	if err != nil {
		return nil, err
	}
	note, err := normalizeNote(p.Note)
	if err != nil {
		return nil, err
	}
	now := p.Now.UTC()
	return &RecurringRule{
		id: p.ID, userID: p.UserID, accountID: p.Account.ID(), categoryID: p.Category.ID(),
		typ: p.Type, amount: p.Amount, note: note, freq: v.freq, interval: v.interval,
		monthDay: v.start.Day(), start: v.start, end: v.end, next: v.start, status: RuleActive,
		version: 1, createdAt: now, updatedAt: now,
	}, nil
}

type validSchedule struct {
	freq     Frequency
	interval int
	start    time.Time
	end      *time.Time
}

func validateSchedule(freq string, interval int, start time.Time, end *time.Time, count int, now time.Time) (validSchedule, error) {
	f, err := ParseFrequency(freq, FreqDaily, FreqWeekly, FreqMonthly, FreqYearly)
	if err != nil {
		return validSchedule{}, err
	}
	n, err := validateInterval(interval)
	if err != nil {
		return validSchedule{}, err
	}
	s, err := validatePlanDate("start_date", start, now)
	if err != nil {
		return validSchedule{}, err
	}
	var e *time.Time
	if count != 0 {
		if end != nil {
			return validSchedule{}, &ValidationError{Field: "count", Reason: "use either end_date or count"}
		}
		if count < 1 || count > MaxOccurrenceCount {
			return validSchedule{}, &ValidationError{Field: "count", Reason: "must be between 1 and 1000"}
		}
		d := NthOccurrence(f, n, s, count)
		return validSchedule{freq: f, interval: n, start: s, end: &d}, nil
	}
	if end != nil {
		d := DateOf(*end)
		if d.Before(s) {
			return validSchedule{}, &ValidationError{Field: "end_date", Reason: "must not be before start_date"}
		}
		e = &d
	}
	return validSchedule{freq: f, interval: n, start: s, end: e}, nil
}

// RecurringRuleChange: nil = tidak diubah. Mengubah jadwal me-reset next_run
// ke start_date baru bila start berubah.
type RecurringRuleChange struct {
	Account   *Account
	Category  *Category
	Type      *TxType
	Amount    *money.Money
	Note      *string
	Frequency *string
	Interval  *int
	StartDate *time.Time
	EndDate   **time.Time // &nil = hapus end date
	Count     *int        // end_date dihitung ulang dari start_date (eksklusif dengan EndDate)
	Now       time.Time
}

func (r *RecurringRule) Update(c RecurringRuleChange) error {
	if r.status == RuleEnded {
		return ErrRuleEnded
	}
	if c.Account == nil || c.Category == nil {
		return ErrAccountNotFound
	}
	typ, amount := r.typ, r.amount
	if c.Type != nil {
		typ = *c.Type
	}
	if c.Amount != nil {
		amount = *c.Amount
	}
	if err := validateTxTarget(r.userID, c.Account, c.Category, typ, amount); err != nil {
		return err
	}
	freq, interval, start, end := string(r.freq), r.interval, r.start, r.end
	if c.Frequency != nil {
		freq = *c.Frequency
	}
	if c.Interval != nil {
		interval = *c.Interval
		if interval == 0 {
			return &ValidationError{Field: "interval", Reason: "must be between 1 and 365"}
		}
	}
	if c.StartDate != nil {
		start = *c.StartDate
	}
	if c.EndDate != nil {
		end = *c.EndDate
	}
	count := 0
	if c.Count != nil {
		if count = *c.Count; count == 0 {
			return &ValidationError{Field: "count", Reason: "must be between 1 and 1000"}
		}
		if c.EndDate == nil {
			end = nil
		}
	}
	v, err := validateSchedule(freq, interval, start, end, count, c.Now)
	if err != nil {
		return err
	}
	note := r.note
	if c.Note != nil {
		if note, err = normalizeNote(*c.Note); err != nil {
			return err
		}
	}
	scheduleChanged := v.freq != r.freq || v.interval != r.interval || !v.start.Equal(r.start)
	r.accountID, r.categoryID, r.typ, r.amount, r.note = c.Account.ID(), c.Category.ID(), typ, amount, note
	r.freq, r.interval, r.end = v.freq, v.interval, v.end
	if scheduleChanged {
		r.start, r.monthDay = v.start, v.start.Day()
		r.next = r.firstOnOrAfter(r.resumeFrom())
	}
	r.syncEnded()
	r.updatedAt = c.Now.UTC()
	return nil
}

// syncEnded: rule aktif/paused berakhir bila kemunculan berikutnya melewati end_date;
// sebaliknya rule ended yang end_date-nya diperpanjang tidak dihidupkan otomatis.
func (r *RecurringRule) syncEnded() {
	if r.end != nil && r.next.After(*r.end) {
		r.status = RuleEnded
	}
}

// NthOccurrence mengembalikan tanggal kemunculan ke-n (n >= 1) dari start.
func NthOccurrence(f Frequency, interval int, start time.Time, n int) time.Time {
	d := DateOf(start)
	for i := 1; i < n; i++ {
		d = NextDate(f, interval, start.Day(), d)
	}
	return d
}

// resumeFrom: kemunculan berikutnya tidak boleh mengulang tanggal yang sudah dijalankan.
func (r *RecurringRule) resumeFrom() time.Time {
	if r.lastRun != nil && !r.lastRun.Before(r.start) {
		return r.lastRun.AddDate(0, 0, 1)
	}
	return r.start
}

// firstOnOrAfter mencari kemunculan pertama >= d mengikuti jadwal dari start.
func (r *RecurringRule) firstOnOrAfter(d time.Time) time.Time {
	cur := r.start
	for i := 0; cur.Before(d) && i < 100_000; i++ {
		cur = NextDate(r.freq, r.interval, r.monthDay, cur)
	}
	return cur
}

// Pause menghentikan sementara (reason opsional, mis. akun diarsip).
func (r *RecurringRule) Pause(reason string, now time.Time) error {
	if r.status == RuleEnded {
		return ErrRuleEnded
	}
	n, err := normalizeNote(reason)
	if err != nil {
		return &ValidationError{Field: "reason", Reason: "max 255 characters"}
	}
	r.status, r.pauseReason, r.updatedAt = RulePaused, n, now.UTC()
	return nil
}

// Resume mengaktifkan lagi. Kemunculan yang terlewat selama pause TIDAK
// dibuat: next_run digeser ke kemunculan pertama >= hari ini.
func (r *RecurringRule) Resume(today, now time.Time) error {
	if r.status == RuleEnded {
		return ErrRuleEnded
	}
	if r.status == RuleActive {
		return nil
	}
	from := r.resumeFrom()
	if t := DateOf(today); t.After(from) {
		from = t
	}
	r.next = r.firstOnOrAfter(from)
	r.status, r.pauseReason, r.updatedAt = RuleActive, "", now.UTC()
	r.syncEnded()
	return nil
}

// DueDates mengembalikan tanggal kemunculan yang jatuh tempo (<= today),
// maksimal limit. Tidak mengubah state; panggil MarkRun per tanggal.
func (r *RecurringRule) DueDates(today time.Time, limit int) []time.Time {
	if r.status != RuleActive {
		return nil
	}
	today = DateOf(today)
	out := []time.Time{}
	for d := r.next; !d.After(today) && len(out) < limit; d = NextDate(r.freq, r.interval, r.monthDay, d) {
		if r.end != nil && d.After(*r.end) {
			break
		}
		out = append(out, d)
	}
	return out
}

// MarkRun mencatat kemunculan d sudah diproses dan memajukan next_run.
// Rule otomatis berakhir bila next melewati end_date.
func (r *RecurringRule) MarkRun(d, now time.Time) {
	d = DateOf(d)
	r.lastRun = &d
	r.next = NextDate(r.freq, r.interval, r.monthDay, d)
	r.syncEnded()
	r.updatedAt = now.UTC()
}

// Upcoming mengembalikan n kemunculan berikutnya (preview untuk UI).
func (r *RecurringRule) Upcoming(n int) []time.Time {
	if r.status == RuleEnded {
		return nil
	}
	out := []time.Time{}
	for d := r.next; len(out) < n; d = NextDate(r.freq, r.interval, r.monthDay, d) {
		if r.end != nil && d.After(*r.end) {
			break
		}
		out = append(out, d)
	}
	return out
}

// BuildTransaction membuat transaksi untuk kemunculan d. Tanggal transaksi =
// tanggal kemunculan (bisa di masa lalu saat catch-up).
func (r *RecurringRule) BuildTransaction(id uuid.UUID, acc *Account, cat *Category, d, now time.Time) (*Transaction, error) {
	rid, occ := r.id, DateOf(d)
	return NewTransaction(NewTransactionParams{
		ID: id, UserID: r.userID, Account: acc, Category: cat, Type: r.typ, Amount: r.amount,
		Date: occ, Note: r.note, Source: SourceRecurring, RecurringRuleID: &rid, OccurrenceDate: &occ,
		Now: now, Location: time.UTC,
	})
}

type RecurringRuleState struct {
	ID, UserID, AccountID, CategoryID uuid.UUID
	Type                              TxType
	Amount                            money.Money
	Note                              string
	Frequency                         Frequency
	Interval, MonthDay                int
	StartDate                         time.Time
	EndDate                           *time.Time
	NextRunDate                       time.Time
	LastRunDate                       *time.Time
	Status                            RuleStatus
	PauseReason                       string
	Version                           int
	CreatedAt, UpdatedAt              time.Time
}

func RehydrateRecurringRule(s RecurringRuleState) *RecurringRule {
	return &RecurringRule{
		id: s.ID, userID: s.UserID, accountID: s.AccountID, categoryID: s.CategoryID, typ: s.Type,
		amount: s.Amount, note: s.Note, freq: s.Frequency, interval: s.Interval, monthDay: s.MonthDay,
		start: DateOf(s.StartDate), end: datePtr(s.EndDate), next: DateOf(s.NextRunDate),
		lastRun: datePtr(s.LastRunDate), status: s.Status, pauseReason: s.PauseReason,
		version: s.Version, createdAt: s.CreatedAt, updatedAt: s.UpdatedAt,
	}
}

func datePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	d := DateOf(*t)
	return &d
}

func (r *RecurringRule) ID() uuid.UUID           { return r.id }
func (r *RecurringRule) UserID() uuid.UUID       { return r.userID }
func (r *RecurringRule) AccountID() uuid.UUID    { return r.accountID }
func (r *RecurringRule) CategoryID() uuid.UUID   { return r.categoryID }
func (r *RecurringRule) Type() TxType            { return r.typ }
func (r *RecurringRule) Amount() money.Money     { return r.amount }
func (r *RecurringRule) Note() string            { return r.note }
func (r *RecurringRule) Frequency() Frequency    { return r.freq }
func (r *RecurringRule) Interval() int           { return r.interval }
func (r *RecurringRule) MonthDay() int           { return r.monthDay }
func (r *RecurringRule) StartDate() time.Time    { return r.start }
func (r *RecurringRule) EndDate() *time.Time     { return r.end }
func (r *RecurringRule) NextRunDate() time.Time  { return r.next }
func (r *RecurringRule) LastRunDate() *time.Time { return r.lastRun }
func (r *RecurringRule) Status() RuleStatus      { return r.status }
func (r *RecurringRule) PauseReason() string     { return r.pauseReason }
func (r *RecurringRule) Version() int            { return r.version }
func (r *RecurringRule) CreatedAt() time.Time    { return r.createdAt }
func (r *RecurringRule) UpdatedAt() time.Time    { return r.updatedAt }
func (r *RecurringRule) SyncVersion(v int)       { r.version = v }
