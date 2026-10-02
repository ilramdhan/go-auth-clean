package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	// MaxTagNameLength adalah panjang maksimal nama tag (rune).
	MaxTagNameLength = 30
	// MaxTagsPerTransaction membatasi jumlah tag pada satu transaksi.
	MaxTagsPerTransaction = 10
)

var tagColorRe = regexp.MustCompile(`^#[0-9A-F]{6}$`)

// Tag adalah label bebas milik user yang bisa ditempel ke banyak transaksi.
type Tag struct {
	id        uuid.UUID
	userID    uuid.UUID
	name      string
	color     string
	createdAt time.Time
	updatedAt time.Time
}

func NewTag(id, userID uuid.UUID, name, color string, now time.Time) (*Tag, error) {
	n, err := normalizeTagName(name)
	if err != nil {
		return nil, err
	}
	c, err := normalizeColor(color)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	return &Tag{id: id, userID: userID, name: n, color: c, createdAt: now, updatedAt: now}, nil
}

// Update mengganti nama/warna (nil = tidak diubah).
func (t *Tag) Update(name, color *string, now time.Time) error {
	n, c := t.name, t.color
	var err error
	if name != nil {
		if n, err = normalizeTagName(*name); err != nil {
			return err
		}
	}
	if color != nil {
		if c, err = normalizeColor(*color); err != nil {
			return err
		}
	}
	t.name, t.color, t.updatedAt = n, c, now.UTC()
	return nil
}

func normalizeTagName(s string) (string, error) {
	name := strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(s), "#")), " ")
	n := utf8.RuneCountInString(name)
	if n == 0 {
		return "", &ValidationError{Field: "name", Reason: "must not be empty"}
	}
	if n > MaxTagNameLength {
		return "", &ValidationError{Field: "name", Reason: "max 30 characters"}
	}
	return name, nil
}

func normalizeColor(s string) (string, error) {
	c := strings.ToUpper(strings.TrimSpace(s))
	if c != "" && !tagColorRe.MatchString(c) {
		return "", &ValidationError{Field: "color", Reason: "must be #RRGGBB"}
	}
	return c, nil
}

// NormalizeTagIDs membuang duplikat dan memeriksa batas jumlah tag.
func NormalizeTagIDs(ids []uuid.UUID) ([]uuid.UUID, error) {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) > MaxTagsPerTransaction {
		return nil, ErrTooManyTags
	}
	return out, nil
}

func RehydrateTag(id, userID uuid.UUID, name, color string, createdAt, updatedAt time.Time) *Tag {
	return &Tag{id: id, userID: userID, name: name, color: color, createdAt: createdAt, updatedAt: updatedAt}
}

func (t *Tag) ID() uuid.UUID        { return t.id }
func (t *Tag) UserID() uuid.UUID    { return t.userID }
func (t *Tag) Name() string         { return t.name }
func (t *Tag) Color() string        { return t.color }
func (t *Tag) CreatedAt() time.Time { return t.createdAt }
func (t *Tag) UpdatedAt() time.Time { return t.updatedAt }
