package http

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
	"go-auth-clean/internal/shared/pagination"
)

// ===== Exchange rates =====

// CreateRateRequest adalah body POST /exchange-rates (1 base = rate quote).
type CreateRateRequest struct {
	Base  string `json:"base" validate:"required,len=3" example:"USD"`
	Quote string `json:"quote" validate:"required,len=3" example:"IDR"`
	Rate  string `json:"rate" validate:"required,max=32" example:"16250.5"`
	// AsOf "YYYY-MM-DD", kurs berlaku mulai tanggal ini.
	AsOf string `json:"as_of" validate:"required" example:"2026-10-01"`
} //	@name	finance.CreateRateRequest

// UpdateRateRequest adalah body PATCH /exchange-rates/{id}.
type UpdateRateRequest struct {
	Rate string `json:"rate" validate:"required,max=32" example:"16300"`
} //	@name	finance.UpdateRateRequest

// RateResponse adalah satu kurs manual milik user.
type RateResponse struct {
	ID        string    `json:"id" format:"uuid"`
	Base      string    `json:"base" example:"USD"`
	Quote     string    `json:"quote" example:"IDR"`
	Rate      string    `json:"rate" example:"16250.5"`
	AsOf      string    `json:"as_of" example:"2026-10-01"`
	CreatedAt time.Time `json:"created_at"`
} //	@name	finance.RateResponse

// RateEnvelope adalah response {"data": RateResponse}.
type RateEnvelope struct {
	Data RateResponse `json:"data"`
} //	@name	finance.RateEnvelope

// RateListEnvelope adalah response {"data": [RateResponse]}.
type RateListEnvelope struct {
	Data []RateResponse `json:"data"`
} //	@name	finance.RateListEnvelope

// ConvertResponse adalah hasil konversi nominal.
type ConvertResponse struct {
	From MoneyResponse `json:"from"`
	To   MoneyResponse `json:"to"`
} //	@name	finance.ConvertResponse

// ConvertEnvelope adalah response {"data": ConvertResponse}.
type ConvertEnvelope struct {
	Data ConvertResponse `json:"data"`
} //	@name	finance.ConvertEnvelope

func toRateResponse(r *domain.ExchangeRate) RateResponse {
	return RateResponse{ID: r.ID().String(), Base: r.Base().Code(), Quote: r.Quote().Code(), Rate: r.RateString(),
		AsOf: r.AsOf().Format(dateLayout), CreatedAt: r.CreatedAt()}
}

func toMoneyResponse(m money.Money) MoneyResponse {
	return MoneyResponse{Amount: m.String(), Currency: m.Currency().Code()}
}

// ===== Savings goals =====

// CreateGoalRequest adalah body POST /savings-goals.
type CreateGoalRequest struct {
	Name   string `json:"name" validate:"required,max=100" example:"Dana darurat"`
	Target string `json:"target" validate:"required,max=32" example:"30000000"`
	// Currency kosong = currency akun tertaut, atau base currency.
	Currency   string  `json:"currency,omitempty" validate:"omitempty,len=3" example:"IDR"`
	TargetDate *string `json:"target_date,omitempty" example:"2027-06-30"`
	AccountID  *string `json:"account_id,omitempty" format:"uuid"`
} //	@name	finance.CreateGoalRequest

// UpdateGoalRequest adalah body PATCH /savings-goals/{id}. String kosong pada
// target_date/account_id menghapus nilainya.
type UpdateGoalRequest struct {
	Name       *string `json:"name,omitempty" validate:"omitempty,max=100"`
	Target     *string `json:"target,omitempty" validate:"omitempty,max=32"`
	TargetDate *string `json:"target_date,omitempty" example:"2027-06-30"`
	AccountID  *string `json:"account_id,omitempty" format:"uuid"`
	Archived   *bool   `json:"archived,omitempty"`
} //	@name	finance.UpdateGoalRequest

