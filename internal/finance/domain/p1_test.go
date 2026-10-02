package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func TestNextDate(t *testing.T) {
	cases := []struct {
		name     string
		f        Frequency
		interval int
		anchor   int
		from     time.Time
		want     time.Time
	}{
		{"daily", FreqDaily, 1, 0, d(2026, 1, 31), d(2026, 2, 1)},
		{"interval 0 = 1", FreqDaily, 0, 0, d(2026, 1, 1), d(2026, 1, 2)},
		{"weekly x2", FreqWeekly, 2, 0, d(2026, 1, 1), d(2026, 1, 15)},
		{"monthly clamp feb", FreqMonthly, 1, 31, d(2026, 1, 31), d(2026, 2, 28)},
		{"monthly back to anchor", FreqMonthly, 1, 31, d(2026, 2, 28), d(2026, 3, 31)},
		{"monthly leap", FreqMonthly, 1, 30, d(2028, 1, 30), d(2028, 2, 29)},
		{"monthly anchor 0 = day", FreqMonthly, 1, 0, d(2026, 1, 15), d(2026, 2, 15)},
		{"quarterly", FreqMonthly, 3, 31, d(2026, 11, 30), d(2027, 2, 28)},
		{"yearly leap", FreqYearly, 1, 29, d(2028, 2, 29), d(2029, 2, 28)},
		{"once", FreqOnce, 1, 0, d(2026, 1, 1), time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NextDate(c.f, c.interval, c.anchor, c.from); !got.Equal(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
	if _, err := ParseFrequency("once", FreqDaily); !errors.Is(err, ErrInvalidFrequency) {
		t.Fatal("once tidak diizinkan")
	}
	if got := NthOccurrence(FreqMonthly, 1, d(2026, 1, 31), 3); !got.Equal(d(2026, 3, 31)) {
		t.Fatalf("nth = %v", got)
	}
}

func newRule(t *testing.T, p NewRecurringRuleParams) *RecurringRule {
	t.Helper()
	r, err := NewRecurringRule(p)
	if err != nil {
		t.Fatalf("NewRecurringRule: %v", err)
	}
	return r
}

func ruleParams(user uuid.UUID, acc *Account, cat *Category) NewRecurringRuleParams {
	return NewRecurringRuleParams{ID: uuid.New(), UserID: user, Account: acc, Category: cat, Type: TxExpense,
		Amount: money.New(100, acc.Currency()), Note: " Netflix ", Frequency: "monthly", StartDate: d(2026, 1, 31), Now: tNow}
}

func TestNewRecurringRule(t *testing.T) {
	user := uuid.New()
	acc := newAcc(t, user, AccountBank, 0, idr)
	cat := sysCat(TxExpense)
	r := newRule(t, ruleParams(user, acc, cat))
	if r.Note() != "Netflix" || r.MonthDay() != 31 || !r.NextRunDate().Equal(d(2026, 1, 31)) || r.Status() != RuleActive ||
		r.Interval() != 1 || r.Frequency() != FreqMonthly || r.Version() != 1 || r.EndDate() != nil {
		t.Fatalf("rule %+v", r)
	}
	end := d(2026, 1, 1)
	far := d(2200, 1, 1)
	cases := []struct {
		name  string
		mod   func(*NewRecurringRuleParams)
		field string
		err   error
	}{
		{"freq", func(p *NewRecurringRuleParams) { p.Frequency = "once" }, "", ErrInvalidFrequency},
		{"interval", func(p *NewRecurringRuleParams) { p.Interval = 366 }, "interval", nil},
		{"start old", func(p *NewRecurringRuleParams) { p.StartDate = d(1960, 1, 1) }, "", ErrDateTooOld},
		{"start far", func(p *NewRecurringRuleParams) { p.StartDate = far }, "start_date", nil},
		{"end before start", func(p *NewRecurringRuleParams) { p.EndDate = &end }, "end_date", nil},
		{"count+end", func(p *NewRecurringRuleParams) { p.Count, p.EndDate = 2, &far }, "count", nil},
		{"count big", func(p *NewRecurringRuleParams) { p.Count = 1001 }, "count", nil},
		{"note", func(p *NewRecurringRuleParams) { p.Note = strings.Repeat("x", 256) }, "note", nil},
		{"cat type", func(p *NewRecurringRuleParams) { p.Category = sysCat(TxIncome) }, "", ErrCategoryTypeMismatch},
		{"currency", func(p *NewRecurringRuleParams) { p.Amount = money.New(1, usd) }, "", money.ErrCurrencyMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := ruleParams(user, acc, cat)
			c.mod(&p)
			_, err := NewRecurringRule(p)
			if c.err != nil {
				wantErr(t, err, c.err)
				return
			}
			wantValidation(t, err, c.field)
		})
	}
	p := ruleParams(user, acc, cat)
	p.Count = 3
	if r := newRule(t, p); !r.EndDate().Equal(d(2026, 3, 31)) {
		t.Fatalf("count end = %v", r.EndDate())
	}
}

func TestRecurringRule_RunLifecycle(t *testing.T) {
	user := uuid.New()
	acc := newAcc(t, user, AccountBank, 0, idr)
	cat := sysCat(TxExpense)
	p := ruleParams(user, acc, cat)
	p.Count = 3
	r := newRule(t, p)

	if up := r.Upcoming(5); len(up) != 3 || !up[1].Equal(d(2026, 2, 28)) {
		t.Fatalf("upcoming %v", up)
	}
	due := r.DueDates(d(2026, 2, 28), MaxCatchUp)
	if len(due) != 2 {
		t.Fatalf("due %v", due)
	}
	if lim := r.DueDates(d(2026, 12, 1), 1); len(lim) != 1 {
		t.Fatal("limit")
	}
	if all := r.DueDates(d(2026, 12, 1), 10); len(all) != 3 {
		t.Fatalf("end date membatasi: %v", all)
	}
	tx, err := r.BuildTransaction(uuid.New(), acc, cat, due[0], tNow)
	if err != nil || tx.Source() != SourceRecurring || *tx.RecurringRuleID() != r.ID() || !tx.OccurrenceDate().Equal(due[0]) {
		t.Fatalf("tx %+v err %v", tx, err)
	}
	for _, x := range due {
		r.MarkRun(x, tNow)
	}
	if !r.NextRunDate().Equal(d(2026, 3, 31)) || !r.LastRunDate().Equal(d(2026, 2, 28)) || r.Status() != RuleActive {
		t.Fatalf("next %v", r.NextRunDate())
	}

	// pause -> tidak ada due; resume lompat ke kemunculan >= today.
	if err := r.Pause("akun diarsip", tNow); err != nil || r.Status() != RulePaused || r.PauseReason() != "akun diarsip" {
		t.Fatal(err)
	}
	if r.DueDates(d(2026, 12, 1), 10) != nil {
		t.Fatal("paused tidak due")
	}
	wantValidation(t, r.Pause(strings.Repeat("x", 300), tNow), "reason")
	if err := r.Resume(d(2026, 3, 15), tNow); err != nil || r.Status() != RuleActive || r.PauseReason() != "" ||
		!r.NextRunDate().Equal(d(2026, 3, 31)) {
		t.Fatalf("resume next %v err %v", r.NextRunDate(), err)
	}
	if err := r.Resume(d(2026, 3, 15), tNow); err != nil {
		t.Fatal("resume aktif = no-op")
	}
	r.MarkRun(d(2026, 3, 31), tNow)
	if r.Status() != RuleEnded || r.Upcoming(3) != nil {
		t.Fatalf("status %s", r.Status())
	}
	wantErr(t, r.Pause("", tNow), ErrRuleEnded)
	wantErr(t, r.Resume(tNow, tNow), ErrRuleEnded)
	wantErr(t, r.Update(RecurringRuleChange{Account: acc, Category: cat, Now: tNow}), ErrRuleEnded)

	// resume setelah pause panjang -> kemunculan terlewat dilewati, ended bila lewat end.
	p2 := ruleParams(user, acc, cat)
	p2.Frequency, p2.StartDate = "daily", d(2026, 3, 1)
	end := d(2026, 3, 10)
	p2.EndDate = &end
	r2 := newRule(t, p2)
	_ = r2.Pause("", tNow)
	if err := r2.Resume(d(2026, 3, 5), tNow); err != nil || !r2.NextRunDate().Equal(d(2026, 3, 5)) {
		t.Fatalf("next %v", r2.NextRunDate())
	}
	_ = r2.Pause("", tNow)
	_ = r2.Resume(d(2026, 3, 20), tNow)
	if r2.Status() != RuleEnded {
		t.Fatalf("status %s", r2.Status())
	}
}

func TestRecurringRule_Update(t *testing.T) {
	user := uuid.New()
	acc := newAcc(t, user, AccountBank, 0, idr)
	cat := sysCat(TxExpense)
	inc := sysCat(TxIncome)
	r := newRule(t, ruleParams(user, acc, cat))
	r.MarkRun(d(2026, 1, 31), tNow)
	r.MarkRun(d(2026, 2, 28), tNow)

	// Ubah jadwal: next tidak boleh mengulang tanggal yang sudah dijalankan.
	typ, amt := TxIncome, money.New(500, idr)
	if err := r.Update(RecurringRuleChange{Account: acc, Category: inc, Type: &typ, Amount: &amt, Note: ptr("gaji"),
		Frequency: ptr("weekly"), StartDate: ptr(d(2026, 2, 1)), Now: tNow}); err != nil {
		t.Fatal(err)
	}
	if r.Type() != TxIncome || r.Amount().Amount() != 500 || r.Note() != "gaji" || r.MonthDay() != 1 ||
		!r.NextRunDate().Equal(d(2026, 3, 1)) || r.CategoryID() != inc.ID() || r.AccountID() != acc.ID() {
		t.Fatalf("rule next %v %+v", r.NextRunDate(), r)
	}
	// Count menghitung ulang end_date; EndDate &nil menghapus.
	if err := r.Update(RecurringRuleChange{Account: acc, Category: inc, Count: ptr(2), Now: tNow}); err != nil ||
		!r.EndDate().Equal(d(2026, 2, 8)) || r.Status() != RuleEnded {
		t.Fatalf("count end %v status %s err %v", r.EndDate(), r.Status(), err)
	}

	r = newRule(t, ruleParams(user, acc, cat))
	var nilEnd *time.Time
	far := d(2027, 1, 1)
	if err := r.Update(RecurringRuleChange{Account: acc, Category: cat, EndDate: ptr(&far), Interval: ptr(2), Now: tNow}); err != nil ||
		!r.EndDate().Equal(far) || r.Interval() != 2 {
		t.Fatal(err)
	}
	if err := r.Update(RecurringRuleChange{Account: acc, Category: cat, EndDate: &nilEnd, Now: tNow}); err != nil || r.EndDate() != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		c     RecurringRuleChange
		field string
		err   error
	}{
		{"nil acc", RecurringRuleChange{Category: cat}, "", ErrAccountNotFound},
		{"type mismatch", RecurringRuleChange{Account: acc, Category: inc}, "", ErrCategoryTypeMismatch},
		{"interval 0", RecurringRuleChange{Account: acc, Category: cat, Interval: ptr(0)}, "interval", nil},
		{"count 0", RecurringRuleChange{Account: acc, Category: cat, Count: ptr(0)}, "count", nil},
		{"freq", RecurringRuleChange{Account: acc, Category: cat, Frequency: ptr("hourly")}, "", ErrInvalidFrequency},
		{"note", RecurringRuleChange{Account: acc, Category: cat, Note: ptr(strings.Repeat("x", 256))}, "note", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.c.Now = tNow
			err := r.Update(c.c)
			if c.err != nil {
				wantErr(t, err, c.err)
				return
			}
			wantValidation(t, err, c.field)
		})
	}
	st := RecurringRuleState{ID: r.ID(), UserID: user, AccountID: acc.ID(), CategoryID: cat.ID(), Type: TxExpense,
		Amount: r.Amount(), Frequency: FreqDaily, Interval: 1, StartDate: d(2026, 1, 1), NextRunDate: d(2026, 1, 2),
		LastRunDate: ptr(d(2026, 1, 1)), Status: RulePaused, PauseReason: "x", Version: 4, CreatedAt: tNow, UpdatedAt: tNow}
	rr := RehydrateRecurringRule(st)
	rr.SyncVersion(5)
	if rr.Version() != 5 || rr.UserID() != user || !rr.StartDate().Equal(d(2026, 1, 1)) || rr.CreatedAt() != tNow ||
		rr.UpdatedAt() != tNow || !rr.LastRunDate().Equal(d(2026, 1, 1)) || rr.EndDate() != nil {
		t.Fatal("rehydrate")
	}
}

