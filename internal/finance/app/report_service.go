package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/logger"
	"go-auth-clean/internal/shared/money"
)

// CategoryShare adalah total kategori beserta persentase (string, tampilan saja).
type CategoryShare struct {
	CategoryID uuid.UUID
	Name       string
	Total      money.Money
	Percent    string
}

// CurrencySummary adalah ringkasan income/expense untuk satu currency.
type CurrencySummary struct {
	Currency money.Currency
	Income   money.Money
	Expense  money.Money
	Net      money.Money
}

// Summary adalah data dashboard satu bulan.
type Summary struct {
	Month        time.Time
	Currency     money.Currency // currency untuk by_category & cashflow (base currency user)
	TotalBalance []money.Money
	Totals       []CurrencySummary
	ByCategory   []CategoryShare
	Cashflow     []CashflowItem
}

// CashflowItem adalah satu titik seri cashflow.
type CashflowItem struct {
	Period  time.Time
	Income  money.Money
	Expense money.Money
	Net     money.Money
}

// CashflowInput: From/To inklusif. Currency kosong = base currency user.
type CashflowInput struct {
	UserID      uuid.UUID
	From, To    *time.Time
	Granularity string
	Currency    string
}

// CategoryReportInput: From/To inklusif. Type kosong = expense.
type CategoryReportInput struct {
	UserID   uuid.UUID
	From, To *time.Time
	Type     string
	Currency string
}

// Summary menyusun dashboard bulan tertentu ("YYYY-MM", kosong = bulan ini di
// timezone user). Transfer tidak dihitung sebagai income/expense.
func (s *Service) Summary(ctx context.Context, userID uuid.UUID, month string) (*Summary, error) {
	st, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	from, to, err := domain.ParseMonth(month, s.now(), st.Location())
	if err != nil {
		return nil, err
	}
	cur := st.BaseCurrency()
	balances, err := s.reports.TotalBalances(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("finance.Summary balances: %w", err)
	}
	ie, err := s.reports.IncomeExpense(ctx, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("finance.Summary income: %w", err)
	}
	totals, err := toCurrencySummaries(ie)
	if err != nil {
		return nil, err
	}
	byCat, err := s.categoryShares(ctx, userID, domain.TxExpense, cur, from, to)
	if err != nil {
		return nil, err
	}
	flow, err := s.cashflow(ctx, userID, cur, domain.GranularityDay, from, to)
	if err != nil {
		return nil, err
	}
	if balances == nil {
		balances = []money.Money{}
	}
	return &Summary{Month: from, Currency: cur, TotalBalance: balances, Totals: totals, ByCategory: byCat, Cashflow: flow}, nil
}

// Cashflow mengembalikan seri income/expense per hari/bulan, lengkap dengan
// periode kosong bernilai 0 (agar grafik tidak bolong). Default: 12 bulan terakhir.
func (s *Service) Cashflow(ctx context.Context, in CashflowInput) ([]CashflowItem, money.Currency, error) {
	g, err := domain.ParseGranularity(in.Granularity)
	if err != nil {
		return nil, money.Currency{}, err
	}
	st, err := s.GetSettings(ctx, in.UserID)
	if err != nil {
		return nil, money.Currency{}, err
	}
	cur := st.BaseCurrency()
	if in.Currency != "" {
		if cur, err = money.ParseCurrency(in.Currency); err != nil {
			return nil, money.Currency{}, err
		}
	}
	from, toIncl := defaultRange(in.From, in.To, g, domain.Today(s.now(), st.Location()))
	if err := domain.ValidateRange(from, toIncl, g); err != nil {
		return nil, money.Currency{}, err
	}
	if g == domain.GranularityMonth {
		from = time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	items, err := s.cashflow(ctx, in.UserID, cur, g, from, toIncl.AddDate(0, 0, 1))
	return items, cur, err
}

// defaultRange: tanpa from/to -> bulan ini (day) atau 12 bulan terakhir (month).
func defaultRange(from, to *time.Time, g domain.Granularity, today time.Time) (time.Time, time.Time) {
	end := today
	if to != nil {
		end = domain.DateOf(*to)
	}
	var start time.Time
	switch {
	case from != nil:
		start = domain.DateOf(*from)
	case g == domain.GranularityDay:
		start = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		start = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -11, 0)
	}
	return start, end
}