// ContributeRequest adalah body POST /savings-goals/{id}/contributions dan
// /withdrawals. transfer_id menautkan transfer (amount diambil dari transfer);
// tanpa transfer, amount + date wajib (setoran standalone).
type ContributeRequest struct {
	Amount     string `json:"amount,omitempty" validate:"max=32" example:"500000"`
	Date       string `json:"date,omitempty" example:"2026-10-01"`
	TransferID string `json:"transfer_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	Note       string `json:"note,omitempty" validate:"max=500"`
} //	@name	finance.ContributeRequest

// GoalResponse adalah goal beserta progress.
type GoalResponse struct {
	ID            string    `json:"id" format:"uuid"`
	Name          string    `json:"name" example:"Dana darurat"`
	Target        string    `json:"target" example:"30000000"`
	Currency      string    `json:"currency" example:"IDR"`
	TargetDate    *string   `json:"target_date" example:"2027-06-30"`
	AccountID     *string   `json:"account_id" format:"uuid"`
	Status        string    `json:"status" enums:"active,achieved,archived"`
	Saved         string    `json:"saved" example:"12000000"`
	Remaining     string    `json:"remaining" example:"18000000"`
	Progress      string    `json:"progress" example:"40.00"`
	MonthlyNeeded string    `json:"monthly_needed" example:"2000000"`
	Version       int       `json:"version" example:"1"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
} //	@name	finance.GoalResponse

// GoalEnvelope adalah response {"data": GoalResponse}.
type GoalEnvelope struct {
	Data GoalResponse `json:"data"`
} //	@name	finance.GoalEnvelope

// GoalListEnvelope adalah response {"data": [GoalResponse]}.
type GoalListEnvelope struct {
	Data []GoalResponse `json:"data"`
} //	@name	finance.GoalListEnvelope

// ContributionResponse adalah satu setoran (amount negatif = penarikan).
type ContributionResponse struct {
	ID         string    `json:"id" format:"uuid"`
	GoalID     string    `json:"goal_id" format:"uuid"`
	Amount     string    `json:"amount" example:"500000"`
	Currency   string    `json:"currency" example:"IDR"`
	Date       string    `json:"date" example:"2026-10-01"`
	TransferID *string   `json:"transfer_id" format:"uuid"`
	Note       string    `json:"note"`
	CreatedAt  time.Time `json:"created_at"`
} //	@name	finance.ContributionResponse

// ContributionResult adalah setoran baru beserta goal terkini.
type ContributionResult struct {
	Contribution ContributionResponse `json:"contribution"`
	Goal         GoalResponse         `json:"goal"`
} //	@name	finance.ContributionResult

// ContributionEnvelope adalah response {"data": ContributionResult}.
type ContributionEnvelope struct {
	Data ContributionResult `json:"data"`
} //	@name	finance.ContributionEnvelope

// ContributionListEnvelope adalah response {"data": [ContributionResponse]}.
type ContributionListEnvelope struct {
	Data []ContributionResponse `json:"data"`
} //	@name	finance.ContributionListEnvelope

func toGoalResponse(g app.Goal) GoalResponse {
	cur := g.Goal.Target().Currency()
	return GoalResponse{ID: g.Goal.ID().String(), Name: g.Goal.Name(), Target: g.Goal.Target().String(), Currency: cur.Code(),
		TargetDate: datePtrString(g.Goal.TargetDate()), AccountID: uuidPtrString(g.Goal.AccountID()), Status: string(g.Goal.Status()),
		Saved: moneyString(g.Progress.Saved, cur), Remaining: moneyString(g.Progress.Remaining, cur), Progress: g.Progress.Percent,
		MonthlyNeeded: moneyString(g.Progress.MonthlyNeeded, cur), Version: g.Goal.Version(),
		CreatedAt: g.Goal.CreatedAt(), UpdatedAt: g.Goal.UpdatedAt()}
}

func toContributionResponse(c *domain.GoalContribution) ContributionResponse {
	return ContributionResponse{ID: c.ID.String(), GoalID: c.GoalID.String(), Amount: c.Amount.String(),
		Currency: c.Amount.Currency().Code(), Date: c.Date.Format(dateLayout), TransferID: uuidPtrString(c.TransferID),
		Note: c.Note, CreatedAt: c.CreatedAt}
}

// ===== Debts =====

// CreateDebtRequest adalah body POST /debts. payable = saya berutang,
// receivable = orang lain berutang ke saya.
type CreateDebtRequest struct {
	Direction    string  `json:"direction" validate:"required,oneof=payable receivable" enums:"payable,receivable"`
	Counterparty string  `json:"counterparty" validate:"required,max=100" example:"Budi"`
	Principal    string  `json:"principal" validate:"required,max=32" example:"1500000"`
	Currency     string  `json:"currency,omitempty" validate:"omitempty,len=3" example:"IDR"`
	StartDate    string  `json:"start_date" validate:"required" example:"2026-10-01"`
	DueDate      *string `json:"due_date,omitempty" example:"2026-12-31"`
	Note         string  `json:"note,omitempty" validate:"max=500"`
} //	@name	finance.CreateDebtRequest

// UpdateDebtRequest adalah body PATCH /debts/{id}; due_date "" menghapus jatuh tempo.
type UpdateDebtRequest struct {
	Counterparty *string `json:"counterparty,omitempty" validate:"omitempty,max=100"`
	Principal    *string `json:"principal,omitempty" validate:"omitempty,max=32"`
	DueDate      *string `json:"due_date,omitempty" example:"2026-12-31"`
	Note         *string `json:"note,omitempty" validate:"omitempty,max=500"`
} //	@name	finance.UpdateDebtRequest

// PayDebtRequest adalah body POST /debts/{id}/payments. transaction_id menautkan
// transaksi yang sudah ada; account_id (+category_id) membuat transaksi baru.
type PayDebtRequest struct {
	Amount        string `json:"amount,omitempty" validate:"max=32" example:"500000"`
	Date          string `json:"date,omitempty" example:"2026-10-15"`
	TransactionID string `json:"transaction_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	AccountID     string `json:"account_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	CategoryID    string `json:"category_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	Note          string `json:"note,omitempty" validate:"max=500"`
} //	@name	finance.PayDebtRequest

