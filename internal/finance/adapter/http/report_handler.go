package http

import (
	"net/http"
	"strings"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/platform/httpx"
)

// summary godoc
//
//	@Summary		Monthly dashboard
//	@Description	Total balance per currency, income vs expense per currency, expense breakdown by root category and daily cashflow for one month in the user's timezone. Transfers are excluded; transfer fees count as expense.
//	@Tags			reports
//	@Produce		json
//	@Security		BearerAuth
//	@Param			month	query		string	false	"Month YYYY-MM (default: current month in user's timezone)"
//	@Success		200		{object}	SummaryEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Router			/reports/summary [get]
func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	s, err := h.svc.Summary(r.Context(), uid, r.URL.Query().Get("month"))
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toSummaryResponse(s))
}

// cashflow godoc
//
//	@Summary		Cashflow series
//	@Description	Income/expense/net per day or month, including empty periods. Defaults: current month (day) or last 12 months (month). Max 366 days / 120 months.
//	@Tags			reports
//	@Produce		json
//	@Security		BearerAuth
//	@Param			from		query		string	false	"From date (YYYY-MM-DD)"			format(date)
//	@Param			to			query		string	false	"To date (YYYY-MM-DD), inclusive"	format(date)
//	@Param			granularity	query		string	false	"Granularity (default month)"		Enums(day, month)
//	@Param			currency	query		string	false	"Currency (default base currency)"
//	@Success		200			{object}	CashflowEnvelope
//	@Failure		400			{object}	httpx.ErrorResponse	"INVALID_PARAMETER / INVALID_RANGE"
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		422			{object}	httpx.ErrorResponse	"VALIDATION_FAILED / UNSUPPORTED_CURRENCY"
//	@Router			/reports/cashflow [get]
func (h *Handler) cashflow(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	in := app.CashflowInput{UserID: uid, Granularity: r.URL.Query().Get("granularity"),
		Currency: strings.ToUpper(r.URL.Query().Get("currency"))}
	var err error
	if in.From, err = queryDate(r, "from"); err == nil {
		in.To, err = queryDate(r, "to")
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items, cur, err := h.svc.Cashflow(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	g := in.Granularity
	if g == "" {
		g = "month"
	}
	httpx.Data(w, http.StatusOK, CashflowResponse{Currency: cur.Code(), Granularity: g, Points: toCashflow(items)})
}

// categoryReport godoc
//
//	@Summary		Category breakdown
//	@Description	Totals per root category (sub categories rolled up) with percentage share. Default range: current month to date.
//	@Tags			reports
//	@Produce		json
//	@Security		BearerAuth
//	@Param			from		query		string	false	"From date (YYYY-MM-DD)"			format(date)
//	@Param			to			query		string	false	"To date (YYYY-MM-DD), inclusive"	format(date)
//	@Param			type		query		string	false	"Type (default expense)"			Enums(income, expense)
//	@Param			currency	query		string	false	"Currency (default base currency)"
//	@Success		200			{object}	CategoryReportEnvelope
//	@Failure		400			{object}	httpx.ErrorResponse	"INVALID_PARAMETER / INVALID_RANGE"
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		422			{object}	httpx.ErrorResponse	"INVALID_TYPE / UNSUPPORTED_CURRENCY"
//	@Router			/reports/categories [get]
func (h *Handler) categoryReport(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	in := app.CategoryReportInput{UserID: uid, Type: r.URL.Query().Get("type"),
		Currency: strings.ToUpper(r.URL.Query().Get("currency"))}
	var err error
	if in.From, err = queryDate(r, "from"); err == nil {
		in.To, err = queryDate(r, "to")
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items, cur, err := h.svc.CategoryReport(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	typ := in.Type
	if typ == "" {
		typ = "expense"
	}
	httpx.Data(w, http.StatusOK, CategoryReportResponse{Currency: cur.Code(), Type: typ, Items: toShares(items)})
}

// reconciliation godoc
//
//	@Summary		Balance reconciliation
//	@Description	Recomputes every account balance from its history and reports accounts whose cached balance differs. Read-only: nothing is fixed automatically.
//	@Tags			reports
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	ReconciliationEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router			/reports/reconciliation [get]
func (h *Handler) reconciliation(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	drifts, err := h.svc.ReconcileBalances(r.Context(), &uid)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toReconciliation(drifts))
}
