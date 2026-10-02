// Package domain berisi model bisnis bounded context finance: aggregate Account,
// Category, Transaction, Transfer, dan UserSettings beserta invariant-nya.
// Package ini TIDAK boleh import adapter (http, postgres) maupun bounded context lain.
package domain

import "errors"

// Sentinel error tanpa HTTP status. Pemetaan ke protokol dilakukan di adapter.
var (
	ErrAccountNotFound     = errors.New("account not found")
	ErrCategoryNotFound    = errors.New("category not found")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrTransferNotFound    = errors.New("transfer not found")

	ErrVersionConflict        = errors.New("version conflict")
	ErrDuplicateName          = errors.New("name already exists")
	ErrInvalidAccountType     = errors.New("invalid account type")
	ErrAccountArchived        = errors.New("account is archived")
	ErrAccountHasTransactions = errors.New("account still has transactions")
	ErrInsufficientBalance    = errors.New("insufficient balance")

	ErrInvalidAmount  = errors.New("amount must be greater than zero")
	ErrAmountTooLarge = errors.New("amount exceeds maximum")
	ErrInvalidTxType  = errors.New("invalid transaction type")
	ErrDateInFuture   = errors.New("date is in the future")
	ErrDateTooOld     = errors.New("date is before 1970-01-01")

	ErrCategoryTypeMismatch   = errors.New("category type does not match transaction type")
	ErrCategoryReadOnly       = errors.New("system category cannot be modified")
	ErrCategoryInUse          = errors.New("category is in use")
	ErrCategoryHasChildren    = errors.New("category still has sub categories")
	ErrCategoryNestingTooDeep = errors.New("category can only have one parent level")
	ErrInvalidReassignTarget  = errors.New("invalid reassign target category")

	ErrSameAccountTransfer = errors.New("cannot transfer to the same account")
	ErrManagedByTransfer   = errors.New("transaction is managed by a transfer")

	ErrBudgetNotFound       = errors.New("budget not found")
	ErrBudgetExists         = errors.New("budget for this category and month already exists")
	ErrRecurringNotFound    = errors.New("recurring rule not found")
	ErrRuleEnded            = errors.New("recurring rule has ended")
	ErrTagNotFound          = errors.New("tag not found")
	ErrTooManyTags          = errors.New("too many tags")
	ErrRateNotFound         = errors.New("exchange rate not found")
	ErrGoalNotFound         = errors.New("savings goal not found")
	ErrContributionNotFound = errors.New("goal contribution not found")
	ErrGoalArchived         = errors.New("savings goal is archived")
	ErrDuplicateLink        = errors.New("transfer already linked")
	ErrDebtNotFound         = errors.New("debt not found")
	ErrDebtSettled          = errors.New("debt already settled")
	ErrOverpayment          = errors.New("payment exceeds remaining amount")
	ErrBillNotFound         = errors.New("bill not found")
	ErrBillDone             = errors.New("bill is done")
	ErrMemberNotFound       = errors.New("account member not found")
	ErrInvalidRole          = errors.New("role must be viewer or editor")
	ErrInvalidFrequency     = errors.New("invalid frequency")
	ErrDuplicateImport      = errors.New("transaction already imported")
	ErrRateExists           = errors.New("exchange rate for this pair and date already exists")
	ErrRateUnavailable      = errors.New("no exchange rate available for this currency pair")
	ErrMemberExists         = errors.New("user is already a member of this account")
	ErrUserNotFound         = errors.New("user not found")
	// ErrForbidden: user anggota akun bersama tetapi role-nya tidak cukup.
	// Non-anggota tetap mendapat *NotFound (anti IDOR).
	ErrForbidden = errors.New("insufficient role for this shared account")

	ErrInvalidTimezone  = errors.New("invalid timezone")
	ErrInvalidWeekStart = errors.New("week start must be between 0 and 6")
)

// ValidationError dipakai untuk invariant yang punya detail per field.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }
