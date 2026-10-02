package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
)

const (
	key8     = "key-12345678"
	goalBody = `{"name":"Laptop","target":"1000000","target_date":"2027-09-01","account_id":"01920000-0000-7000-8000-000000000001"}`
	debtBody = `{"direction":"payable","counterparty":"Budi","principal":"100000","start_date":"2026-09-01","due_date":"2026-12-01"}`
	billBody = `{"name":"Listrik","amount":"450000","frequency":"monthly","due_date":"2026-10-01","account_id":"01920000-0000-7000-8000-000000000001","remind_days_before":3}`
)

func TestP2Routes_StatusMatrix(t *testing.T) {
	rid, gid, did, bid, aid, pid := rateID.String(), goalID.String(), debtID.String(), billID.String(), accID.String(), peerID.String()
	sub := uuid.New().String()
	cases := []struct {
		name, method, target, body string
		headers                    []string
		want                       int
	}{
		{"list rates", "GET", "/api/v1/exchange-rates", "", nil, 200},
		{"create rate", "POST", "/api/v1/exchange-rates", `{"base":"usd","quote":"IDR","rate":"16250","as_of":"2026-09-01"}`, nil, 201},
		{"get rate", "GET", "/api/v1/exchange-rates/" + rid, "", nil, 200},
		{"patch rate", "PATCH", "/api/v1/exchange-rates/" + rid, `{"rate":"16300"}`, nil, 200},
		{"delete rate", "DELETE", "/api/v1/exchange-rates/" + rid, "", nil, 204},
		{"convert", "GET", "/api/v1/exchange-rates/convert?amount=1&from=usd&date=2026-09-01", "", nil, 200},
		{"yearly", "GET", "/api/v1/reports/yearly?year=2026", "", nil, 200},
		{"yearly default", "GET", "/api/v1/reports/yearly", "", nil, 200},
		{"audit", "GET", "/api/v1/audit-logs?entity=transaction&entity_id=" + txID.String(), "", nil, 200},

		{"list goals", "GET", "/api/v1/savings-goals", "", nil, 200},
		{"create goal", "POST", "/api/v1/savings-goals", goalBody, nil, 201},
		{"get goal", "GET", "/api/v1/savings-goals/" + gid, "", nil, 200},
		{"patch goal", "PATCH", "/api/v1/savings-goals/" + gid, `{"target_date":"","account_id":"","archived":true}`, []string{"If-Match", "1"}, 200},
		{"delete goal", "DELETE", "/api/v1/savings-goals/" + gid, "", nil, 204},
		{"list contributions", "GET", "/api/v1/savings-goals/" + gid + "/contributions", "", nil, 200},
		{"contribute", "POST", "/api/v1/savings-goals/" + gid + "/contributions", `{"transfer_id":"` + trID.String() + `"}`, nil, 201},
		{"withdraw", "POST", "/api/v1/savings-goals/" + gid + "/withdrawals", `{"amount":"1000","date":"2026-09-02"}`, nil, 201},
		{"delete contribution", "DELETE", "/api/v1/savings-goals/" + gid + "/contributions/" + sub, "", nil, 204},

		{"list debts", "GET", "/api/v1/debts?status=open", "", nil, 200},
		{"create debt", "POST", "/api/v1/debts", debtBody, nil, 201},
		{"get debt", "GET", "/api/v1/debts/" + did, "", nil, 200},
		{"patch debt", "PATCH", "/api/v1/debts/" + did, `{"due_date":"","note":"x"}`, nil, 200},
		{"delete debt", "DELETE", "/api/v1/debts/" + did, "", nil, 204},
		{"list payments", "GET", "/api/v1/debts/" + did + "/payments", "", nil, 200},
		{"pay debt", "POST", "/api/v1/debts/" + did + "/payments", `{"amount":"40000","date":"2026-09-02","account_id":"` + aid + `"}`, []string{idemKey, key8}, 201},
		{"delete payment", "DELETE", "/api/v1/debts/" + did + "/payments/" + sub, "", nil, 204},

		{"list bills", "GET", "/api/v1/bills", "", nil, 200},
		{"create bill", "POST", "/api/v1/bills", billBody, nil, 201},
		{"get bill", "GET", "/api/v1/bills/" + bid, "", nil, 200},
		{"patch bill", "PATCH", "/api/v1/bills/" + bid, `{"account_id":"","due_date":"2026-10-05","paused":true}`, nil, 200},
		{"delete bill", "DELETE", "/api/v1/bills/" + bid, "", nil, 204},
		{"pay bill", "POST", "/api/v1/bills/" + bid + "/pay", `{"create_transaction":true,"date":"2026-09-02"}`, []string{idemKey, key8}, 200},
		{"pay bill no body", "POST", "/api/v1/bills/" + bid + "/pay", "", []string{idemKey, key8}, 200},

		{"list members", "GET", "/api/v1/accounts/" + aid + "/members", "", nil, 200},
		{"add member id", "POST", "/api/v1/accounts/" + aid + "/members", `{"user_id":"` + pid + `","role":"viewer"}`, nil, 201},
		{"add member email", "POST", "/api/v1/accounts/" + aid + "/members", `{"email":"a@b.io","role":"editor"}`, nil, 201},
		{"patch member", "PATCH", "/api/v1/accounts/" + aid + "/members/" + pid, `{"role":"editor"}`, nil, 200},
		{"remove member", "DELETE", "/api/v1/accounts/" + aid + "/members/" + pid, "", nil, 204},
		{"shared accounts", "GET", "/api/v1/shared-accounts", "", nil, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := server(t, &fakeSvc{audit: sampleAudit(2)})
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

// TestP2_ErrorMapping memastikan error domain P2 dipetakan ke status yang benar;
// terutama non-member = 404 dan role kurang = 403.
func TestP2_ErrorMapping(t *testing.T) {
	aid := accID.String()
	cases := []struct {
		err          error
		method, path string
		body         string
		status       int
		code         string
	}{
		{domain.ErrAccountNotFound, "GET", "/api/v1/accounts/" + aid + "/members", "", 404, "ACCOUNT_NOT_FOUND"},
		{domain.ErrForbidden, "POST", "/api/v1/accounts/" + aid + "/members", `{"user_id":"` + peerID.String() + `","role":"viewer"}`, 403, "FORBIDDEN"},
		{domain.ErrMemberExists, "POST", "/api/v1/accounts/" + aid + "/members", `{"email":"a@b.io","role":"viewer"}`, 409, "MEMBER_EXISTS"},
		{domain.ErrUserNotFound, "POST", "/api/v1/accounts/" + aid + "/members", `{"email":"a@b.io","role":"viewer"}`, 404, "USER_NOT_FOUND"},
		{domain.ErrMemberNotFound, "PATCH", "/api/v1/accounts/" + aid + "/members/" + peerID.String(), `{"role":"viewer"}`, 404, "MEMBER_NOT_FOUND"},
		{domain.ErrForbidden, "DELETE", "/api/v1/accounts/" + aid + "/members/" + peerID.String(), "", 403, "FORBIDDEN"},
		{domain.ErrRateExists, "POST", "/api/v1/exchange-rates", `{"base":"USD","quote":"IDR","rate":"1","as_of":"2026-09-01"}`, 409, "RATE_EXISTS"},
		{domain.ErrRateUnavailable, "GET", "/api/v1/exchange-rates/convert?amount=1&from=EUR", "", 422, "RATE_UNAVAILABLE"},
		{domain.ErrRateNotFound, "PATCH", "/api/v1/exchange-rates/" + rateID.String(), `{"rate":"2"}`, 404, "RATE_NOT_FOUND"},
		{domain.ErrGoalNotFound, "GET", "/api/v1/savings-goals/" + goalID.String(), "", 404, "GOAL_NOT_FOUND"},
		{domain.ErrDuplicateLink, "POST", "/api/v1/savings-goals/" + goalID.String() + "/contributions", `{"transfer_id":"` + trID.String() + `"}`, 409, "DUPLICATE_LINK"},
		{domain.ErrGoalArchived, "POST", "/api/v1/savings-goals/" + goalID.String() + "/withdrawals", `{"amount":"1"}`, 422, "GOAL_ARCHIVED"},
		{domain.ErrContributionNotFound, "DELETE", "/api/v1/savings-goals/" + goalID.String() + "/contributions/" + txID.String(), "", 404, "CONTRIBUTION_NOT_FOUND"},
		{domain.ErrOverpayment, "POST", "/api/v1/debts/" + debtID.String() + "/payments", `{"amount":"1"}`, 422, "OVERPAYMENT"},
		{domain.ErrDebtSettled, "POST", "/api/v1/debts/" + debtID.String() + "/payments", `{"amount":"1"}`, 422, "DEBT_SETTLED"},
		{domain.ErrDebtNotFound, "PATCH", "/api/v1/debts/" + debtID.String(), `{}`, 404, "DEBT_NOT_FOUND"},
		{domain.ErrBillDone, "POST", "/api/v1/bills/" + billID.String() + "/pay", `{}`, 422, "BILL_DONE"},
		{domain.ErrBillNotFound, "PATCH", "/api/v1/bills/" + billID.String(), `{}`, 404, "BILL_NOT_FOUND"},
		{domain.ErrVersionConflict, "PATCH", "/api/v1/savings-goals/" + goalID.String(), `{}`, 409, "VERSION_CONFLICT"},
		{errBoom, "GET", "/api/v1/audit-logs", "", 500, "INTERNAL"},
		{errBoom, "GET", "/api/v1/reports/yearly", "", 500, "INTERNAL"},
		{errBoom, "GET", "/api/v1/shared-accounts", "", 500, "INTERNAL"},
		{errBoom, "GET", "/api/v1/bills", "", 500, "INTERNAL"},
		{errBoom, "GET", "/api/v1/debts", "", 500, "INTERNAL"},
		{errBoom, "GET", "/api/v1/savings-goals", "", 500, "INTERNAL"},
		{errBoom, "GET", "/api/v1/exchange-rates", "", 500, "INTERNAL"},
		{errBoom, "POST", "/api/v1/savings-goals", goalBody, 500, "INTERNAL"},
		{errBoom, "POST", "/api/v1/debts", debtBody, 500, "INTERNAL"},
		{errBoom, "POST", "/api/v1/bills", billBody, 500, "INTERNAL"},
		{errBoom, "GET", "/api/v1/bills/" + billID.String(), "", 500, "INTERNAL"},
		{errBoom, "GET", "/api/v1/debts/" + debtID.String() + "/payments", "", 500, "INTERNAL"},
	}
	for _, tc := range cases {
		t.Run(tc.code+" "+tc.method+" "+tc.path, func(t *testing.T) {
			rec, env := do(t, server(t, &fakeSvc{err: tc.err}), tc.method, tc.path, tc.body, idemKey, key8)
			wantStatus(t, rec, tc.status)
			wantCode(t, env, tc.code)
			if strings.Contains(rec.Body.String(), "secret") {
				t.Fatal("internal detail leaked")
			}
		})
	}
}

func TestP2_BadInput(t *testing.T) {
	gid, did, bid, aid := goalID.String(), debtID.String(), billID.String(), accID.String()
	cases := []struct {
		name, method, target, body string
		status                     int
	}{
		{"bad rate id", "GET", "/api/v1/exchange-rates/xyz", "", 404},
		{"rate bad date", "POST", "/api/v1/exchange-rates", `{"base":"USD","quote":"IDR","rate":"1","as_of":"01-09-2026"}`, 400},
		{"rate missing", "POST", "/api/v1/exchange-rates", `{"base":"USD"}`, 422},
		{"convert bad date", "GET", "/api/v1/exchange-rates/convert?amount=1&from=USD&date=x", "", 400},
		{"yearly bad year", "GET", "/api/v1/reports/yearly?year=abc", "", 400},
		{"audit bad entity", "GET", "/api/v1/audit-logs?entity=password", "", 400},
		{"audit bad entity id", "GET", "/api/v1/audit-logs?entity_id=x", "", 400},
		{"audit bad limit", "GET", "/api/v1/audit-logs?limit=x", "", 400},
		{"audit bad cursor", "GET", "/api/v1/audit-logs?cursor=zzz", "", 400},
		{"goal bad date", "POST", "/api/v1/savings-goals", `{"name":"a","target":"1","target_date":"x"}`, 400},
		{"goal bad account", "POST", "/api/v1/savings-goals", `{"name":"a","target":"1","account_id":"x"}`, 400},
		{"goal patch bad date", "PATCH", "/api/v1/savings-goals/" + gid, `{"target_date":"x"}`, 400},
		{"goal patch bad acc", "PATCH", "/api/v1/savings-goals/" + gid, `{"account_id":"x"}`, 400},
		{"goal patch bad if-match", "PATCH", "/api/v1/savings-goals/" + gid, `{}`, 400},
		{"goal bad id", "GET", "/api/v1/savings-goals/x/contributions", "", 404},
		{"contrib bad date", "POST", "/api/v1/savings-goals/" + gid + "/contributions", `{"amount":"1","date":"x"}`, 400},
		{"contrib bad json", "POST", "/api/v1/savings-goals/" + gid + "/contributions", `{`, 400},
		{"contrib bad cid", "DELETE", "/api/v1/savings-goals/" + gid + "/contributions/x", "", 404},
		{"debt bad dir", "POST", "/api/v1/debts", `{"direction":"x","counterparty":"a","principal":"1","start_date":"2026-01-01"}`, 422},
		{"debt bad start", "POST", "/api/v1/debts", `{"direction":"payable","counterparty":"a","principal":"1","start_date":"x"}`, 400},
		{"debt patch bad due", "PATCH", "/api/v1/debts/" + did, `{"due_date":"x"}`, 400},
		{"debt patch bad json", "PATCH", "/api/v1/debts/" + did, `[`, 400},
		{"pay debt bad acc", "POST", "/api/v1/debts/" + did + "/payments", `{"account_id":"x"}`, 422},
		{"pay debt bad date", "POST", "/api/v1/debts/" + did + "/payments", `{"date":"x"}`, 400},
		{"debt bad pid", "DELETE", "/api/v1/debts/" + did + "/payments/x", "", 404},
		{"debt bad id", "GET", "/api/v1/debts/x/payments", "", 404},
		{"bill bad due", "POST", "/api/v1/bills", `{"name":"a","amount":"1","frequency":"monthly","due_date":"x"}`, 400},
		{"bill bad cat", "POST", "/api/v1/bills", `{"name":"a","amount":"1","frequency":"monthly","due_date":"2026-01-01","category_id":"x"}`, 400},
		{"bill patch bad cat", "PATCH", "/api/v1/bills/" + bid, `{"category_id":"x"}`, 400},
		{"bill patch bad json", "PATCH", "/api/v1/bills/" + bid, `x`, 400},
		{"pay bill bad date", "POST", "/api/v1/bills/" + bid + "/pay", `{"date":"x"}`, 400},
		{"pay bill bad id", "POST", "/api/v1/bills/x/pay", `{}`, 404},
		{"member bad role", "POST", "/api/v1/accounts/" + aid + "/members", `{"email":"a@b.io","role":"owner"}`, 422},
		{"member bad acc", "GET", "/api/v1/accounts/x/members", "", 404},
		{"member bad mid", "PATCH", "/api/v1/accounts/" + aid + "/members/x", `{"role":"viewer"}`, 404},
		{"member remove bad mid", "DELETE", "/api/v1/accounts/" + aid + "/members/x", "", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := []string{idemKey, key8}
			if strings.Contains(tc.name, "if-match") {
				h = append(h, "If-Match", "abc")
			}
			rec, _ := do(t, server(t, &fakeSvc{}), tc.method, tc.target, tc.body, h...)
			wantStatus(t, rec, tc.status)
		})
	}
}

func TestP2_InputMapping(t *testing.T) {
	svc := &fakeSvc{}
	mux := server(t, svc)

	do(t, mux, "PATCH", "/api/v1/savings-goals/"+goalID.String(), `{"target_date":"","account_id":"`+acc2ID.String()+`"}`, "If-Match", `"3"`)
	g := svc.p2In.(app.UpdateGoalInput)
	if !g.ClearTargetDate || g.AccountID == nil || *g.AccountID != acc2ID || g.ClearAccount || *g.ExpectedVersion != 3 {
		t.Fatalf("goal input = %+v", g)
	}
	rec, env := do(t, mux, "POST", "/api/v1/savings-goals/"+goalID.String()+"/withdrawals", `{"amount":"500","date":"2026-09-02","note":"x"}`)
	c := svc.p2In.(app.ContributeInput)
	if !c.Withdraw || c.Amount != "500" || c.Date.Day() != 2 {
		t.Fatalf("contribute = %+v", c)
	}
	var cr ContributionResult
	_ = json.Unmarshal(env.Data, &cr)
	if cr.Goal.Saved != "250000" || cr.Goal.Remaining != "750000" || cr.Contribution.TransferID == nil || rec.Code != 201 {
		t.Fatalf("contribution body = %+v", cr)
	}

	_, env = do(t, mux, "POST", "/api/v1/debts/"+debtID.String()+"/payments",
		`{"transaction_id":"`+txID.String()+`"}`, idemKey, key8)
	p := svc.p2In.(app.PayDebtInput)
	if p.TransactionID == nil || *p.TransactionID != txID || !p.Date.IsZero() {
		t.Fatalf("pay = %+v", p)
	}
	if svc.gotIdem.Path != "/api/v1/debts/"+debtID.String()+"/payments" {
		t.Fatalf("idem path = %s", svc.gotIdem.Path)
	}
	var pr DebtPaymentResult
	_ = json.Unmarshal(env.Data, &pr)
	if pr.Debt.Remaining != "60000" || pr.Debt.Paid != "40000" {
		t.Fatalf("debt body = %+v", pr.Debt)
	}

	_, env = do(t, mux, "GET", "/api/v1/bills/"+billID.String(), "")
	var b BillResponse
	_ = json.Unmarshal(env.Data, &b)
	if !b.Overdue || b.NextDueDate != "2026-09-01" || b.AccountID == nil || b.CategoryID != nil {
		t.Fatalf("bill = %+v", b)
	}
	do(t, mux, "PATCH", "/api/v1/bills/"+billID.String(), `{"category_id":"","account_id":"`+acc2ID.String()+`"}`)
	ub := svc.p2In.(app.UpdateBillInput)
	if !ub.ClearCategory || ub.ClearAccount || *ub.AccountID != acc2ID {
		t.Fatalf("bill upd = %+v", ub)
	}
	_, env = do(t, mux, "POST", "/api/v1/bills/"+billID.String()+"/pay", `{}`, idemKey, key8)
	var pb PayBillResult
	_ = json.Unmarshal(env.Data, &pb)
	if pb.Transaction != nil {
		t.Fatal("no tx expected")
	}

	do(t, mux, "POST", "/api/v1/accounts/"+accID.String()+"/members", `{"email":"x@y.io","role":"viewer"}`)
	am := svc.p2In.(app.AddMemberInput)
	if am.Email != "x@y.io" || am.MemberID != nil || am.OwnerID != testUser || am.AccountID != accID {
		t.Fatalf("add member = %+v", am)
	}
	_, env = do(t, mux, "GET", "/api/v1/shared-accounts", "")
	var sh []SharedAccountResponse
	_ = json.Unmarshal(env.Data, &sh)
	if len(sh) != 1 || sh[0].Role != "editor" || sh[0].OwnerID != testUser.String() {
		t.Fatalf("shared = %+v", sh)
	}

	do(t, mux, "POST", "/api/v1/exchange-rates", `{"base":"usd","quote":"idr","rate":"16250","as_of":"2026-09-01"}`)
	if r := svc.p2In.(app.CreateRateInput); r.Base != "USD" || r.Quote != "IDR" {
		t.Fatalf("rate = %+v", r)
	}
	_, env = do(t, mux, "GET", "/api/v1/exchange-rates/convert?amount=1&from=usd&to=idr", "")
	var cv ConvertResponse
	_ = json.Unmarshal(env.Data, &cv)
	if cv.To.Amount != "16250" || svc.p2In.(app.ConvertInput).To != "IDR" {
		t.Fatalf("convert = %+v", cv)
	}
	_, env = do(t, mux, "GET", "/api/v1/reports/yearly?year=2025", "")
	var y YearlyResponse
	_ = json.Unmarshal(env.Data, &y)
	if y.Year != 2025 || len(y.Unconverted) != 1 || y.Months[0].Month != "2026-09" {
		t.Fatalf("yearly = %+v", y)
	}
}

func TestP2_AuditPagination(t *testing.T) {
	svc := &fakeSvc{audit: sampleAudit(3)}
	mux := server(t, svc)
	_, env := do(t, mux, "GET", "/api/v1/audit-logs?limit=2&entity=transaction", "")
	var logs []AuditLogResponse
	_ = json.Unmarshal(env.Data, &logs)
	if len(logs) != 2 || env.Meta == nil || !env.Meta.HasMore || string(logs[0].After) != "null" || logs[0].ActorID != peerID.String() {
		t.Fatalf("logs = %+v meta %+v", logs, env.Meta)
	}
	if f := svc.p2In.(domain.AuditFilter); f.Limit != 2 || f.Entity != "transaction" || f.After != nil {
		t.Fatalf("filter = %+v", f)
	}
	rec, _ := do(t, mux, "GET", "/api/v1/audit-logs?limit=2&entity=transaction&cursor="+env.Meta.NextCursor, "")
	wantStatus(t, rec, 200)
	if f := svc.p2In.(domain.AuditFilter); f.After == nil || f.After.ID != svc.audit[1].ID {
		t.Fatalf("cursor = %+v", f.After)
	}
	// cursor dari filter lain ditolak
	rec, env = do(t, mux, "GET", "/api/v1/audit-logs?limit=2&entity=account&cursor="+env.Meta.NextCursor, "")
	wantStatus(t, rec, 400)
	wantCode(t, env, "INVALID_CURSOR")
}
