package http

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/shared/pagination"
)

// writeItem menulis satu resource (200 + ETag bila version > 0) atau error.
func writeItem(w http.ResponseWriter, r *http.Request, status, version int, body any, err error) {
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	if version > 0 {
		setETag(w, version)
	}
	httpx.Data(w, status, body)
}

func writeList[T, R any](w http.ResponseWriter, r *http.Request, items []T, err error, conv func(T) R) {
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	out := make([]R, 0, len(items))
	for _, x := range items {
		out = append(out, conv(x))
	}
	httpx.Data(w, http.StatusOK, out)
}

// pathUUID mem-parsing path value; tidak valid = notFound (404).
func pathUUID(r *http.Request, name string, notFound error) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, notFound
	}
	return id, nil
}

// optionalUUID: "" = tidak diisi.
func optionalUUID(field, s string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // kosong = tidak diisi
	}
	return parseUUIDPtr(field, &s)
}

// clearableUUID: nil = tetap, "" = hapus, selain itu UUID baru.
func clearableUUID(field string, s *string) (id *uuid.UUID, unset bool, err error) {
	if s == nil {
		return nil, false, nil
	}
	if *s == "" {
		return nil, true, nil
	}
	id, err = parseUUIDPtr(field, s)
	return id, false, err
}

// clearableDate: nil = tetap, "" = hapus, selain itu tanggal baru.
func clearableDate(field string, s *string) (d *time.Time, unset bool, err error) {
	if s == nil {
		return nil, false, nil
	}
	if *s == "" {
		return nil, true, nil
	}
	d, err = parseDatePtr(field, s)
	return d, false, err
}

// optionalDate: "" = zero time (service memakai default / validasi).
func optionalDate(field, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return parseDate(field, s)
}

// ===== Exchange rates =====

// listRates godoc
//
//	@Summary	List exchange rates
//	@Tags		exchange-rates
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	RateListEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router		/exchange-rates [get]
func (h *Handler) listRates(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	rates, err := h.svc.ListRates(r.Context(), uid)
	writeList(w, r, rates, err, toRateResponse)
}

// createRate godoc
//
//	@Summary		Create exchange rate
//	@Description	Manual rate: 1 base = rate quote, valid from as_of. The inverse pair is derived automatically when missing.
//	@Tags			exchange-rates
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		CreateRateRequest	true	"Rate"
//	@Success		201		{object}	RateEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		409		{object}	httpx.ErrorResponse	"RATE_EXISTS"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / UNSUPPORTED_CURRENCY"
//	@Router			/exchange-rates [post]
func (h *Handler) createRate(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateRateRequest](h, w, r)
	var rate *domain.ExchangeRate
	if err == nil {
		var asOf time.Time
		if asOf, err = parseDate("as_of", req.AsOf); err == nil {
			rate, err = h.svc.CreateRate(r.Context(), app.CreateRateInput{UserID: uid, Base: strings.ToUpper(req.Base),
				Quote: strings.ToUpper(req.Quote), Rate: req.Rate, AsOf: asOf})
		}
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusCreated, toRateResponse(rate))
}

// getRate godoc
//
//	@Summary	Get exchange rate
//	@Tags		exchange-rates
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Rate ID"	format(uuid)
//	@Success	200	{object}	RateEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"RATE_NOT_FOUND"
//	@Router		/exchange-rates/{id} [get]
func (h *Handler) getRate(w http.ResponseWriter, r *http.Request) {
	h.rateAction(w, r, func(uid, id uuid.UUID) (*domain.ExchangeRate, error) {
		return h.svc.GetRate(r.Context(), uid, id)
	})
}

// updateRate godoc
//
//	@Summary	Update exchange rate value
//	@Tags		exchange-rates
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id		path		string				true	"Rate ID"	format(uuid)
//	@Param		body	body		UpdateRateRequest	true	"New rate"
//	@Success	200		{object}	RateEnvelope
//	@Failure	401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404		{object}	httpx.ErrorResponse	"RATE_NOT_FOUND"
//	@Failure	422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Router		/exchange-rates/{id} [patch]
func (h *Handler) updateRate(w http.ResponseWriter, r *http.Request) {
	h.rateAction(w, r, func(uid, id uuid.UUID) (*domain.ExchangeRate, error) {
		req, err := decodeAndValidate[UpdateRateRequest](h, w, r)
		if err != nil {
			return nil, err
		}
		return h.svc.UpdateRate(r.Context(), uid, id, req.Rate)
	})
}

