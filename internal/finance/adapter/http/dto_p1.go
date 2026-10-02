package http

import (
	"time"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
	"go-auth-clean/internal/shared/pagination"
)

// ===== Budgets =====

// CreateBudgetRequest adalah body POST /budgets.
type CreateBudgetRequest struct {
	CategoryID string `json:"category_id" validate:"required,uuid" format:"uuid" example:"01920000-0000-7000-8000-000000000101"`
	// PeriodMonth "YYYY-MM"; kosong = bulan berjalan (timezone user).
	PeriodMonth string `json:"period_month,omitempty" validate:"omitempty,datetime=2006-01" example:"2026-10"`
	Amount      string `json:"amount" validate:"required,max=32" example:"2000000"`
	// Currency kosong = base currency user.
	Currency          string `json:"currency,omitempty" validate:"omitempty,len=3" example:"IDR"`
	AlertThresholdPct int    `json:"alert_threshold_pct,omitempty" validate:"omitempty,min=1,max=100" example:"80"`
} //	@name	finance.CreateBudgetRequest

// UpdateBudgetRequest adalah body PATCH /budgets/{id}.
type UpdateBudgetRequest struct {
	Amount            *string `json:"amount,omitempty" validate:"omitempty,max=32" example:"2500000"`
	AlertThresholdPct *int    `json:"alert_threshold_pct,omitempty" validate:"omitempty,min=1,max=100" example:"90"`
} //	@name	finance.UpdateBudgetRequest

// BudgetResponse adalah budget beserta progress (spent dihitung saat dibaca).
type BudgetResponse struct {
	ID                string `json:"id" format:"uuid"`
	CategoryID        string `json:"category_id" format:"uuid"`
	PeriodMonth       string `json:"period_month" example:"2026-10"`
	Amount            string `json:"amount" example:"2000000"`
	Currency          string `json:"currency" example:"IDR"`
	AlertThresholdPct int    `json:"alert_threshold_pct" example:"80"`
	Spent             string `json:"spent" example:"1700000"`
	// Remaining negatif = overspent.
	Remaining string `json:"remaining" example:"300000"`
	// Progress persen pemakaian, 2 desimal.
	Progress  string    `json:"progress" example:"85.00"`
	Status    string    `json:"status" enums:"ok,warning,exceeded" example:"warning"`
	Overspent bool      `json:"overspent" example:"false"`
	Version   int       `json:"version" example:"1"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
} //	@name	finance.BudgetResponse

// BudgetEnvelope adalah response {"data": BudgetResponse}.
type BudgetEnvelope struct {
	Data BudgetResponse `json:"data"`
} //	@name	finance.BudgetEnvelope

// BudgetListEnvelope adalah response {"data": [BudgetResponse]}.
type BudgetListEnvelope struct {
	Data []BudgetResponse `json:"data"`
} //	@name	finance.BudgetListEnvelope

func toBudgetResponse(v app.BudgetView) BudgetResponse {
	b, p := v.Budget, v.Progress
	cur := b.Amount().Currency()
	status := "ok"
	if p.Level != domain.AlertNone {
		status = string(p.Level)
	}
	return BudgetResponse{
		ID: b.ID().String(), CategoryID: b.CategoryID().String(), PeriodMonth: b.Month().Format("2006-01"),
		Amount: b.Amount().String(), Currency: cur.Code(), AlertThresholdPct: b.Threshold(),
		Spent: moneyString(p.Spent, cur), Remaining: moneyString(p.Remaining, cur), Progress: p.Percent,
		Status: status, Overspent: p.Overspent, Version: b.Version(), CreatedAt: b.CreatedAt(), UpdatedAt: b.UpdatedAt(),
	}
}

// ===== Tags =====

// CreateTagRequest adalah body POST /tags.
type CreateTagRequest struct {
	Name  string `json:"name" validate:"required,max=40" example:"liburan"`
	Color string `json:"color,omitempty" validate:"omitempty,len=7" example:"#FF8800"`
} //	@name	finance.CreateTagRequest

// UpdateTagRequest adalah body PATCH /tags/{id}.
type UpdateTagRequest struct {
	Name  *string `json:"name,omitempty" validate:"omitempty,max=40" example:"liburan-2026"`
	Color *string `json:"color,omitempty" validate:"omitempty,max=7" example:"#00AA00"`
} //	@name	finance.UpdateTagRequest

// SetTransactionTagsRequest adalah body PUT /transactions/{id}/tags (mengganti semua tag).
type SetTransactionTagsRequest struct {
	TagIDs []string `json:"tag_ids" validate:"max=10,dive,uuid" format:"uuid"`
} //	@name	finance.SetTransactionTagsRequest

// TagResponse adalah satu tag.
type TagResponse struct {
	ID        string    `json:"id" format:"uuid"`
	Name      string    `json:"name" example:"liburan"`
	Color     string    `json:"color" example:"#FF8800"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
} //	@name	finance.TagResponse

