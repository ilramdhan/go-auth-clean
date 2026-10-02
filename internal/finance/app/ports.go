// Package app berisi use case bounded context finance. Batas DB transaction
// diputuskan di sini (lewat port TxManager); repository hanya ikut tx dari ctx.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

// TxManager menjalankan fn dalam satu DB transaction (nested call join tx luar).
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type Clock interface {
	Now() time.Time
}

// IDGenerator membuat ID baru (UUIDv7 time-ordered di produksi).
type IDGenerator interface {
	NewID() (uuid.UUID, error)
}

// UUIDv7 adalah IDGenerator default.
type UUIDv7 struct{}

func (UUIDv7) NewID() (uuid.UUID, error) { return uuid.NewV7() }

// IdempotencyStore menyimpan hasil request POST per (user, key).
// Claim & Complete dipanggil di dalam tx yang sama dengan use case sehingga
// bila use case gagal, klaim key ikut di-rollback.
type IdempotencyStore interface {
	// Claim mengembalikan response tersimpan (replay) bila key sudah selesai,
	// atau nil bila key baru berhasil diklaim.
	Claim(ctx context.Context, rec IdempotencyRecord) (*StoredResponse, error)
	Complete(ctx context.Context, userID uuid.UUID, key string, resp StoredResponse) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// IdempotencyRecord adalah klaim baru.
type IdempotencyRecord struct {
	UserID      uuid.UUID
	Key         string
	Method      string
	Path        string
	RequestHash []byte
	Now         time.Time
	ExpiresAt   time.Time
}

// StoredResponse adalah response yang disimpan untuk replay.
type StoredResponse struct {
	Status int
	Body   []byte
}

// Error idempotency (dipetakan di adapter http).
var (
	ErrIdempotencyKeyReused   = errors.New("idempotency key reused with different request")
	ErrIdempotencyInProgress  = errors.New("idempotency key is still being processed")
	ErrIdempotencyKeyRequired = errors.New("idempotency key is required")
	ErrInvalidIdempotencyKey  = errors.New("invalid idempotency key")
)

// BudgetAlert adalah event BudgetThresholdReached (sekali per budget per level).
type BudgetAlert struct {
	UserID   uuid.UUID
	BudgetID uuid.UUID
	Level    string // warning | exceeded
	Spent    int64
	Amount   int64
}

// BudgetNotifier menerima alert budget. Dipanggil di dalam tx: implementasi
// sebaiknya hanya mengantre (outbox/log), bukan I/O lambat.
type BudgetNotifier interface {
	BudgetThresholdReached(ctx context.Context, a BudgetAlert)
}

type noopNotifier struct{}

func (noopNotifier) BudgetThresholdReached(context.Context, BudgetAlert) {}
func (noopNotifier) BillNotice(context.Context, BillNotice)              {}

// BillNotice adalah event pengingat tagihan.
type BillNotice struct {
	UserID   uuid.UUID
	BillID   uuid.UUID
	Name     string
	Kind     string // due_soon | overdue
	DueDate  time.Time
	Amount   string // major unit, mis. "150000"
	Currency string
}

// Jenis BillNotice.
const (
	BillDueSoon = "due_soon"
	BillOverdue = "overdue"
)

// BillNotifier menerima pengingat tagihan dari BillWorker (dipanggil setelah
// commit; implementasi sebaiknya cepat: mengantre/log).
type BillNotifier interface {
	BillNotice(ctx context.Context, n BillNotice)
}

// UserDirectory mencari user di bounded context lain (auth) tanpa finance
// meng-import package-nya. LookupByEmail wajib mengembalikan ErrUserNotFound
// (boleh di-wrap) bila email tidak terdaftar.
type UserDirectory interface {
	LookupByEmail(ctx context.Context, email string) (uuid.UUID, error)
}

// ErrUserNotFound dipakai implementasi UserDirectory.
var ErrUserNotFound = domain.ErrUserNotFound
