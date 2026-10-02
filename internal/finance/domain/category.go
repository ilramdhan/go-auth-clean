package domain

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FeeCategoryID adalah kategori system "Biaya Admin" (seed migration) yang
// dipakai untuk mencatat biaya transfer sebagai expense.
var FeeCategoryID = uuid.MustParse("01920000-0000-7000-8000-000000000108")

var (
	colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	iconPattern  = regexp.MustCompile(`^[a-z0-9-]{1,30}$`)
)

// Category adalah kategori income/expense. userID nil = kategori system.
type Category struct {
	id        uuid.UUID
	userID    *uuid.UUID
	parentID  *uuid.UUID
	typ       TxType
	name      string
	icon      string
	color     string
	createdAt time.Time
	updatedAt time.Time
}

// NewCategoryParams adalah input kategori custom milik user.
type NewCategoryParams struct {
	ID     uuid.UUID
	UserID uuid.UUID
	// Parent opsional; harus terlihat oleh user (dicek repository).
	Parent *Category
	Type   TxType
	Name   string
	Icon   string
	Color  string
	Now    time.Time
}

// NewCategory membuat kategori custom. Parent hanya boleh satu level dan
// bertipe sama.
func NewCategory(p NewCategoryParams) (*Category, error) {
	if _, err := ParseTxType(string(p.Type)); err != nil {
		return nil, err
	}
	name, err := normalizeName("name", p.Name)
	if err != nil {
		return nil, err
	}
	icon, color, err := validateLook(p.Icon, p.Color)
	if err != nil {
		return nil, err
	}
	c := &Category{id: p.ID, typ: p.Type, name: name, icon: icon, color: color,
		createdAt: p.Now.UTC(), updatedAt: p.Now.UTC()}
	uid := p.UserID
	c.userID = &uid
	if p.Parent != nil {
		if !p.Parent.VisibleTo(p.UserID) {
			return nil, ErrCategoryNotFound
		}
		if p.Parent.parentID != nil {
			return nil, ErrCategoryNestingTooDeep
		}
		if p.Parent.typ != p.Type {
			return nil, ErrCategoryTypeMismatch
		}
		pid := p.Parent.id
		c.parentID = &pid
	}
	return c, nil
}

// CategoryState adalah snapshot untuk rehydrate dari database.
type CategoryState struct {
	ID        uuid.UUID
	UserID    *uuid.UUID
	ParentID  *uuid.UUID
	Type      TxType
	Name      string
	Icon      string
	Color     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RehydrateCategory hanya dipakai repository.
func RehydrateCategory(s CategoryState) *Category {
	return &Category{id: s.ID, userID: s.UserID, parentID: s.ParentID, typ: s.Type,
		name: s.Name, icon: s.Icon, color: s.Color, createdAt: s.CreatedAt, updatedAt: s.UpdatedAt}
}

func (c *Category) ID() uuid.UUID        { return c.id }
func (c *Category) UserID() *uuid.UUID   { return c.userID }
func (c *Category) ParentID() *uuid.UUID { return c.parentID }
func (c *Category) Type() TxType         { return c.typ }
func (c *Category) Name() string         { return c.name }
func (c *Category) Icon() string         { return c.icon }
func (c *Category) Color() string        { return c.color }
func (c *Category) CreatedAt() time.Time { return c.createdAt }
func (c *Category) UpdatedAt() time.Time { return c.updatedAt }
func (c *Category) IsSystem() bool       { return c.userID == nil }

// VisibleTo: kategori system terlihat semua user, custom hanya pemiliknya.
func (c *Category) VisibleTo(userID uuid.UUID) bool {
	return c.userID == nil || *c.userID == userID
}

// EnsureModifiableBy menolak perubahan kategori system / milik user lain.
func (c *Category) EnsureModifiableBy(userID uuid.UUID) error {
	if c.IsSystem() {
		return ErrCategoryReadOnly
	}
	if *c.userID != userID {
		return ErrCategoryNotFound
	}
	return nil
}

// CategoryUpdate berisi perubahan opsional (nil = tidak diubah).
type CategoryUpdate struct {
	Name  *string
	Icon  *string
	Color *string
	Now   time.Time
}

// Update mengubah nama/ikon/warna kategori custom.
func (c *Category) Update(userID uuid.UUID, u CategoryUpdate) error {
	if err := c.EnsureModifiableBy(userID); err != nil {
		return err
	}
	name, icon, color := c.name, c.icon, c.color
	if u.Name != nil {
		n, err := normalizeName("name", *u.Name)
		if err != nil {
			return err
		}
		name = n
	}
	if u.Icon != nil {
		icon = *u.Icon
	}
	if u.Color != nil {
		color = *u.Color
	}
	icon, color, err := validateLook(icon, color)
	if err != nil {
		return err
	}
	c.name, c.icon, c.color, c.updatedAt = name, icon, color, u.Now.UTC()
	return nil
}

// ValidateReassignTarget memastikan kategori tujuan reassign valid.
func (c *Category) ValidateReassignTarget(userID uuid.UUID, target *Category) error {
	if target == nil || target.id == c.id || !target.VisibleTo(userID) || target.typ != c.typ {
		return ErrInvalidReassignTarget
	}
	return nil
}

func validateLook(icon, color string) (string, string, error) {
	icon = strings.TrimSpace(icon)
	color = strings.TrimSpace(color)
	if icon != "" && !iconPattern.MatchString(icon) {
		return "", "", &ValidationError{Field: "icon", Reason: "must match [a-z0-9-], max 30 characters"}
	}
	if color != "" && !colorPattern.MatchString(color) {
		return "", "", &ValidationError{Field: "color", Reason: "must be a hex color like #1A2B3C"}
	}
	return icon, strings.ToUpper(color), nil
}
