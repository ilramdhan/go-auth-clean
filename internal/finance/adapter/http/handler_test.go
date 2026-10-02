package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/authctx"
	"go-auth-clean/internal/platform/validator"
	"go-auth-clean/internal/shared/money"
	"go-auth-clean/internal/shared/pagination"
)

var (
	testUser = uuid.MustParse("01920000-0000-7000-8000-00000000aaaa")
	accID    = uuid.MustParse("01920000-0000-7000-8000-000000000001")
	acc2ID   = uuid.MustParse("01920000-0000-7000-8000-000000000002")
	catID    = uuid.MustParse("01920000-0000-7000-8000-000000000101")
	txID     = uuid.MustParse("01920000-0000-7000-8000-000000000201")
	trID     = uuid.MustParse("01920000-0000-7000-8000-000000000301")
	tNow     = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	errBoom  = errors.New("pq: secret internal detail")
)

// fakeSvc: setiap method mencatat input terakhir dan mengembalikan err bila diisi.
type fakeSvc struct {
	err      error
	replayed bool
	txs      []*domain.Transaction
	trs      []*domain.Transfer
	drifts   []domain.BalanceDrift

	gotTxList  app.ListTransactionsInput
	gotTrList  app.ListTransfersInput
	gotUpdTx   app.UpdateTransactionInput
	gotUpdAcc  app.UpdateAccountInput
	gotCreTx   app.CreateTransactionInput
	gotCreTr   app.CreateTransferInput
	gotIdem    app.IdempotentRequest
	gotReassig *uuid.UUID
	gotCash    app.CashflowInput
	gotCatRep  app.CategoryReportInput
	gotMonth   string
	gotDeleted uuid.UUID

	// P1
	exportErrAfterWrite bool
	gotBudget           app.CreateBudgetInput
	gotUpdBudget        app.UpdateBudgetInput
	gotBudgetMonth      string
	gotTagIDs           []uuid.UUID
	gotUpdTag           app.UpdateTagInput
	gotRule             app.CreateRecurringInput
	gotUpdRule          app.UpdateRecurringInput
	gotRuleAfter        *domain.PageKey
	gotPauseReason      string
	gotExport           app.ListTransactionsInput
	gotImport           app.ImportInput
	gotImportBody       string
	rules               []*domain.RecurringRule

	// P2: input terakhir (tipe app.*Input) dan audit logs yang dikembalikan.
	p2In  any
	audit []*domain.AuditEntry
}

func sampleAccount() *domain.Account {
	return domain.RehydrateAccount(domain.AccountState{ID: accID, UserID: testUser, Name: "BCA", Type: domain.AccountBank,
		InitialBalance: money.New(1000, money.IDR), Balance: money.New(1500, money.IDR), AllowNegative: true,
		Version: 3, CreatedAt: tNow, UpdatedAt: tNow})
}

func sampleCategory() *domain.Category {
	u := testUser
	return domain.RehydrateCategory(domain.CategoryState{ID: catID, UserID: &u, Type: domain.TxExpense, Name: "Kopi",
		CreatedAt: tNow, UpdatedAt: tNow})
}

func sampleTx(id uuid.UUID, day int) *domain.Transaction {
	return domain.RehydrateTransaction(domain.TransactionState{ID: id, UserID: testUser, AccountID: accID, CategoryID: catID,
		Type: domain.TxExpense, Amount: money.New(35000, money.IDR), Date: time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC),
		Note: "Makan", Source: domain.SourceManual, Version: 2, CreatedAt: tNow, UpdatedAt: tNow})
}

func sampleTransfer() *domain.Transfer {
	fee := txID
	return domain.RehydrateTransfer(domain.TransferState{ID: trID, UserID: testUser, FromAccountID: accID, ToAccountID: acc2ID,
		Amount: money.New(500000, money.IDR), Fee: money.New(6500, money.IDR), FeeTransactionID: &fee,
		Date: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Version: 1, CreatedAt: tNow, UpdatedAt: tNow})
}

func sampleSettings() *domain.UserSettings {
	return domain.RehydrateUserSettings(testUser, money.IDR, "Asia/Jakarta", 1, tNow, tNow)
}

func (f *fakeSvc) GetSettings(context.Context, uuid.UUID) (*domain.UserSettings, error) {
	return sampleSettings(), f.err
}

func (f *fakeSvc) UpdateSettings(context.Context, app.UpdateSettingsInput) (*domain.UserSettings, error) {
	return sampleSettings(), f.err
}

func (f *fakeSvc) ListCurrencies(context.Context) ([]domain.CurrencyInfo, error) {
	return []domain.CurrencyInfo{{Code: "IDR", Name: "Rupiah", MinorUnit: 0, Symbol: "Rp"}}, f.err
}

