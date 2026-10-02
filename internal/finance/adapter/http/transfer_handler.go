package http

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/shared/pagination"
)

// listTransfers godoc
//
//	@Summary		List transfers
//	@Description	Transfers between the user's accounts, newest first, cursor paginated. Transfers are not counted as income/expense in reports.
//	@Tags			transfers
//	@Produce		json
//	@Security		BearerAuth
//	@Param			from		query		string	false	"From date (YYYY-MM-DD)"		format(date)
//	@Param			to			query		string	false	"To date (YYYY-MM-DD)"			format(date)
//	@Param			account_id	query		string	false	"Source or destination account"	format(uuid)
//	@Param			limit		query		int		false	"Page size (1-100, default 20)"
//	@Param			cursor		query		string	false	"Opaque cursor from meta.next_cursor"
//	@Success		200			{object}	TransferListEnvelope
//	@Failure		400			{object}	httpx.ErrorResponse	"INVALID_PARAMETER / INVALID_CURSOR / INVALID_RANGE"
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router			/transfers [get]
func (h *Handler) listTransfers(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	in, filter, limit, err := parseTransferQuery(r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	in.UserID = uid
	trs, err := h.svc.ListTransfers(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	page, meta := pagination.Page(trs, limit, filter, func(t *domain.Transfer) (time.Time, uuid.UUID) {
		return t.Date(), t.ID()
	})
	out := make([]TransferResponse, 0, len(page))
	for _, t := range page {
		out = append(out, toTransferResponse(t))
	}
	httpx.List(w, out, meta)
}

func parseTransferQuery(r *http.Request) (in app.ListTransfersInput, filter string, limit int, err error) {
	q := r.URL.Query()
	if limit, err = pagination.ParseLimit(q.Get("limit")); err != nil {
		return in, "", 0, invalidParam("limit", err.Error())
	}
	if in.From, err = queryDate(r, "from"); err != nil {
		return in, "", 0, err
	}
	if in.To, err = queryDate(r, "to"); err != nil {
		return in, "", 0, err
	}
	if v := q.Get("account_id"); v != "" {
		if in.AccountID, err = parseUUIDPtr("account_id", &v); err != nil {
			return in, "", 0, err
		}
	}
	in.Limit = limit
	filter = pagination.FilterHash("transfers", q.Get("from"), q.Get("to"), q.Get("account_id"))
	cur, err := pagination.Decode(q.Get("cursor"), filter)
	if err != nil {
		return in, "", 0, err
	}
	if cur != nil {
		in.After = &domain.PageKey{Date: cur.Time, ID: cur.ID}
	}
	return in, filter, limit, nil
}

// createTransfer godoc
//
//	@Summary		Create transfer
//	@Description	Moves money between two of the user's accounts (same currency) atomically. An optional fee is recorded as an expense ("Biaya Admin") on the source account. Requires an Idempotency-Key header (same semantics as POST /transactions).
//	@Tags			transfers
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			Idempotency-Key	header		string					true	"Unique key per logical request (8-128 chars), e.g. a UUID"
//	@Param			body			body		CreateTransferRequest	true	"Transfer"
//	@Success		201				{object}	TransferEnvelope
//	@Header			201				{string}	Idempotent-Replayed	"true when the response is a replay"
//	@Failure		400				{object}	httpx.ErrorResponse	"INVALID_JSON / IDEMPOTENCY_KEY_REQUIRED / INVALID_IDEMPOTENCY_KEY"
//	@Failure		401				{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404				{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND"
//	@Failure		409				{object}	httpx.ErrorResponse	"IDEMPOTENCY_IN_PROGRESS"
//	@Failure		422				{object}	httpx.ErrorResponse	"VALIDATION_FAILED / SAME_ACCOUNT_TRANSFER / INSUFFICIENT_BALANCE / CURRENCY_MISMATCH / ACCOUNT_ARCHIVED / IDEMPOTENCY_KEY_REUSED"
//	@Router			/transfers [post]
func (h *Handler) createTransfer(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateTransferRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	in := app.CreateTransferInput{UserID: uid, Amount: req.Amount, Fee: req.Fee, ToAmount: req.ToAmount, Note: req.Note}
	if in.FromAccountID, err = uuid.Parse(req.FromAccountID); err != nil {
		httpx.WriteError(w, r, invalidParam("from_account_id", "harus UUID"))
		return
	}
	if in.ToAccountID, err = uuid.Parse(req.ToAccountID); err != nil {
		httpx.WriteError(w, r, invalidParam("to_account_id", "harus UUID"))
		return
	}
	if in.Date, err = parseDate("transfer_date", req.TransferDate); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.idempotent(w, r, uid, "/api/v1/transfers", req, func(ctx context.Context) (int, any, error) {
		t, err := h.svc.CreateTransfer(ctx, in)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, toTransferResponse(t), nil
	})
}

// getTransfer godoc
//
//	@Summary	Get transfer
//	@Tags		transfers
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Transfer ID"	format(uuid)
//	@Success	200	{object}	TransferEnvelope
//	@Header		200	{string}	ETag				"Current version, use with If-Match"
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"TRANSFER_NOT_FOUND"
//	@Router		/transfers/{id} [get]
func (h *Handler) getTransfer(w http.ResponseWriter, r *http.Request) {
	h.transferAction(w, r, func(uid, id uuid.UUID) (*domain.Transfer, error) {
		return h.svc.GetTransfer(r.Context(), uid, id)
	})
}

// updateTransfer godoc
//
//	@Summary		Update transfer
//	@Description	Partial update; all affected balances (up to 4 accounts) are adjusted in one DB transaction. fee "0" removes the fee transaction.
//	@Tags			transfers
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id			path		string					true	"Transfer ID"	format(uuid)
//	@Param			If-Match	header		string					false	"Expected version, e.g. \"1\""
//	@Param			body		body		UpdateTransferRequest	true	"Fields to change"
//	@Success		200			{object}	TransferEnvelope
//	@Failure		400			{object}	httpx.ErrorResponse	"INVALID_JSON / INVALID_IF_MATCH / INVALID_PARAMETER"
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404			{object}	httpx.ErrorResponse	"TRANSFER_NOT_FOUND / ACCOUNT_NOT_FOUND"
//	@Failure		409			{object}	httpx.ErrorResponse	"VERSION_CONFLICT"
//	@Failure		422			{object}	httpx.ErrorResponse	"VALIDATION_FAILED / SAME_ACCOUNT_TRANSFER / INSUFFICIENT_BALANCE / CURRENCY_MISMATCH"
//	@Router			/transfers/{id} [patch]
func (h *Handler) updateTransfer(w http.ResponseWriter, r *http.Request) {
	h.transferAction(w, r, func(uid, id uuid.UUID) (*domain.Transfer, error) {
		ver, err := ifMatch(r)
		if err != nil {
			return nil, err
		}
		req, err := decodeAndValidate[UpdateTransferRequest](h, w, r)
		if err != nil {
			return nil, err
		}
		in := app.UpdateTransferInput{UserID: uid, ID: id, ExpectedVersion: ver,
			Amount: req.Amount, Fee: req.Fee, ToAmount: req.ToAmount, Note: req.Note}
		if in.FromAccountID, err = parseUUIDPtr("from_account_id", req.FromAccountID); err != nil {
			return nil, err
		}
		if in.ToAccountID, err = parseUUIDPtr("to_account_id", req.ToAccountID); err != nil {
			return nil, err
		}
		if in.Date, err = parseDatePtr("transfer_date", req.TransferDate); err != nil {
			return nil, err
		}
		return h.svc.UpdateTransfer(r.Context(), in)
	})
}

// deleteTransfer godoc
//
//	@Summary		Delete transfer
//	@Description	Soft deletes the transfer (and its fee transaction) and reverts both legs.
//	@Tags			transfers
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Transfer ID"	format(uuid)
//	@Success		204
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404	{object}	httpx.ErrorResponse	"TRANSFER_NOT_FOUND"
//	@Failure		409	{object}	httpx.ErrorResponse	"VERSION_CONFLICT"
//	@Failure		422	{object}	httpx.ErrorResponse	"INSUFFICIENT_BALANCE / ACCOUNT_ARCHIVED"
//	@Router			/transfers/{id} [delete]
func (h *Handler) deleteTransfer(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrTransferNotFound, h.svc.DeleteTransfer)
}

func (h *Handler) transferAction(w http.ResponseWriter, r *http.Request, fn func(uid, id uuid.UUID) (*domain.Transfer, error)) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrTransferNotFound)
	if err == nil {
		var t *domain.Transfer
		if t, err = fn(uid, id); err == nil {
			setETag(w, t.Version())
			httpx.Data(w, http.StatusOK, toTransferResponse(t))
			return
		}
	}
	httpx.WriteError(w, r, mapError(err))
}
