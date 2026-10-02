package http

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/httpx"
)

// listBudgets godoc
//
//	@Summary		List budgets
//	@Description	Budgets of a month with progress. spent = expense of the category and its sub categories in the budget currency, computed on read.
//	@Tags			budgets
//	@Produce		json
//	@Security		BearerAuth
//	@Param			month	query		string	false	"Month (YYYY-MM), default current month in user timezone"
//	@Success		200		{object}	BudgetListEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Router			/budgets [get]
func (h *Handler) listBudgets(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	views, err := h.svc.ListBudgets(r.Context(), uid, r.URL.Query().Get("month"))
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	out := make([]BudgetResponse, 0, len(views))
	for _, v := range views {
		out = append(out, toBudgetResponse(v))
	}
	httpx.Data(w, http.StatusOK, out)
}

// createBudget godoc
//
//	@Summary		Create budget
//	@Description	Monthly budget for an expense category. Unique per (category, month). Requires Idempotency-Key.
//	@Tags			budgets
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			Idempotency-Key	header		string				true	"Unique key per logical request (8-128 chars)"
//	@Param			body			body		CreateBudgetRequest	true	"Budget"
//	@Success		201				{object}	BudgetEnvelope
//	@Failure		400				{object}	httpx.ErrorResponse	"INVALID_JSON / IDEMPOTENCY_KEY_REQUIRED"
//	@Failure		401				{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404				{object}	httpx.ErrorResponse	"CATEGORY_NOT_FOUND"
//	@Failure		409				{object}	httpx.ErrorResponse	"BUDGET_EXISTS / IDEMPOTENCY_IN_PROGRESS"
//	@Failure		422				{object}	httpx.ErrorResponse	"VALIDATION_FAILED / CATEGORY_TYPE_MISMATCH / INVALID_AMOUNT"
//	@Router			/budgets [post]
func (h *Handler) createBudget(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateBudgetRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	in := app.CreateBudgetInput{UserID: uid, Month: req.PeriodMonth, Amount: req.Amount,
		Currency: strings.ToUpper(req.Currency), Threshold: req.AlertThresholdPct}
	if in.CategoryID, err = uuid.Parse(req.CategoryID); err != nil {
		httpx.WriteError(w, r, invalidParam("category_id", "harus UUID"))
		return
	}
	h.idempotent(w, r, uid, "/api/v1/budgets", req, func(ctx context.Context) (int, any, error) {
		v, err := h.svc.CreateBudget(ctx, in)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, toBudgetResponse(*v), nil
	})
}

// getBudget godoc
//
//	@Summary	Get budget progress
//	@Tags		budgets
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Budget ID"	format(uuid)
//	@Success	200	{object}	BudgetEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"BUDGET_NOT_FOUND"
//	@Router		/budgets/{id} [get]
func (h *Handler) getBudget(w http.ResponseWriter, r *http.Request) {
	h.budgetAction(w, r, func(uid, id uuid.UUID) (*app.BudgetView, error) {
		return h.svc.GetBudget(r.Context(), uid, id)
	})
}

// updateBudget godoc
//
//	@Summary	Update budget
//	@Tags		budgets
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id			path		string				true	"Budget ID"	format(uuid)
//	@Param		If-Match	header		string				false	"Expected version"
//	@Param		body		body		UpdateBudgetRequest	true	"Fields to change"
//	@Success	200			{object}	BudgetEnvelope
//	@Failure	401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404			{object}	httpx.ErrorResponse	"BUDGET_NOT_FOUND"
//	@Failure	409			{object}	httpx.ErrorResponse	"VERSION_CONFLICT"
//	@Failure	422			{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_AMOUNT"
//	@Router		/budgets/{id} [patch]
func (h *Handler) updateBudget(w http.ResponseWriter, r *http.Request) {
	h.budgetAction(w, r, func(uid, id uuid.UUID) (*app.BudgetView, error) {
		ver, err := ifMatch(r)
		if err != nil {
			return nil, err
		}
		req, err := decodeAndValidate[UpdateBudgetRequest](h, w, r)
		if err != nil {
			return nil, err
		}
		return h.svc.UpdateBudget(r.Context(), app.UpdateBudgetInput{UserID: uid, ID: id, ExpectedVersion: ver,
			Amount: req.Amount, Threshold: req.AlertThresholdPct})
	})
}

// deleteBudget godoc
//
//	@Summary	Delete budget
//	@Tags		budgets
//	@Security	BearerAuth
//	@Param		id	path	string	true	"Budget ID"	format(uuid)
//	@Success	204
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"BUDGET_NOT_FOUND"
//	@Router		/budgets/{id} [delete]
func (h *Handler) deleteBudget(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrBudgetNotFound, h.svc.DeleteBudget)
}

func (h *Handler) budgetAction(w http.ResponseWriter, r *http.Request, fn func(uid, id uuid.UUID) (*app.BudgetView, error)) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrBudgetNotFound)
	if err == nil {
		var v *app.BudgetView
		if v, err = fn(uid, id); err == nil {
			setETag(w, v.Budget.Version())
			httpx.Data(w, http.StatusOK, toBudgetResponse(*v))
			return
		}
	}
	httpx.WriteError(w, r, mapError(err))
}
