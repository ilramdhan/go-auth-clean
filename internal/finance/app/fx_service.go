package app

import (
	"cmp"
	"context"
	"errors"
	"math/big"
	"slices"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

// CreateRateInput: Rate desimal string (1 Base = Rate Quote) berlaku mulai AsOf.
type CreateRateInput struct {
	UserID      uuid.UUID
	Base, Quote string
	Rate        string
	AsOf        time.Time
}

var errRatesDisabled = &domain.ValidationError{Field: "rate", Reason: "exchange rates are not enabled"}

func (s *Service) CreateRate(ctx context.Context, in CreateRateInput) (*domain.ExchangeRate, error) {
	if s.rates == nil {
		return nil, errRatesDisabled
	}
	base, err := money.ParseCurrency(in.Base)
	if err != nil {
		return nil, &domain.ValidationError{Field: "base", Reason: err.Error()}
	}
	quote, err := money.ParseCurrency(in.Quote)
	if err != nil {
		return nil, &domain.ValidationError{Field: "quote", Reason: err.Error()}
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	e, err := domain.NewExchangeRate(id, in.UserID, base, quote, in.Rate, in.AsOf, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.rates.Create(ctx, e); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Service) GetRate(ctx context.Context, userID, id uuid.UUID) (*domain.ExchangeRate, error) {
	if s.rates == nil {
		return nil, domain.ErrRateNotFound
	}
	return s.rates.Get(ctx, userID, id)
}

func (s *Service) ListRates(ctx context.Context, userID uuid.UUID) ([]*domain.ExchangeRate, error) {
	if s.rates == nil {
		return []*domain.ExchangeRate{}, nil
	}
	return s.rates.List(ctx, userID)
}

// UpdateRate hanya mengubah nilai kurs (pasangan & tanggal adalah identitas).
func (s *Service) UpdateRate(ctx context.Context, userID, id uuid.UUID, rate string) (*domain.ExchangeRate, error) {
	if s.rates == nil {
		return nil, domain.ErrRateNotFound
	}
	e, err := s.rates.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if err := e.SetRate(rate); err != nil {
		return nil, err
	}
	if err := s.rates.Update(ctx, e); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Service) DeleteRate(ctx context.Context, userID, id uuid.UUID) error {
	if s.rates == nil {
		return domain.ErrRateNotFound
	}
	return s.rates.Delete(ctx, userID, id)
}

// rateBook adalah kurs user di memori untuk konversi banyak baris laporan.
type rateBook struct{ rates []*domain.ExchangeRate }

func (s *Service) loadRates(ctx context.Context, userID uuid.UUID) (*rateBook, error) {
	if s.rates == nil {
		return &rateBook{}, nil
	}
	list, err := s.rates.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &rateBook{rates: list}, nil
}

// rate memilih kurs from->to untuk tanggal date: kurs langsung atau kebalikan
// dengan as_of terbaru <= date; bila semua kurs lebih baru dari date, dipakai
// yang paling awal (lebih baik daripada tidak terkonversi).
func (b *rateBook) rate(from, to money.Currency, date time.Time) (*big.Rat, error) {
	if from == to {
		return big.NewRat(1, 1), nil
	}
	var best *domain.ExchangeRate
	better := func(e *domain.ExchangeRate) bool {
		if best == nil {
			return true
		}
		eOK, bOK := !e.AsOf().After(date), !best.AsOf().After(date)
		switch {
		case eOK != bOK:
			return eOK
		case eOK: // keduanya <= date: ambil terbaru
			return e.AsOf().After(best.AsOf())
		default: // keduanya > date: ambil paling awal
			return e.AsOf().Before(best.AsOf())
		}
	}
	for _, e := range b.rates {
		if (e.Base() == from && e.Quote() == to) || (e.Base() == to && e.Quote() == from) {
			if better(e) {
				best = e
			}
		}
	}
	if best == nil {
		return nil, domain.ErrRateUnavailable
	}
	if best.Base() == from {
		return best.Rate(), nil
	}
	return domain.Invert(best.Rate()), nil
}

func (b *rateBook) convert(m money.Money, to money.Currency, date time.Time) (money.Money, error) {
	r, err := b.rate(m.Currency(), to, date)
	if err != nil {
		return money.Money{}, err
	}
	return domain.ConvertAmount(m, r, to)
}

// ConvertInput: Date nil = hari ini (UTC).
type ConvertInput struct {
	UserID uuid.UUID
	Amount string
	From   string
	To     string // kosong = base currency
	Date   *time.Time
}

// Convert mengonversi nominal memakai kurs user (berguna untuk mengisi to_amount transfer lintas currency).
func (s *Service) Convert(ctx context.Context, in ConvertInput) (money.Money, money.Money, error) {
	from, err := money.ParseCurrency(in.From)
	if err != nil {
		return money.Money{}, money.Money{}, &domain.ValidationError{Field: "from", Reason: err.Error()}
	}
	to, err := s.currencyOrDefault(ctx, in.UserID, in.To)
	if err != nil {
		return money.Money{}, money.Money{}, &domain.ValidationError{Field: "to", Reason: err.Error()}
	}
	m, err := money.Parse(in.Amount, from)
	if err != nil {
		return money.Money{}, money.Money{}, &domain.ValidationError{Field: "amount", Reason: err.Error()}
	}
	date := domain.DateOf(s.now())
	if in.Date != nil {
		date = domain.DateOf(*in.Date)
	}
	book, err := s.loadRates(ctx, in.UserID)
	if err != nil {
		return money.Money{}, money.Money{}, err
	}
	out, err := book.convert(m, to, date)
	return m, out, err
}

// YearMonth adalah income/expense satu bulan dalam base currency.
type YearMonth struct {
	Month   time.Time
	Income  money.Money
	Expense money.Money
	Net     money.Money
}

// YearlyReport adalah ringkasan 12 bulan dalam base currency. Currency tanpa
// kurs sama sekali dilaporkan di Unconverted (tidak ikut dijumlah).
type YearlyReport struct {
	Year        int
	Currency    money.Currency
	Months      []YearMonth
	Income      money.Money
	Expense     money.Money
	Net         money.Money
	SavingsRate string // net / income (%), "0.00" bila income 0
	Unconverted []CurrencySummary
}

// Yearly menyusun laporan tahunan per bulan; transaksi currency lain dikonversi
// ke base currency dengan kurs per tanggal transaksi.
func (s *Service) Yearly(ctx context.Context, userID uuid.UUID, year int) (*YearlyReport, error) {
	if year < 2000 || year > 2100 {
		return nil, &domain.ValidationError{Field: "year", Reason: "must be between 2000 and 2100"}
	}
	st, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	base := st.BaseCurrency()
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.reports.DailyTotals(ctx, userID, from, from.AddDate(1, 0, 0))
	if err != nil {
		return nil, err
	}
	book, err := s.loadRates(ctx, userID)
	if err != nil {
		return nil, err
	}
	income, expense := make([]int64, 12), make([]int64, 12)
	unconv := map[money.Currency]*[2]int64{}
	for _, d := range rows {
		m := int(d.Date.Month()) - 1
		in, errIn := book.convert(money.New(d.Income, d.Currency), base, d.Date)
		ex, errEx := book.convert(money.New(d.Expense, d.Currency), base, d.Date)
		if err := errors.Join(errIn, errEx); err != nil {
			if !errors.Is(err, domain.ErrRateUnavailable) {
				return nil, err
			}
			u := unconv[d.Currency]
			if u == nil {
				u = &[2]int64{}
				unconv[d.Currency] = u
			}
			if u[0], err = addInt64(u[0], d.Income); err != nil {
				return nil, err
			}
			if u[1], err = addInt64(u[1], d.Expense); err != nil {
				return nil, err
			}
			continue
		}
		if income[m], err = addInt64(income[m], in.Amount()); err != nil {
			return nil, err
		}
		if expense[m], err = addInt64(expense[m], ex.Amount()); err != nil {
			return nil, err
		}
	}
	rep := &YearlyReport{Year: year, Currency: base, Months: make([]YearMonth, 12), Unconverted: []CurrencySummary{}}
	var ti, te int64
	for i := range 12 {
		rep.Months[i] = YearMonth{Month: from.AddDate(0, i, 0), Income: money.New(income[i], base),
			Expense: money.New(expense[i], base), Net: money.New(income[i]-expense[i], base)}
		ti += income[i] // tiap bulan sudah lolos addInt64; total 12 bulan dibatasi MaxAmount per baris
		te += expense[i]
	}
	rep.Income, rep.Expense, rep.Net = money.New(ti, base), money.New(te, base), money.New(ti-te, base)
	rep.SavingsRate = domain.Percent(ti-te, ti)
	curs := make([]money.Currency, 0, len(unconv))
	for c := range unconv {
		curs = append(curs, c)
	}
	slices.SortFunc(curs, func(a, b money.Currency) int { return cmp.Compare(a.Code(), b.Code()) })
	for _, c := range curs {
		u := unconv[c]
		rep.Unconverted = append(rep.Unconverted, CurrencySummary{Currency: c, Income: money.New(u[0], c),
			Expense: money.New(u[1], c), Net: money.New(u[0]-u[1], c)})
	}
	return rep, nil
}

// ListAuditLogs mengembalikan audit trail milik userID (data user tsb, termasuk
// perubahan yang dilakukan member pada akun bersamanya). Limit+1 baris.
func (s *Service) ListAuditLogs(ctx context.Context, userID uuid.UUID, f domain.AuditFilter) ([]*domain.AuditEntry, error) {
	if s.audit == nil {
		return []*domain.AuditEntry{}, nil
	}
	return s.audit.List(ctx, userID, f)
}

// autoToAmount mengisi to_amount transfer lintas currency dari kurs user bila
// client tidak mengirimnya; tanpa kurs, domain menolak (to_amount wajib).
func (s *Service) autoToAmount(ctx context.Context, owner uuid.UUID, toAmount, amount money.Money, to money.Currency, date time.Time) (money.Money, error) {
	if !toAmount.Currency().IsZero() || amount.Currency() == to || s.rates == nil {
		return toAmount, nil
	}
	book, err := s.loadRates(ctx, owner)
	if err != nil {
		return money.Money{}, err
	}
	out, err := book.convert(amount, to, domain.DateOf(date))
	if errors.Is(err, domain.ErrRateUnavailable) {
		return toAmount, nil
	}
	return out, err
}
