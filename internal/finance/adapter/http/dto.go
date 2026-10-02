package http

import (
	"time"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
	"go-auth-clean/internal/shared/pagination"
)

// Request/response DTO = kontrak API publik, terpisah dari entity domain.
// Nominal uang selalu string major unit ("35000", "12.50") agar aman di JS.

const dateLayout = "2006-01-02"

// ===== Settings =====

// UpdateSettingsRequest adalah body PUT /settings.
type UpdateSettingsRequest struct {
	BaseCurrency string `json:"base_currency" validate:"required,len=3" example:"IDR"`
	Timezone     string `json:"timezone" validate:"required,max=64" example:"Asia/Jakarta"`
	// WeekStart: 0 = Sunday ... 6 = Saturday.
	WeekStart *int `json:"week_start" validate:"required,min=0,max=6" example:"1"`
} //	@name	finance.UpdateSettingsRequest

// SettingsResponse adalah preferensi finance user.
type SettingsResponse struct {
	BaseCurrency string    `json:"base_currency" example:"IDR"`
	Timezone     string    `json:"timezone" example:"Asia/Jakarta"`
	WeekStart    int       `json:"week_start" example:"1"`
	UpdatedAt    time.Time `json:"updated_at" example:"2026-09-30T10:00:00Z"`
} //	@name	finance.SettingsResponse

// CurrencyResponse adalah currency yang didukung.
type CurrencyResponse struct {
	Code      string `json:"code" example:"IDR"`
	Name      string `json:"name" example:"Indonesian Rupiah"`
	MinorUnit int    `json:"minor_unit" example:"0"`
	Symbol    string `json:"symbol" example:"Rp"`
} //	@name	finance.CurrencyResponse

// ===== Accounts =====

// CreateAccountRequest adalah body POST /accounts.
type CreateAccountRequest struct {
	Name string `json:"name" validate:"required,max=50" example:"BCA"`
	Type string `json:"type" validate:"required,oneof=cash bank ewallet credit_card" enums:"cash,bank,ewallet,credit_card" example:"bank"`
	// Currency kosong = base currency user.
	Currency       string `json:"currency,omitempty" validate:"omitempty,len=3" example:"IDR"`
	InitialBalance string `json:"initial_balance,omitempty" validate:"max=32" example:"1500000"`
	// AllowNegative kosong = default per tipe (true untuk bank & credit_card).
	AllowNegative *bool `json:"allow_negative,omitempty" example:"false"`
} //	@name	finance.CreateAccountRequest

// UpdateAccountRequest adalah body PATCH /accounts/{id}; field kosong = tidak diubah.
type UpdateAccountRequest struct {
	Name           *string `json:"name,omitempty" validate:"omitempty,max=50" example:"BCA Utama"`
	InitialBalance *string `json:"initial_balance,omitempty" validate:"omitempty,max=32" example:"2000000"`
	AllowNegative  *bool   `json:"allow_negative,omitempty" example:"true"`
} //	@name	finance.UpdateAccountRequest

// AccountResponse adalah akun beserta saldo cache.
type AccountResponse struct {
	ID             string     `json:"id" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6a"`
	Name           string     `json:"name" example:"BCA"`
	Type           string     `json:"type" enums:"cash,bank,ewallet,credit_card" example:"bank"`
	Currency       string     `json:"currency" example:"IDR"`
	InitialBalance string     `json:"initial_balance" example:"1500000"`
	Balance        string     `json:"balance" example:"1465000"`
	AllowNegative  bool       `json:"allow_negative" example:"true"`
	Archived       bool       `json:"archived" example:"false"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty" example:"2026-09-30T10:00:00Z"`
	Version        int        `json:"version" example:"3"`
	CreatedAt      time.Time  `json:"created_at" example:"2026-09-30T10:00:00Z"`
	UpdatedAt      time.Time  `json:"updated_at" example:"2026-09-30T10:00:00Z"`
} //	@name	finance.AccountResponse

