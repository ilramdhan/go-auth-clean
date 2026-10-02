package domain

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// Semua method menerima userID secara eksplisit sehingga query selalu di-scope
// per user (anti IDOR). Data milik user lain diperlakukan sebagai "not found".

type SettingsRepository interface {
	// GetOrCreate mengembalikan settings user; bila belum ada, def disimpan lalu dikembalikan.
	GetOrCreate(ctx context.Context, def *UserSettings) (*UserSettings, error)
	// Upsert menyimpan settings dan mengembalikan state tersimpan (created_at asli).
	Upsert(ctx context.Context, s *UserSettings) (*UserSettings, error)
	ListCurrencies(ctx context.Context) ([]CurrencyInfo, error)
}

type AccountRepository interface {
	Create(ctx context.Context, a *Account) error
	Get(ctx context.Context, userID, id uuid.UUID) (*Account, error)
	// GetForUpdate mengunci baris (SELECT ... FOR UPDATE). Wajib di dalam tx.
	GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*Account, error)
	List(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]*Account, error)
	// Update memakai optimistic locking (WHERE version = a.Version()) lalu
	// menaikkan version; gagal ErrVersionConflict bila version berubah.
	Update(ctx context.Context, a *Account) error
	// HasActivity: masih ada transaksi/transfer aktif yang memakai akun ini?
	HasActivity(ctx context.Context, userID, id uuid.UUID) (bool, error)
	SoftDelete(ctx context.Context, userID, id uuid.UUID, at time.Time) error
}

type CategoryRepository interface {
	// Get mengembalikan kategori milik user ATAU kategori system.
	Get(ctx context.Context, userID, id uuid.UUID) (*Category, error)
	List(ctx context.Context, userID uuid.UUID, typ *TxType) ([]*Category, error)
	Create(ctx context.Context, c *Category) error
	Update(ctx context.Context, c *Category) error
	HasChildren(ctx context.Context, userID, id uuid.UUID) (bool, error)
	IsInUse(ctx context.Context, userID, id uuid.UUID) (bool, error)
	// Reassign memindahkan transaksi user dari kategori from ke to (di dalam tx).
	Reassign(ctx context.Context, userID, from, to uuid.UUID, at time.Time) error
	SoftDelete(ctx context.Context, userID, id uuid.UUID, at time.Time) error
}

type TransactionRepository interface {
	Create(ctx context.Context, t *Transaction) error
	Get(ctx context.Context, userID, id uuid.UUID) (*Transaction, error)
	// Update memakai optimistic locking seperti AccountRepository.Update.
	Update(ctx context.Context, t *Transaction) error
	// SoftDelete juga memakai optimistic locking (version) agar delete ganda
	// yang bersamaan tidak me-revert saldo dua kali.
	SoftDelete(ctx context.Context, t *Transaction, at time.Time) error
	// List mengembalikan maksimal f.Limit+1 baris (untuk deteksi has_more).
	List(ctx context.Context, userID uuid.UUID, f TransactionFilter) ([]*Transaction, error)
	// CreateOccurrence menyimpan transaksi hasil recurring dengan
	// ON CONFLICT (recurring_rule_id, occurrence_date) DO NOTHING.
	// inserted = false bila kemunculan itu sudah pernah dibuat.
	CreateOccurrence(ctx context.Context, t *Transaction) (inserted bool, err error)
	// ExistingImportHashes mengembalikan hash (hex) yang sudah ada milik user.
	ExistingImportHashes(ctx context.Context, userID uuid.UUID, hashes [][]byte) (map[string]bool, error)
	// Export men-stream semua transaksi sesuai filter (Limit/After diabaikan),
	// terbaru dulu; fn dipanggil per baris sehingga memori konstan.
	Export(ctx context.Context, userID uuid.UUID, f TransactionFilter, fn func(ExportRow) error) error
}

// ExportRow adalah satu baris export CSV (nama akun/kategori/tag sudah di-join).
type ExportRow struct {
	Transaction  *Transaction
	AccountName  string
	CategoryName string
	Tags         []string
}

// TransactionFilter: semua tanggal inklusif; nil/empty = tanpa filter.
type TransactionFilter struct {
	From, To        *time.Time
	Type            *TxType
	AccountIDs      []uuid.UUID
	CategoryIDs     []uuid.UUID
	IncludeChildren bool
	Currency        *money.Currency
	MinAmount       *int64
	MaxAmount       *int64
	Query           string
	// TagName memfilter transaksi yang memiliki tag bernama ini (case-insensitive).
	TagName string
	Limit   int
	After   *PageKey
}

// PageKey adalah posisi keyset (tanggal, id) untuk urutan DESC.
type PageKey struct {
	Date time.Time
	ID   uuid.UUID
}

type TransferRepository interface {
	Create(ctx context.Context, t *Transfer) error
	Get(ctx context.Context, userID, id uuid.UUID) (*Transfer, error)
	Update(ctx context.Context, t *Transfer) error
	SoftDelete(ctx context.Context, t *Transfer, at time.Time) error
	List(ctx context.Context, userID uuid.UUID, f TransferFilter) ([]*Transfer, error)
}