func (f *fakeSvc) CreateAccount(context.Context, app.CreateAccountInput) (*domain.Account, error) {
	return sampleAccount(), f.err
}

func (f *fakeSvc) GetAccount(context.Context, uuid.UUID, uuid.UUID) (*domain.Account, error) {
	return sampleAccount(), f.err
}

func (f *fakeSvc) ListAccounts(context.Context, uuid.UUID, bool) ([]*domain.Account, error) {
	return []*domain.Account{sampleAccount()}, f.err
}

func (f *fakeSvc) UpdateAccount(_ context.Context, in app.UpdateAccountInput) (*domain.Account, error) {
	f.gotUpdAcc = in
	return sampleAccount(), f.err
}

func (f *fakeSvc) ArchiveAccount(context.Context, uuid.UUID, uuid.UUID) (*domain.Account, error) {
	return sampleAccount(), f.err
}

func (f *fakeSvc) UnarchiveAccount(context.Context, uuid.UUID, uuid.UUID) (*domain.Account, error) {
	return sampleAccount(), f.err
}

func (f *fakeSvc) DeleteAccount(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}

func (f *fakeSvc) ListCategories(context.Context, uuid.UUID, string) ([]app.CategoryNode, error) {
	return []app.CategoryNode{{Category: sampleCategory(), Children: []*domain.Category{sampleCategory()}}}, f.err
}

func (f *fakeSvc) GetCategory(context.Context, uuid.UUID, uuid.UUID) (*domain.Category, error) {
	return sampleCategory(), f.err
}

func (f *fakeSvc) CreateCategory(context.Context, app.CreateCategoryInput) (*domain.Category, error) {
	return sampleCategory(), f.err
}

func (f *fakeSvc) UpdateCategory(context.Context, app.UpdateCategoryInput) (*domain.Category, error) {
	return sampleCategory(), f.err
}

func (f *fakeSvc) DeleteCategory(_ context.Context, _, id uuid.UUID, reassign *uuid.UUID) error {
	f.gotDeleted, f.gotReassig = id, reassign
	return f.err
}

func (f *fakeSvc) CreateTransaction(_ context.Context, in app.CreateTransactionInput) (*domain.Transaction, error) {
	f.gotCreTx = in
	return sampleTx(txID, 30), f.err
}

func (f *fakeSvc) GetTransaction(context.Context, uuid.UUID, uuid.UUID) (*domain.Transaction, error) {
	return sampleTx(txID, 30), f.err
}

func (f *fakeSvc) UpdateTransaction(_ context.Context, in app.UpdateTransactionInput) (*domain.Transaction, error) {
	f.gotUpdTx = in
	return sampleTx(txID, 30), f.err
}

func (f *fakeSvc) DeleteTransaction(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}

func (f *fakeSvc) ListTransactions(_ context.Context, in app.ListTransactionsInput) ([]*domain.Transaction, error) {
	f.gotTxList = in
	return f.txs, f.err
}

func (f *fakeSvc) CreateTransfer(_ context.Context, in app.CreateTransferInput) (*domain.Transfer, error) {
	f.gotCreTr = in
	return sampleTransfer(), f.err
}

func (f *fakeSvc) GetTransfer(context.Context, uuid.UUID, uuid.UUID) (*domain.Transfer, error) {
	return sampleTransfer(), f.err
}

func (f *fakeSvc) UpdateTransfer(context.Context, app.UpdateTransferInput) (*domain.Transfer, error) {
	return sampleTransfer(), f.err
}

func (f *fakeSvc) DeleteTransfer(_ context.Context, _, id uuid.UUID) error {
	f.gotDeleted = id
	return f.err
}

func (f *fakeSvc) ListTransfers(_ context.Context, in app.ListTransfersInput) ([]*domain.Transfer, error) {
	f.gotTrList = in
	return f.trs, f.err
}

func (f *fakeSvc) Summary(_ context.Context, _ uuid.UUID, month string) (*app.Summary, error) {
	f.gotMonth = month
	m := money.New(100, money.IDR)
	return &app.Summary{Month: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Currency: money.IDR,
		TotalBalance: []money.Money{m},
		Totals:       []app.CurrencySummary{{Currency: money.IDR, Income: m, Expense: m, Net: money.Zero(money.IDR)}},
		ByCategory:   []app.CategoryShare{{CategoryID: catID, Name: "Kopi", Total: m, Percent: "100.00"}},
		Cashflow:     []app.CashflowItem{{Period: tNow, Income: m, Expense: m, Net: money.Zero(money.IDR)}},
	}, f.err
}