func TestBudget(t *testing.T) {
	user := uuid.New()
	cat := sysCat(TxExpense)
	b, err := NewBudget(NewBudgetParams{ID: uuid.New(), UserID: user, Category: cat, Month: d(2026, 3, 17),
		Amount: money.New(1000, idr), Now: tNow})
	if err != nil || !b.Month().Equal(d(2026, 3, 1)) || !b.MonthEnd().Equal(d(2026, 4, 1)) || b.Threshold() != 80 ||
		b.Version() != 1 || b.UserID() != user || b.CategoryID() != cat.ID() || b.CreatedAt() != tNow || b.UpdatedAt() != tNow {
		t.Fatalf("budget %+v err %v", b, err)
	}
	cases := []struct {
		name  string
		p     NewBudgetParams
		field string
		err   error
	}{
		{"nil cat", NewBudgetParams{Amount: money.New(1, idr)}, "", ErrCategoryNotFound},
		{"foreign cat", NewBudgetParams{Category: RehydrateCategory(CategoryState{ID: uuid.New(), UserID: ptr(uuid.New()), Type: TxExpense}), Amount: money.New(1, idr)}, "", ErrCategoryNotFound},
		{"income", NewBudgetParams{Category: sysCat(TxIncome), Amount: money.New(1, idr)}, "", ErrCategoryTypeMismatch},
		{"zero", NewBudgetParams{Category: cat, Amount: money.New(0, idr)}, "", ErrInvalidAmount},
		{"threshold", NewBudgetParams{Category: cat, Amount: money.New(1, idr), Threshold: -1}, "alert_threshold_pct", nil},
		{"old", NewBudgetParams{Category: cat, Amount: money.New(1, idr), Month: d(1960, 1, 1)}, "", ErrDateTooOld},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.p.UserID, c.p.Now = user, tNow
			if c.p.Month.IsZero() {
				c.p.Month = tNow
			}
			_, err := NewBudget(c.p)
			if c.err != nil {
				wantErr(t, err, c.err)
				return
			}
			wantValidation(t, err, c.field)
		})
	}

	progress := []struct {
		spent     int64
		level     AlertLevel
		pct       string
		overspent bool
	}{
		{0, AlertNone, "0.00", false},
		{799, AlertNone, "79.90", false},
		{800, AlertWarning, "80.00", false},
		{1000, AlertExceeded, "100.00", false},
		{1500, AlertExceeded, "150.00", true},
	}
	for _, c := range progress {
		p := b.Progress(c.spent)
		if p.Level != c.level || p.Percent != c.pct || p.Overspent != c.overspent || p.Remaining != 1000-c.spent {
			t.Errorf("spent %d: %+v", c.spent, p)
		}
	}

	// alert naik sekali per level; turun = dirty tanpa notifikasi.
	steps := []struct {
		spent  int64
		notify AlertLevel
		dirty  bool
	}{
		{100, AlertNone, false}, {850, AlertWarning, true}, {900, AlertNone, false},
		{1200, AlertExceeded, true}, {500, AlertNone, true}, {1200, AlertExceeded, true},
	}
	for i, s := range steps {
		n, dirty := b.EvaluateAlert(s.spent)
		if n != s.notify || dirty != s.dirty {
			t.Errorf("step %d: notify %q dirty %v", i, n, dirty)
		}
	}
	if b.LastAlert() != AlertExceeded {
		t.Fatal("last alert")
	}

	amt := money.New(2000, idr)
	if err := b.Update(&amt, ptr(90), tNow); err != nil || b.Amount().Amount() != 2000 || b.Threshold() != 90 {
		t.Fatal(err)
	}
	wantErr(t, b.Update(ptr(money.New(1, usd)), nil, tNow), money.ErrCurrencyMismatch)
	wantErr(t, b.Update(ptr(money.New(0, idr)), nil, tNow), ErrInvalidAmount)
	wantValidation(t, b.Update(nil, ptr(0), tNow), "alert_threshold_pct")
	wantValidation(t, b.Update(nil, ptr(101), tNow), "alert_threshold_pct")

	rb := RehydrateBudget(BudgetState{ID: b.ID(), Month: d(2026, 3, 1), Amount: amt, Threshold: 80, LastAlert: AlertWarning, Version: 3})
	rb.SyncVersion(4)
	if rb.Version() != 4 || rb.LastAlert() != AlertWarning || rb.ID() != b.ID() {
		t.Fatal("rehydrate")
	}
}