// deleteRate godoc
//
//	@Summary	Delete exchange rate
//	@Tags		exchange-rates
//	@Security	BearerAuth
//	@Param		id	path	string	true	"Rate ID"	format(uuid)
//	@Success	204
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"RATE_NOT_FOUND"
//	@Router		/exchange-rates/{id} [delete]
func (h *Handler) deleteRate(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrRateNotFound, h.svc.DeleteRate)
}

func (h *Handler) rateAction(w http.ResponseWriter, r *http.Request, fn func(uid, id uuid.UUID) (*domain.ExchangeRate, error)) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrRateNotFound)
	var rate *domain.ExchangeRate
	if err == nil {
		rate, err = fn(uid, id)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toRateResponse(rate))
}

// convert godoc
//
//	@Summary		Convert amount
//	@Description	Converts using the latest rate with as_of <= date (direct, inverse, or via base currency).
//	@Tags			exchange-rates
//	@Produce		json
//	@Security		BearerAuth
//	@Param			amount	query		string	true	"Amount in major units"	example(100)
//	@Param			from	query		string	true	"Source currency"		example(USD)
//	@Param			to		query		string	false	"Target currency, default base currency"
//	@Param			date	query		string	false	"Rate date (YYYY-MM-DD), default today"
//	@Success		200		{object}	ConvertEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_PARAMETER"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		422		{object}	httpx.ErrorResponse	"RATE_UNAVAILABLE / VALIDATION_FAILED"
//	@Router			/exchange-rates/convert [get]
func (h *Handler) convert(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	date, err := queryDate(r, "date")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	from, to, err := h.svc.Convert(r.Context(), app.ConvertInput{UserID: uid, Amount: q.Get("amount"),
		From: strings.ToUpper(q.Get("from")), To: strings.ToUpper(q.Get("to")), Date: date})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, ConvertResponse{From: toMoneyResponse(from), To: toMoneyResponse(to)})
}

// ===== Savings goals =====

// listGoals godoc
//
//	@Summary	List savings goals
//	@Tags		savings-goals
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	GoalListEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router		/savings-goals [get]
func (h *Handler) listGoals(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	goals, err := h.svc.ListGoals(r.Context(), uid)
	writeList(w, r, goals, err, func(g *app.Goal) GoalResponse { return toGoalResponse(*g) })
}

// createGoal godoc
//
//	@Summary	Create savings goal
//	@Tags		savings-goals
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		CreateGoalRequest	true	"Goal"
//	@Success	201		{object}	GoalEnvelope
//	@Failure	401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404		{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND"
//	@Failure	409		{object}	httpx.ErrorResponse	"DUPLICATE_NAME"
//	@Failure	422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_AMOUNT"
//	@Router		/savings-goals [post]
func (h *Handler) createGoal(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateGoalRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	in := app.CreateGoalInput{UserID: uid, Name: req.Name, Target: req.Target, Currency: strings.ToUpper(req.Currency)}
	if in.TargetDate, err = parseDatePtr("target_date", req.TargetDate); err == nil {
		in.AccountID, err = parseUUIDPtr("account_id", req.AccountID)
	}
	var g *app.Goal
	if err == nil {
		g, err = h.svc.CreateGoal(r.Context(), in)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	setETag(w, g.Goal.Version())
	httpx.Data(w, http.StatusCreated, toGoalResponse(*g))
}

// getGoal godoc
//
//	@Summary	Get savings goal progress
//	@Tags		savings-goals
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Goal ID"	format(uuid)
//	@Success	200	{object}	GoalEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"GOAL_NOT_FOUND"
//	@Router		/savings-goals/{id} [get]
func (h *Handler) getGoal(w http.ResponseWriter, r *http.Request) {
	h.goalAction(w, r, func(uid, id uuid.UUID) (*app.Goal, error) { return h.svc.GetGoal(r.Context(), uid, id) })
}

// updateGoal godoc
//
//	@Summary		Update savings goal
//	@Description	Empty string on target_date/account_id clears the value. archived=true archives the goal.
//	@Tags			savings-goals
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id			path		string				true	"Goal ID"	format(uuid)
//	@Param			If-Match	header		string				false	"Expected version"
//	@Param			body		body		UpdateGoalRequest	true	"Fields to change"
//	@Success		200			{object}	GoalEnvelope
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404			{object}	httpx.ErrorResponse	"GOAL_NOT_FOUND / ACCOUNT_NOT_FOUND"
//	@Failure		409			{object}	httpx.ErrorResponse	"VERSION_CONFLICT / DUPLICATE_NAME"
//	@Failure		422			{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Router			/savings-goals/{id} [patch]
func (h *Handler) updateGoal(w http.ResponseWriter, r *http.Request) {
	h.goalAction(w, r, func(uid, id uuid.UUID) (*app.Goal, error) {
		ver, err := ifMatch(r)
		if err != nil {
			return nil, err
		}
		req, err := decodeAndValidate[UpdateGoalRequest](h, w, r)
		if err != nil {
			return nil, err
		}
		in := app.UpdateGoalInput{UserID: uid, ID: id, ExpectedVersion: ver, Name: req.Name, Target: req.Target, Archived: req.Archived}
		if in.TargetDate, in.ClearTargetDate, err = clearableDate("target_date", req.TargetDate); err != nil {
			return nil, err
		}
		if in.AccountID, in.ClearAccount, err = clearableUUID("account_id", req.AccountID); err != nil {
			return nil, err
		}
		return h.svc.UpdateGoal(r.Context(), in)
	})
}

// deleteGoal godoc
//
//	@Summary	Delete savings goal
//	@Tags		savings-goals
//	@Security	BearerAuth
//	@Param		id	path	string	true	"Goal ID"	format(uuid)
//	@Success	204
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"GOAL_NOT_FOUND"
//	@Router		/savings-goals/{id} [delete]
func (h *Handler) deleteGoal(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrGoalNotFound, h.svc.DeleteGoal)
}