// DebtResponse adalah utang/piutang beserta total pembayaran.
type DebtResponse struct {
	ID           string    `json:"id" format:"uuid"`
	Direction    string    `json:"direction" enums:"payable,receivable"`
	Counterparty string    `json:"counterparty" example:"Budi"`
	Principal    string    `json:"principal" example:"1500000"`
	Currency     string    `json:"currency" example:"IDR"`
	Paid         string    `json:"paid" example:"500000"`
	Remaining    string    `json:"remaining" example:"1000000"`
	StartDate    string    `json:"start_date" example:"2026-10-01"`
	DueDate      *string   `json:"due_date" example:"2026-12-31"`
	Note         string    `json:"note"`
	Status       string    `json:"status" enums:"open,settled"`
	Version      int       `json:"version" example:"1"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
} //	@name	finance.DebtResponse

// DebtEnvelope adalah response {"data": DebtResponse}.
type DebtEnvelope struct {
	Data DebtResponse `json:"data"`
} //	@name	finance.DebtEnvelope

// DebtListEnvelope adalah response {"data": [DebtResponse]}.
type DebtListEnvelope struct {
	Data []DebtResponse `json:"data"`
} //	@name	finance.DebtListEnvelope

// DebtPaymentResponse adalah satu pembayaran utang/piutang.
type DebtPaymentResponse struct {
	ID            string    `json:"id" format:"uuid"`
	DebtID        string    `json:"debt_id" format:"uuid"`
	Amount        string    `json:"amount" example:"500000"`
	Currency      string    `json:"currency" example:"IDR"`
	Date          string    `json:"date" example:"2026-10-15"`
	TransactionID *string   `json:"transaction_id" format:"uuid"`
	Note          string    `json:"note"`
	CreatedAt     time.Time `json:"created_at"`
} //	@name	finance.DebtPaymentResponse

// DebtPaymentResult adalah pembayaran baru beserta status utang terkini.
type DebtPaymentResult struct {
	Payment DebtPaymentResponse `json:"payment"`
	Debt    DebtResponse        `json:"debt"`
} //	@name	finance.DebtPaymentResult

// DebtPaymentEnvelope adalah response {"data": DebtPaymentResult}.
type DebtPaymentEnvelope struct {
	Data DebtPaymentResult `json:"data"`
} //	@name	finance.DebtPaymentEnvelope

// DebtPaymentListEnvelope adalah response {"data": [DebtPaymentResponse]}.
type DebtPaymentListEnvelope struct {
	Data []DebtPaymentResponse `json:"data"`
} //	@name	finance.DebtPaymentListEnvelope

func toDebtResponse(d app.Debt) DebtResponse {
	p := d.Debt.Principal()
	cur := p.Currency()
	return DebtResponse{ID: d.Debt.ID().String(), Direction: string(d.Debt.Direction()), Counterparty: d.Debt.Counterparty(),
		Principal: p.String(), Currency: cur.Code(), Paid: moneyString(d.Paid, cur), Remaining: moneyString(max(p.Amount()-d.Paid, 0), cur),
		StartDate: d.Debt.StartDate().Format(dateLayout), DueDate: datePtrString(d.Debt.DueDate()), Note: d.Debt.Note(),
		Status: string(d.Debt.Status()), Version: d.Debt.Version(), CreatedAt: d.Debt.CreatedAt(), UpdatedAt: d.Debt.UpdatedAt()}
}

func toDebtPaymentResponse(p *domain.DebtPayment) DebtPaymentResponse {
	return DebtPaymentResponse{ID: p.ID.String(), DebtID: p.DebtID.String(), Amount: p.Amount.String(),
		Currency: p.Amount.Currency().Code(), Date: p.Date.Format(dateLayout), TransactionID: uuidPtrString(p.TransactionID),
		Note: p.Note, CreatedAt: p.CreatedAt}
}

// ===== Bills =====

// CreateBillRequest adalah body POST /bills.
type CreateBillRequest struct {
	Name     string `json:"name" validate:"required,max=100" example:"Listrik PLN"`
	Amount   string `json:"amount" validate:"required,max=32" example:"450000"`
	Currency string `json:"currency,omitempty" validate:"omitempty,len=3" example:"IDR"`
	// AccountID/CategoryID default untuk transaksi saat bill dibayar.
	AccountID  *string `json:"account_id,omitempty" format:"uuid"`
	CategoryID *string `json:"category_id,omitempty" format:"uuid"`
	Frequency  string  `json:"frequency" validate:"required" enums:"once,weekly,monthly,yearly" example:"monthly"`
	DueDate    string  `json:"due_date" validate:"required" example:"2026-10-20"`
	// RemindDaysBefore 0-30, default 3.
	RemindDaysBefore *int `json:"remind_days_before,omitempty" validate:"omitempty,min=0,max=30" example:"3"`
} //	@name	finance.CreateBillRequest

// UpdateBillRequest adalah body PATCH /bills/{id}; "" pada account_id/category_id menghapus default.
type UpdateBillRequest struct {
	Name             *string `json:"name,omitempty" validate:"omitempty,max=100"`
	Amount           *string `json:"amount,omitempty" validate:"omitempty,max=32"`
	AccountID        *string `json:"account_id,omitempty" format:"uuid"`
	CategoryID       *string `json:"category_id,omitempty" format:"uuid"`
	Frequency        *string `json:"frequency,omitempty" enums:"once,weekly,monthly,yearly"`
	DueDate          *string `json:"due_date,omitempty" example:"2026-10-25"`
	RemindDaysBefore *int    `json:"remind_days_before,omitempty" validate:"omitempty,min=0,max=30"`
	Paused           *bool   `json:"paused,omitempty"`
} //	@name	finance.UpdateBillRequest

// PayBillRequest adalah body POST /bills/{id}/pay. create_transaction=true
// mencatat expense (akun/kategori default bill bila tidak diisi).
type PayBillRequest struct {
	CreateTransaction bool   `json:"create_transaction,omitempty"`
	AccountID         string `json:"account_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	CategoryID        string `json:"category_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	// Amount kosong = nominal bill.
	Amount string  `json:"amount,omitempty" validate:"max=32"`
	Date   *string `json:"date,omitempty" example:"2026-10-20"`
	Note   string  `json:"note,omitempty" validate:"max=500"`
} //	@name	finance.PayBillRequest

