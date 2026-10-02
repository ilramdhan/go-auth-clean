package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

var _ domain.ReportRepository = (*ReportRepository)(nil)

// ReportRepository adalah read model; transfer tidak pernah dihitung sebagai
// income/expense (biaya transfer masuk lewat transaksi fee).
type ReportRepository struct{ base }

func NewReportRepository(db *pgxpool.Pool) *ReportRepository {
	return &ReportRepository{base{db}}
}

// TotalBalances menjumlah saldo semua akun aktif (termasuk arsip) per currency.
func (r *ReportRepository) TotalBalances(ctx context.Context, userID uuid.UUID) ([]money.Money, error) {
	const q = `SELECT currency, COALESCE(SUM(current_balance), 0)::bigint FROM accounts
		WHERE user_id = $1 AND deleted_at IS NULL GROUP BY currency ORDER BY currency`
	rows, err := r.conn(ctx).Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("reportRepo.TotalBalances: %w", err)
	}
	defer rows.Close()
	out := []money.Money{}
	for rows.Next() {
		var code string
		var sum int64
		if err := rows.Scan(&code, &sum); err != nil {
			return nil, fmt.Errorf("reportRepo.TotalBalances scan: %w", err)
		}
		c, err := currency(code)
		if err != nil {
			return nil, err
		}
		out = append(out, money.New(sum, c))
	}
	return out, rows.Err()
}

func (r *ReportRepository) IncomeExpense(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]domain.IncomeExpense, error) {
	const q = `SELECT currency,
			COALESCE(SUM(amount) FILTER (WHERE type = 'income'), 0)::bigint,
			COALESCE(SUM(amount) FILTER (WHERE type = 'expense'), 0)::bigint
		FROM transactions
		WHERE user_id = $1 AND deleted_at IS NULL AND transaction_date >= $2 AND transaction_date < $3
		GROUP BY currency ORDER BY currency`
	rows, err := r.conn(ctx).Query(ctx, q, userID, domain.DateOf(from), domain.DateOf(to))
	if err != nil {
		return nil, fmt.Errorf("reportRepo.IncomeExpense: %w", err)
	}
	defer rows.Close()
	out := []domain.IncomeExpense{}
	for rows.Next() {
		var code string
		var ie domain.IncomeExpense
		if err := rows.Scan(&code, &ie.Income, &ie.Expense); err != nil {
			return nil, fmt.Errorf("reportRepo.IncomeExpense scan: %w", err)
		}
		if ie.Currency, err = currency(code); err != nil {
			return nil, err
		}
		out = append(out, ie)
	}
	return out, rows.Err()
}

// CategoryBreakdown me-roll-up sub kategori ke parent-nya, urut total terbesar.
func (r *ReportRepository) CategoryBreakdown(ctx context.Context, userID uuid.UUID, typ domain.TxType, cur money.Currency, from, to time.Time) ([]domain.CategoryTotal, error) {
	const q = `SELECT root.id, root.name::text, SUM(t.amount)::bigint AS total
		FROM transactions t
		JOIN categories c ON c.id = t.category_id
		JOIN categories root ON root.id = COALESCE(c.parent_id, c.id)
		WHERE t.user_id = $1 AND t.deleted_at IS NULL AND t.type = $2 AND t.currency = $3
		  AND t.transaction_date >= $4 AND t.transaction_date < $5
		GROUP BY root.id, root.name
		ORDER BY total DESC, root.id`
	rows, err := r.conn(ctx).Query(ctx, q, userID, string(typ), cur.Code(), domain.DateOf(from), domain.DateOf(to))
	if err != nil {
		return nil, fmt.Errorf("reportRepo.CategoryBreakdown: %w", err)
	}
	defer rows.Close()
	out := []domain.CategoryTotal{}
	for rows.Next() {
		var ct domain.CategoryTotal
		if err := rows.Scan(&ct.CategoryID, &ct.Name, &ct.Total); err != nil {
			return nil, fmt.Errorf("reportRepo.CategoryBreakdown scan: %w", err)
		}
		out = append(out, ct)
	}
	return out, rows.Err()
}

