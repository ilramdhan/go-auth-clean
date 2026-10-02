package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
)

const (
	idemKey    = "Idempotency-Key"
	budgetBody = `{"category_id":"01920000-0000-7000-8000-000000000101","period_month":"2026-09","amount":"100000","alert_threshold_pct":80}`
	ruleBody   = `{"account_id":"01920000-0000-7000-8000-000000000001","category_id":"01920000-0000-7000-8000-000000000101","type":"expense","amount":"50000","frequency":"monthly","start_date":"2026-01-31","end_date":"2026-12-31"}`
)

func TestP1Routes_StatusMatrix(t *testing.T) {
	bid, tid, rid := budgetID.String(), tagID.String(), ruleID.String()
	cases := []struct {
		name, method, target, body string
		headers                    []string
		want                       int
	}{
		{"list budgets", "GET", "/api/v1/budgets?month=2026-09", "", nil, 200},
		{"create budget", "POST", "/api/v1/budgets", budgetBody, []string{idemKey, "key-12345678"}, 201},
		{"get budget", "GET", "/api/v1/budgets/" + bid, "", nil, 200},
		{"patch budget", "PATCH", "/api/v1/budgets/" + bid, `{"amount":"200000"}`, []string{"If-Match", "1"}, 200},
		{"delete budget", "DELETE", "/api/v1/budgets/" + bid, "", nil, 204},
		{"list tags", "GET", "/api/v1/tags", "", nil, 200},
		{"create tag", "POST", "/api/v1/tags", `{"name":"liburan","color":"#ff0000"}`, nil, 201},
		{"patch tag", "PATCH", "/api/v1/tags/" + tid, `{"name":"x"}`, nil, 200},
		{"delete tag", "DELETE", "/api/v1/tags/" + tid, "", nil, 204},
		{"set tx tags", "PUT", "/api/v1/transactions/" + txID.String() + "/tags", `{"tag_ids":["` + tid + `"]}`, nil, 200},
		{"list rules", "GET", "/api/v1/recurring-rules", "", nil, 200},
		{"create rule", "POST", "/api/v1/recurring-rules", ruleBody, []string{idemKey, "key-12345678"}, 201},
		{"get rule", "GET", "/api/v1/recurring-rules/" + rid, "", nil, 200},
		{"patch rule", "PATCH", "/api/v1/recurring-rules/" + rid, `{"end_date":""}`, nil, 200},
		{"delete rule", "DELETE", "/api/v1/recurring-rules/" + rid, "", nil, 204},
		{"pause rule", "POST", "/api/v1/recurring-rules/" + rid + "/pause", `{"reason":"libur"}`, nil, 200},
		{"pause rule no body", "POST", "/api/v1/recurring-rules/" + rid + "/pause", "", nil, 200},
		{"resume rule", "POST", "/api/v1/recurring-rules/" + rid + "/resume", "", nil, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := server(t, &fakeSvc{})
			rec, env := do(t, mux, tc.method, tc.target, tc.body, tc.headers...)
			wantStatus(t, rec, tc.want)
			if tc.want != 204 && len(env.Data) == 0 {
				t.Fatalf("missing data: %s", rec.Body.String())
			}
			rec, env = do(t, mux, tc.method, tc.target, tc.body, append([]string{"X-Test-User", "none"}, tc.headers...)...)
			wantStatus(t, rec, 401)
			wantCode(t, env, "UNAUTHENTICATED")
		})
	}
}

func TestP1ServiceErrors(t *testing.T) {
	mux := server(t, &fakeSvc{err: domain.ErrVersionConflict})
	bid, tid, rid := budgetID.String(), tagID.String(), ruleID.String()
	routes := [][3]string{
		{"GET", "/api/v1/budgets", ""},
		{"GET", "/api/v1/budgets/" + bid, ""},
		{"PATCH", "/api/v1/budgets/" + bid, `{}`},
		{"DELETE", "/api/v1/budgets/" + bid, ""},
		{"GET", "/api/v1/tags", ""},
		{"POST", "/api/v1/tags", `{"name":"a"}`},
		{"PATCH", "/api/v1/tags/" + tid, `{}`},
		{"DELETE", "/api/v1/tags/" + tid, ""},
		{"PUT", "/api/v1/transactions/" + txID.String() + "/tags", `{"tag_ids":[]}`},
		{"GET", "/api/v1/recurring-rules", ""},
		{"GET", "/api/v1/recurring-rules/" + rid, ""},
		{"PATCH", "/api/v1/recurring-rules/" + rid, `{}`},
		{"DELETE", "/api/v1/recurring-rules/" + rid, ""},
		{"POST", "/api/v1/recurring-rules/" + rid + "/pause", ""},
		{"POST", "/api/v1/recurring-rules/" + rid + "/resume", ""},
		{"GET", "/api/v1/transactions/export", ""},
	}
	for _, rt := range routes {
		rec, env := do(t, mux, rt[0], rt[1], rt[2])
		if rec.Code != 409 || env.Error == nil || env.Error.Code != "VERSION_CONFLICT" {
			t.Errorf("%s %s: status %d body %s", rt[0], rt[1], rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{budgetBody, ruleBody} {
		target := "/api/v1/budgets"
		if body == ruleBody {
			target = "/api/v1/recurring-rules"
		}
		rec, _ := do(t, mux, "POST", target, body, idemKey, "key-12345678")
		wantStatus(t, rec, 409)
	}
}

func TestP1ErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrBudgetNotFound, 404, "BUDGET_NOT_FOUND"},
		{domain.ErrBudgetExists, 409, "BUDGET_EXISTS"},
		{domain.ErrTagNotFound, 404, "TAG_NOT_FOUND"},
		{domain.ErrTooManyTags, 422, "TOO_MANY_TAGS"},
		{domain.ErrRecurringNotFound, 404, "RECURRING_RULE_NOT_FOUND"},
		{domain.ErrRuleEnded, 422, "RULE_ENDED"},
		{domain.ErrInvalidFrequency, 422, "INVALID_FREQUENCY"},
		{domain.ErrDuplicateImport, 409, "DUPLICATE_IMPORT"},
		{app.ErrImportTooLarge, 413, "IMPORT_TOO_LARGE"},
		{app.ErrImportTooManyRow, 422, "IMPORT_TOO_MANY_ROWS"},
		{app.ErrImportBadHeader, 400, "INVALID_CSV_HEADER"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			rec, env := do(t, server(t, &fakeSvc{err: tc.err}), "GET", "/api/v1/tags", "")
			wantStatus(t, rec, tc.status)
			wantCode(t, env, tc.code)
		})
	}
}

func TestBudget_InputAndResponse(t *testing.T) {
	svc := &fakeSvc{}
	mux := server(t, svc)
	rec, env := do(t, mux, "POST", "/api/v1/budgets", budgetBody, idemKey, "key-12345678")
	wantStatus(t, rec, 201)
	if svc.gotBudget.CategoryID != catID || svc.gotBudget.Month != "2026-09" || svc.gotBudget.Amount != "100000" || svc.gotBudget.Threshold != 80 {
		t.Fatalf("input = %+v", svc.gotBudget)
	}
	var b BudgetResponse
	if err := json.Unmarshal(env.Data, &b); err != nil {
		t.Fatal(err)
	}
	if b.Spent != "120000" || b.Remaining != "-20000" || b.Status != "exceeded" || !b.Overspent || b.PeriodMonth != "2026-09" {
		t.Fatalf("response = %+v", b)
	}
	do(t, mux, "GET", "/api/v1/budgets?month=2026-08", "")
	if svc.gotBudgetMonth != "2026-08" {
		t.Fatalf("month = %q", svc.gotBudgetMonth)
	}
	rec, _ = do(t, mux, "PATCH", "/api/v1/budgets/"+budgetID.String(), `{"amount":"5","alert_threshold_pct":90}`, "If-Match", `"1"`)
	wantStatus(t, rec, 200)
	if rec.Header().Get("ETag") == "" || svc.gotUpdBudget.ExpectedVersion == nil || *svc.gotUpdBudget.Threshold != 90 {
		t.Fatalf("update = %+v", svc.gotUpdBudget)
	}
	bad := []struct{ method, target, body string }{
		{"POST", "/api/v1/budgets", `{"category_id":"x","amount":"1"}`},
		{"POST", "/api/v1/budgets", `{"category_id":"` + catID.String() + `","amount":"1","alert_threshold_pct":150}`},
		{"PATCH", "/api/v1/budgets/not-uuid", `{}`},
	}
	for _, c := range bad {
		rec, _ := do(t, mux, c.method, c.target, c.body, idemKey, "key-12345678")
		if rec.Code < 400 || rec.Code >= 500 {
			t.Errorf("%s %s: status %d", c.method, c.target, rec.Code)
		}
	}
}

