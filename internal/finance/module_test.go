package finance_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance"
	"go-auth-clean/internal/platform/authctx"
	"go-auth-clean/internal/platform/database/dbtest"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// TestModule_EndToEnd: wiring nyata (postgres + service + handler) lewat HTTP.
func TestModule_EndToEnd(t *testing.T) {
	pool := dbtest.New(t)
	dbtest.Truncate(t, pool, "goal_contributions", "savings_goals", "debt_payments", "debts", "bills", "account_members", "finance_audit_logs", "exchange_rates", "transaction_tags", "tags", "budgets", "recurring_rules", "idempotency_keys", "transfers", "transactions", "accounts", "user_settings")
	mod := finance.NewModule(finance.Deps{Pool: pool, Config: finance.Config{WorkerInterval: time.Hour}})
	if mod.Service() == nil || len(mod.Workers()) != 3 {
		t.Fatal("module not wired")
	}
	user := uuid.New()
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(authctx.WithIdentity(r.Context(), authctx.Identity{UserID: user})))
		})
	})
	call := func(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
		var req *http.Request
		if body == "" {
			req = httptest.NewRequestWithContext(t.Context(), method, path, nil)
		} else {
			req = httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := call("POST", "/api/v1/accounts", `{"name":"Cash","type":"cash","initial_balance":"100000"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create account: %d %s", rec.Code, rec.Body)
	}
	accID := extractID(t, rec.Body.String())
	txBody := `{"account_id":"` + accID + `","category_id":"01920000-0000-7000-8000-000000000101","type":"expense","amount":"35000","transaction_date":"` + time.Now().In(jakarta(t)).Format("2006-01-02") + `"}`
	first := call("POST", "/api/v1/transactions", txBody, "Idempotency-Key", "e2e-key-0001")
	second := call("POST", "/api/v1/transactions", txBody, "Idempotency-Key", "e2e-key-0001")
	if first.Code != 201 || second.Code != 201 || second.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("idempotent create: %d/%d %s", first.Code, second.Code, second.Body)
	}
	if strings.TrimSpace(first.Body.String()) != strings.TrimSpace(second.Body.String()) {
		t.Fatal("replay body differs")
	}
	rec = call("GET", "/api/v1/accounts/"+accID, "")
	if !strings.Contains(rec.Body.String(), `"balance":"65000"`) {
		t.Fatalf("balance not applied once: %s", rec.Body)
	}
	if rec = call("POST", "/api/v1/transactions", strings.Replace(txBody, "35000", "1", 1), "Idempotency-Key", "e2e-key-0001"); rec.Code != 422 {
		t.Fatalf("key reuse: %d", rec.Code)
	}
	if rec = call("GET", "/api/v1/reports/reconciliation", ""); !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("reconciliation: %s", rec.Body)
	}

	// Worker berhenti bersih saat ctx dibatalkan.
	ctx, cancel := context.WithCancel(t.Context())
	mod.StartWorkers(ctx)
	cancel()
	done := make(chan struct{})
	go func() { mod.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func extractID(t *testing.T, body string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, `"id":"`)
	if !ok || len(rest) < 36 {
		t.Fatalf("no id in %s", body)
	}
	return rest[:36]
}

func jakarta(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