// BillResponse adalah tagihan berulang.
type BillResponse struct {
	ID               string    `json:"id" format:"uuid"`
	Name             string    `json:"name" example:"Listrik PLN"`
	Amount           string    `json:"amount" example:"450000"`
	Currency         string    `json:"currency" example:"IDR"`
	AccountID        *string   `json:"account_id" format:"uuid"`
	CategoryID       *string   `json:"category_id" format:"uuid"`
	Frequency        string    `json:"frequency" example:"monthly"`
	NextDueDate      string    `json:"next_due_date" example:"2026-10-20"`
	RemindDaysBefore int       `json:"remind_days_before" example:"3"`
	Overdue          bool      `json:"overdue"`
	Status           string    `json:"status" enums:"active,paused,done"`
	Version          int       `json:"version" example:"1"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
} //	@name	finance.BillResponse

// BillEnvelope adalah response {"data": BillResponse}.
type BillEnvelope struct {
	Data BillResponse `json:"data"`
} //	@name	finance.BillEnvelope

// BillListEnvelope adalah response {"data": [BillResponse]}.
type BillListEnvelope struct {
	Data []BillResponse `json:"data"`
} //	@name	finance.BillListEnvelope

// PayBillResult adalah bill setelah dibayar beserta transaksi (bila dibuat).
type PayBillResult struct {
	Bill        BillResponse         `json:"bill"`
	Transaction *TransactionResponse `json:"transaction"`
} //	@name	finance.PayBillResult

// PayBillEnvelope adalah response {"data": PayBillResult}.
type PayBillEnvelope struct {
	Data PayBillResult `json:"data"`
} //	@name	finance.PayBillEnvelope

func toBillResponse(b *domain.Bill) BillResponse {
	return BillResponse{ID: b.ID().String(), Name: b.Name(), Amount: b.Amount().String(), Currency: b.Amount().Currency().Code(),
		AccountID: uuidPtrString(b.AccountID()), CategoryID: uuidPtrString(b.CategoryID()), Frequency: string(b.Frequency()),
		NextDueDate: b.NextDueDate().Format(dateLayout), RemindDaysBefore: b.RemindDays(),
		Overdue: b.OverdueFor() != nil && b.OverdueFor().Equal(b.NextDueDate()), Status: string(b.Status()),
		Version: b.Version(), CreatedAt: b.CreatedAt(), UpdatedAt: b.UpdatedAt()}
}

// ===== Shared wallets =====

// AddMemberRequest adalah body POST /accounts/{id}/members. Isi salah satu:
// user_id atau email.
type AddMemberRequest struct {
	UserID string `json:"user_id,omitempty" validate:"omitempty,uuid" format:"uuid"`
	Email  string `json:"email,omitempty" validate:"omitempty,email,max=254" example:"istri@example.com"`
	Role   string `json:"role" validate:"required,oneof=viewer editor" enums:"viewer,editor"`
} //	@name	finance.AddMemberRequest

// UpdateMemberRequest adalah body PATCH /accounts/{id}/members/{member_id}.
type UpdateMemberRequest struct {
	Role string `json:"role" validate:"required,oneof=viewer editor" enums:"viewer,editor"`
} //	@name	finance.UpdateMemberRequest

// MemberResponse adalah satu anggota akun bersama.
type MemberResponse struct {
	AccountID string    `json:"account_id" format:"uuid"`
	OwnerID   string    `json:"owner_id" format:"uuid"`
	UserID    string    `json:"user_id" format:"uuid"`
	Role      string    `json:"role" enums:"viewer,editor"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
} //	@name	finance.MemberResponse

// MemberEnvelope adalah response {"data": MemberResponse}.
type MemberEnvelope struct {
	Data MemberResponse `json:"data"`
} //	@name	finance.MemberEnvelope

// MemberListEnvelope adalah response {"data": [MemberResponse]}.
type MemberListEnvelope struct {
	Data []MemberResponse `json:"data"`
} //	@name	finance.MemberListEnvelope

// SharedAccountResponse adalah akun milik user lain yang dibagikan ke saya.
type SharedAccountResponse struct {
	Account AccountResponse `json:"account"`
	OwnerID string          `json:"owner_id" format:"uuid"`
	Role    string          `json:"role" enums:"viewer,editor"`
} //	@name	finance.SharedAccountResponse

