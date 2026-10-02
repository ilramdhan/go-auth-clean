package http

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/platform/httpx"
)

// multipartOverhead: ruang ekstra untuk header multipart di atas batas file.
const multipartOverhead = 64 << 10

// exportTransactions godoc
//
//	@Summary		Export transactions as CSV
//	@Description	Streams all transactions matching the same filters as GET /transactions (no paging), oldest first. Columns: date,type,amount,currency,account,category,note,tags (tags separated by ";"). Cells starting with = + - @ are prefixed with ' to prevent formula injection.
//	@Tags			transactions
//	@Produce		text/csv
//	@Security		BearerAuth
//	@Param			from		query		string		false	"From date (YYYY-MM-DD)"	format(date)
//	@Param			to			query		string		false	"To date (YYYY-MM-DD)"		format(date)
//	@Param			type		query		string		false	"Type"						Enums(income, expense)
//	@Param			account_id	query		[]string	false	"Account IDs"				collectionFormat(multi)
//	@Param			category_id	query		[]string	false	"Category IDs"				collectionFormat(multi)
//	@Param			tag			query		string		false	"Tag name"
//	@Param			q			query		string		false	"Search in note"
//	@Param			format		query		string		false	"Only csv is supported"	Enums(csv)
//	@Success		200			{file}		file
//	@Failure		400			{object}	httpx.ErrorResponse	"INVALID_PARAMETER / INVALID_RANGE"
//	@Failure		401			{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router			/transactions/export [get]
func (h *Handler) exportTransactions(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	if f := r.URL.Query().Get("format"); f != "" && f != "csv" {
		httpx.WriteError(w, r, invalidParam("format", "hanya csv"))
		return
	}
	in, _, _, err := parseTransactionQuery(r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	in.UserID, in.Limit, in.After = uid, 0, nil
	sw := &lazyCSVWriter{w: w}
	if err := h.svc.ExportTransactions(r.Context(), in, sw); err != nil {
		if !sw.started {
			httpx.WriteError(w, r, mapError(err))
			return
		}
		// Header sudah terkirim: hanya bisa memutus stream (client melihat file terpotong).
		panic(http.ErrAbortHandler)
	}
	sw.start()
}

// lazyCSVWriter menunda header response sampai byte pertama ditulis, sehingga
// error validasi filter tetap bisa dikirim sebagai JSON.
type lazyCSVWriter struct {
	w       http.ResponseWriter
	started bool
}

func (l *lazyCSVWriter) start() {
	if l.started {
		return
	}
	l.started = true
	l.w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	l.w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "transactions.csv"}))
	l.w.Header().Set("Cache-Control", "no-store")
	l.w.WriteHeader(http.StatusOK)
}

func (l *lazyCSVWriter) Write(p []byte) (int, error) {
	l.start()
	return l.w.Write(p)
}

// importTransactions godoc
//
//	@Summary		Import transactions from CSV
//	@Description	Multipart upload (field "file", max 5 MB / 10,000 rows) in the export format; account and category may be names or IDs, currency and tags optional (tags must exist). Rows are validated individually; possible duplicates (same date, amount, note and account as an earlier import) are skipped. With dry_run=true nothing is written. All valid rows are committed in one DB transaction.
//	@Tags			transactions
//	@Accept			multipart/form-data
//	@Produce		json
//	@Security		BearerAuth
//	@Param			file	formData	file				true	"CSV file"
//	@Param			dry_run	query		bool				false	"Validate only"
//	@Success		200		{object}	ImportEnvelope		"dry run"
//	@Success		201		{object}	ImportEnvelope		"imported"
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_PARAMETER / INVALID_CSV_HEADER"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		413		{object}	httpx.ErrorResponse	"IMPORT_TOO_LARGE"
//	@Failure		422		{object}	httpx.ErrorResponse	"IMPORT_TOO_MANY_ROWS / VALIDATION_FAILED"
//	@Router			/transactions/import [post]
func (h *Handler) importTransactions(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxImportBytes+multipartOverhead)
	file, _, err := r.FormFile("file")
	if err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			httpx.WriteError(w, r, mapError(app.ErrImportTooLarge))
			return
		}
		httpx.WriteError(w, r, invalidParam("file", "multipart field file wajib diisi"))
		return
	}
	defer func() { _ = file.Close() }()
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	dryRun := strings.EqualFold(r.URL.Query().Get("dry_run"), "true")
	res, err := h.svc.ImportTransactions(r.Context(), app.ImportInput{UserID: uid, File: file, DryRun: dryRun})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	status := http.StatusCreated
	if dryRun {
		status = http.StatusOK
	}
	httpx.Data(w, status, toImportResponse(res))
}