func (h *Handler) goalAction(w http.ResponseWriter, r *http.Request, fn func(uid, id uuid.UUID) (*app.Goal, error)) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrGoalNotFound)
	var g *app.Goal
	if err == nil {
		g, err = fn(uid, id)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	writeItem(w, r, http.StatusOK, g.Goal.Version(), toGoalResponse(*g), nil)
}

// listContributions godoc
//
//	@Summary	List goal contributions
//	@Tags		savings-goals
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Goal ID"	format(uuid)
//	@Success	200	{object}	ContributionListEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"GOAL_NOT_FOUND"
//	@Router		/savings-goals/{id}/contributions [get]
func (h *Handler) listContributions(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrGoalNotFound)
	var cs []*domain.GoalContribution
	if err == nil {
		cs, err = h.svc.ListContributions(r.Context(), uid, id)
	}
	writeList(w, r, cs, err, toContributionResponse)
}

// contribute godoc
//
//	@Summary		Add contribution
//	@Description	Links a transfer into the goal account (amount taken from the transfer) or records a standalone deposit (amount + date). Status becomes achieved once saved >= target.
//	@Tags			savings-goals
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		string				true	"Goal ID"	format(uuid)
//	@Param			body	body		ContributeRequest	true	"Contribution"
//	@Success		201		{object}	ContributionEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404		{object}	httpx.ErrorResponse	"GOAL_NOT_FOUND / TRANSFER_NOT_FOUND"
//	@Failure		409		{object}	httpx.ErrorResponse	"DUPLICATE_LINK"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / GOAL_ARCHIVED"
//	@Router			/savings-goals/{id}/contributions [post]
func (h *Handler) contribute(w http.ResponseWriter, r *http.Request) { h.contribution(w, r, false) }

// withdraw godoc
//
//	@Summary		Withdraw from goal
//	@Description	Records a withdrawal (negative contribution). Linking a transfer out of the goal account is supported too.
//	@Tags			savings-goals
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		string				true	"Goal ID"	format(uuid)
//	@Param			body	body		ContributeRequest	true	"Withdrawal"
//	@Success		201		{object}	ContributionEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404		{object}	httpx.ErrorResponse	"GOAL_NOT_FOUND / TRANSFER_NOT_FOUND"
//	@Failure		409		{object}	httpx.ErrorResponse	"DUPLICATE_LINK"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / GOAL_ARCHIVED"
//	@Router			/savings-goals/{id}/withdrawals [post]
func (h *Handler) withdraw(w http.ResponseWriter, r *http.Request) { h.contribution(w, r, true) }

func (h *Handler) contribution(w http.ResponseWriter, r *http.Request, withdraw bool) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrGoalNotFound)
	var req ContributeRequest
	if err == nil {
		req, err = decodeAndValidate[ContributeRequest](h, w, r)
	}
	in := app.ContributeInput{UserID: uid, GoalID: id, Amount: req.Amount, Withdraw: withdraw, Note: req.Note}
	if err == nil {
		if in.Date, err = optionalDate("date", req.Date); err == nil {
			in.TransferID, err = optionalUUID("transfer_id", req.TransferID)
		}
	}
	var c *domain.GoalContribution
	var g *app.Goal
	if err == nil {
		c, g, err = h.svc.Contribute(r.Context(), in)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusCreated, ContributionResult{Contribution: toContributionResponse(c), Goal: toGoalResponse(*g)})
}