func TestTags_InputAndTransactionTags(t *testing.T) {
	svc := &fakeSvc{txs: []*domain.Transaction{sampleTx(txID, 30)}}
	mux := server(t, svc)
	body := `{"tag_ids":["` + tagID.String() + `","` + tagID.String() + `"]}`
	rec, env := do(t, mux, "PUT", "/api/v1/transactions/"+txID.String()+"/tags", body)
	wantStatus(t, rec, 200)
	if len(svc.gotTagIDs) != 2 || !strings.Contains(string(env.Data), "liburan") {
		t.Fatalf("ids %v data %s", svc.gotTagIDs, env.Data)
	}
	rec, _ = do(t, mux, "PUT", "/api/v1/transactions/"+txID.String()+"/tags", `{"tag_ids":["nope"]}`)
	wantStatus(t, rec, 422)
	rec, _ = do(t, mux, "PUT", "/api/v1/transactions/bad/tags", `{"tag_ids":[]}`)
	wantStatus(t, rec, 404)

	// tag ikut tampil di list/get transaksi, filter ?tag= diteruskan.
	rec, env = do(t, mux, "GET", "/api/v1/transactions?tag=%20Liburan%20", "")
	wantStatus(t, rec, 200)
	if svc.gotTxList.Tag != "Liburan" || !strings.Contains(string(env.Data), `"tags":[{`) {
		t.Fatalf("tag filter %q data %s", svc.gotTxList.Tag, env.Data)
	}
	rec, _ = do(t, mux, "GET", "/api/v1/transactions?tag="+strings.Repeat("x", 31), "")
	wantStatus(t, rec, 400)
	_, env = do(t, mux, "GET", "/api/v1/transactions/"+txID.String(), "")
	if !strings.Contains(string(env.Data), "liburan") {
		t.Fatalf("get tx without tags: %s", env.Data)
	}

	txBody := `{"account_id":"` + accID.String() + `","category_id":"` + catID.String() + `","type":"expense","amount":"1","transaction_date":"2026-09-30","tag_ids":["` + tagID.String() + `"]}`
	rec, _ = do(t, mux, "POST", "/api/v1/transactions", txBody, idemKey, "key-12345678")
	wantStatus(t, rec, 201)
	if len(svc.gotCreTx.TagIDs) != 1 || svc.gotCreTx.TagIDs[0] != tagID {
		t.Fatalf("tag ids = %v", svc.gotCreTx.TagIDs)
	}
	rec, _ = do(t, mux, "PATCH", "/api/v1/tags/"+tagID.String(), `{"color":"#00ff00"}`)
	wantStatus(t, rec, 200)
	if svc.gotUpdTag.Color == nil || *svc.gotUpdTag.Color != "#00ff00" {
		t.Fatalf("upd tag = %+v", svc.gotUpdTag)
	}
}

