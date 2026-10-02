// Package http adalah delivery layer REST untuk bounded context finance.
package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/authctx"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/shared/money"
)

// maxBody: body endpoint finance kecil; batasi 64 KiB.
const maxBody int64 = 64 << 10

// HeaderIdempotencyKey & HeaderReplayed dipakai POST yang menggerakkan uang.
const (
	HeaderIdempotencyKey = "Idempotency-Key"
	HeaderReplayed       = "Idempotent-Replayed"
)

// Service adalah port yang dibutuhkan handler (didefinisikan oleh consumer).
type Service interface {
	GetSettings(ctx context.Context, userID uuid.UUID) (*domain.UserSettings, error)
	UpdateSettings(ctx context.Context, in app.UpdateSettingsInput) (*domain.UserSettings, error)
	ListCurrencies(ctx context.Context) ([]domain.CurrencyInfo, error)

	CreateAccount(ctx context.Context, in app.CreateAccountInput) (*domain.Account, error)
	GetAccount(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error)
	ListAccounts(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]*domain.Account, error)
	UpdateAccount(ctx context.Context, in app.UpdateAccountInput) (*domain.Account, error)
	ArchiveAccount(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error)
	UnarchiveAccount(ctx context.Context, userID, id uuid.UUID) (*domain.Account, error)
	DeleteAccount(ctx context.Context, userID, id uuid.UUID) error

	ListCategories(ctx context.Context, userID uuid.UUID, typ string) ([]app.CategoryNode, error)
	GetCategory(ctx context.Context, userID, id uuid.UUID) (*domain.Category, error)
	CreateCategory(ctx context.Context, in app.CreateCategoryInput) (*domain.Category, error)
	UpdateCategory(ctx context.Context, in app.UpdateCategoryInput) (*domain.Category, error)
	DeleteCategory(ctx context.Context, userID, id uuid.UUID, reassignTo *uuid.UUID) error

	CreateTransaction(ctx context.Context, in app.CreateTransactionInput) (*domain.Transaction, error)
	GetTransaction(ctx context.Context, userID, id uuid.UUID) (*domain.Transaction, error)
	UpdateTransaction(ctx context.Context, in app.UpdateTransactionInput) (*domain.Transaction, error)
	DeleteTransaction(ctx context.Context, userID, id uuid.UUID) error
	ListTransactions(ctx context.Context, in app.ListTransactionsInput) ([]*domain.Transaction, error)

	CreateTransfer(ctx context.Context, in app.CreateTransferInput) (*domain.Transfer, error)
	GetTransfer(ctx context.Context, userID, id uuid.UUID) (*domain.Transfer, error)
	UpdateTransfer(ctx context.Context, in app.UpdateTransferInput) (*domain.Transfer, error)
	DeleteTransfer(ctx context.Context, userID, id uuid.UUID) error
	ListTransfers(ctx context.Context, in app.ListTransfersInput) ([]*domain.Transfer, error)

	Summary(ctx context.Context, userID uuid.UUID, month string) (*app.Summary, error)
	Cashflow(ctx context.Context, in app.CashflowInput) ([]app.CashflowItem, money.Currency, error)
	CategoryReport(ctx context.Context, in app.CategoryReportInput) ([]app.CategoryShare, money.Currency, error)
	ReconcileBalances(ctx context.Context, userID *uuid.UUID) ([]domain.BalanceDrift, error)

	CreateBudget(ctx context.Context, in app.CreateBudgetInput) (*app.BudgetView, error)
	GetBudget(ctx context.Context, userID, id uuid.UUID) (*app.BudgetView, error)
	ListBudgets(ctx context.Context, userID uuid.UUID, month string) ([]app.BudgetView, error)
	UpdateBudget(ctx context.Context, in app.UpdateBudgetInput) (*app.BudgetView, error)
	DeleteBudget(ctx context.Context, userID, id uuid.UUID) error

	CreateTag(ctx context.Context, userID uuid.UUID, name, color string) (*domain.Tag, error)
	ListTags(ctx context.Context, userID uuid.UUID) ([]*domain.Tag, error)
	UpdateTag(ctx context.Context, in app.UpdateTagInput) (*domain.Tag, error)
	DeleteTag(ctx context.Context, userID, id uuid.UUID) error
	SetTransactionTags(ctx context.Context, userID, txID uuid.UUID, tagIDs []uuid.UUID) ([]*domain.Tag, error)
	TransactionTags(ctx context.Context, userID uuid.UUID, txIDs []uuid.UUID) (map[uuid.UUID][]*domain.Tag, error)

	CreateRecurringRule(ctx context.Context, in app.CreateRecurringInput) (*domain.RecurringRule, error)
	GetRecurringRule(ctx context.Context, userID, id uuid.UUID) (*domain.RecurringRule, error)
	ListRecurringRules(ctx context.Context, userID uuid.UUID, limit int, after *domain.PageKey) ([]*domain.RecurringRule, error)
	UpdateRecurringRule(ctx context.Context, in app.UpdateRecurringInput) (*domain.RecurringRule, error)
	DeleteRecurringRule(ctx context.Context, userID, id uuid.UUID) error
	PauseRecurringRule(ctx context.Context, userID, id uuid.UUID, reason string) (*domain.RecurringRule, error)
	ResumeRecurringRule(ctx context.Context, userID, id uuid.UUID) (*domain.RecurringRule, error)

	ExportTransactions(ctx context.Context, in app.ListTransactionsInput, w io.Writer) error
	ImportTransactions(ctx context.Context, in app.ImportInput) (*app.ImportResult, error)

	CreateRate(ctx context.Context, in app.CreateRateInput) (*domain.ExchangeRate, error)
	GetRate(ctx context.Context, userID, id uuid.UUID) (*domain.ExchangeRate, error)
	ListRates(ctx context.Context, userID uuid.UUID) ([]*domain.ExchangeRate, error)
	UpdateRate(ctx context.Context, userID, id uuid.UUID, rate string) (*domain.ExchangeRate, error)
	DeleteRate(ctx context.Context, userID, id uuid.UUID) error
	Convert(ctx context.Context, in app.ConvertInput) (money.Money, money.Money, error)
	Yearly(ctx context.Context, userID uuid.UUID, year int) (*app.YearlyReport, error)
	ListAuditLogs(ctx context.Context, userID uuid.UUID, f domain.AuditFilter) ([]*domain.AuditEntry, error)

	CreateGoal(ctx context.Context, in app.CreateGoalInput) (*app.Goal, error)
	GetGoal(ctx context.Context, userID, id uuid.UUID) (*app.Goal, error)
	ListGoals(ctx context.Context, userID uuid.UUID) ([]*app.Goal, error)
	UpdateGoal(ctx context.Context, in app.UpdateGoalInput) (*app.Goal, error)
	DeleteGoal(ctx context.Context, userID, id uuid.UUID) error
	Contribute(ctx context.Context, in app.ContributeInput) (*domain.GoalContribution, *app.Goal, error)
	ListContributions(ctx context.Context, userID, goalID uuid.UUID) ([]*domain.GoalContribution, error)
	DeleteContribution(ctx context.Context, userID, goalID, id uuid.UUID) error

	CreateDebt(ctx context.Context, in app.CreateDebtInput) (*app.Debt, error)
	GetDebt(ctx context.Context, userID, id uuid.UUID) (*app.Debt, error)
	ListDebts(ctx context.Context, userID uuid.UUID, status string) ([]*app.Debt, error)
	UpdateDebt(ctx context.Context, in app.UpdateDebtInput) (*app.Debt, error)
	DeleteDebt(ctx context.Context, userID, id uuid.UUID) error
	PayDebt(ctx context.Context, in app.PayDebtInput) (*domain.DebtPayment, *app.Debt, error)
	ListDebtPayments(ctx context.Context, userID, debtID uuid.UUID) ([]*domain.DebtPayment, error)
	DeleteDebtPayment(ctx context.Context, userID, debtID, id uuid.UUID) error

	CreateBill(ctx context.Context, in app.CreateBillInput) (*domain.Bill, error)
	GetBill(ctx context.Context, userID, id uuid.UUID) (*domain.Bill, error)
	ListBills(ctx context.Context, userID uuid.UUID) ([]*domain.Bill, error)
	UpdateBill(ctx context.Context, in app.UpdateBillInput) (*domain.Bill, error)
	DeleteBill(ctx context.Context, userID, id uuid.UUID) error
	PayBill(ctx context.Context, in app.PayBillInput) (*domain.Bill, *domain.Transaction, error)

	AddMember(ctx context.Context, in app.AddMemberInput) (*domain.AccountMember, error)
	ListMembers(ctx context.Context, actor, accountID uuid.UUID) ([]*domain.AccountMember, error)
	UpdateMemberRole(ctx context.Context, owner, accountID, memberID uuid.UUID, role string) (*domain.AccountMember, error)
	RemoveMember(ctx context.Context, actor, accountID, memberID uuid.UUID) error
	ListSharedAccounts(ctx context.Context, userID uuid.UUID) ([]domain.SharedAccount, error)

	Idempotent(ctx context.Context, req app.IdempotentRequest, fn func(ctx context.Context) (app.StoredResponse, error)) (app.StoredResponse, bool, error)
}