// ===== Categories =====

// CreateCategoryRequest adalah body POST /categories.
type CreateCategoryRequest struct {
	Name     string `json:"name" validate:"required,max=50" example:"Kopi"`
	Type     string `json:"type" validate:"required,oneof=income expense" enums:"income,expense" example:"expense"`
	ParentID string `json:"parent_id,omitempty" validate:"omitempty,uuid" format:"uuid" example:"01920000-0000-7000-8000-000000000101"`
	Icon     string `json:"icon,omitempty" validate:"max=30" example:"coffee"`
	Color    string `json:"color,omitempty" validate:"max=7" example:"#A0522D"`
} //	@name	finance.CreateCategoryRequest

// UpdateCategoryRequest adalah body PATCH /categories/{id}.
type UpdateCategoryRequest struct {
	Name  *string `json:"name,omitempty" validate:"omitempty,max=50" example:"Kopi Susu"`
	Icon  *string `json:"icon,omitempty" validate:"omitempty,max=30" example:"coffee"`
	Color *string `json:"color,omitempty" validate:"omitempty,max=7" example:"#6F4E37"`
} //	@name	finance.UpdateCategoryRequest

// CategoryResponse adalah satu kategori (system atau milik user).
type CategoryResponse struct {
	ID       string  `json:"id" format:"uuid" example:"01920000-0000-7000-8000-000000000101"`
	ParentID *string `json:"parent_id,omitempty" format:"uuid"`
	Type     string  `json:"type" enums:"income,expense" example:"expense"`
	Name     string  `json:"name" example:"Makan & Minum"`
	Icon     string  `json:"icon" example:"utensils"`
	Color    string  `json:"color" example:"#FF8800"`
	IsSystem bool    `json:"is_system" example:"true"`
} //	@name	finance.CategoryResponse

// CategoryNodeResponse adalah kategori root beserta sub kategorinya.
type CategoryNodeResponse struct {
	CategoryResponse
	Children []CategoryResponse `json:"children"`
} //	@name	finance.CategoryNodeResponse

// ===== Transactions =====

// CreateTransactionRequest adalah body POST /transactions.
type CreateTransactionRequest struct {
	AccountID       string `json:"account_id" validate:"required,uuid" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6a"`
	CategoryID      string `json:"category_id" validate:"required,uuid" format:"uuid" example:"01920000-0000-7000-8000-000000000101"`
	Type            string `json:"type" validate:"required,oneof=income expense" enums:"income,expense" example:"expense"`
	Amount          string `json:"amount" validate:"required,max=32" example:"35000"`
	TransactionDate string `json:"transaction_date" validate:"required,datetime=2006-01-02" example:"2026-09-30"`
	Note            string `json:"note,omitempty" validate:"max=1024" example:"Makan siang"`
	// TagIDs opsional, maksimal 10 tag milik user.
	TagIDs []string `json:"tag_ids,omitempty" validate:"omitempty,max=10,dive,uuid" format:"uuid"`
} //	@name	finance.CreateTransactionRequest

// UpdateTransactionRequest adalah body PATCH /transactions/{id}.
type UpdateTransactionRequest struct {
	AccountID       *string `json:"account_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	CategoryID      *string `json:"category_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	Type            *string `json:"type,omitempty" validate:"omitempty,oneof=income expense" enums:"income,expense"`
	Amount          *string `json:"amount,omitempty" validate:"omitempty,max=32" example:"40000"`
	TransactionDate *string `json:"transaction_date,omitempty" validate:"omitempty,datetime=2006-01-02" example:"2026-09-29"`
	Note            *string `json:"note,omitempty" validate:"omitempty,max=1024" example:"Makan malam"`
} //	@name	finance.UpdateTransactionRequest