// TagEnvelope adalah response {"data": TagResponse}.
type TagEnvelope struct {
	Data TagResponse `json:"data"`
} //	@name	finance.TagEnvelope

// TagListEnvelope adalah response {"data": [TagResponse]}.
type TagListEnvelope struct {
	Data []TagResponse `json:"data"`
} //	@name	finance.TagListEnvelope

func toTagResponse(t *domain.Tag) TagResponse {
	return TagResponse{ID: t.ID().String(), Name: t.Name(), Color: t.Color(), CreatedAt: t.CreatedAt(), UpdatedAt: t.UpdatedAt()}
}

func toTagResponses(tags []*domain.Tag) []TagResponse {
	out := make([]TagResponse, 0, len(tags))
	for _, t := range tags {
		out = append(out, toTagResponse(t))
	}
	return out
}

// ===== Recurring rules =====

// CreateRecurringRuleRequest adalah body POST /recurring-rules. Hari pada
// start_date menjadi anchor bulanan (31 -> clamp ke akhir bulan).
type CreateRecurringRuleRequest struct {
	AccountID  string `json:"account_id" validate:"required,uuid" format:"uuid"`
	CategoryID string `json:"category_id" validate:"required,uuid" format:"uuid"`
	Type       string `json:"type" validate:"required,oneof=income expense" enums:"income,expense" example:"expense"`
	Amount     string `json:"amount" validate:"required,max=32" example:"186000"`
	Note       string `json:"note,omitempty" validate:"max=255" example:"Netflix"`
	Frequency  string `json:"frequency" validate:"required,oneof=daily weekly monthly yearly" enums:"daily,weekly,monthly,yearly" example:"monthly"`
	Interval   int    `json:"interval,omitempty" validate:"omitempty,min=1,max=365" example:"1"`
	StartDate  string `json:"start_date" validate:"required,datetime=2006-01-02" example:"2026-10-03"`
	// EndDate dan Count saling eksklusif (opsional).
	EndDate string `json:"end_date,omitempty" validate:"omitempty,datetime=2006-01-02,excluded_with=Count" example:"2027-10-03"`
	Count   int    `json:"count,omitempty" validate:"omitempty,min=1,max=1000" example:"12"`
} //	@name	finance.CreateRecurringRuleRequest

// UpdateRecurringRuleRequest adalah body PATCH /recurring-rules/{id}.
// end_date "" = hapus end date.
type UpdateRecurringRuleRequest struct {
	AccountID  *string `json:"account_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	CategoryID *string `json:"category_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	Type       *string `json:"type,omitempty" validate:"omitempty,oneof=income expense" enums:"income,expense"`
	Amount     *string `json:"amount,omitempty" validate:"omitempty,max=32"`
	Note       *string `json:"note,omitempty" validate:"omitempty,max=255"`
	Frequency  *string `json:"frequency,omitempty" validate:"omitempty,oneof=daily weekly monthly yearly" enums:"daily,weekly,monthly,yearly"`
	Interval   *int    `json:"interval,omitempty" validate:"omitempty,min=1,max=365"`
	StartDate  *string `json:"start_date,omitempty" validate:"omitempty,datetime=2006-01-02"`
	EndDate    *string `json:"end_date,omitempty" validate:"omitempty,max=10"`
	Count      *int    `json:"count,omitempty" validate:"omitempty,min=1,max=1000"`
} //	@name	finance.UpdateRecurringRuleRequest

// PauseRecurringRuleRequest adalah body opsional POST /recurring-rules/{id}/pause.
type PauseRecurringRuleRequest struct {
	Reason string `json:"reason,omitempty" validate:"max=255" example:"Langganan dihentikan sementara"`
} //	@name	finance.PauseRecurringRuleRequest