type Validator interface {
	Struct(s any) error
}

type Handler struct {
	svc      Service
	validate Validator
}

func NewHandler(svc Service, v Validator) *Handler {
	return &Handler{svc: svc, validate: v}
}

// Routes mendaftarkan endpoint finance; semuanya butuh login.
func (h *Handler) Routes(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler) {
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/settings":   h.getSettings,
		"PUT /api/v1/settings":   h.updateSettings,
		"GET /api/v1/currencies": h.listCurrencies,

		"GET /api/v1/accounts":                 h.listAccounts,
		"POST /api/v1/accounts":                h.createAccount,
		"GET /api/v1/accounts/{id}":            h.getAccount,
		"PATCH /api/v1/accounts/{id}":          h.updateAccount,
		"DELETE /api/v1/accounts/{id}":         h.deleteAccount,
		"POST /api/v1/accounts/{id}/archive":   h.archiveAccount,
		"POST /api/v1/accounts/{id}/unarchive": h.unarchiveAccount,

		"GET /api/v1/categories":         h.listCategories,
		"POST /api/v1/categories":        h.createCategory,
		"GET /api/v1/categories/{id}":    h.getCategory,
		"PATCH /api/v1/categories/{id}":  h.updateCategory,
		"DELETE /api/v1/categories/{id}": h.deleteCategory,

		"GET /api/v1/transactions":           h.listTransactions,
		"POST /api/v1/transactions":          h.createTransaction,
		"GET /api/v1/transactions/{id}":      h.getTransaction,
		"PATCH /api/v1/transactions/{id}":    h.updateTransaction,
		"DELETE /api/v1/transactions/{id}":   h.deleteTransaction,
		"PUT /api/v1/transactions/{id}/tags": h.setTransactionTags,
		// Literal "export"/"import" lebih spesifik dari {id} (ServeMux Go 1.22).
		"GET /api/v1/transactions/export":  h.exportTransactions,
		"POST /api/v1/transactions/import": h.importTransactions,

		"GET /api/v1/budgets":         h.listBudgets,
		"POST /api/v1/budgets":        h.createBudget,
		"GET /api/v1/budgets/{id}":    h.getBudget,
		"PATCH /api/v1/budgets/{id}":  h.updateBudget,
		"DELETE /api/v1/budgets/{id}": h.deleteBudget,

		"GET /api/v1/tags":         h.listTags,
		"POST /api/v1/tags":        h.createTag,
		"PATCH /api/v1/tags/{id}":  h.updateTag,
		"DELETE /api/v1/tags/{id}": h.deleteTag,

		"GET /api/v1/recurring-rules":              h.listRecurringRules,
		"POST /api/v1/recurring-rules":             h.createRecurringRule,
		"GET /api/v1/recurring-rules/{id}":         h.getRecurringRule,
		"PATCH /api/v1/recurring-rules/{id}":       h.updateRecurringRule,
		"DELETE /api/v1/recurring-rules/{id}":      h.deleteRecurringRule,
		"POST /api/v1/recurring-rules/{id}/pause":  h.pauseRecurringRule,
		"POST /api/v1/recurring-rules/{id}/resume": h.resumeRecurringRule,

		"GET /api/v1/transfers":         h.listTransfers,
		"POST /api/v1/transfers":        h.createTransfer,
		"GET /api/v1/transfers/{id}":    h.getTransfer,
		"PATCH /api/v1/transfers/{id}":  h.updateTransfer,
		"DELETE /api/v1/transfers/{id}": h.deleteTransfer,

		"GET /api/v1/reports/summary":        h.summary,
		"GET /api/v1/reports/cashflow":       h.cashflow,
		"GET /api/v1/reports/categories":     h.categoryReport,
		"GET /api/v1/reports/reconciliation": h.reconciliation,
		"GET /api/v1/reports/yearly":         h.yearly,

		"GET /api/v1/exchange-rates":         h.listRates,
		"POST /api/v1/exchange-rates":        h.createRate,
		"GET /api/v1/exchange-rates/convert": h.convert,
		"GET /api/v1/exchange-rates/{id}":    h.getRate,
		"PATCH /api/v1/exchange-rates/{id}":  h.updateRate,
		"DELETE /api/v1/exchange-rates/{id}": h.deleteRate,

		"GET /api/v1/savings-goals":                             h.listGoals,
		"POST /api/v1/savings-goals":                            h.createGoal,
		"GET /api/v1/savings-goals/{id}":                        h.getGoal,
		"PATCH /api/v1/savings-goals/{id}":                      h.updateGoal,
		"DELETE /api/v1/savings-goals/{id}":                     h.deleteGoal,
		"GET /api/v1/savings-goals/{id}/contributions":          h.listContributions,
		"POST /api/v1/savings-goals/{id}/contributions":         h.contribute,
		"DELETE /api/v1/savings-goals/{id}/contributions/{cid}": h.deleteContribution,
		"POST /api/v1/savings-goals/{id}/withdrawals":           h.withdraw,

		"GET /api/v1/debts":                        h.listDebts,
		"POST /api/v1/debts":                       h.createDebt,
		"GET /api/v1/debts/{id}":                   h.getDebt,
		"PATCH /api/v1/debts/{id}":                 h.updateDebt,
		"DELETE /api/v1/debts/{id}":                h.deleteDebt,
		"GET /api/v1/debts/{id}/payments":          h.listDebtPayments,
		"POST /api/v1/debts/{id}/payments":         h.payDebt,
		"DELETE /api/v1/debts/{id}/payments/{pid}": h.deleteDebtPayment,

		"GET /api/v1/bills":           h.listBills,
		"POST /api/v1/bills":          h.createBill,
		"GET /api/v1/bills/{id}":      h.getBill,
		"PATCH /api/v1/bills/{id}":    h.updateBill,
		"DELETE /api/v1/bills/{id}":   h.deleteBill,
		"POST /api/v1/bills/{id}/pay": h.payBill,

		"GET /api/v1/accounts/{id}/members":                h.listMembers,
		"POST /api/v1/accounts/{id}/members":               h.addMember,
		"PATCH /api/v1/accounts/{id}/members/{member_id}":  h.updateMember,
		"DELETE /api/v1/accounts/{id}/members/{member_id}": h.removeMember,
		"GET /api/v1/shared-accounts":                      h.listSharedAccounts,

		"GET /api/v1/audit-logs": h.listAuditLogs,
	}
	for pattern, fn := range routes {
		mux.Handle(pattern, requireAuth(fn))
	}
}

