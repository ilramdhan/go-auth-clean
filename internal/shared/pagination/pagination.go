// Package pagination menyediakan cursor (keyset) pagination yang opaque.
// Cursor = base64url(JSON {t, id, f}) dengan t = sort key waktu, id = UUIDv7
// tie-breaker, f = hash filter (opsional) agar cursor tidak dipakai di filter lain.
//
// Query keyset (urut DESC):
//
//	WHERE user_id = $1 AND (created_at, id) < ($2, $3) ORDER BY created_at DESC, id DESC LIMIT $4+1
//
// Ambil limit+1 baris; bila hasil > limit berarti has_more = true (lihat Page).
package pagination

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
	// maxCursorLen mencegah decode string raksasa dari client.
	maxCursorLen = 512
)

// ErrInvalidCursor dipetakan adapter http ke 400 INVALID_CURSOR.
var ErrInvalidCursor = errors.New("invalid cursor")

// Cursor adalah posisi terakhir yang sudah dikirim ke client.
type Cursor struct {
	Time time.Time
	ID   uuid.UUID
	// Filter adalah hash dari parameter filter (lihat FilterHash); kosong = tidak dicek.
	Filter string
}

type wireCursor struct {
	T string `json:"t"`
	I string `json:"i"`
	F string `json:"f,omitempty"`
}

// Encode menghasilkan string opaque (base64url tanpa padding).
func (c Cursor) Encode() string {
	b, _ := json.Marshal(wireCursor{T: c.Time.UTC().Format(time.RFC3339Nano), I: c.ID.String(), F: c.Filter})
	return base64.RawURLEncoding.EncodeToString(b)
}

// Decode mem-parsing cursor. String kosong mengembalikan nil (halaman pertama).
// wantFilter: hash filter request saat ini; mismatch -> ErrInvalidCursor.
func Decode(s, wantFilter string) (*Cursor, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // nil cursor = halaman pertama, bukan error
	}
	if len(s) > maxCursorLen {
		return nil, ErrInvalidCursor
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var w wireCursor
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, w.T)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	id, err := uuid.Parse(w.I)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	if w.F != wantFilter {
		return nil, ErrInvalidCursor
	}
	return &Cursor{Time: t.UTC(), ID: id, Filter: w.F}, nil
}

// FilterHash membuat hash pendek dan stabil dari parameter filter (urutan penting).
// Contoh: FilterHash("account="+acc, "type="+typ, "from="+from).
func FilterHash(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:8])
}

// ClampLimit menormalkan limit: <=0 -> DefaultLimit, >MaxLimit -> MaxLimit.
func ClampLimit(n int) int {
	switch {
	case n <= 0:
		return DefaultLimit
	case n > MaxLimit:
		return MaxLimit
	default:
		return n
	}
}

// ParseLimit membaca query param limit (string kosong -> default).
// Nilai non-angka -> error agar client tahu inputnya salah.
func ParseLimit(s string) (int, error) {
	if s == "" {
		return DefaultLimit, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, errors.New("limit harus bilangan bulat positif")
	}
	return ClampLimit(n), nil
}

// PageMeta adalah isi "meta" pada response list.
type PageMeta struct {
	NextCursor string `json:"next_cursor,omitempty" example:"eyJ0IjoiMjAyNi0wOS0zMFQwMDowMDowMFoiLCJpIjoiLi4uIn0"`
	HasMore    bool   `json:"has_more" example:"true"`
	Limit      int    `json:"limit" example:"20"`
}

// Page memotong hasil query limit+1 menjadi satu halaman dan membangun meta.
// keyOf mengambil (time, id) dari item terakhir untuk cursor berikutnya.
func Page[T any](items []T, limit int, filter string, keyOf func(T) (time.Time, uuid.UUID)) ([]T, PageMeta) {
	meta := PageMeta{Limit: limit}
	if len(items) > limit {
		items = items[:limit]
		meta.HasMore = true
		t, id := keyOf(items[len(items)-1])
		meta.NextCursor = Cursor{Time: t, ID: id, Filter: filter}.Encode()
	}
	if items == nil {
		items = []T{} // JSON "[]" bukan "null"
	}
	return items, meta
}