func (f *fakeSvc) Cashflow(_ context.Context, in app.CashflowInput) ([]app.CashflowItem, money.Currency, error) {
	f.gotCash = in
	m := money.New(100, money.IDR)
	return []app.CashflowItem{{Period: tNow, Income: m, Expense: m, Net: money.Zero(money.IDR)}}, money.IDR, f.err
}

func (f *fakeSvc) CategoryReport(_ context.Context, in app.CategoryReportInput) ([]app.CategoryShare, money.Currency, error) {
	f.gotCatRep = in
	return []app.CategoryShare{{CategoryID: catID, Name: "Kopi", Total: money.New(5, money.IDR), Percent: "100.00"}}, money.IDR, f.err
}

func (f *fakeSvc) ReconcileBalances(context.Context, *uuid.UUID) ([]domain.BalanceDrift, error) {
	return f.drifts, f.err
}

// Idempotent: tanpa key -> error seperti service asli; replayed=true mengembalikan body tersimpan.
func (f *fakeSvc) Idempotent(ctx context.Context, req app.IdempotentRequest, fn func(ctx context.Context) (app.StoredResponse, error)) (app.StoredResponse, bool, error) {
	f.gotIdem = req
	if err := app.ValidateIdempotencyKey(req.Key); err != nil {
		return app.StoredResponse{}, false, err
	}
	resp, err := fn(ctx)
	return resp, f.replayed && err == nil, err
}

// server merakit mux dengan requireAuth stub: header X-Test-User=none -> tanpa identitas.
func server(t *testing.T, svc *fakeSvc) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	requireAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-User") != "none" {
				r = r.WithContext(authctx.WithIdentity(r.Context(), authctx.Identity{UserID: testUser}))
			}
			next.ServeHTTP(w, r)
		})
	}
	NewHandler(svc, validator.New()).Routes(mux, requireAuth)
	return mux
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Meta  *pagination.PageMeta
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Field string `json:"field"`
		} `json:"details"`
	} `json:"error"`
}

func do(t *testing.T, mux *http.ServeMux, method, target, body string, headers ...string) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequestWithContext(t.Context(), method, target, nil)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var env envelope
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("invalid json %q: %v", rec.Body.String(), err)
		}
	}
	return rec, env
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, want, rec.Body.String())
	}
}

func wantCode(t *testing.T, env envelope, code string) {
	t.Helper()
	if env.Error == nil || env.Error.Code != code {
		t.Fatalf("error = %+v, want code %s", env.Error, code)
	}
}