// decodeAndValidate adalah satu helper generic untuk semua tipe request.
func decodeAndValidate[T any](h *Handler, w http.ResponseWriter, r *http.Request) (T, error) {
	var req T
	if err := httpx.DecodeLimit(w, r, &req, maxBody); err != nil {
		return req, err
	}
	return req, h.validate.Struct(req)
}

// userID mengambil user dari context; false (dan 401 sudah ditulis) bila tidak ada.
func userID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := authctx.FromContext(r.Context())
	if !ok || id.UserID == uuid.Nil {
		httpx.WriteError(w, r, errUnauthenticated)
		return uuid.Nil, false
	}
	return id.UserID, true
}

// pathID mem-parsing {id}; ID tidak valid diperlakukan sebagai not found.
func pathID(r *http.Request, notFound error) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, notFound
	}
	return id, nil
}

func parseUUIDPtr(field string, s *string) (*uuid.UUID, error) {
	if s == nil {
		return nil, nil //nolint:nilnil // nil = field tidak dikirim
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, invalidParam(field, "harus UUID")
	}
	return &id, nil
}

func parseDate(field, s string) (time.Time, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, invalidParam(field, "format tanggal harus YYYY-MM-DD")
	}
	return t, nil
}

func parseDatePtr(field string, s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil //nolint:nilnil // nil = field tidak dikirim
	}
	t, err := parseDate(field, *s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// queryDate membaca query param tanggal opsional.
func queryDate(r *http.Request, field string) (*time.Time, error) {
	v := r.URL.Query().Get(field)
	if v == "" {
		return nil, nil //nolint:nilnil // nil = tanpa filter
	}
	return parseDatePtr(field, &v)
}

// ifMatch membaca header If-Match ("3", 3 atau W/"3"). Kosong = tidak dicek.
func ifMatch(r *http.Request) (*int, error) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if v == "" {
		return nil, nil //nolint:nilnil // tanpa If-Match = tidak dicek
	}
	v = strings.Trim(strings.TrimPrefix(v, "W/"), `"`)
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return nil, errInvalidIfMatch
	}
	return &n, nil
}

