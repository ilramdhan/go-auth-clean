package http

import (
	"github.com/google/uuid"

	"net/http"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/httpx"
)

// getSettings godoc
//
//	@Summary		Get finance settings
//	@Description	Returns the user's finance preferences. Created lazily with server defaults on first access.
//	@Tags			settings
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	SettingsEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router			/settings [get]
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	st, err := h.svc.GetSettings(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toSettingsResponse(st))
}

// updateSettings godoc
//
//	@Summary		Update finance settings
//	@Description	Replaces base currency, IANA timezone and week start. Timezone controls "today" and monthly report boundaries.
//	@Tags			settings
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		UpdateSettingsRequest	true	"Settings"
//	@Success		200		{object}	SettingsEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON / INVALID_TIMEZONE"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / UNSUPPORTED_CURRENCY / INVALID_WEEK_START"
//	@Router			/settings [put]
func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[UpdateSettingsRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	st, err := h.svc.UpdateSettings(r.Context(), app.UpdateSettingsInput{UserID: uid,
		BaseCurrency: req.BaseCurrency, Timezone: req.Timezone, WeekStart: *req.WeekStart})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toSettingsResponse(st))
}

// listCurrencies godoc
//
//	@Summary		List supported currencies
//	@Description	Currencies with their minor unit (number of decimals used by amount strings).
//	@Tags			settings
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	CurrencyListEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router			/currencies [get]
func (h *Handler) listCurrencies(w http.ResponseWriter, r *http.Request) {
	if _, ok := userID(w, r); !ok {
		return
	}
	cs, err := h.svc.ListCurrencies(r.Context())
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	out := make([]CurrencyResponse, 0, len(cs))
	for _, c := range cs {
		out = append(out, CurrencyResponse(c))
	}
	httpx.Data(w, http.StatusOK, out)
}

// listAccounts godoc
//
//	@Summary		List accounts
//	@Description	Accounts of the current user with cached balance. Archived accounts are hidden unless include_archived=true.
//	@Tags			accounts
//	@Produce		json
//	@Security		BearerAuth
//	@Param			include_archived	query		bool	false	"Include archived accounts"
//	@Success		200					{object}	AccountListEnvelope
//	@Failure		401					{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router			/accounts [get]
func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	accs, err := h.svc.ListAccounts(r.Context(), uid, r.URL.Query().Get("include_archived") == "true")
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	out := make([]AccountResponse, 0, len(accs))
	for _, a := range accs {
		out = append(out, toAccountResponse(a))
	}
	httpx.Data(w, http.StatusOK, out)
}

// createAccount godoc
//
//	@Summary		Create account
//	@Description	Creates a cash/bank/ewallet/credit_card account. Amounts are strings in major units (e.g. "1500000" IDR, "12.50" USD).
//	@Tags			accounts
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		CreateAccountRequest	true	"Account"
//	@Success		201		{object}	AccountEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		409		{object}	httpx.ErrorResponse	"DUPLICATE_NAME"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_AMOUNT / UNSUPPORTED_CURRENCY / INSUFFICIENT_BALANCE"
//	@Router			/accounts [post]
func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateAccountRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	a, err := h.svc.CreateAccount(r.Context(), app.CreateAccountInput{UserID: uid, Name: req.Name, Type: req.Type,
		Currency: req.Currency, InitialBalance: req.InitialBalance, AllowNegative: req.AllowNegative})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	setETag(w, a.Version())
	httpx.Data(w, http.StatusCreated, toAccountResponse(a))
}

// getAccount godoc
//
//	@Summary	Get account
//	@Tags		accounts
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Account ID"	format(uuid)
//	@Success	200	{object}	AccountEnvelope
//	@Header		200	{string}	ETag				"Current version, use with If-Match"
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND"
//	@Router		/accounts/{id} [get]
func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) {
	h.accountAction(w, r, func(w http.ResponseWriter, r *http.Request, uid, id uuid.UUID) (*domain.Account, error) {
		return h.svc.GetAccount(r.Context(), uid, id)
	})
}