func TestRoutes_StatusMatrix(t *testing.T) {
	const txBody = `{"account_id":"01920000-0000-7000-8000-000000000001","category_id":"01920000-0000-7000-8000-000000000101","type":"expense","amount":"35000","transaction_date":"2026-09-30"}`
	const trBody = `{"from_account_id":"01920000-0000-7000-8000-000000000001","to_account_id":"01920000-0000-7000-8000-000000000002","amount":"500000","fee":"6500","transfer_date":"2026-09-30"}`
	const key = "Idempotency-Key"
	cases := []struct {
		name, method, target, body string
		headers                    []string
		want                       int
	}{
		{"get settings", "GET", "/api/v1/settings", "", nil, 200},
		{"put settings", "PUT", "/api/v1/settings", `{"base_currency":"IDR","timezone":"Asia/Jakarta","week_start":1}`, nil, 200},
		{"currencies", "GET", "/api/v1/currencies", "", nil, 200},
		{"list accounts", "GET", "/api/v1/accounts?include_archived=true", "", nil, 200},
		{"create account", "POST", "/api/v1/accounts", `{"name":"BCA","type":"bank","initial_balance":"1000"}`, nil, 201},
		{"get account", "GET", "/api/v1/accounts/" + accID.String(), "", nil, 200},
		{"patch account", "PATCH", "/api/v1/accounts/" + accID.String(), `{"name":"BCA 2"}`, []string{"If-Match", `"3"`}, 200},
		{"archive account", "POST", "/api/v1/accounts/" + accID.String() + "/archive", "", nil, 200},
		{"unarchive account", "POST", "/api/v1/accounts/" + accID.String() + "/unarchive", "", nil, 200},
		{"delete account", "DELETE", "/api/v1/accounts/" + accID.String(), "", nil, 204},
		{"list categories", "GET", "/api/v1/categories?type=expense", "", nil, 200},
		{"create category", "POST", "/api/v1/categories", `{"name":"Kopi","type":"expense","parent_id":"` + catID.String() + `"}`, nil, 201},
		{"get category", "GET", "/api/v1/categories/" + catID.String(), "", nil, 200},
		{"patch category", "PATCH", "/api/v1/categories/" + catID.String(), `{"name":"Kopi Susu"}`, nil, 200},
		{"delete category", "DELETE", "/api/v1/categories/" + catID.String(), "", nil, 204},
		{"create tx", "POST", "/api/v1/transactions", txBody, []string{key, "key-12345678"}, 201},
		{"get tx", "GET", "/api/v1/transactions/" + txID.String(), "", nil, 200},
		{"patch tx", "PATCH", "/api/v1/transactions/" + txID.String(), `{"amount":"40000","transaction_date":"2026-09-29","account_id":"` + acc2ID.String() + `"}`, nil, 200},
		{"delete tx", "DELETE", "/api/v1/transactions/" + txID.String(), "", nil, 204},
		{"list tx", "GET", "/api/v1/transactions", "", nil, 200},
		{"create transfer", "POST", "/api/v1/transfers", trBody, []string{key, "key-12345678"}, 201},
		{"get transfer", "GET", "/api/v1/transfers/" + trID.String(), "", nil, 200},
		{"patch transfer", "PATCH", "/api/v1/transfers/" + trID.String(), `{"fee":"0","transfer_date":"2026-09-29","to_account_id":"` + acc2ID.String() + `"}`, []string{"If-Match", "1"}, 200},
		{"delete transfer", "DELETE", "/api/v1/transfers/" + trID.String(), "", nil, 204},
		{"list transfers", "GET", "/api/v1/transfers?account_id=" + accID.String() + "&from=2026-09-01&to=2026-09-30", "", nil, 200},
		{"summary", "GET", "/api/v1/reports/summary?month=2026-09", "", nil, 200},
		{"cashflow", "GET", "/api/v1/reports/cashflow?from=2026-01-01&to=2026-09-30&granularity=day", "", nil, 200},
		{"category report", "GET", "/api/v1/reports/categories?type=income&from=2026-09-01", "", nil, 200},
		{"reconciliation", "GET", "/api/v1/reports/reconciliation", "", nil, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := server(t, &fakeSvc{})
			rec, env := do(t, mux, tc.method, tc.target, tc.body, tc.headers...)
			wantStatus(t, rec, tc.want)
			if tc.want != 204 && len(env.Data) == 0 {
				t.Fatalf("missing data envelope: %s", rec.Body.String())
			}
			// Tanpa identitas -> 401 di semua route.
			rec, env = do(t, mux, tc.method, tc.target, tc.body, append([]string{"X-Test-User", "none"}, tc.headers...)...)
			wantStatus(t, rec, 401)
			wantCode(t, env, "UNAUTHENTICATED")
		})
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrAccountNotFound, 404, "ACCOUNT_NOT_FOUND"},
		{domain.ErrVersionConflict, 409, "VERSION_CONFLICT"},
		{domain.ErrInsufficientBalance, 422, "INSUFFICIENT_BALANCE"},
		{domain.ErrInvalidRange, 400, "INVALID_RANGE"},
		{&domain.ValidationError{Field: "name", Reason: "required"}, 422, "VALIDATION_FAILED"},
		{errors.Join(errors.New("wrap"), money.ErrCurrencyMismatch), 422, "CURRENCY_MISMATCH"},
		{app.ErrIdempotencyKeyReused, 422, "IDEMPOTENCY_KEY_REUSED"},
		{errBoom, 500, "INTERNAL"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			rec, env := do(t, server(t, &fakeSvc{err: tc.err}), "GET", "/api/v1/accounts/"+accID.String(), "")
			wantStatus(t, rec, tc.status)
			wantCode(t, env, tc.code)
			if strings.Contains(rec.Body.String(), "secret") {
				t.Fatal("internal error detail leaked")
			}
		})
	}
}

func TestAccount_ETagAndIfMatch(t *testing.T) {
	svc := &fakeSvc{}
	mux := server(t, svc)
	rec, _ := do(t, mux, "POST", "/api/v1/accounts", `{"name":"BCA","type":"bank"}`)
	wantStatus(t, rec, 201)
	if got := rec.Header().Get("ETag"); got != `"3"` {
		t.Fatalf("ETag = %q", got)
	}
	rec, _ = do(t, mux, "PATCH", "/api/v1/accounts/"+accID.String(), `{"name":"X"}`, "If-Match", `W/"7"`)
	wantStatus(t, rec, 200)
	if svc.gotUpdAcc.ExpectedVersion == nil || *svc.gotUpdAcc.ExpectedVersion != 7 {
		t.Fatalf("expected version = %v", svc.gotUpdAcc.ExpectedVersion)
	}
	for _, bad := range []string{"abc", `"0"`, "-1"} {
		rec, env := do(t, mux, "PATCH", "/api/v1/accounts/"+accID.String(), `{"name":"X"}`, "If-Match", bad)
		wantStatus(t, rec, 400)
		wantCode(t, env, "INVALID_IF_MATCH")
	}
}