// TransactionResponse adalah satu transaksi income/expense.
type TransactionResponse struct {
	ID              string `json:"id" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6b"`
	AccountID       string `json:"account_id" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6a"`
	CategoryID      string `json:"category_id" format:"uuid" example:"01920000-0000-7000-8000-000000000101"`
	Type            string `json:"type" enums:"income,expense" example:"expense"`
	Amount          string `json:"amount" example:"35000"`
	Currency        string `json:"currency" example:"IDR"`
	TransactionDate string `json:"transaction_date" example:"2026-09-30"`
	Note            string `json:"note" example:"Makan siang"`
	// Source transfer_fee = dikelola transfer (read-only).
	Source string `json:"source" enums:"manual,transfer_fee,recurring,import" example:"manual"`
	// Tags diisi pada list & get (dimuat batch, tanpa N+1).
	Tags      []TagResponse `json:"tags,omitempty"`
	Version   int           `json:"version" example:"1"`
	CreatedAt time.Time     `json:"created_at" example:"2026-09-30T10:00:00Z"`
	UpdatedAt time.Time     `json:"updated_at" example:"2026-09-30T10:00:00Z"`
} //	@name	finance.TransactionResponse

// ===== Transfers =====

// CreateTransferRequest adalah body POST /transfers.
type CreateTransferRequest struct {
	FromAccountID string `json:"from_account_id" validate:"required,uuid" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6a"`
	ToAccountID   string `json:"to_account_id" validate:"required,uuid" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6c"`
	Amount        string `json:"amount" validate:"required,max=32" example:"500000"`
	Fee           string `json:"fee,omitempty" validate:"max=32" example:"6500"`
	// ToAmount wajib bila currency akun tujuan berbeda (nominal yang diterima).
	ToAmount     string `json:"to_amount,omitempty" validate:"max=32" example:"31.25"`
	TransferDate string `json:"transfer_date" validate:"required,datetime=2006-01-02" example:"2026-09-30"`
	Note         string `json:"note,omitempty" validate:"max=1024" example:"Top up GoPay"`
} //	@name	finance.CreateTransferRequest

// UpdateTransferRequest adalah body PATCH /transfers/{id}; fee "0" = hapus biaya.
type UpdateTransferRequest struct {
	FromAccountID *string `json:"from_account_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	ToAccountID   *string `json:"to_account_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	Amount        *string `json:"amount,omitempty" validate:"omitempty,max=32" example:"450000"`
	Fee           *string `json:"fee,omitempty" validate:"omitempty,max=32" example:"0"`
	ToAmount      *string `json:"to_amount,omitempty" validate:"omitempty,max=32" example:"31.25"`
	TransferDate  *string `json:"transfer_date,omitempty" validate:"omitempty,datetime=2006-01-02" example:"2026-09-30"`
	Note          *string `json:"note,omitempty" validate:"omitempty,max=1024"`
} //	@name	finance.UpdateTransferRequest

// TransferResponse adalah perpindahan uang antar akun milik user.
type TransferResponse struct {
	ID            string `json:"id" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6d"`
	FromAccountID string `json:"from_account_id" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6a"`
	ToAccountID   string `json:"to_account_id" format:"uuid" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6c"`
	Amount        string `json:"amount" example:"500000"`
	Fee           string `json:"fee" example:"6500"`
	Currency      string `json:"currency" example:"IDR"`
	// ToAmount/ToCurrency adalah nominal yang diterima akun tujuan (= amount bila satu currency).
	ToAmount   string `json:"to_amount" example:"500000"`
	ToCurrency string `json:"to_currency" example:"IDR"`
	// FeeTransactionID adalah expense "Biaya Admin" yang dibuat otomatis.
	FeeTransactionID *string   `json:"fee_transaction_id,omitempty" format:"uuid"`
	TransferDate     string    `json:"transfer_date" example:"2026-09-30"`
	Note             string    `json:"note" example:"Top up GoPay"`
	Version          int       `json:"version" example:"1"`
	CreatedAt        time.Time `json:"created_at" example:"2026-09-30T10:00:00Z"`
	UpdatedAt        time.Time `json:"updated_at" example:"2026-09-30T10:00:00Z"`
} //	@name	finance.TransferResponse