func TestRecurring_InputAndPagination(t *testing.T) {
	svc := &fakeSvc{}
	mux := server(t, svc)
	rec, env := do(t, mux, "POST", "/api/v1/recurring-rules", ruleBody, idemKey, "key-12345678")
	wantStatus(t, rec, 201)
	if svc.gotRule.AccountID != accID || svc.gotRule.Frequency != "monthly" || svc.gotRule.EndDate == nil ||
		svc.gotRule.StartDate.Day() != 31 {
		t.Fatalf("input = %+v", svc.gotRule)
	}
	var rr RecurringRuleResponse
	if err := json.Unmarshal(env.Data, &rr); err != nil {
		t.Fatal(err)
	}
	if rr.Amount != "50000" || rr.NextRunDate != "2026-10-31" || len(rr.Upcoming) == 0 {
		t.Fatalf("response = %+v", rr)
	}

	// end_date + count bersamaan ditolak validator.
	both := strings.Replace(ruleBody, `"end_date"`, `"count":3,"end_date"`, 1)
	rec, _ = do(t, mux, "POST", "/api/v1/recurring-rules", both, idemKey, "key-12345678")
	wantStatus(t, rec, 422)
	for _, b := range []string{
		strings.Replace(ruleBody, accID.String(), "x", 1),
		strings.Replace(ruleBody, "2026-01-31", "2026-02-31", 1),
		strings.Replace(ruleBody, "2026-12-31", "2026-13-01", 1),
	} {
		rec, _ = do(t, mux, "POST", "/api/v1/recurring-rules", b, idemKey, "key-12345678")
		if rec.Code != 400 && rec.Code != 422 {
			t.Errorf("bad body status %d: %s", rec.Code, b)
		}
	}

	// PATCH: end_date "" = hapus, nilai = ubah, tidak ada = tetap.
	do(t, mux, "PATCH", "/api/v1/recurring-rules/"+ruleID.String(), `{"end_date":""}`)
	if svc.gotUpdRule.EndDate == nil || *svc.gotUpdRule.EndDate != nil {
		t.Fatalf("clear end = %+v", svc.gotUpdRule.EndDate)
	}
	do(t, mux, "PATCH", "/api/v1/recurring-rules/"+ruleID.String(), `{"end_date":"2027-01-01","start_date":"2026-02-01","account_id":"`+acc2ID.String()+`","category_id":"`+catID.String()+`"}`, "If-Match", "2")
	if svc.gotUpdRule.EndDate == nil || *svc.gotUpdRule.EndDate == nil || svc.gotUpdRule.StartDate == nil || *svc.gotUpdRule.AccountID != acc2ID {
		t.Fatalf("set end = %+v", svc.gotUpdRule)
	}
	do(t, mux, "PATCH", "/api/v1/recurring-rules/"+ruleID.String(), `{}`)
	if svc.gotUpdRule.EndDate != nil {
		t.Fatal("end date should be untouched")
	}
	for _, b := range []string{`{"end_date":"bad"}`, `{"start_date":"bad"}`, `{"account_id":"bad"}`, `{"category_id":"bad"}`} {
		rec, _ = do(t, mux, "PATCH", "/api/v1/recurring-rules/"+ruleID.String(), b)
		if rec.Code != 400 && rec.Code != 422 {
			t.Errorf("patch %s: %d", b, rec.Code)
		}
	}
	rec, _ = do(t, mux, "PATCH", "/api/v1/recurring-rules/"+ruleID.String(), `{}`, "If-Match", "abc")
	wantStatus(t, rec, 400)
	do(t, mux, "POST", "/api/v1/recurring-rules/"+ruleID.String()+"/pause", `{"reason":"cuti"}`)
	if svc.gotPauseReason != "cuti" {
		t.Fatalf("reason = %q", svc.gotPauseReason)
	}
	rec, _ = do(t, mux, "POST", "/api/v1/recurring-rules/"+ruleID.String()+"/pause", `{bad`)
	wantStatus(t, rec, 400)

	// keyset pagination: limit+1 baris -> next_cursor, cursor dipakai di request berikutnya.
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	svc.rules = []*domain.RecurringRule{
		sampleRule(uuid.New(), base.Add(3*time.Hour)), sampleRule(uuid.New(), base.Add(2*time.Hour)), sampleRule(uuid.New(), base.Add(time.Hour)),
	}
	rec, env = do(t, mux, "GET", "/api/v1/recurring-rules?limit=2", "")
	wantStatus(t, rec, 200)
	if env.Meta == nil || env.Meta.NextCursor == "" {
		t.Fatalf("expected next cursor: %s", rec.Body.String())
	}
	do(t, mux, "GET", "/api/v1/recurring-rules?limit=2&cursor="+env.Meta.NextCursor, "")
	if svc.gotRuleAfter == nil || !svc.gotRuleAfter.Date.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("after = %+v", svc.gotRuleAfter)
	}
	for _, q := range []string{"?limit=0", "?cursor=garbage"} {
		rec, _ = do(t, mux, "GET", "/api/v1/recurring-rules"+q, "")
		wantStatus(t, rec, 400)
	}
}