func TestBadInput(t *testing.T) {
	mux := server(t, &fakeSvc{})
	cases := []struct {
		name, method, target, body string
		headers                    []string
		status                     int
		code                       string
	}{
		{"invalid json", "POST", "/api/v1/accounts", `{"name":`, nil, 400, "INVALID_JSON"},
		{"unknown field", "POST", "/api/v1/accounts", `{"name":"a","type":"bank","x":1}`, nil, 400, "INVALID_JSON"},
		{"wrong content type", "POST", "/api/v1/accounts", `{}`, []string{"Content-Type", "text/plain"}, 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"validation", "POST", "/api/v1/accounts", `{"name":"","type":"gold"}`, nil, 422, "VALIDATION_FAILED"},
		{"too large", "POST", "/api/v1/accounts", `{"name":"` + strings.Repeat("a", 70<<10) + `"}`, nil, 413, "BODY_TOO_LARGE"},
		{"bad path id", "GET", "/api/v1/accounts/not-a-uuid", "", nil, 404, "ACCOUNT_NOT_FOUND"},
		{"bad tx path id", "DELETE", "/api/v1/transactions/xyz", "", nil, 404, "TRANSACTION_NOT_FOUND"},
		{"bad transfer path id", "GET", "/api/v1/transfers/xyz", "", nil, 404, "TRANSFER_NOT_FOUND"},
		{"bad category path id", "GET", "/api/v1/categories/xyz", "", nil, 404, "CATEGORY_NOT_FOUND"},
		{"bad reassign", "DELETE", "/api/v1/categories/" + catID.String() + "?reassign_to=x", "", nil, 400, "INVALID_PARAMETER"},
		{"bad limit", "GET", "/api/v1/transactions?limit=0", "", nil, 400, "INVALID_PARAMETER"},
		{"bad from", "GET", "/api/v1/transactions?from=2026/01/01", "", nil, 400, "INVALID_PARAMETER"},
		{"bad to", "GET", "/api/v1/transactions?to=x", "", nil, 400, "INVALID_PARAMETER"},
		{"bad account_id", "GET", "/api/v1/transactions?account_id=x", "", nil, 400, "INVALID_PARAMETER"},
		{"bad category_id", "GET", "/api/v1/transactions?category_id=x", "", nil, 400, "INVALID_PARAMETER"},
		{"long q", "GET", "/api/v1/transactions?q=" + strings.Repeat("a", 101), "", nil, 400, "INVALID_PARAMETER"},
		{"bad cursor", "GET", "/api/v1/transactions?cursor=!!!notbase64", "", nil, 400, "INVALID_CURSOR"},
		{"transfer bad limit", "GET", "/api/v1/transfers?limit=x", "", nil, 400, "INVALID_PARAMETER"},
		{"transfer bad account", "GET", "/api/v1/transfers?account_id=x", "", nil, 400, "INVALID_PARAMETER"},
		{"transfer bad cursor", "GET", "/api/v1/transfers?cursor=abc", "", nil, 400, "INVALID_CURSOR"},
		{"cashflow bad date", "GET", "/api/v1/reports/cashflow?to=x", "", nil, 400, "INVALID_PARAMETER"},
		{"category report bad date", "GET", "/api/v1/reports/categories?from=x", "", nil, 400, "INVALID_PARAMETER"},
		{"tx missing key", "POST", "/api/v1/transactions", `{"account_id":"` + accID.String() + `","category_id":"` + catID.String() + `","type":"expense","amount":"1","transaction_date":"2026-09-30"}`, nil, 400, "IDEMPOTENCY_KEY_REQUIRED"},
		{"tx short key", "POST", "/api/v1/transactions", `{"account_id":"` + accID.String() + `","category_id":"` + catID.String() + `","type":"expense","amount":"1","transaction_date":"2026-09-30"}`, []string{"Idempotency-Key", "abc"}, 400, "INVALID_IDEMPOTENCY_KEY"},
		{"tx bad date", "POST", "/api/v1/transactions", `{"account_id":"` + accID.String() + `","category_id":"` + catID.String() + `","type":"expense","amount":"1","transaction_date":"2026-13-30"}`, []string{"Idempotency-Key", "key-12345678"}, 422, "VALIDATION_FAILED"},
		{"patch tx bad account", "PATCH", "/api/v1/transactions/" + txID.String(), `{"account_id":"x"}`, nil, 422, "VALIDATION_FAILED"},
		{"patch tx bad if-match", "PATCH", "/api/v1/transactions/" + txID.String(), `{}`, []string{"If-Match", "x"}, 400, "INVALID_IF_MATCH"},
		{"transfer validation", "POST", "/api/v1/transfers", `{"from_account_id":"x"}`, []string{"Idempotency-Key", "key-12345678"}, 422, "VALIDATION_FAILED"},
		{"patch transfer bad if-match", "PATCH", "/api/v1/transfers/" + trID.String(), `{}`, []string{"If-Match", "0"}, 400, "INVALID_IF_MATCH"},
		{"settings validation", "PUT", "/api/v1/settings", `{"base_currency":"ID","timezone":"x"}`, nil, 422, "VALIDATION_FAILED"},
		{"category validation", "POST", "/api/v1/categories", `{"name":"x","type":"foo"}`, nil, 422, "VALIDATION_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, env := do(t, mux, tc.method, tc.target, tc.body, tc.headers...)
			wantStatus(t, rec, tc.status)
			wantCode(t, env, tc.code)
		})
	}
}

