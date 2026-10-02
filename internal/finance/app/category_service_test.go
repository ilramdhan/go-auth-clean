package app

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

func TestCategoryService(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	parent, err := f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "Hobi", Color: "#112233"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "Game", ParentID: ptr(parent.ID())})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "Deep", ParentID: ptr(child.ID())})
	wantErr(t, err, domain.ErrCategoryNestingTooDeep)
	_, err = f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "x", Name: "a"})
	wantErr(t, err, domain.ErrInvalidTxType)
	_, err = f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "a", ParentID: ptr(uuid.New())})
	wantErr(t, err, domain.ErrCategoryNotFound)
	_, err = f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: ""})
	if err == nil {
		t.Fatal("nama kosong")
	}
	f.ids.err = errBoom
	_, err = f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "a"})
	wantErr(t, err, errBoom)
	f.ids.err = nil
	f.db.failOn["categories.create"] = errBoom
	_, err = f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "a"})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "categories.create")

	tree, err := f.svc.ListCategories(ctx, f.user, "expense")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range tree {
		if n.Category.ID() == parent.ID() && len(n.Children) == 1 && n.Children[0].ID() == child.ID() {
			found = true
		}
		if n.Category.Type() != domain.TxExpense {
			t.Fatal("filter type")
		}
	}
	if !found || len(tree) != 3 {
		t.Fatalf("tree = %d", len(tree))
	}
	_, err = f.svc.ListCategories(ctx, f.user, "bad")
	wantErr(t, err, domain.ErrInvalidTxType)
	other, _ := f.svc.ListCategories(ctx, uuid.New(), "")
	if len(other) != 3 {
		t.Fatalf("user lain hanya lihat system: %d", len(other))
	}
	f.db.failOn["categories.list"] = errBoom
	_, err = f.svc.ListCategories(ctx, f.user, "")
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "categories.list")

	if c, err := f.svc.GetCategory(ctx, f.user, child.ID()); err != nil || c.Name() != "Game" {
		t.Fatal(err)
	}
	up, err := f.svc.UpdateCategory(ctx, UpdateCategoryInput{UserID: f.user, ID: child.ID(), Name: ptr("Games")})
	if err != nil || up.Name() != "Games" {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateCategory(ctx, UpdateCategoryInput{UserID: f.user, ID: f.expense.ID(), Name: ptr("x")})
	wantErr(t, err, domain.ErrCategoryReadOnly)
	_, err = f.svc.UpdateCategory(ctx, UpdateCategoryInput{UserID: uuid.New(), ID: child.ID()})
	wantErr(t, err, domain.ErrCategoryNotFound)
	f.db.failOn["categories.update"] = errBoom
	_, err = f.svc.UpdateCategory(ctx, UpdateCategoryInput{UserID: f.user, ID: child.ID()})
	wantErr(t, err, errBoom)
}

func TestDeleteCategory(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	parent, _ := f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "Hobi"})
	child, _ := f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "expense", Name: "Game", ParentID: ptr(parent.ID())})
	inc, _ := f.svc.CreateCategory(ctx, CreateCategoryInput{UserID: f.user, Type: "income", Name: "Jual"})
	acc := f.account(t, "cash", "100")
	tx, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: acc.ID(), CategoryID: child.ID(),
		Type: "expense", Amount: "10", Date: today})
	if err != nil {
		t.Fatal(err)
	}
	wantErr(t, f.svc.DeleteCategory(ctx, f.user, f.expense.ID(), nil), domain.ErrCategoryReadOnly)
	wantErr(t, f.svc.DeleteCategory(ctx, f.user, parent.ID(), nil), domain.ErrCategoryHasChildren)
	wantErr(t, f.svc.DeleteCategory(ctx, f.user, child.ID(), nil), domain.ErrCategoryInUse)
	wantErr(t, f.svc.DeleteCategory(ctx, f.user, child.ID(), ptr(uuid.New())), domain.ErrInvalidReassignTarget)
	wantErr(t, f.svc.DeleteCategory(ctx, f.user, child.ID(), ptr(inc.ID())), domain.ErrInvalidReassignTarget)
	wantErr(t, f.svc.DeleteCategory(ctx, uuid.New(), child.ID(), nil), domain.ErrCategoryNotFound)
	f.db.failOn["categories.reassign"] = errBoom
	wantErr(t, f.svc.DeleteCategory(ctx, f.user, child.ID(), ptr(f.expense.ID())), errBoom)
	delete(f.db.failOn, "categories.reassign")
	if err := f.svc.DeleteCategory(ctx, f.user, child.ID(), ptr(f.expense.ID())); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.GetTransaction(ctx, f.user, tx.ID())
	if got.CategoryID() != f.expense.ID() {
		t.Fatal("transaksi harus dipindah")
	}
	if err := f.svc.DeleteCategory(ctx, f.user, parent.ID(), nil); err != nil {
		t.Fatalf("parent tanpa anak: %v", err)
	}
}