// setETag mengirim version sebagai ETag agar client bisa memakai If-Match.
func setETag(w http.ResponseWriter, version int) {
	w.Header().Set("ETag", `"`+strconv.Itoa(version)+`"`)
}

// idempotent menjalankan create sekali per Idempotency-Key. Hash dihitung dari
// DTO yang sudah di-decode lalu di-marshal ulang (kanonis), bukan raw body.
func (h *Handler) idempotent(w http.ResponseWriter, r *http.Request, uid uuid.UUID, path string, dto any,
	create func(ctx context.Context) (status int, body any, err error),
) {
	canon, err := json.Marshal(dto)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	req := app.IdempotentRequest{UserID: uid, Key: r.Header.Get(HeaderIdempotencyKey),
		Method: r.Method, Path: path, Body: canon}
	resp, replayed, err := h.svc.Idempotent(r.Context(), req, func(ctx context.Context) (app.StoredResponse, error) {
		status, body, err := create(ctx)
		if err != nil {
			return app.StoredResponse{}, err
		}
		b, err := json.Marshal(httpx.DataResponse{Data: body})
		if err != nil {
			return app.StoredResponse{}, err
		}
		return app.StoredResponse{Status: status, Body: b}, nil
	})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if replayed {
		w.Header().Set(HeaderReplayed, "true")
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write(append(resp.Body, '\n'))
}

// deleteAction: pola umum DELETE /{id} -> 204.
func (h *Handler) deleteAction(w http.ResponseWriter, r *http.Request, notFound error, del func(ctx context.Context, userID, id uuid.UUID) error) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, notFound)
	if err == nil {
		err = del(r.Context(), uid, id)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