func TestListTransactions_PaginationAndFilters(t *testing.T) {
	svc := &fakeSvc{txs: []*domain.Transaction{sampleTx(txID, 30), sampleTx(uuid.MustParse("01920000-0000-7000-8000-000000000200"), 29), sampleTx(uuid.MustParse("01920000-0000-7000-8000-000000000199"), 28)}}
	mux := server(t, svc)
	q := "/api/v1/transactions?limit=2&type=expense&currency=idr&from=2026-09-01&to=2026-09-30&min_amount=1&max_amount=99999&include_children=true&q=+makan+" +
		"&account_id=" + acc2ID.String() + "," + accID.String() + "&account_id=" + accID.String() + "&category_id=" + catID.String()
	rec, env := do(t, mux, "GET", q, "")
	wantStatus(t, rec, 200)
	var items []TransactionResponse
	if err := json.Unmarshal(env.Data, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || env.Meta == nil || !env.Meta.HasMore || env.Meta.NextCursor == "" || env.Meta.Limit != 2 {
		t.Fatalf("items=%d meta=%+v", len(items), env.Meta)
	}
	in := svc.gotTxList
	if in.UserID != testUser || in.Limit != 2 || in.Currency != "IDR" || in.Query != "makan" || !in.IncludeChildren ||
		len(in.AccountIDs) != 2 || in.AccountIDs[0] != accID || len(in.CategoryIDs) != 1 || in.From == nil || in.To == nil {
		t.Fatalf("input = %+v", in)
	}
	if items[0].Amount != "35000" || items[0].Currency != "IDR" || items[0].TransactionDate != "2026-09-30" {
		t.Fatalf("item = %+v", items[0])
	}

	// Cursor dipakai ulang dengan filter sama -> After terisi.
	rec, _ = do(t, mux, "GET", q+"&cursor="+env.Meta.NextCursor, "")
	wantStatus(t, rec, 200)
	if svc.gotTxList.After == nil || svc.gotTxList.After.Date.Day() != 29 {
		t.Fatalf("after = %+v", svc.gotTxList.After)
	}
	// Cursor dipakai di filter berbeda -> ditolak.
	rec, env = do(t, mux, "GET", "/api/v1/transactions?cursor="+env.Meta.NextCursor, "")
	wantStatus(t, rec, 400)
	wantCode(t, env, "INVALID_CURSOR")

	// List kosong tetap [] bukan null.
	svc.txs = nil
	rec, _ = do(t, mux, "GET", "/api/v1/transactions", "")
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Fatalf("body = %s", rec.Body.String())
	}

	ids := make([]string, maxFilterIDs+1)
	for i := range ids {
		ids[i] = "account_id=" + uuid.NewString()
	}
	rec, _ = do(t, mux, "GET", "/api/v1/transactions?"+strings.Join(ids, "&"), "")
	wantStatus(t, rec, 400)
	rec, _ = do(t, mux, "GET", "/api/v1/transactions?account_id="+strings.TrimPrefix(strings.ReplaceAll(strings.Join(ids, ","), "account_id=", ""), ""), "")
	wantStatus(t, rec, 400)
}

