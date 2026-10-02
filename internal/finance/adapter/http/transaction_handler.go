package http

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/shared/pagination"
)

// maxFilterIDs membatasi jumlah account_id/category_id per request.
const maxFilterIDs = 50

// listTransactions godoc
//
//	@Summary		List transactions
//	@Description	Income/expense transactions, newest first (transaction_date, id DESC), cursor paginated. Dates are inclusive. Repeat account_id / category_id for multiple values. A cursor is bound to the filter it was issued for.
//	@Tags			transactions
//	@Produce		json
//	@Security		BearerAuth
//	@Param			from				query		string		false	"From date (YYYY-MM-DD)"	format(date)
//	@Param			to					query		string		false	"To date (YYYY-MM-DD)"		format(date)
//	@Param			type				query		string		false	"Type"						Enums(income, expense)
//	@Param			account_id			query		[]string	false	"Account IDs"				collectionFormat(multi)
//	@Param			category_id			query		[]string	false	"Category IDs"				collectionFormat(multi)
//	@Param			include_children	query		bool		false	"Also match sub categories of category_id"
//	@Param			min_amount			query		string		false	"Minimum amount (major units)"
//	@Param			max_amount			query		string		false	"Maximum amount (major units)"
//	@Param			currency			query		string		false	"Currency for amount filters (default base currency)"
//	@Param			q					query		string		false	"Search in note"
//	@Param			tag					query		string		false	"Tag name (case-insensitive)"
//	@Param			limit				query		int			false	"Page size (1-100, default 20)"
//	@Param			cursor				query		string		false	"Opaque cursor from meta.next_cursor"
//	@Success		200					{object}	TransactionListEnvelope
//	@Failure		400					{object}	httpx.ErrorResponse	"INVALID_PARAMETER / INVALID_CURSOR / INVALID_RANGE"
//	@Failure		401					{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		422					{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INVALID_AMOUNT"
//	@Router			/transactions [get]
func (h *Handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	in, filter, limit, err := parseTransactionQuery(r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	in.UserID = uid
	txs, err := h.svc.ListTransactions(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	page, meta := pagination.Page(txs, limit, filter, func(t *domain.Transaction) (time.Time, uuid.UUID) {
		return t.Date(), t.ID()
	})
	out, err := h.withTags(r.Context(), uid, page...)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.List(w, out, meta)
}

func parseTransactionQuery(r *http.Request) (in app.ListTransactionsInput, filter string, limit int, err error) {
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
	if in.AccountIDs, err = queryUUIDs(q["account_id"], "account_id"); err != nil {
		return in, "", 0, err
	}
	if in.CategoryIDs, err = queryUUIDs(q["category_id"], "category_id"); err != nil {
		return in, "", 0, err
	}
	in.Type, in.Currency = q.Get("type"), strings.ToUpper(q.Get("currency"))
	in.MinAmount, in.MaxAmount = q.Get("min_amount"), q.Get("max_amount")
	in.IncludeChildren = q.Get("include_children") == "true"
	in.Query = strings.TrimSpace(q.Get("q"))
	if len([]rune(in.Query)) > 100 {
		return in, "", 0, invalidParam("q", "maksimal 100 karakter")
	}
	in.Tag = strings.TrimSpace(q.Get("tag"))
	if len([]rune(in.Tag)) > domain.MaxTagNameLength {
		return in, "", 0, invalidParam("tag", "nama tag terlalu panjang")
	}
	in.Limit = limit
	filter = pagination.FilterHash("transactions", q.Get("from"), q.Get("to"), in.Type,
		joinIDs(in.AccountIDs), joinIDs(in.CategoryIDs), q.Get("include_children"),
		in.MinAmount, in.MaxAmount, in.Currency, in.Query, strings.ToLower(in.Tag))
	cur, err := pagination.Decode(q.Get("cursor"), filter)
	if err != nil {
		return in, "", 0, err
	}
	if cur != nil {
		in.After = &domain.PageKey{Date: cur.Time, ID: cur.ID}
	}
	return in, filter, limit, nil
}

func queryUUIDs(vals []string, field string) ([]uuid.UUID, error) {
	if len(vals) > maxFilterIDs {
		return nil, invalidParam(field, "terlalu banyak nilai")
	}
	out := make([]uuid.UUID, 0, len(vals))
	for _, v := range vals {
		for part := range strings.SplitSeq(v, ",") {
			if part = strings.TrimSpace(part); part == "" {
				continue
			}
			id, err := uuid.Parse(part)
			if err != nil {
				return nil, invalidParam(field, "harus UUID")
			}
			out = append(out, id)
		}
	}
	if len(out) > maxFilterIDs {
		return nil, invalidParam(field, "terlalu banyak nilai")
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return slices.Compact(out), nil
}

func joinIDs(ids []uuid.UUID) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = id.String()
	}
	return strings.Join(s, ",")
}

// createTransaction godoc
//
//	@Summary		Create transaction
//	@Description	Records income/expense and updates the account balance atomically. Requires an Idempotency-Key header: retrying with the same key and body returns the original response (header Idempotent-Replayed: true); the same key with a different body is rejected.
//	@Tags			transactions
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			Idempotency-Key	header		string						true	"Unique key per logical request (8-128 chars), e.g. a UUID"
//	@Param			body			body		CreateTransactionRequest	true	"Transaction"
//	@Success		201				{object}	TransactionEnvelope
//	@Header			201				{string}	Idempotent-Replayed	"true when the response is a replay"
//	@Failure		400				{object}	httpx.ErrorResponse	"INVALID_JSON / IDEMPOTENCY_KEY_REQUIRED / INVALID_IDEMPOTENCY_KEY"
//	@Failure		401				{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404				{object}	httpx.ErrorResponse	"ACCOUNT_NOT_FOUND / CATEGORY_NOT_FOUND"
//	@Failure		409				{object}	httpx.ErrorResponse	"IDEMPOTENCY_IN_PROGRESS"
//	@Failure		422				{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INSUFFICIENT_BALANCE / CURRENCY_MISMATCH / CATEGORY_TYPE_MISMATCH / ACCOUNT_ARCHIVED / DATE_IN_FUTURE / IDEMPOTENCY_KEY_REUSED"
//	@Router			/transactions [post]
func (h *Handler) createTransaction(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateTransactionRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	in := app.CreateTransactionInput{UserID: uid, Type: req.Type, Amount: req.Amount, Note: req.Note}
	if in.AccountID, err = uuid.Parse(req.AccountID); err != nil {
		httpx.WriteError(w, r, invalidParam("account_id", "harus UUID"))
		return
	}
	if in.CategoryID, err = uuid.Parse(req.CategoryID); err != nil {
		httpx.WriteError(w, r, invalidParam("category_id", "harus UUID"))
		return
	}
	if in.Date, err = parseDate("transaction_date", req.TransactionDate); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if in.TagIDs, err = parseTagIDs(req.TagIDs); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.idempotent(w, r, uid, "/api/v1/transactions", req, func(ctx context.Context) (int, any, error) {
		t, err := h.svc.CreateTransaction(ctx, in)
		if err != nil {
			return 0, nil, err
		}
		out, err := h.withTags(ctx, uid, t)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, out[0], nil
	})
}

// getTransaction godoc
//
//	@Summary	Get transaction
//	@Tags		transactions
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Transaction ID"	format(uuid)
//	@Success	200	{object}	TransactionEnvelope
//	@Header		200	{string}	ETag				"Current version, use with If-Match"
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"TRANSACTION_NOT_FOUND"
//	@Router		/transactions/{id} [get]
func (h *Handler) getTransaction(w http.ResponseWriter, r *http.Request) {
	h.transactionAction(w, r, func(uid, id uuid.UUID) (*domain.Transaction, error) {
		return h.svc.GetTransaction(r.Context(), uid, id)
	})
}

// updateTransaction godoc
//
//	@Summary		Update transaction
//	@Description	Partial update; balances of the old and new account are adjusted in the same DB transaction. Transfer fee transactions are read-only (edit the transfer).
//	@Tags			transactions
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id			path		string						true	"Transaction ID"	format(uuid)
//	@Param			If-Match	header		string						false	"Expected version, e.g. \"1\""
//	@Param			body		body		UpdateTransactionRequest	true	"Fields to change"
//	@Success		200			{object}	TransactionEnvelope
//	@Failure		400			{object}	httpx.ErrorResponse	"INVALID_JSON / INVALID_IF_MATCH / INVALID_PARAMETER"
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404			{object}	httpx.ErrorResponse	"TRANSACTION_NOT_FOUND / ACCOUNT_NOT_FOUND / CATEGORY_NOT_FOUND"
//	@Failure		409			{object}	httpx.ErrorResponse	"VERSION_CONFLICT"
//	@Failure		422			{object}	httpx.ErrorResponse	"VALIDATION_FAILED / INSUFFICIENT_BALANCE / MANAGED_BY_TRANSFER / ACCOUNT_ARCHIVED"
//	@Router			/transactions/{id} [patch]
func (h *Handler) updateTransaction(w http.ResponseWriter, r *http.Request) {
	h.transactionAction(w, r, func(uid, id uuid.UUID) (*domain.Transaction, error) {
		ver, err := ifMatch(r)
		if err != nil {
			return nil, err
		}
		req, err := decodeAndValidate[UpdateTransactionRequest](h, w, r)
		if err != nil {
			return nil, err
		}
		in := app.UpdateTransactionInput{UserID: uid, ID: id, ExpectedVersion: ver,
			Type: req.Type, Amount: req.Amount, Note: req.Note}
		if in.AccountID, err = parseUUIDPtr("account_id", req.AccountID); err != nil {
			return nil, err
		}
		if in.CategoryID, err = parseUUIDPtr("category_id", req.CategoryID); err != nil {
			return nil, err
		}
		if in.Date, err = parseDatePtr("transaction_date", req.TransactionDate); err != nil {
			return nil, err
		}
		return h.svc.UpdateTransaction(r.Context(), in)
	})
}

// deleteTransaction godoc
//
//	@Summary		Delete transaction
//	@Description	Soft deletes the transaction and reverts its effect on the account balance.
//	@Tags			transactions
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Transaction ID"	format(uuid)
//	@Success		204
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404	{object}	httpx.ErrorResponse	"TRANSACTION_NOT_FOUND"
//	@Failure		409	{object}	httpx.ErrorResponse	"VERSION_CONFLICT"
//	@Failure		422	{object}	httpx.ErrorResponse	"INSUFFICIENT_BALANCE / MANAGED_BY_TRANSFER / ACCOUNT_ARCHIVED"
//	@Router			/transactions/{id} [delete]
func (h *Handler) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrTransactionNotFound, h.svc.DeleteTransaction)
}