type TransferFilter struct {
	From, To  *time.Time
	AccountID *uuid.UUID
	Limit     int
	After     *PageKey
}

// ReportRepository adalah read model untuk dashboard & laporan.
// Rentang tanggal [from, to) eksklusif di ujung kanan.
type ReportRepository interface {
	TotalBalances(ctx context.Context, userID uuid.UUID) ([]money.Money, error)
	IncomeExpense(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]IncomeExpense, error)
	CategoryBreakdown(ctx context.Context, userID uuid.UUID, typ TxType, cur money.Currency, from, to time.Time) ([]CategoryTotal, error)
	Cashflow(ctx context.Context, userID uuid.UUID, cur money.Currency, g Granularity, from, to time.Time) ([]CashflowPoint, error)
	// BalanceDrifts membandingkan saldo cache dengan hasil hitung ulang.
	// userID nil = semua user (dipakai job rekonsiliasi).
	BalanceDrifts(ctx context.Context, userID *uuid.UUID) ([]BalanceDrift, error)
	// DailyTotals mengelompokkan income/expense per (tanggal, currency) dalam
	// [from, to); dipakai untuk konversi kurs per tanggal transaksi.
	DailyTotals(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]DailyTotal, error)
}

// CurrencyInfo adalah data referensi tabel currencies.
type CurrencyInfo struct {
	Code      string
	Name      string
	MinorUnit int
	Symbol    string
}

// BudgetRepository: semua query di-scope user; budget terhapus = not found.
type BudgetRepository interface {
	// Create gagal ErrBudgetExists bila (user, kategori, bulan) sudah ada.
	Create(ctx context.Context, b *Budget) error
	Get(ctx context.Context, userID, id uuid.UUID) (*Budget, error)
	Update(ctx context.Context, b *Budget) error
	SoftDelete(ctx context.Context, b *Budget, at time.Time) error
	// ListWithSpent mengembalikan budget bulan tsb beserta total expense
	// kategori (termasuk sub kategori) dalam currency budget, satu query.
	ListWithSpent(ctx context.Context, userID uuid.UUID, month time.Time) ([]BudgetSpent, error)
	// Spent menghitung pemakaian satu budget (termasuk sub kategori).
	Spent(ctx context.Context, b *Budget) (int64, error)
}

type TagRepository interface {
	// Create/Update gagal ErrDuplicateName bila nama (case-insensitive) sudah dipakai.
	Create(ctx context.Context, t *Tag) error
	Get(ctx context.Context, userID, id uuid.UUID) (*Tag, error)
	List(ctx context.Context, userID uuid.UUID) ([]*Tag, error)
	Update(ctx context.Context, t *Tag) error
	Delete(ctx context.Context, userID, id uuid.UUID) error
	// GetMany mengembalikan tag milik user dari ids (yang tidak ada diabaikan).
	GetMany(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) ([]*Tag, error)
	// SetForTransaction mengganti seluruh tag transaksi.
	SetForTransaction(ctx context.Context, txID uuid.UUID, tagIDs []uuid.UUID) error
	// ListForTransactions memuat tag banyak transaksi sekaligus (WHERE transaction_id = ANY($1)).
	ListForTransactions(ctx context.Context, userID uuid.UUID, txIDs []uuid.UUID) (map[uuid.UUID][]*Tag, error)
}

type RecurringRepository interface {
	Create(ctx context.Context, r *RecurringRule) error
	Get(ctx context.Context, userID, id uuid.UUID) (*RecurringRule, error)
	// Update memakai optimistic locking (version).
	Update(ctx context.Context, r *RecurringRule) error
	SoftDelete(ctx context.Context, r *RecurringRule, at time.Time) error
	// List keyset (created_at, id) DESC; mengembalikan Limit+1 baris.
	List(ctx context.Context, userID uuid.UUID, limit int, after *PageKey) ([]*RecurringRule, error)
	// ClaimDue mengunci rule aktif dengan next_run_date <= before
	// (FOR UPDATE SKIP LOCKED, urut next_run_date, id). Wajib di dalam tx.
	// exclude = rule yang sudah diproses di putaran ini.
	ClaimDue(ctx context.Context, before time.Time, limit int, exclude []uuid.UUID) ([]*RecurringRule, error)
}

// ===== P2 =====

// ExchangeRateRepository menyimpan kurs manual milik user.
type ExchangeRateRepository interface {
	// Create gagal ErrRateExists bila (base, quote, as_of) sudah ada.
	Create(ctx context.Context, e *ExchangeRate) error
	Get(ctx context.Context, userID, id uuid.UUID) (*ExchangeRate, error)
	Update(ctx context.Context, e *ExchangeRate) error
	Delete(ctx context.Context, userID, id uuid.UUID) error
	// List mengurutkan (base, quote, as_of DESC). Jumlah kurs per user kecil.
	List(ctx context.Context, userID uuid.UUID) ([]*ExchangeRate, error)
}