func TestTag(t *testing.T) {
	user := uuid.New()
	tg, err := NewTag(uuid.New(), user, "  #Liburan   Bali ", "#ff8800", tNow)
	if err != nil || tg.Name() != "Liburan Bali" || tg.Color() != "#FF8800" || tg.UserID() != user || tg.CreatedAt() != tNow {
		t.Fatalf("tag %+v err %v", tg, err)
	}
	_, err = NewTag(uuid.New(), user, "#", "", tNow)
	wantValidation(t, err, "name")
	_, err = NewTag(uuid.New(), user, strings.Repeat("é", 31), "", tNow)
	wantValidation(t, err, "name")
	_, err = NewTag(uuid.New(), user, "x", "#GGGGGG", tNow)
	wantValidation(t, err, "color")
	later := tNow.Add(time.Hour)
	if err := tg.Update(ptr("kerja"), ptr(""), later); err != nil || tg.Name() != "kerja" || tg.Color() != "" || tg.UpdatedAt() != later {
		t.Fatal(err)
	}
	wantValidation(t, tg.Update(ptr(""), nil, tNow), "name")
	wantValidation(t, tg.Update(nil, ptr("blue"), tNow), "color")
	if tg.Name() != "kerja" {
		t.Fatal("gagal update tidak boleh mengubah state")
	}
	a, b := uuid.New(), uuid.New()
	ids, err := NormalizeTagIDs([]uuid.UUID{a, b, a})
	if err != nil || len(ids) != 2 || ids[0] != a {
		t.Fatal(err)
	}
	many := make([]uuid.UUID, MaxTagsPerTransaction+1)
	for i := range many {
		many[i] = uuid.New()
	}
	_, err = NormalizeTagIDs(many)
	wantErr(t, err, ErrTooManyTags)
	r := RehydrateTag(a, user, "n", "", tNow, tNow)
	if r.ID() != a {
		t.Fatal("rehydrate")
	}
}

func ptr[T any](v T) *T { return &v }