// SharedAccountListEnvelope adalah response {"data": [SharedAccountResponse]}.
type SharedAccountListEnvelope struct {
	Data []SharedAccountResponse `json:"data"`
} //	@name	finance.SharedAccountListEnvelope

func toMemberResponse(m *domain.AccountMember) MemberResponse {
	return MemberResponse{AccountID: m.AccountID.String(), OwnerID: m.OwnerID.String(), UserID: m.MemberID.String(),
		Role: string(m.Role), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

// ===== Yearly report & audit =====

// YearMonthResponse adalah income/expense/net satu bulan.
type YearMonthResponse struct {
	Month   string `json:"month" example:"2026-01"`
	Income  string `json:"income" example:"15000000"`
	Expense string `json:"expense" example:"9000000"`
	Net     string `json:"net" example:"6000000"`
} //	@name	finance.YearMonthResponse

// YearlyResponse adalah laporan tahunan dalam base currency.
type YearlyResponse struct {
	Year        int                 `json:"year" example:"2026"`
	Currency    string              `json:"currency" example:"IDR"`
	Months      []YearMonthResponse `json:"months"`
	Income      string              `json:"income"`
	Expense     string              `json:"expense"`
	Net         string              `json:"net"`
	SavingsRate string              `json:"savings_rate" example:"40.00"`
	// Unconverted: currency tanpa kurs ke base currency (tidak ikut dijumlah).
	Unconverted []CurrencyTotalsResponse `json:"unconverted"`
} //	@name	finance.YearlyResponse

// YearlyEnvelope adalah response {"data": YearlyResponse}.
type YearlyEnvelope struct {
	Data YearlyResponse `json:"data"`
} //	@name	finance.YearlyEnvelope

// AuditLogResponse adalah satu entri audit trail.
type AuditLogResponse struct {
	ID       string `json:"id" format:"uuid"`
	ActorID  string `json:"actor_id" format:"uuid"`
	Entity   string `json:"entity" enums:"account,transaction,transfer,account_member"`
	EntityID string `json:"entity_id" format:"uuid"`
	Action   string `json:"action" enums:"create,update,delete"`
	// Before/After snapshot JSON entity (null untuk create/delete).
	Before    json.RawMessage `json:"before" swaggertype:"object"`
	After     json.RawMessage `json:"after" swaggertype:"object"`
	CreatedAt time.Time       `json:"created_at"`
} //	@name	finance.AuditLogResponse

// AuditLogListEnvelope adalah response {"data": [...], "meta": PageMeta}.
type AuditLogListEnvelope struct {
	Data []AuditLogResponse  `json:"data"`
	Meta pagination.PageMeta `json:"meta"`
} //	@name	finance.AuditLogListEnvelope

func toYearlyResponse(r *app.YearlyReport) YearlyResponse {
	out := YearlyResponse{Year: r.Year, Currency: r.Currency.Code(), Months: make([]YearMonthResponse, 0, len(r.Months)),
		Income: r.Income.String(), Expense: r.Expense.String(), Net: r.Net.String(), SavingsRate: r.SavingsRate,
		Unconverted: make([]CurrencyTotalsResponse, 0, len(r.Unconverted))}
	for _, m := range r.Months {
		out.Months = append(out.Months, YearMonthResponse{Month: m.Month.Format("2006-01"), Income: m.Income.String(),
			Expense: m.Expense.String(), Net: m.Net.String()})
	}
	for _, t := range r.Unconverted {
		out.Unconverted = append(out.Unconverted, CurrencyTotalsResponse{Currency: t.Currency.Code(), Income: t.Income.String(),
			Expense: t.Expense.String(), Net: t.Net.String()})
	}
	return out
}

func toAuditLogResponse(a *domain.AuditEntry) AuditLogResponse {
	return AuditLogResponse{ID: a.ID.String(), ActorID: a.ActorID.String(), Entity: a.Entity, EntityID: a.EntityID.String(),
		Action: string(a.Action), Before: rawOrNull(a.Before), After: rawOrNull(a.After), CreatedAt: a.CreatedAt}
}

func rawOrNull(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("null")
	}
	return b
}

func datePtrString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(dateLayout)
	return &s
}

func uuidPtrString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}