// ===== Reports =====

// MoneyResponse adalah nominal + currency.
type MoneyResponse struct {
	Amount   string `json:"amount" example:"1465000"`
	Currency string `json:"currency" example:"IDR"`
} //	@name	finance.MoneyResponse

// CurrencyTotalsResponse adalah income/expense/net untuk satu currency.
type CurrencyTotalsResponse struct {
	Currency string `json:"currency" example:"IDR"`
	Income   string `json:"income" example:"10000000"`
	Expense  string `json:"expense" example:"3500000"`
	Net      string `json:"net" example:"6500000"`
} //	@name	finance.CurrencyTotalsResponse

// CategoryShareResponse adalah total satu kategori root dan persentasenya.
type CategoryShareResponse struct {
	CategoryID string `json:"category_id" format:"uuid" example:"01920000-0000-7000-8000-000000000101"`
	Name       string `json:"name" example:"Makan & Minum"`
	Total      string `json:"total" example:"1250000"`
	Percent    string `json:"percent" example:"35.71"`
} //	@name	finance.CategoryShareResponse

// CashflowPointResponse adalah satu titik seri (period = awal hari/bulan).
type CashflowPointResponse struct {
	Period  string `json:"period" example:"2026-09-01"`
	Income  string `json:"income" example:"10000000"`
	Expense string `json:"expense" example:"3500000"`
	Net     string `json:"net" example:"6500000"`
} //	@name	finance.CashflowPointResponse

// SummaryResponse adalah dashboard bulanan. Transfer tidak dihitung sebagai income/expense.
type SummaryResponse struct {
	Month        string                   `json:"month" example:"2026-09"`
	Currency     string                   `json:"currency" example:"IDR"`
	TotalBalance []MoneyResponse          `json:"total_balance"`
	Totals       []CurrencyTotalsResponse `json:"totals"`
	ByCategory   []CategoryShareResponse  `json:"by_category"`
	Cashflow     []CashflowPointResponse  `json:"cashflow"`
} //	@name	finance.SummaryResponse

// CashflowResponse adalah seri cashflow.
type CashflowResponse struct {
	Currency    string                  `json:"currency" example:"IDR"`
	Granularity string                  `json:"granularity" enums:"day,month" example:"month"`
	Points      []CashflowPointResponse `json:"points"`
} //	@name	finance.CashflowResponse

// CategoryReportResponse adalah breakdown per kategori.
type CategoryReportResponse struct {
	Currency string                  `json:"currency" example:"IDR"`
	Type     string                  `json:"type" enums:"income,expense" example:"expense"`
	Items    []CategoryShareResponse `json:"items"`
} //	@name	finance.CategoryReportResponse

// BalanceDriftResponse adalah akun yang saldo cache-nya tidak cocok.
type BalanceDriftResponse struct {
	AccountID       string `json:"account_id" format:"uuid"`
	Currency        string `json:"currency" example:"IDR"`
	CachedBalance   string `json:"cached_balance" example:"100000"`
	ExpectedBalance string `json:"expected_balance" example:"95000"`
} //	@name	finance.BalanceDriftResponse

// ReconciliationResponse: ok=true bila semua saldo cocok dengan riwayat.
type ReconciliationResponse struct {
	OK     bool                   `json:"ok" example:"true"`
	Drifts []BalanceDriftResponse `json:"drifts"`
} //	@name	finance.ReconciliationResponse

// ===== Envelopes (untuk swagger) =====

// SettingsEnvelope adalah response {"data": SettingsResponse}.
type SettingsEnvelope struct {
	Data SettingsResponse `json:"data"`
} //	@name	finance.SettingsEnvelope

// CurrencyListEnvelope adalah response {"data": [CurrencyResponse]}.
type CurrencyListEnvelope struct {
	Data []CurrencyResponse `json:"data"`
} //	@name	finance.CurrencyListEnvelope