// updateAccount godoc
//
//	@Summary		Update account
//	@Description	Partial update. Changing initial_balance shifts the current balance by the same delta. Send If-Match with the version for optimistic locking.
//	@Tags			accounts
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id			path		string					true	"Account ID"	format(uuid)
//	@Param			If-Match	header		string					false	"Expected version, e.g. \"3\""
//	@Param			body		body		UpdateAccountRequest	true	"Fields to change"
//	@Success		200			{object}	AccountEnvelope
//	@Failure		400			{object}	httpx.ErrorResponse	"INVALID_JSON / INVALID_IF_MATCH"
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404			{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND"
//	@Failure		409			{object}	httpx.ErrorResponse	"VERSION_CONFLICT / DUPLICATE_NAME"
//	@Failure		422			{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INSUFFICIENT_BALANCE / ACCOUNT_ARCHIVED"
//	@Router			/accounts/{id} [patch]
func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) {
	h.accountAction(w, r, func(w http.ResponseWriter, r *http.Request, uid, id uuid.UUID) (*domain.Account, error) {
		ver, err := ifMatch(r)
		if err != nil {
			return nil, err
		}
		req, err := decodeAndValidate[UpdateAccountRequest](h, w, r)
		if err != nil {
			return nil, err
		}
		return h.svc.UpdateAccount(r.Context(), app.UpdateAccountInput{UserID: uid, ID: id, ExpectedVersion: ver,
			Name: req.Name, InitialBalance: req.InitialBalance, AllowNegative: req.AllowNegative})
	})
}

// archiveAccount godoc
//
//	@Summary		Archive account
//	@Description	Archived accounts keep their history but cannot receive new transactions or transfers. Idempotent.
//	@Tags			accounts
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string	true	"Account ID"	format(uuid)
//	@Success		200	{object}	AccountEnvelope
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404	{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND"
//	@Router			/accounts/{id}/archive [post]
func (h *Handler) archiveAccount(w http.ResponseWriter, r *http.Request) {
	h.accountAction(w, r, func(w http.ResponseWriter, r *http.Request, uid, id uuid.UUID) (*domain.Account, error) {
		return h.svc.ArchiveAccount(r.Context(), uid, id)
	})
}

// unarchiveAccount godoc
//
//	@Summary	Unarchive account
//	@Tags		accounts
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Account ID"	format(uuid)
//	@Success	200	{object}	AccountEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND"
//	@Router		/accounts/{id}/unarchive [post]
func (h *Handler) unarchiveAccount(w http.ResponseWriter, r *http.Request) {
	h.accountAction(w, r, func(w http.ResponseWriter, r *http.Request, uid, id uuid.UUID) (*domain.Account, error) {
		return h.svc.UnarchiveAccount(r.Context(), uid, id)
	})
}

// deleteAccount godoc
//
//	@Summary		Delete account
//	@Description	Soft delete. Only allowed when the account has no transactions or transfers; archive it otherwise.
//	@Tags			accounts
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Account ID"	format(uuid)
//	@Success		204
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404	{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND"
//	@Failure		409	{object}	httpx.ErrorResponse	"ACCOUNT_HAS_TRANSACTIONS"
//	@Router			/accounts/{id} [delete]
func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrAccountNotFound, h.svc.DeleteAccount)
}

// accountAction: pola umum endpoint yang mengembalikan satu akun.
func (h *Handler) accountAction(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, uuid.UUID, uuid.UUID) (*domain.Account, error)) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrAccountNotFound)
	if err == nil {
		var a *domain.Account
		if a, err = fn(w, r, uid, id); err == nil {
			setETag(w, a.Version())
			httpx.Data(w, http.StatusOK, toAccountResponse(a))
			return
		}
	}
	httpx.WriteError(w, r, mapError(err))
}