// deleteContribution godoc
//
//	@Summary	Delete contribution
//	@Tags		savings-goals
//	@Security	BearerAuth
//	@Param		id	path	string	true	"Goal ID"			format(uuid)
//	@Param		cid	path	string	true	"Contribution ID"	format(uuid)
//	@Success	204
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"GOAL_NOT_FOUND / CONTRIBUTION_NOT_FOUND"
//	@Router		/savings-goals/{id}/contributions/{cid} [delete]
func (h *Handler) deleteContribution(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrGoalNotFound, func(ctx context.Context, uid, id uuid.UUID) error {
		cid, err := pathUUID(r, "cid", domain.ErrContributionNotFound)
		if err != nil {
			return err
		}
		return h.svc.DeleteContribution(ctx, uid, id, cid)
	})
}

// ===== Debts =====

// listDebts godoc
//
//	@Summary	List debts and receivables
//	@Tags		debts
//	@Produce	json
//	@Security	BearerAuth
//	@Param		status	query		string	false	"Filter status"	Enums(open, settled)
//	@Success	200		{object}	DebtListEnvelope
//	@Failure	401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Router		/debts [get]
func (h *Handler) listDebts(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	debts, err := h.svc.ListDebts(r.Context(), uid, r.URL.Query().Get("status"))
	writeList(w, r, debts, err, func(d *app.Debt) DebtResponse { return toDebtResponse(*d) })
}

// createDebt godoc
//
//	@Summary	Create debt or receivable
//	@Tags		debts
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		CreateDebtRequest	true	"Debt"
//	@Success	201		{object}	DebtEnvelope
//	@Failure	401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_AMOUNT"
//	@Router		/debts [post]
func (h *Handler) createDebt(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateDebtRequest](h, w, r)
	in := app.CreateDebtInput{UserID: uid, Direction: req.Direction, Counterparty: req.Counterparty, Principal: req.Principal,
		Currency: strings.ToUpper(req.Currency), Note: req.Note}
	if err == nil {
		if in.StartDate, err = parseDate("start_date", req.StartDate); err == nil {
			in.DueDate, err = parseDatePtr("due_date", req.DueDate)
		}
	}
	var d *app.Debt
	if err == nil {
		d, err = h.svc.CreateDebt(r.Context(), in)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	writeItem(w, r, http.StatusCreated, d.Debt.Version(), toDebtResponse(*d), nil)
}

// getDebt godoc
//
//	@Summary	Get debt
//	@Tags		debts
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Debt ID"	format(uuid)
//	@Success	200	{object}	DebtEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"DEBT_NOT_FOUND"
//	@Router		/debts/{id} [get]
func (h *Handler) getDebt(w http.ResponseWriter, r *http.Request) {
	h.debtAction(w, r, func(uid, id uuid.UUID) (*app.Debt, error) { return h.svc.GetDebt(r.Context(), uid, id) })
}

// updateDebt godoc
//
//	@Summary		Update debt
//	@Description	due_date "" clears the due date. Changing principal re-evaluates settled status.
//	@Tags			debts
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id			path		string				true	"Debt ID"	format(uuid)
//	@Param			If-Match	header		string				false	"Expected version"
//	@Param			body		body		UpdateDebtRequest	true	"Fields to change"
//	@Success		200			{object}	DebtEnvelope
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404			{object}	httpx.ErrorResponse	"DEBT_NOT_FOUND"
//	@Failure		409			{object}	httpx.ErrorResponse	"VERSION_CONFLICT"
//	@Failure		422			{object}	httpx.ErrorResponse	"VALIDATION_FAILED / OVERPAYMENT"
//	@Router			/debts/{id} [patch]
func (h *Handler) updateDebt(w http.ResponseWriter, r *http.Request) {
	h.debtAction(w, r, func(uid, id uuid.UUID) (*app.Debt, error) {
		ver, err := ifMatch(r)
		if err != nil {
			return nil, err
		}
		req, err := decodeAndValidate[UpdateDebtRequest](h, w, r)
		if err != nil {
			return nil, err
		}
		in := app.UpdateDebtInput{UserID: uid, ID: id, ExpectedVersion: ver, Counterparty: req.Counterparty,
			Principal: req.Principal, Note: req.Note}
		if in.DueDate, in.ClearDueDate, err = clearableDate("due_date", req.DueDate); err != nil {
			return nil, err
		}
		return h.svc.UpdateDebt(r.Context(), in)
	})
}