// AccountEnvelope adalah response {"data": AccountResponse}.
type AccountEnvelope struct {
	Data AccountResponse `json:"data"`
} //	@name	finance.AccountEnvelope

// AccountListEnvelope adalah response {"data": [AccountResponse]}.
type AccountListEnvelope struct {
	Data []AccountResponse `json:"data"`
} //	@name	finance.AccountListEnvelope

// CategoryEnvelope adalah response {"data": CategoryResponse}.
type CategoryEnvelope struct {
	Data CategoryResponse `json:"data"`
} //	@name	finance.CategoryEnvelope

// CategoryTreeEnvelope adalah response {"data": [CategoryNodeResponse]}.
type CategoryTreeEnvelope struct {
	Data []CategoryNodeResponse `json:"data"`
} //	@name	finance.CategoryTreeEnvelope

// TransactionEnvelope adalah response {"data": TransactionResponse}.
type TransactionEnvelope struct {
	Data TransactionResponse `json:"data"`
} //	@name	finance.TransactionEnvelope

// TransactionListEnvelope adalah response {"data": [...], "meta": PageMeta}.
type TransactionListEnvelope struct {
	Data []TransactionResponse `json:"data"`
	Meta pagination.PageMeta   `json:"meta"`
} //	@name	finance.TransactionListEnvelope

// TransferEnvelope adalah response {"data": TransferResponse}.
type TransferEnvelope struct {
	Data TransferResponse `json:"data"`
} //	@name	finance.TransferEnvelope

// TransferListEnvelope adalah response {"data": [...], "meta": PageMeta}.
type TransferListEnvelope struct {
	Data []TransferResponse  `json:"data"`
	Meta pagination.PageMeta `json:"meta"`
} //	@name	finance.TransferListEnvelope

// SummaryEnvelope adalah response {"data": SummaryResponse}.
type SummaryEnvelope struct {
	Data SummaryResponse `json:"data"`
} //	@name	finance.SummaryEnvelope

// CashflowEnvelope adalah response {"data": CashflowResponse}.
type CashflowEnvelope struct {
	Data CashflowResponse `json:"data"`
} //	@name	finance.CashflowEnvelope

// CategoryReportEnvelope adalah response {"data": CategoryReportResponse}.
type CategoryReportEnvelope struct {
	Data CategoryReportResponse `json:"data"`
} //	@name	finance.CategoryReportEnvelope

// ReconciliationEnvelope adalah response {"data": ReconciliationResponse}.
type ReconciliationEnvelope struct {
	Data ReconciliationResponse `json:"data"`
} //	@name	finance.ReconciliationEnvelope

// ===== Mapper =====

func toSettingsResponse(s *domain.UserSettings) SettingsResponse {
	return SettingsResponse{BaseCurrency: s.BaseCurrency().Code(), Timezone: s.Timezone(),
		WeekStart: s.WeekStart(), UpdatedAt: s.UpdatedAt()}
}

func toAccountResponse(a *domain.Account) AccountResponse {
	return AccountResponse{
		ID: a.ID().String(), Name: a.Name(), Type: string(a.Type()), Currency: a.Currency().Code(),
		InitialBalance: a.InitialBalance().String(), Balance: a.Balance().String(),
		AllowNegative: a.AllowNegative(), Archived: a.IsArchived(), ArchivedAt: a.ArchivedAt(),
		Version: a.Version(), CreatedAt: a.CreatedAt(), UpdatedAt: a.UpdatedAt(),
	}
}

func toCategoryResponse(c *domain.Category) CategoryResponse {
	out := CategoryResponse{ID: c.ID().String(), Type: string(c.Type()), Name: c.Name(),
		Icon: c.Icon(), Color: c.Color(), IsSystem: c.IsSystem()}
	if p := c.ParentID(); p != nil {
		s := p.String()
		out.ParentID = &s
	}
	return out
}

