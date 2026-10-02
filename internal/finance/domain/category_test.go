package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewCategory(t *testing.T) {
	user := uuid.New()
	parent := sysCat(TxExpense)
	other := uuid.New()
	foreign := RehydrateCategory(CategoryState{ID: uuid.New(), UserID: &other, Type: TxExpense, Name: "f"})
	pid := parent.ID()
	child := RehydrateCategory(CategoryState{ID: uuid.New(), UserID: &user, ParentID: &pid, Type: TxExpense, Name: "c"})

	c, err := NewCategory(NewCategoryParams{ID: uuid.New(), UserID: user, Parent: parent, Type: TxExpense,
		Name: " Kopi ", Icon: "coffee", Color: "#a1b2c3", Now: tNow})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "Kopi" || c.Color() != "#A1B2C3" || c.Icon() != "coffee" || *c.ParentID() != parent.ID() ||
		c.IsSystem() || *c.UserID() != user || c.Type() != TxExpense || c.ID() == uuid.Nil ||
		!c.CreatedAt().Equal(tNow) || !c.UpdatedAt().Equal(tNow) {
		t.Fatalf("state salah")
	}
	base := NewCategoryParams{UserID: user, Type: TxExpense, Name: "x", Now: tNow}
	cases := []struct {
		name string
		mod  func(p *NewCategoryParams)
		err  error
		fld  string
	}{
		{"bad type", func(p *NewCategoryParams) { p.Type = "x" }, ErrInvalidTxType, ""},
		{"empty name", func(p *NewCategoryParams) { p.Name = "" }, nil, "name"},
		{"bad icon", func(p *NewCategoryParams) { p.Icon = "Ikon!" }, nil, "icon"},
		{"bad color", func(p *NewCategoryParams) { p.Color = "red" }, nil, "color"},
		{"foreign parent", func(p *NewCategoryParams) { p.Parent = foreign }, ErrCategoryNotFound, ""},
		{"too deep", func(p *NewCategoryParams) { p.Parent = child }, ErrCategoryNestingTooDeep, ""},
		{"type mismatch", func(p *NewCategoryParams) { p.Parent = sysCat(TxIncome) }, ErrCategoryTypeMismatch, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p := base
			tt.mod(&p)
			_, err := NewCategory(p)
			if tt.fld != "" {
				wantValidation(t, err, tt.fld)
			} else {
				wantErr(t, err, tt.err)
			}
		})
	}
}

func TestCategoryUpdateAndAccess(t *testing.T) {
	user, other := uuid.New(), uuid.New()
	sys := sysCat(TxExpense)
	if !sys.IsSystem() || !sys.VisibleTo(user) {
		t.Fatal("system harus terlihat")
	}
	wantErr(t, sys.EnsureModifiableBy(user), ErrCategoryReadOnly)
	c, _ := NewCategory(NewCategoryParams{ID: uuid.New(), UserID: user, Type: TxExpense, Name: "a", Now: tNow})
	if c.VisibleTo(other) {
		t.Fatal("custom tidak boleh terlihat user lain")
	}
	wantErr(t, c.EnsureModifiableBy(other), ErrCategoryNotFound)
	wantErr(t, c.Update(other, CategoryUpdate{}), ErrCategoryNotFound)
	n, i, col := "Baru", "food", "#000000"
	if err := c.Update(user, CategoryUpdate{Name: &n, Icon: &i, Color: &col, Now: tNow}); err != nil {
		t.Fatal(err)
	}
	if c.Name() != "Baru" || c.Icon() != "food" || c.Color() != "#000000" {
		t.Fatal("update gagal")
	}
	bad, empty := "zz", ""
	wantValidation(t, c.Update(user, CategoryUpdate{Color: &bad}), "color")
	wantValidation(t, c.Update(user, CategoryUpdate{Name: &empty}), "name")
	if c.Color() != "#000000" {
		t.Fatal("state berubah walau gagal")
	}
	// reassign
	wantErr(t, c.ValidateReassignTarget(user, nil), ErrInvalidReassignTarget)
	wantErr(t, c.ValidateReassignTarget(user, c), ErrInvalidReassignTarget)
	wantErr(t, c.ValidateReassignTarget(user, sysCat(TxIncome)), ErrInvalidReassignTarget)
	if err := c.ValidateReassignTarget(user, sys); err != nil {
		t.Fatal(err)
	}
}