// Cashflow memakai generate_series sehingga periode tanpa transaksi tetap muncul (0).
func (r *ReportRepository) Cashflow(ctx context.Context, userID uuid.UUID, cur money.Currency, g domain.Granularity, from, to time.Time) ([]domain.CashflowPoint, error) {
	unit := "day"
	if g == domain.GranularityMonth {
		unit = "month"
	}
	const q = `WITH periods AS (
			SELECT generate_series(date_trunc($5, $3::date::timestamp),
				date_trunc($5, ($4::date - 1)::timestamp), ('1 ' || $5)::interval)::date AS p
		), agg AS (
			SELECT date_trunc($5, transaction_date::timestamp)::date AS p,
				SUM(amount) FILTER (WHERE type = 'income') AS income,
				SUM(amount) FILTER (WHERE type = 'expense') AS expense
			FROM transactions
			WHERE user_id = $1 AND currency = $2 AND deleted_at IS NULL
			  AND transaction_date >= $3 AND transaction_date < $4
			GROUP BY 1
		)
		SELECT periods.p, COALESCE(agg.income, 0)::bigint, COALESCE(agg.expense, 0)::bigint
		FROM periods LEFT JOIN agg USING (p) ORDER BY periods.p`
	rows, err := r.conn(ctx).Query(ctx, q, userID, cur.Code(), domain.DateOf(from), domain.DateOf(to), unit)
	if err != nil {
		return nil, fmt.Errorf("reportRepo.Cashflow: %w", err)
	}
	defer rows.Close()
	out := []domain.CashflowPoint{}
	for rows.Next() {
		var p domain.CashflowPoint
		if err := rows.Scan(&p.Period, &p.Income, &p.Expense); err != nil {
			return nil, fmt.Errorf("reportRepo.Cashflow scan: %w", err)
		}
		p.Period = domain.DateOf(p.Period)
		out = append(out, p)
	}
	return out, rows.Err()
}

// BalanceDrifts: expected = initial + income - expense (termasuk transaksi fee)
// - transfer keluar + transfer masuk.
func (r *ReportRepository) BalanceDrifts(ctx context.Context, userID *uuid.UUID) ([]domain.BalanceDrift, error) {
	const q = `SELECT a.id, a.user_id, a.currency, a.current_balance, e.expected FROM accounts a
		CROSS JOIN LATERAL (SELECT a.initial_balance
			+ COALESCE((SELECT SUM(CASE WHEN t.type = 'income' THEN t.amount ELSE -t.amount END)
				FROM transactions t WHERE t.account_id = a.id AND t.deleted_at IS NULL), 0)
			- COALESCE((SELECT SUM(tr.amount) FROM transfers tr
				WHERE tr.from_account_id = a.id AND tr.deleted_at IS NULL), 0)
			+ COALESCE((SELECT SUM(tr.to_amount) FROM transfers tr
				WHERE tr.to_account_id = a.id AND tr.deleted_at IS NULL), 0) AS expected) e
		WHERE a.deleted_at IS NULL AND ($1::uuid IS NULL OR a.user_id = $1)
		  AND a.current_balance <> e.expected
		ORDER BY a.user_id, a.id`
	rows, err := r.conn(ctx).Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("reportRepo.BalanceDrifts: %w", err)
	}
	defer rows.Close()
	out := []domain.BalanceDrift{}
	for rows.Next() {
		var d domain.BalanceDrift
		var code string
		var expected int64
		if err := rows.Scan(&d.AccountID, &d.UserID, &code, &d.Cached, &expected); err != nil {
			return nil, fmt.Errorf("reportRepo.BalanceDrifts scan: %w", err)
		}
		d.Expected = expected
		if d.Currency, err = currency(code); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DailyTotals: income/expense per tanggal & currency (to eksklusif), dipakai
// laporan yang dikonversi ke base currency memakai kurs per tanggal.
func (r *ReportRepository) DailyTotals(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]domain.DailyTotal, error) {
	const q = `SELECT transaction_date, currency,
			COALESCE(SUM(amount) FILTER (WHERE type = 'income'), 0)::bigint,
			COALESCE(SUM(amount) FILTER (WHERE type = 'expense'), 0)::bigint
		FROM transactions
		WHERE user_id = $1 AND deleted_at IS NULL AND transaction_date >= $2 AND transaction_date < $3
		GROUP BY 1, 2 ORDER BY 1, 2`
	rows, err := r.conn(ctx).Query(ctx, q, userID, domain.DateOf(from), domain.DateOf(to))
	if err != nil {
		return nil, fmt.Errorf("reportRepo.DailyTotals: %w", err)
	}
	defer rows.Close()
	out := []domain.DailyTotal{}
	for rows.Next() {
		var d domain.DailyTotal
		var code string
		if err := rows.Scan(&d.Date, &code, &d.Income, &d.Expense); err != nil {
			return nil, fmt.Errorf("reportRepo.DailyTotals scan: %w", err)
		}
		if d.Currency, err = currency(code); err != nil {
			return nil, err
		}
		d.Date = domain.DateOf(d.Date)
		out = append(out, d)
	}
	return out, rows.Err()
}