// deleteDebt godoc
//
//	@Summary	Delete debt
//	@Tags		debts
//	@Security	BearerAuth
//	@Param		id	path	string	true	"Debt ID"	format(uuid)
//	@Success	204
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"DEBT_NOT_FOUND"
//	@Router		/debts/{id} [delete]
func (h *Handler) deleteDebt(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrDebtNotFound, h.svc.DeleteDebt)
}

func (h *Handler) debtAction(w http.ResponseWriter, r *http.Request, fn func(uid, id uuid.UUID) (*app.Debt, error)) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrDebtNotFound)
	var d *app.Debt
	if err == nil {
		d, err = fn(uid, id)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	writeItem(w, r, http.StatusOK, d.Debt.Version(), toDebtResponse(*d), nil)
}

// listDebtPayments godoc
//
//	@Summary	List debt payments
//	@Tags		debts
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Debt ID"	format(uuid)
//	@Success	200	{object}	DebtPaymentListEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"DEBT_NOT_FOUND"
//	@Router		/debts/{id}/payments [get]
func (h *Handler) listDebtPayments(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrDebtNotFound)
	var ps []*domain.DebtPayment
	if err == nil {
		ps, err = h.svc.ListDebtPayments(r.Context(), uid, id)
	}
	writeList(w, r, ps, err, toDebtPaymentResponse)
}

// payDebt godoc
//
//	@Summary		Record debt payment
//	@Description	Partial payments allowed; status becomes settled when paid = principal. transaction_id links an existing transaction; account_id (+ category_id) creates one (expense for payable, income for receivable). Requires Idempotency-Key.
//	@Tags			debts
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id				path		string			true	"Debt ID"	format(uuid)
//	@Param			Idempotency-Key	header		string			true	"Unique key per logical request (8-128 chars)"
//	@Param			body			body		PayDebtRequest	true	"Payment"
//	@Success		201				{object}	DebtPaymentEnvelope
//	@Failure		400				{object}	httpx.ErrorResponse	"IDEMPOTENCY_KEY_REQUIRED"
//	@Failure		401				{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404				{object}	httpx.ErrorResponse	"DEBT_NOT_FOUND / ACCOUNT_NOT_FOUND / TRANSACTION_NOT_FOUND"
//	@Failure		409				{object}	httpx.ErrorResponse	"DUPLICATE_LINK"
//	@Failure		422				{object}	httpx.ErrorResponse	"OVERPAYMENT / DEBT_SETTLED / VALIDATION_FAILED"
//	@Router			/debts/{id}/payments [post]
func (h *Handler) payDebt(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrDebtNotFound)
	var req PayDebtRequest
	if err == nil {
		req, err = decodeAndValidate[PayDebtRequest](h, w, r)
	}
	in := app.PayDebtInput{UserID: uid, DebtID: id, Amount: req.Amount, Note: req.Note}
	if err == nil {
		err = firstErr(
			func() (e error) { in.Date, e = optionalDate("date", req.Date); return },
			func() (e error) { in.TransactionID, e = optionalUUID("transaction_id", req.TransactionID); return },
			func() (e error) { in.AccountID, e = optionalUUID("account_id", req.AccountID); return },
			func() (e error) { in.CategoryID, e = optionalUUID("category_id", req.CategoryID); return },
		)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	h.idempotent(w, r, uid, r.URL.Path, req, func(ctx context.Context) (int, any, error) {
		p, d, err := h.svc.PayDebt(ctx, in)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, DebtPaymentResult{Payment: toDebtPaymentResponse(p), Debt: toDebtResponse(*d)}, nil
	})
}

// deleteDebtPayment godoc
//
//	@Summary		Delete debt payment
//	@Description	The linked transaction (if any) is kept; delete it separately.
//	@Tags			debts
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Debt ID"		format(uuid)
//	@Param			pid	path	string	true	"Payment ID"	format(uuid)
//	@Success		204
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404	{object}	httpx.ErrorResponse	"DEBT_NOT_FOUND"
//	@Router			/debts/{id}/payments/{pid} [delete]
func (h *Handler) deleteDebtPayment(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrDebtNotFound, func(ctx context.Context, uid, id uuid.UUID) error {
		pid, err := pathUUID(r, "pid", domain.ErrDebtNotFound)
		if err != nil {
			return err
		}
		return h.svc.DeleteDebtPayment(ctx, uid, id, pid)
	})
}