func TestListTransfers_Pagination(t *testing.T) {
	a, b := sampleTransfer(), sampleTransfer()
	svc := &fakeSvc{trs: []*domain.Transfer{a, b}}
	mux := server(t, svc)
	rec, env := do(t, mux, "GET", "/api/v1/transfers?limit=1", "")
	wantStatus(t, rec, 200)
	if env.Meta == nil || !env.Meta.HasMore {
		t.Fatalf("meta = %+v", env.Meta)
	}
	rec, _ = do(t, mux, "GET", "/api/v1/transfers?limit=1&cursor="+env.Meta.NextCursor, "")
	wantStatus(t, rec, 200)
	if svc.gotTrList.After == nil || svc.gotTrList.Limit != 1 {
		t.Fatalf("input = %+v", svc.gotTrList)
	}
}

func TestCreateTransaction_IdempotencyAndInput(t *testing.T) {
	svc := &fakeSvc{replayed: true}
	mux := server(t, svc)
	body := `{"account_id":"` + accID.String() + `","category_id":"` + catID.String() + `","type":"expense","amount":"35000","transaction_date":"2026-09-30","note":"Makan"}`
	rec, env := do(t, mux, "POST", "/api/v1/transactions", body, "Idempotency-Key", "key-12345678")
	wantStatus(t, rec, 201)
	if rec.Header().Get(HeaderReplayed) != "true" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", rec.Header())
	}
	var tr TransactionResponse
	if err := json.Unmarshal(env.Data, &tr); err != nil || tr.ID != txID.String() {
		t.Fatalf("data = %s err %v", env.Data, err)
	}
	if svc.gotIdem.Path != "/api/v1/transactions" || svc.gotIdem.Method != "POST" || svc.gotIdem.UserID != testUser || len(svc.gotIdem.Body) == 0 {
		t.Fatalf("idem = %+v", svc.gotIdem)
	}
	in := svc.gotCreTx
	if in.AccountID != accID || in.CategoryID != catID || in.Amount != "35000" || in.Date.Format(dateLayout) != "2026-09-30" || in.Note != "Makan" {
		t.Fatalf("input = %+v", in)
	}

	// Error use case di dalam idempotent dipetakan.
	svc.err = domain.ErrInsufficientBalance
	rec, env = do(t, mux, "POST", "/api/v1/transactions", body, "Idempotency-Key", "key-12345678")
	wantStatus(t, rec, 422)
	wantCode(t, env, "INSUFFICIENT_BALANCE")
	if rec.Header().Get(HeaderReplayed) != "" {
		t.Fatal("replayed header on error")
	}
}

func TestCreateTransfer_Input(t *testing.T) {
	svc := &fakeSvc{}
	mux := server(t, svc)
	body := `{"from_account_id":"` + accID.String() + `","to_account_id":"` + acc2ID.String() + `","amount":"500000","fee":"6500","transfer_date":"2026-09-30","note":"Top up"}`
	rec, env := do(t, mux, "POST", "/api/v1/transfers", body, "Idempotency-Key", "key-abcdefgh")
	wantStatus(t, rec, 201)
	if rec.Header().Get(HeaderReplayed) != "" {
		t.Fatal("unexpected replayed header")
	}
	var tr TransferResponse
	if err := json.Unmarshal(env.Data, &tr); err != nil || tr.Fee != "6500" || tr.FeeTransactionID == nil {
		t.Fatalf("data = %s err %v", env.Data, err)
	}
	if svc.gotCreTr.FromAccountID != accID || svc.gotCreTr.ToAccountID != acc2ID || svc.gotCreTr.Fee != "6500" || svc.gotIdem.Path != "/api/v1/transfers" {
		t.Fatalf("input = %+v", svc.gotCreTr)
	}
}

func TestUpdateTransaction_PassesFields(t *testing.T) {
	svc := &fakeSvc{}
	mux := server(t, svc)
	rec, _ := do(t, mux, "PATCH", "/api/v1/transactions/"+txID.String(),
		`{"category_id":"`+catID.String()+`","type":"income","note":"x"}`, "If-Match", `"2"`)
	wantStatus(t, rec, 200)
	if rec.Header().Get("ETag") != `"2"` {
		t.Fatalf("etag = %q", rec.Header().Get("ETag"))
	}
	in := svc.gotUpdTx
	if in.ID != txID || in.CategoryID == nil || *in.CategoryID != catID || in.Type == nil || *in.Type != "income" ||
		in.Note == nil || in.Amount != nil || in.ExpectedVersion == nil || *in.ExpectedVersion != 2 {
		t.Fatalf("input = %+v", in)
	}
}

func TestDeleteCategory_Reassign(t *testing.T) {
	svc := &fakeSvc{}
	rec, _ := do(t, server(t, svc), "DELETE", "/api/v1/categories/"+catID.String()+"?reassign_to="+acc2ID.String(), "")
	wantStatus(t, rec, 204)
	if svc.gotDeleted != catID || svc.gotReassig == nil || *svc.gotReassig != acc2ID {
		t.Fatalf("deleted=%v reassign=%v", svc.gotDeleted, svc.gotReassig)
	}
}