// GoalWithSaved adalah goal beserta total kontribusinya.
type GoalWithSaved struct {
	Goal  *SavingsGoal
	Saved int64
}

type GoalRepository interface {
	// Create/Update gagal ErrDuplicateName bila nama sudah dipakai.
	Create(ctx context.Context, g *SavingsGoal) error
	Get(ctx context.Context, userID, id uuid.UUID) (*SavingsGoal, error)
	// GetForUpdate mengunci goal (serialisasi kontribusi). Wajib di dalam tx.
	GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*SavingsGoal, error)
	Update(ctx context.Context, g *SavingsGoal) error
	SoftDelete(ctx context.Context, g *SavingsGoal, at time.Time) error
	List(ctx context.Context, userID uuid.UUID) ([]GoalWithSaved, error)
	Saved(ctx context.Context, userID, goalID uuid.UUID) (int64, error)
	// AddContribution gagal ErrDuplicateLink bila transfer sudah ditautkan ke goal ini.
	AddContribution(ctx context.Context, c *GoalContribution) error
	ListContributions(ctx context.Context, userID, goalID uuid.UUID) ([]*GoalContribution, error)
	DeleteContribution(ctx context.Context, userID, goalID, id uuid.UUID) error
}

// DebtWithPaid adalah hutang beserta total pembayarannya.
type DebtWithPaid struct {
	Debt *Debt
	Paid int64
}

type DebtRepository interface {
	Create(ctx context.Context, d *Debt) error
	Get(ctx context.Context, userID, id uuid.UUID) (*Debt, error)
	GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*Debt, error)
	Update(ctx context.Context, d *Debt) error
	SoftDelete(ctx context.Context, d *Debt, at time.Time) error
	// List: status nil = semua.
	List(ctx context.Context, userID uuid.UUID, status *DebtStatus) ([]DebtWithPaid, error)
	Paid(ctx context.Context, userID, debtID uuid.UUID) (int64, error)
	// AddPayment gagal ErrDuplicateLink bila transaksi sudah dipakai pembayaran lain.
	AddPayment(ctx context.Context, p *DebtPayment) error
	ListPayments(ctx context.Context, userID, debtID uuid.UUID) ([]*DebtPayment, error)
	DeletePayment(ctx context.Context, userID, debtID, id uuid.UUID) error
}

type BillRepository interface {
	Create(ctx context.Context, b *Bill) error
	Get(ctx context.Context, userID, id uuid.UUID) (*Bill, error)
	GetForUpdate(ctx context.Context, userID, id uuid.UUID) (*Bill, error)
	Update(ctx context.Context, b *Bill) error
	SoftDelete(ctx context.Context, b *Bill, at time.Time) error
	// List urut next_due_date, id.
	List(ctx context.Context, userID uuid.UUID) ([]*Bill, error)
	// ClaimAttention mengunci (FOR UPDATE SKIP LOCKED) tagihan aktif yang
	// perlu pengingat atau sudah lewat jatuh tempo per tanggal before, belum
	// diproses untuk jatuh tempo saat ini. Wajib di dalam tx.
	ClaimAttention(ctx context.Context, before time.Time, limit int, exclude []uuid.UUID) ([]*Bill, error)
}

// MemberRepository mengelola akses shared wallet.
type MemberRepository interface {
	// Add gagal ErrMemberExists bila user sudah menjadi member.
	Add(ctx context.Context, m *AccountMember) error
	// Get mengembalikan keanggotaan memberID pada akun (ErrMemberNotFound bila tidak ada).
	Get(ctx context.Context, accountID, memberID uuid.UUID) (*AccountMember, error)
	UpdateRole(ctx context.Context, m *AccountMember) error
	Remove(ctx context.Context, accountID, memberID uuid.UUID) error
	List(ctx context.Context, ownerID, accountID uuid.UUID) ([]*AccountMember, error)
	// ListShared mengembalikan akun milik user lain yang dibagikan ke memberID.
	ListShared(ctx context.Context, memberID uuid.UUID) ([]SharedAccount, error)
	// TransactionOwner: pemilik transaksi bila memberID anggota akun transaksi
	// tsb; selain itu ErrTransactionNotFound.
	TransactionOwner(ctx context.Context, memberID, txID uuid.UUID) (uuid.UUID, error)
	// TransferOwner: pemilik transfer bila memberID anggota salah satu akunnya;
	// selain itu ErrTransferNotFound.
	TransferOwner(ctx context.Context, memberID, transferID uuid.UUID) (uuid.UUID, error)
}

type AuditRepository interface {
	Create(ctx context.Context, e *AuditEntry) error
	// List keyset (created_at, id) DESC; mengembalikan Limit+1 baris.
	List(ctx context.Context, userID uuid.UUID, f AuditFilter) ([]*AuditEntry, error)
}

// DailyTotal adalah income/expense satu currency pada satu tanggal (untuk konversi kurs).
type DailyTotal struct {
	Date     time.Time
	Currency money.Currency
	Income   int64
	Expense  int64
}