func firstErr(fns ...func() error) error {
	for _, fn := range fns {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

// ===== Bills =====

// listBills godoc
//
//	@Summary	List bills
//	@Tags		bills
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	BillListEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router		/bills [get]
func (h *Handler) listBills(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	bills, err := h.svc.ListBills(r.Context(), uid)
	writeList(w, r, bills, err, toBillResponse)
}

// createBill godoc
//
//	@Summary		Create bill
//	@Description	Recurring bill reminder. The worker sends due_soon remind_days_before the due date and overdue once past it.
//	@Tags			bills
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		CreateBillRequest	true	"Bill"
//	@Success		201		{object}	BillEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404		{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND / CATEGORY_NOT_FOUND"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_FREQUENCY"
//	@Router			/bills [post]
func (h *Handler) createBill(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateBillRequest](h, w, r)
	in := app.CreateBillInput{UserID: uid, Name: req.Name, Amount: req.Amount, Currency: strings.ToUpper(req.Currency),
		Frequency: req.Frequency, RemindDays: req.RemindDaysBefore}
	if err == nil {
		err = firstErr(
			func() (e error) { in.DueDate, e = parseDate("due_date", req.DueDate); return },
			func() (e error) { in.AccountID, e = parseUUIDPtr("account_id", req.AccountID); return },
			func() (e error) { in.CategoryID, e = parseUUIDPtr("category_id", req.CategoryID); return },
		)
	}
	var b *domain.Bill
	if err == nil {
		b, err = h.svc.CreateBill(r.Context(), in)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	writeItem(w, r, http.StatusCreated, b.Version(), toBillResponse(b), nil)
}

// getBill godoc
//
//	@Summary	Get bill
//	@Tags		bills
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Bill ID"	format(uuid)
//	@Success	200	{object}	BillEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"BILL_NOT_FOUND"
//	@Router		/bills/{id} [get]
func (h *Handler) getBill(w http.ResponseWriter, r *http.Request) {
	h.billAction(w, r, func(uid, id uuid.UUID) (*domain.Bill, error) { return h.svc.GetBill(r.Context(), uid, id) })
}

// updateBill godoc
//
//	@Summary		Update bill
//	@Description	"" on account_id/category_id clears the default. paused=true pauses reminders.
//	@Tags			bills
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id			path		string				true	"Bill ID"	format(uuid)
//	@Param			If-Match	header		string				false	"Expected version"
//	@Param			body		body		UpdateBillRequest	true	"Fields to change"
//	@Success		200			{object}	BillEnvelope
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404			{object}	httpx.ErrorResponse	"BILL_NOT_FOUND"
//	@Failure		409			{object}	httpx.ErrorResponse	"VERSION_CONFLICT"
//	@Failure		422			{object}	httpx.ErrorResponse	"VALIDATION_FAILED / BILL_DONE"
//	@Router			/bills/{id} [patch]
func (h *Handler) updateBill(w http.ResponseWriter, r *http.Request) {
	h.billAction(w, r, func(uid, id uuid.UUID) (*domain.Bill, error) {
		ver, err := ifMatch(r)
		if err != nil {
			return nil, err
		}
		req, err := decodeAndValidate[UpdateBillRequest](h, w, r)
		if err != nil {
			return nil, err
		}
		in := app.UpdateBillInput{UserID: uid, ID: id, ExpectedVersion: ver, Name: req.Name, Amount: req.Amount,
			Frequency: req.Frequency, RemindDays: req.RemindDaysBefore, Paused: req.Paused}
		err = firstErr(
			func() (e error) {
				in.AccountID, in.ClearAccount, e = clearableUUID("account_id", req.AccountID)
				return
			},
			func() (e error) {
				in.CategoryID, in.ClearCategory, e = clearableUUID("category_id", req.CategoryID)
				return
			},
			func() (e error) { in.DueDate, e = parseDatePtr("due_date", req.DueDate); return },
		)
		if err != nil {
			return nil, err
		}
		return h.svc.UpdateBill(r.Context(), in)
	})
}

// deleteBill godoc
//
//	@Summary	Delete bill
//	@Tags		bills
//	@Security	BearerAuth
//	@Param		id	path	string	true	"Bill ID"	format(uuid)
//	@Success	204
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"BILL_NOT_FOUND"
//	@Router		/bills/{id} [delete]
func (h *Handler) deleteBill(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrBillNotFound, h.svc.DeleteBill)
}

func (h *Handler) billAction(w http.ResponseWriter, r *http.Request, fn func(uid, id uuid.UUID) (*domain.Bill, error)) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrBillNotFound)
	var b *domain.Bill
	if err == nil {
		b, err = fn(uid, id)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	writeItem(w, r, http.StatusOK, b.Version(), toBillResponse(b), nil)
}