// CategoryReport mengembalikan breakdown per kategori (roll-up ke parent).
func (s *Service) CategoryReport(ctx context.Context, in CategoryReportInput) ([]CategoryShare, money.Currency, error) {
	typ := domain.TxExpense
	if in.Type != "" {
		t, err := domain.ParseTxType(in.Type)
		if err != nil {
			return nil, money.Currency{}, err
		}
		typ = t
	}
	st, err := s.GetSettings(ctx, in.UserID)
	if err != nil {
		return nil, money.Currency{}, err
	}
	cur := st.BaseCurrency()
	if in.Currency != "" {
		if cur, err = money.ParseCurrency(in.Currency); err != nil {
			return nil, money.Currency{}, err
		}
	}
	from, toIncl := defaultRange(in.From, in.To, domain.GranularityDay, domain.Today(s.now(), st.Location()))
	if err := domain.ValidateRange(from, toIncl, domain.GranularityDay); err != nil {
		return nil, money.Currency{}, err
	}
	shares, err := s.categoryShares(ctx, in.UserID, typ, cur, from, toIncl.AddDate(0, 0, 1))
	return shares, cur, err
}

func (s *Service) categoryShares(ctx context.Context, userID uuid.UUID, typ domain.TxType, cur money.Currency, from, to time.Time) ([]CategoryShare, error) {
	rows, err := s.reports.CategoryBreakdown(ctx, userID, typ, cur, from, to)
	if err != nil {
		return nil, fmt.Errorf("finance.categoryShares: %w", err)
	}
	var total int64
	for _, r := range rows {
		if total, err = addInt64(total, r.Total); err != nil {
			return nil, err
		}
	}
	out := make([]CategoryShare, 0, len(rows))
	for _, r := range rows {
		out = append(out, CategoryShare{CategoryID: r.CategoryID, Name: r.Name,
			Total: money.New(r.Total, cur), Percent: domain.Percent(r.Total, total)})
	}
	return out, nil
}

func (s *Service) cashflow(ctx context.Context, userID uuid.UUID, cur money.Currency, g domain.Granularity, from, to time.Time) ([]CashflowItem, error) {
	pts, err := s.reports.Cashflow(ctx, userID, cur, g, from, to)
	if err != nil {
		return nil, fmt.Errorf("finance.cashflow: %w", err)
	}
	out := make([]CashflowItem, 0, len(pts))
	for _, p := range pts {
		inc, exp := money.New(p.Income, cur), money.New(p.Expense, cur)
		net, err := inc.Sub(exp)
		if err != nil {
			return nil, err
		}
		out = append(out, CashflowItem{Period: p.Period, Income: inc, Expense: exp, Net: net})
	}
	return out, nil
}

func toCurrencySummaries(rows []domain.IncomeExpense) ([]CurrencySummary, error) {
	out := make([]CurrencySummary, 0, len(rows))
	for _, r := range rows {
		inc, exp := money.New(r.Income, r.Currency), money.New(r.Expense, r.Currency)
		net, err := inc.Sub(exp)
		if err != nil {
			return nil, err
		}
		out = append(out, CurrencySummary{Currency: r.Currency, Income: inc, Expense: exp, Net: net})
	}
	return out, nil
}

func addInt64(a, b int64) (int64, error) {
	m, err := money.New(a, money.IDR).Add(money.New(b, money.IDR))
	return m.Amount(), err
}

// ReconcileBalances menghitung ulang saldo dari transaksi & transfer lalu
// melaporkan akun yang drift. Tidak auto-fix: drift = bug yang harus diselidiki.
// userID nil = semua user (job), selain itu hanya akun milik user tersebut.
func (s *Service) ReconcileBalances(ctx context.Context, userID *uuid.UUID) ([]domain.BalanceDrift, error) {
	drifts, err := s.reports.BalanceDrifts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("finance.ReconcileBalances: %w", err)
	}
	for _, d := range drifts {
		logger.FromContext(ctx).ErrorContext(ctx, "balance drift detected",
			slog.String("account_id", d.AccountID.String()),
			slog.Int64("cached", d.Cached), slog.Int64("expected", d.Expected))
	}
	if drifts == nil {
		drifts = []domain.BalanceDrift{}
	}
	return drifts, nil
}