func (h *Handler) transactionAction(w http.ResponseWriter, r *http.Request, fn func(uid, id uuid.UUID) (*domain.Transaction, error)) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrTransactionNotFound)
	if err == nil {
		var t *domain.Transaction
		if t, err = fn(uid, id); err == nil {
			var out []TransactionResponse
			if out, err = h.withTags(r.Context(), uid, t); err == nil {
				setETag(w, t.Version())
				httpx.Data(w, http.StatusOK, out[0])
				return
			}
		}
	}
	httpx.WriteError(w, r, mapError(err))
}

// withTags mengubah transaksi ke response beserta tag-nya (satu query untuk semua).
func (h *Handler) withTags(ctx context.Context, uid uuid.UUID, txs ...*domain.Transaction) ([]TransactionResponse, error) {
	out := make([]TransactionResponse, 0, len(txs))
	if len(txs) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(txs))
	for i, t := range txs {
		ids[i] = t.ID()
	}
	tags, err := h.svc.TransactionTags(ctx, uid, ids)
	if err != nil {
		return nil, err
	}
	for _, t := range txs {
		resp := toTransactionResponse(t)
		if tg := tags[t.ID()]; len(tg) > 0 {
			resp.Tags = toTagResponses(tg)
		}
		out = append(out, resp)
	}
	return out, nil
}