// RecurringRuleResponse adalah satu recurring rule.
type RecurringRuleResponse struct {
	ID          string    `json:"id" format:"uuid"`
	AccountID   string    `json:"account_id" format:"uuid"`
	CategoryID  string    `json:"category_id" format:"uuid"`
	Type        string    `json:"type" enums:"income,expense"`
	Amount      string    `json:"amount" example:"186000"`
	Currency    string    `json:"currency" example:"IDR"`
	Note        string    `json:"note"`
	Frequency   string    `json:"frequency" enums:"daily,weekly,monthly,yearly"`
	Interval    int       `json:"interval" example:"1"`
	ByMonthDay  int       `json:"by_month_day" example:"3"`
	StartDate   string    `json:"start_date" example:"2026-10-03"`
	EndDate     *string   `json:"end_date" example:"2027-10-03"`
	NextRunDate string    `json:"next_run_date" example:"2026-11-03"`
	LastRunDate *string   `json:"last_run_date" example:"2026-10-03"`
	Status      string    `json:"status" enums:"active,paused,ended"`
	PauseReason string    `json:"pause_reason"`
	Upcoming    []string  `json:"upcoming" example:"2026-11-03,2026-12-03"`
	Version     int       `json:"version" example:"1"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
} //	@name	finance.RecurringRuleResponse

// RecurringRuleEnvelope adalah response {"data": RecurringRuleResponse}.
type RecurringRuleEnvelope struct {
	Data RecurringRuleResponse `json:"data"`
} //	@name	finance.RecurringRuleEnvelope

// RecurringRuleListEnvelope adalah response {"data": [...], "meta": PageMeta}.
type RecurringRuleListEnvelope struct {
	Data []RecurringRuleResponse `json:"data"`
	Meta pagination.PageMeta     `json:"meta"`
} //	@name	finance.RecurringRuleListEnvelope

func dateStrPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(dateLayout)
	return &s
}

func toRecurringRuleResponse(r *domain.RecurringRule) RecurringRuleResponse {
	up := r.Upcoming(3)
	upcoming := make([]string, 0, len(up))
	for _, d := range up {
		upcoming = append(upcoming, d.Format(dateLayout))
	}
	return RecurringRuleResponse{
		ID: r.ID().String(), AccountID: r.AccountID().String(), CategoryID: r.CategoryID().String(),
		Type: string(r.Type()), Amount: r.Amount().String(), Currency: r.Amount().Currency().Code(), Note: r.Note(),
		Frequency: string(r.Frequency()), Interval: r.Interval(), ByMonthDay: r.MonthDay(),
		StartDate: r.StartDate().Format(dateLayout), EndDate: dateStrPtr(r.EndDate()),
		NextRunDate: r.NextRunDate().Format(dateLayout), LastRunDate: dateStrPtr(r.LastRunDate()),
		Status: string(r.Status()), PauseReason: r.PauseReason(), Upcoming: upcoming,
		Version: r.Version(), CreatedAt: r.CreatedAt(), UpdatedAt: r.UpdatedAt(),
	}
}

// ===== Import =====

// ImportRowErrorResponse adalah error validasi satu baris CSV (row 1-based, header = 1).
type ImportRowErrorResponse struct {
	Row     int    `json:"row" example:"3"`
	Field   string `json:"field" example:"amount"`
	Message string `json:"message" example:"invalid amount"`
} //	@name	finance.ImportRowError

// ImportResponse adalah hasil import (dry-run: yang akan di-import).
type ImportResponse struct {
	DryRun   bool `json:"dry_run" example:"true"`
	Imported int  `json:"imported" example:"120"`
	// Skipped = kemungkinan duplikat (hash date|amount|note|account sudah ada).
	Skipped    int                      `json:"skipped" example:"2"`
	Duplicates []int                    `json:"duplicate_rows" example:"4,9"`
	Errors     []ImportRowErrorResponse `json:"errors"`
} //	@name	finance.ImportResponse

// ImportEnvelope adalah response {"data": ImportResponse}.
type ImportEnvelope struct {
	Data ImportResponse `json:"data"`
} //	@name	finance.ImportEnvelope

func toImportResponse(r *app.ImportResult) ImportResponse {
	errs := make([]ImportRowErrorResponse, 0, len(r.Errors))
	for _, e := range r.Errors {
		errs = append(errs, ImportRowErrorResponse{Row: e.Row, Field: e.Field, Message: e.Message})
	}
	return ImportResponse{DryRun: r.DryRun, Imported: r.Imported, Skipped: r.Skipped, Duplicates: r.Duplicates, Errors: errs}
}

func moneyString(minor int64, cur money.Currency) string { return money.New(minor, cur).String() }