func TestExport(t *testing.T) {
	svc := &fakeSvc{}
	mux := server(t, svc)
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/transactions/export?type=expense&tag=kopi&cursor=x&limit=5", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	// cursor & limit tidak relevan untuk export, tapi cursor tetap divalidasi.
	wantStatus(t, rec, 400)

	req = httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/transactions/export?type=expense&tag=kopi&format=csv", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	wantStatus(t, rec, 200)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "transactions.csv") {
		t.Fatalf("disposition %q", cd)
	}
	if svc.gotExport.UserID != testUser || svc.gotExport.Type != "expense" || svc.gotExport.Tag != "kopi" || svc.gotExport.Limit != 0 {
		t.Fatalf("export input %+v", svc.gotExport)
	}
	if !strings.HasPrefix(rec.Body.String(), "date,type") {
		t.Fatalf("body %q", rec.Body.String())
	}
	rec, env := do(t, mux, "GET", "/api/v1/transactions/export?format=xlsx", "")
	wantStatus(t, rec, 400)
	wantCode(t, env, "INVALID_PARAMETER")
	rec, _ = do(t, mux, "GET", "/api/v1/transactions/export?from=bad", "")
	wantStatus(t, rec, 400)
	rec, _ = do(t, mux, "GET", "/api/v1/transactions/export", "", "X-Test-User", "none")
	wantStatus(t, rec, 401)

	// error setelah stream dimulai -> koneksi diputus (panic ErrAbortHandler).
	svc = &fakeSvc{err: errBoom, exportErrAfterWrite: true}
	mux = server(t, svc)
	defer func() {
		r := recover()
		if err, _ := r.(error); !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("recover = %v", r)
		}
	}()
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/transactions/export", nil))
}

func multipartReq(t *testing.T, target, field, content string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, "tx.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), "POST", target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestImport(t *testing.T) {
	const csvBody = "date,type,amount,account,category\n2026-09-30,expense,1000,BCA,Kopi\n"
	cases := []struct {
		name, target, field, content string
		svcErr                       error
		want                         int
		code                         string
	}{
		{"commit", "/api/v1/transactions/import", "file", csvBody, nil, 201, ""},
		{"dry run", "/api/v1/transactions/import?dry_run=true", "file", csvBody, nil, 200, ""},
		{"missing file", "/api/v1/transactions/import", "other", csvBody, nil, 400, "INVALID_PARAMETER"},
		{"too large", "/api/v1/transactions/import", "file", strings.Repeat("a", app.MaxImportBytes+multipartOverhead+1), nil, 413, "IMPORT_TOO_LARGE"},
		{"service error", "/api/v1/transactions/import", "file", csvBody, app.ErrImportBadHeader, 400, "INVALID_CSV_HEADER"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeSvc{err: tc.svcErr}
			rec := httptest.NewRecorder()
			server(t, svc).ServeHTTP(rec, multipartReq(t, tc.target, tc.field, tc.content))
			wantStatus(t, rec, tc.want)
			var env envelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if tc.code != "" {
				wantCode(t, env, tc.code)
				return
			}
			if svc.gotImportBody != csvBody || svc.gotImport.DryRun != (tc.want == 200) {
				t.Fatalf("import input dry=%v body=%q", svc.gotImport.DryRun, svc.gotImportBody)
			}
			var res ImportResponse
			if err := json.Unmarshal(env.Data, &res); err != nil {
				t.Fatal(err)
			}
			if res.Imported != 2 || res.Skipped != 1 || len(res.Errors) != 1 || res.Errors[0].Row != 3 {
				t.Fatalf("response %+v", res)
			}
		})
	}
	rec := httptest.NewRecorder()
	req := multipartReq(t, "/api/v1/transactions/import", "file", csvBody)
	req.Header.Set("X-Test-User", "none")
	server(t, &fakeSvc{}).ServeHTTP(rec, req)
	wantStatus(t, rec, 401)
}