func TestReports(t *testing.T) {
	svc := &fakeSvc{drifts: []domain.BalanceDrift{{AccountID: accID, UserID: testUser, Currency: money.USD, Cached: 1050, Expected: 1000}}}
	mux := server(t, svc)

	rec, env := do(t, mux, "GET", "/api/v1/reports/summary?month=2026-09", "")
	wantStatus(t, rec, 200)
	var s SummaryResponse
	if err := json.Unmarshal(env.Data, &s); err != nil || s.Month != "2026-09" || svc.gotMonth != "2026-09" || len(s.ByCategory) != 1 {
		t.Fatalf("summary = %+v err %v", s, err)
	}

	rec, env = do(t, mux, "GET", "/api/v1/reports/cashflow?currency=usd", "")
	wantStatus(t, rec, 200)
	var cf CashflowResponse
	if err := json.Unmarshal(env.Data, &cf); err != nil || cf.Granularity != "month" || len(cf.Points) != 1 || svc.gotCash.Currency != "USD" {
		t.Fatalf("cashflow = %+v err %v", cf, err)
	}

	rec, env = do(t, mux, "GET", "/api/v1/reports/categories", "")
	wantStatus(t, rec, 200)
	var cr CategoryReportResponse
	if err := json.Unmarshal(env.Data, &cr); err != nil || cr.Type != "expense" || len(cr.Items) != 1 {
		t.Fatalf("category report = %+v err %v", cr, err)
	}

	rec, env = do(t, mux, "GET", "/api/v1/reports/reconciliation", "")
	wantStatus(t, rec, 200)
	var rr ReconciliationResponse
	if err := json.Unmarshal(env.Data, &rr); err != nil || rr.OK || len(rr.Drifts) != 1 ||
		rr.Drifts[0].CachedBalance != "10.50" || rr.Drifts[0].ExpectedBalance != "10.00" {
		t.Fatalf("reconciliation = %+v err %v", rr, err)
	}

	svc.err = domain.ErrInvalidRange
	for _, p := range []string{"summary", "cashflow", "categories", "reconciliation"} {
		rec, _ = do(t, mux, "GET", "/api/v1/reports/"+p, "")
		wantStatus(t, rec, 400)
	}
}

// TestServiceErrorsOnEveryRoute memastikan setiap handler memetakan error service.
func TestServiceErrorsOnEveryRoute(t *testing.T) {
	mux := server(t, &fakeSvc{err: domain.ErrVersionConflict})
	routes := [][3]string{
		{"GET", "/api/v1/settings", ""},
		{"PUT", "/api/v1/settings", `{"base_currency":"IDR","timezone":"Asia/Jakarta","week_start":1}`},
		{"GET", "/api/v1/currencies", ""},
		{"GET", "/api/v1/accounts", ""},
		{"POST", "/api/v1/accounts", `{"name":"a","type":"bank"}`},
		{"PATCH", "/api/v1/accounts/" + accID.String(), `{}`},
		{"POST", "/api/v1/accounts/" + accID.String() + "/archive", ""},
		{"POST", "/api/v1/accounts/" + accID.String() + "/unarchive", ""},
		{"DELETE", "/api/v1/accounts/" + accID.String(), ""},
		{"GET", "/api/v1/categories", ""},
		{"POST", "/api/v1/categories", `{"name":"a","type":"expense"}`},
		{"PATCH", "/api/v1/categories/" + catID.String(), `{}`},
		{"DELETE", "/api/v1/categories/" + catID.String(), ""},
		{"GET", "/api/v1/transactions/" + txID.String(), ""},
		{"PATCH", "/api/v1/transactions/" + txID.String(), `{}`},
		{"DELETE", "/api/v1/transactions/" + txID.String(), ""},
		{"GET", "/api/v1/transactions", ""},
		{"GET", "/api/v1/transfers/" + trID.String(), ""},
		{"PATCH", "/api/v1/transfers/" + trID.String(), `{}`},
		{"DELETE", "/api/v1/transfers/" + trID.String(), ""},
		{"GET", "/api/v1/transfers", ""},
	}
	for _, rt := range routes {
		rec, env := do(t, mux, rt[0], rt[1], rt[2])
		if rec.Code != 409 || env.Error == nil || env.Error.Code != "VERSION_CONFLICT" {
			t.Errorf("%s %s: status %d body %s", rt[0], rt[1], rec.Code, rec.Body.String())
		}
	}
}
