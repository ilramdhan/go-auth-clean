package http

import (
	"errors"
	"net/http"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/shared/money"
	"go-auth-clean/internal/shared/pagination"
)

func apiErr(status int, code, msg string) *httpx.Error {
	return &httpx.Error{Status: status, Code: code, Message: msg}
}

// errorTable memetakan sentinel error ke HTTP. Resource milik user lain
// selalu 404 (bukan 403) agar keberadaan ID tidak bocor.
var errorTable = []struct {
	err error
	out *httpx.Error
}{
	{domain.ErrAccountNotFound, apiErr(http.StatusNotFound, "ACCOUNT_NOT_FOUND", "akun tidak ditemukan")},
	{domain.ErrCategoryNotFound, apiErr(http.StatusNotFound, "CATEGORY_NOT_FOUND", "kategori tidak ditemukan")},
	{domain.ErrTransactionNotFound, apiErr(http.StatusNotFound, "TRANSACTION_NOT_FOUND", "transaksi tidak ditemukan")},
	{domain.ErrTransferNotFound, apiErr(http.StatusNotFound, "TRANSFER_NOT_FOUND", "transfer tidak ditemukan")},
	{domain.ErrVersionConflict, apiErr(http.StatusConflict, "VERSION_CONFLICT", "data sudah diubah, muat ulang lalu coba lagi")},
	{domain.ErrDuplicateName, apiErr(http.StatusConflict, "DUPLICATE_NAME", "nama sudah dipakai")},
	{domain.ErrAccountHasTransactions, apiErr(http.StatusConflict, "ACCOUNT_HAS_TRANSACTIONS", "akun masih memiliki transaksi; arsipkan saja")},
	{domain.ErrCategoryInUse, apiErr(http.StatusConflict, "CATEGORY_IN_USE", "kategori masih dipakai; isi reassign_to")},
	{domain.ErrCategoryHasChildren, apiErr(http.StatusConflict, "CATEGORY_HAS_CHILDREN", "kategori masih memiliki sub kategori")},
	{domain.ErrInsufficientBalance, apiErr(http.StatusUnprocessableEntity, "INSUFFICIENT_BALANCE", "saldo tidak mencukupi")},
	{domain.ErrAccountArchived, apiErr(http.StatusUnprocessableEntity, "ACCOUNT_ARCHIVED", "akun sudah diarsipkan")},
	{domain.ErrInvalidAccountType, apiErr(http.StatusUnprocessableEntity, "INVALID_ACCOUNT_TYPE", "tipe akun tidak valid")},
	{domain.ErrInvalidAmount, apiErr(http.StatusUnprocessableEntity, "INVALID_AMOUNT", "nominal harus lebih dari 0")},
	{domain.ErrAmountTooLarge, apiErr(http.StatusUnprocessableEntity, "AMOUNT_TOO_LARGE", "nominal melebihi batas")},
	{domain.ErrInvalidTxType, apiErr(http.StatusUnprocessableEntity, "INVALID_TYPE", "tipe harus income atau expense")},
	{domain.ErrDateInFuture, apiErr(http.StatusUnprocessableEntity, "DATE_IN_FUTURE", "tanggal tidak boleh di masa depan")},
	{domain.ErrDateTooOld, apiErr(http.StatusUnprocessableEntity, "DATE_TOO_OLD", "tanggal minimal 1970-01-01")},
	{domain.ErrCategoryTypeMismatch, apiErr(http.StatusUnprocessableEntity, "CATEGORY_TYPE_MISMATCH", "tipe kategori tidak sesuai tipe transaksi")},
	{domain.ErrCategoryReadOnly, apiErr(http.StatusUnprocessableEntity, "CATEGORY_READ_ONLY", "kategori system tidak bisa diubah")},
	{domain.ErrCategoryNestingTooDeep, apiErr(http.StatusUnprocessableEntity, "CATEGORY_NESTING_TOO_DEEP", "kategori hanya boleh satu level parent")},
	{domain.ErrInvalidReassignTarget, apiErr(http.StatusUnprocessableEntity, "INVALID_REASSIGN_TARGET", "kategori tujuan reassign tidak valid")},
	{domain.ErrSameAccountTransfer, apiErr(http.StatusUnprocessableEntity, "SAME_ACCOUNT_TRANSFER", "akun asal dan tujuan sama")},
	{domain.ErrManagedByTransfer, apiErr(http.StatusUnprocessableEntity, "MANAGED_BY_TRANSFER", "transaksi biaya transfer diubah lewat transfer")},
	{domain.ErrBudgetNotFound, apiErr(http.StatusNotFound, "BUDGET_NOT_FOUND", "budget tidak ditemukan")},
	{domain.ErrBudgetExists, apiErr(http.StatusConflict, "BUDGET_EXISTS", "budget kategori ini untuk bulan tsb sudah ada")},
	{domain.ErrTagNotFound, apiErr(http.StatusNotFound, "TAG_NOT_FOUND", "tag tidak ditemukan")},
	{domain.ErrTooManyTags, apiErr(http.StatusUnprocessableEntity, "TOO_MANY_TAGS", "maksimal 10 tag per transaksi")},
	{domain.ErrRecurringNotFound, apiErr(http.StatusNotFound, "RECURRING_RULE_NOT_FOUND", "recurring rule tidak ditemukan")},
	{domain.ErrRuleEnded, apiErr(http.StatusUnprocessableEntity, "RULE_ENDED", "recurring rule sudah berakhir")},
	{domain.ErrInvalidFrequency, apiErr(http.StatusUnprocessableEntity, "INVALID_FREQUENCY", "frekuensi harus daily, weekly, monthly atau yearly")},
	{domain.ErrDuplicateImport, apiErr(http.StatusConflict, "DUPLICATE_IMPORT", "transaksi sudah pernah di-import")},
	{app.ErrImportTooLarge, apiErr(http.StatusRequestEntityTooLarge, "IMPORT_TOO_LARGE", "file import maksimal 5 MB")},
	{app.ErrImportTooManyRow, apiErr(http.StatusUnprocessableEntity, "IMPORT_TOO_MANY_ROWS", "file import maksimal 10.000 baris")},
	{app.ErrImportBadHeader, apiErr(http.StatusBadRequest, "INVALID_CSV_HEADER", "header CSV tidak valid; wajib: date,type,amount,account,category")},
	{domain.ErrForbidden, apiErr(http.StatusForbidden, "FORBIDDEN", "role Anda pada akun bersama ini tidak mengizinkan aksi tsb")},
	{domain.ErrRateNotFound, apiErr(http.StatusNotFound, "RATE_NOT_FOUND", "kurs tidak ditemukan")},
	{domain.ErrRateExists, apiErr(http.StatusConflict, "RATE_EXISTS", "kurs pasangan currency ini untuk tanggal tsb sudah ada")},
	{domain.ErrRateUnavailable, apiErr(http.StatusUnprocessableEntity, "RATE_UNAVAILABLE", "kurs untuk pasangan currency ini belum tersedia")},
	{domain.ErrGoalNotFound, apiErr(http.StatusNotFound, "GOAL_NOT_FOUND", "target tabungan tidak ditemukan")},
	{domain.ErrContributionNotFound, apiErr(http.StatusNotFound, "CONTRIBUTION_NOT_FOUND", "setoran tidak ditemukan")},
	{domain.ErrGoalArchived, apiErr(http.StatusUnprocessableEntity, "GOAL_ARCHIVED", "target tabungan sudah diarsipkan")},
	{domain.ErrDuplicateLink, apiErr(http.StatusConflict, "DUPLICATE_LINK", "transfer/transaksi sudah ditautkan")},
	{domain.ErrDebtNotFound, apiErr(http.StatusNotFound, "DEBT_NOT_FOUND", "utang/piutang tidak ditemukan")},
	{domain.ErrDebtSettled, apiErr(http.StatusUnprocessableEntity, "DEBT_SETTLED", "utang/piutang sudah lunas")},
	{domain.ErrOverpayment, apiErr(http.StatusUnprocessableEntity, "OVERPAYMENT", "pembayaran melebihi sisa utang")},
	{domain.ErrBillNotFound, apiErr(http.StatusNotFound, "BILL_NOT_FOUND", "tagihan tidak ditemukan")},
	{domain.ErrBillDone, apiErr(http.StatusUnprocessableEntity, "BILL_DONE", "tagihan sudah selesai")},
	{domain.ErrMemberNotFound, apiErr(http.StatusNotFound, "MEMBER_NOT_FOUND", "anggota tidak ditemukan")},
	{domain.ErrMemberExists, apiErr(http.StatusConflict, "MEMBER_EXISTS", "user sudah menjadi anggota akun ini")},
	{domain.ErrUserNotFound, apiErr(http.StatusNotFound, "USER_NOT_FOUND", "user tidak ditemukan")},
	{domain.ErrInvalidRole, apiErr(http.StatusUnprocessableEntity, "INVALID_ROLE", "role harus viewer atau editor")},
	{domain.ErrInvalidTimezone, apiErr(http.StatusBadRequest, "INVALID_TIMEZONE", "timezone IANA tidak valid")},
	{domain.ErrInvalidWeekStart, apiErr(http.StatusUnprocessableEntity, "INVALID_WEEK_START", "week_start harus 0-6")},
	{domain.ErrInvalidRange, apiErr(http.StatusBadRequest, "INVALID_RANGE", "rentang tanggal tidak valid atau terlalu panjang")},
	{money.ErrCurrencyMismatch, apiErr(http.StatusUnprocessableEntity, "CURRENCY_MISMATCH", "currency tidak sama")},
	{money.ErrUnknownCurrency, apiErr(http.StatusUnprocessableEntity, "UNSUPPORTED_CURRENCY", "currency tidak didukung")},
	{money.ErrAmountOverflow, apiErr(http.StatusUnprocessableEntity, "AMOUNT_TOO_LARGE", "nominal melebihi batas")},
	{money.ErrInvalidAmount, apiErr(http.StatusUnprocessableEntity, "INVALID_AMOUNT", "format nominal tidak valid")},
	{money.ErrTooManyDecimals, apiErr(http.StatusUnprocessableEntity, "INVALID_AMOUNT", "jumlah desimal melebihi currency")},
	{pagination.ErrInvalidCursor, apiErr(http.StatusBadRequest, "INVALID_CURSOR", "cursor tidak valid")},
	{app.ErrIdempotencyKeyRequired, apiErr(http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "header Idempotency-Key wajib diisi")},
	{app.ErrInvalidIdempotencyKey, apiErr(http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key harus 8-128 karakter ASCII")},
	{app.ErrIdempotencyInProgress, apiErr(http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS", "request dengan key ini sedang diproses")},
	{app.ErrIdempotencyKeyReused, apiErr(http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSED", "Idempotency-Key sudah dipakai untuk request berbeda")},
}

// mapError menerjemahkan error domain/app ke HTTP. Error tak dikenal diteruskan
// apa adanya (httpx.WriteError menjadikannya 500 tanpa membocorkan detail).
func mapError(err error) error {
	if _, ok := errors.AsType[*httpx.Error](err); ok {
		return err
	}
	if ve, ok := errors.AsType[*domain.ValidationError](err); ok {
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION_FAILED", Message: "input tidak valid",
			Details: []httpx.FieldDetail{{Field: ve.Field, Message: ve.Reason}}}
	}
	for _, e := range errorTable {
		if errors.Is(err, e.err) {
			return e.out
		}
	}
	return err
}

func invalidParam(field, msg string) error {
	return &httpx.Error{Status: http.StatusBadRequest, Code: "INVALID_PARAMETER", Message: msg,
		Details: []httpx.FieldDetail{{Field: field, Message: msg}}}
}

var (
	errUnauthenticated = apiErr(http.StatusUnauthorized, "UNAUTHENTICATED", "token tidak valid atau tidak ada")
	errInvalidIfMatch  = apiErr(http.StatusBadRequest, "INVALID_IF_MATCH", `If-Match harus berupa version, mis. "3"`)
)