func toCategoryTree(nodes []app.CategoryNode) []CategoryNodeResponse {
	out := make([]CategoryNodeResponse, 0, len(nodes))
	for _, n := range nodes {
		children := make([]CategoryResponse, 0, len(n.Children))
		for _, c := range n.Children {
			children = append(children, toCategoryResponse(c))
		}
		out = append(out, CategoryNodeResponse{CategoryResponse: toCategoryResponse(n.Category), Children: children})
	}
	return out
}

func toTransactionResponse(t *domain.Transaction) TransactionResponse {
	return TransactionResponse{
		ID: t.ID().String(), AccountID: t.AccountID().String(), CategoryID: t.CategoryID().String(),
		Type: string(t.Type()), Amount: t.Amount().String(), Currency: t.Amount().Currency().Code(),
		TransactionDate: t.Date().Format(dateLayout), Note: t.Note(), Source: string(t.Source()),
		Version: t.Version(), CreatedAt: t.CreatedAt(), UpdatedAt: t.UpdatedAt(),
	}
}

func toTransferResponse(t *domain.Transfer) TransferResponse {
	out := TransferResponse{
		ID: t.ID().String(), FromAccountID: t.FromAccountID().String(), ToAccountID: t.ToAccountID().String(),
		Amount: t.Amount().String(), Fee: t.Fee().String(), Currency: t.Amount().Currency().Code(),
		ToAmount: t.ToAmount().String(), ToCurrency: t.ToAmount().Currency().Code(),
		TransferDate: t.Date().Format(dateLayout), Note: t.Note(), Version: t.Version(),
		CreatedAt: t.CreatedAt(), UpdatedAt: t.UpdatedAt(),
	}
	if id := t.FeeTransactionID(); id != nil {
		s := id.String()
		out.FeeTransactionID = &s
	}
	return out
}

func toShares(in []app.CategoryShare) []CategoryShareResponse {
	out := make([]CategoryShareResponse, 0, len(in))
	for _, s := range in {
		out = append(out, CategoryShareResponse{CategoryID: s.CategoryID.String(), Name: s.Name,
			Total: s.Total.String(), Percent: s.Percent})
	}
	return out
}

func toCashflow(in []app.CashflowItem) []CashflowPointResponse {
	out := make([]CashflowPointResponse, 0, len(in))
	for _, p := range in {
		out = append(out, CashflowPointResponse{Period: p.Period.Format(dateLayout),
			Income: p.Income.String(), Expense: p.Expense.String(), Net: p.Net.String()})
	}
	return out
}

func toSummaryResponse(s *app.Summary) SummaryResponse {
	balances := make([]MoneyResponse, 0, len(s.TotalBalance))
	for _, m := range s.TotalBalance {
		balances = append(balances, MoneyResponse{Amount: m.String(), Currency: m.Currency().Code()})
	}
	totals := make([]CurrencyTotalsResponse, 0, len(s.Totals))
	for _, t := range s.Totals {
		totals = append(totals, CurrencyTotalsResponse{Currency: t.Currency.Code(), Income: t.Income.String(),
			Expense: t.Expense.String(), Net: t.Net.String()})
	}
	return SummaryResponse{Month: s.Month.Format("2006-01"), Currency: s.Currency.Code(), TotalBalance: balances,
		Totals: totals, ByCategory: toShares(s.ByCategory), Cashflow: toCashflow(s.Cashflow)}
}

func toReconciliation(drifts []domain.BalanceDrift) ReconciliationResponse {
	out := ReconciliationResponse{OK: len(drifts) == 0, Drifts: make([]BalanceDriftResponse, 0, len(drifts))}
	for _, d := range drifts {
		out.Drifts = append(out.Drifts, BalanceDriftResponse{AccountID: d.AccountID.String(), Currency: d.Currency.Code(),
			CachedBalance: money.New(d.Cached, d.Currency).String(), ExpectedBalance: money.New(d.Expected, d.Currency).String()})
	}
	return out
}