// payBill godoc
//
//	@Summary		Mark bill paid
//	@Description	Advances next_due_date by the frequency (once = done). create_transaction=true records an expense. Requires Idempotency-Key.
//	@Tags			bills
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id				path		string			true	"Bill ID"	format(uuid)
//	@Param			Idempotency-Key	header		string			true	"Unique key per logical request (8-128 chars)"
//	@Param			body			body		PayBillRequest	false	"Payment options"
//	@Success		200				{object}	PayBillEnvelope
//	@Failure		400				{object}	httpx.ErrorResponse	"IDEMPOTENCY_KEY_REQUIRED"
//	@Failure		401				{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404				{object}	httpx.ErrorResponse	"BILL_NOT_FOUND / ACCOUNT_NOT_FOUND"
//	@Failure		422				{object}	httpx.ErrorResponse	"BILL_DONE / VALIDATION_FAILED / INSUFFICIENT_BALANCE"
//	@Router			/bills/{id}/pay [post]
func (h *Handler) payBill(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrBillNotFound)
	var req PayBillRequest
	if err == nil && r.ContentLength != 0 {
		req, err = decodeAndValidate[PayBillRequest](h, w, r)
	}
	in := app.PayBillInput{UserID: uid, ID: id, CreateTransaction: req.CreateTransaction, Amount: req.Amount, Note: req.Note}
	if err == nil {
		err = firstErr(
			func() (e error) { in.AccountID, e = optionalUUID("account_id", req.AccountID); return },
			func() (e error) { in.CategoryID, e = optionalUUID("category_id", req.CategoryID); return },
			func() (e error) { in.Date, e = parseDatePtr("date", req.Date); return },
		)
	}
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	h.idempotent(w, r, uid, r.URL.Path, req, func(ctx context.Context) (int, any, error) {
		b, tx, err := h.svc.PayBill(ctx, in)
		if err != nil {
			return 0, nil, err
		}
		out := PayBillResult{Bill: toBillResponse(b)}
		if tx != nil {
			t := toTransactionResponse(tx)
			out.Transaction = &t
		}
		return http.StatusOK, out, nil
	})
}

// ===== Shared wallets =====

// listMembers godoc
//
//	@Summary		List account members
//	@Description	Any member may list; non-members get 404.
//	@Tags			account-members
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string	true	"Account ID"	format(uuid)
//	@Success		200	{object}	MemberListEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404	{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND"
//	@Router			/accounts/{id}/members [get]
func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrAccountNotFound)
	var ms []*domain.AccountMember
	if err == nil {
		ms, err = h.svc.ListMembers(r.Context(), uid, id)
	}
	writeList(w, r, ms, err, toMemberResponse)
}

// addMember godoc
//
//	@Summary		Share account
//	@Description	Owner only. Invite by user_id or email. viewer = read-only, editor = may record transactions/transfers on the account.
//	@Tags			account-members
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		string				true	"Account ID"	format(uuid)
//	@Param			body	body		AddMemberRequest	true	"Member"
//	@Success		201		{object}	MemberEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		403		{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Failure		404		{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND / USER_NOT_FOUND"
//	@Failure		409		{object}	httpx.ErrorResponse	"MEMBER_EXISTS"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_ROLE"
//	@Router			/accounts/{id}/members [post]
func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrAccountNotFound)
	var req AddMemberRequest
	if err == nil {
		req, err = decodeAndValidate[AddMemberRequest](h, w, r)
	}
	in := app.AddMemberInput{OwnerID: uid, AccountID: id, Email: req.Email, Role: req.Role}
	if err == nil {
		in.MemberID, err = optionalUUID("user_id", req.UserID)
	}
	var m *domain.AccountMember
	if err == nil {
		m, err = h.svc.AddMember(r.Context(), in)
	}
	writeItem(w, r, http.StatusCreated, 0, memberBody(m), err)
}

// updateMember godoc
//
//	@Summary	Change member role
//	@Tags		account-members
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id			path		string				true	"Account ID"		format(uuid)
//	@Param		member_id	path		string				true	"Member user ID"	format(uuid)
//	@Param		body		body		UpdateMemberRequest	true	"Role"
//	@Success	200			{object}	MemberEnvelope
//	@Failure	401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	403			{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Failure	404			{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND / MEMBER_NOT_FOUND"
//	@Failure	422			{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Router		/accounts/{id}/members/{member_id} [patch]
func (h *Handler) updateMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrAccountNotFound)
	var mid uuid.UUID
	if err == nil {
		mid, err = pathUUID(r, "member_id", domain.ErrMemberNotFound)
	}
	var req UpdateMemberRequest
	if err == nil {
		req, err = decodeAndValidate[UpdateMemberRequest](h, w, r)
	}
	var m *domain.AccountMember
	if err == nil {
		m, err = h.svc.UpdateMemberRole(r.Context(), uid, id, mid, req.Role)
	}
	writeItem(w, r, http.StatusOK, 0, memberBody(m), err)
}

// removeMember godoc
//
//	@Summary		Remove member
//	@Description	Owner removes any member; a member may remove itself (leave).
//	@Tags			account-members
//	@Security		BearerAuth
//	@Param			id			path	string	true	"Account ID"		format(uuid)
//	@Param			member_id	path	string	true	"Member user ID"	format(uuid)
//	@Success		204
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		403	{object}	httpx.ErrorResponse	"FORBIDDEN"
//	@Failure		404	{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND / MEMBER_NOT_FOUND"
//	@Router			/accounts/{id}/members/{member_id} [delete]
func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrAccountNotFound, func(ctx context.Context, uid, id uuid.UUID) error {
		mid, err := pathUUID(r, "member_id", domain.ErrMemberNotFound)
		if err != nil {
			return err
		}
		return h.svc.RemoveMember(ctx, uid, id, mid)
	})
}

func memberBody(m *domain.AccountMember) any {
	if m == nil {
		return nil
	}
	return toMemberResponse(m)
}

// listSharedAccounts godoc
//
//	@Summary	List accounts shared with me
//	@Tags		account-members
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	SharedAccountListEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router		/shared-accounts [get]
func (h *Handler) listSharedAccounts(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	shared, err := h.svc.ListSharedAccounts(r.Context(), uid)
	writeList(w, r, shared, err, func(s domain.SharedAccount) SharedAccountResponse {
		return SharedAccountResponse{Account: toAccountResponse(s.Account), OwnerID: s.Account.UserID().String(), Role: string(s.Role)}
	})
}

// ===== Yearly report & audit =====

// yearly godoc
//
//	@Summary		Yearly report
//	@Description	Per-month income/expense/net in the base currency; other currencies converted with the user's rates. Currencies without any rate are listed in unconverted.
//	@Tags			reports
//	@Produce		json
//	@Security		BearerAuth
//	@Param			year	query		int	false	"Year, default current year"	example(2026)
//	@Success		200		{object}	YearlyEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_PARAMETER"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Router			/reports/yearly [get]
func (h *Handler) yearly(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	year := time.Now().Year()
	if v := r.URL.Query().Get("year"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			httpx.WriteError(w, r, invalidParam("year", "harus angka tahun"))
			return
		}
		year = n
	}
	rep, err := h.svc.Yearly(r.Context(), uid, year)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toYearlyResponse(rep))
}

// listAuditLogs godoc
//
//	@Summary		List audit logs
//	@Description	Changes to my accounts, transactions, transfers and members (including changes by members of shared accounts), newest first. Cursor-paginated.
//	@Tags			audit
//	@Produce		json
//	@Security		BearerAuth
//	@Param			entity		query		string	false	"Entity filter"		Enums(account, transaction, transfer, account_member)
//	@Param			entity_id	query		string	false	"Entity ID filter"	format(uuid)
//	@Param			limit		query		int		false	"Page size (1-100, default 20)"
//	@Param			cursor		query		string	false	"next_cursor from the previous page"
//	@Success		200			{object}	AuditLogListEnvelope
//	@Failure		400			{object}	httpx.ErrorResponse	"INVALID_PARAMETER / INVALID_CURSOR"
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router			/audit-logs [get]
func (h *Handler) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, err := pagination.ParseLimit(q.Get("limit"))
	if err != nil {
		httpx.WriteError(w, r, invalidParam("limit", err.Error()))
		return
	}
	f := domain.AuditFilter{Entity: q.Get("entity"), Limit: limit}
	switch f.Entity {
	case "", app.EntityAccount, app.EntityTransaction, app.EntityTransfer, app.EntityMember:
	default:
		httpx.WriteError(w, r, invalidParam("entity", "harus account, transaction, transfer atau account_member"))
		return
	}
	if f.EntityID, err = optionalUUID("entity_id", q.Get("entity_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	filter := pagination.FilterHash("audit-logs", f.Entity, q.Get("entity_id"))
	cur, err := pagination.Decode(q.Get("cursor"), filter)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	if cur != nil {
		f.After = &domain.AuditCursor{At: cur.Time, ID: cur.ID}
	}
	logs, err := h.svc.ListAuditLogs(r.Context(), uid, f)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	page, meta := pagination.Page(logs, limit, filter, func(a *domain.AuditEntry) (time.Time, uuid.UUID) { return a.CreatedAt, a.ID })
	out := make([]AuditLogResponse, 0, len(page))
	for _, a := range page {
		out = append(out, toAuditLogResponse(a))
	}
	httpx.List(w, out, meta)
}
