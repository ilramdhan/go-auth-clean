package app

import (
	"context"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

type CreateCategoryInput struct {
	UserID   uuid.UUID
	Type     string
	Name     string
	ParentID *uuid.UUID
	Icon     string
	Color    string
}

type UpdateCategoryInput struct {
	UserID uuid.UUID
	ID     uuid.UUID
	Name   *string
	Icon   *string
	Color  *string
}

// CategoryNode adalah kategori parent beserta sub kategorinya.
type CategoryNode struct {
	Category *domain.Category
	Children []*domain.Category
}

// ListCategories mengembalikan pohon kategori (system + milik user).
func (s *Service) ListCategories(ctx context.Context, userID uuid.UUID, typ string) ([]CategoryNode, error) {
	var filter *domain.TxType
	if typ != "" {
		t, err := domain.ParseTxType(typ)
		if err != nil {
			return nil, err
		}
		filter = &t
	}
	cats, err := s.categories.List(ctx, userID, filter)
	if err != nil {
		return nil, err
	}
	return buildTree(cats), nil
}

func buildTree(cats []*domain.Category) []CategoryNode {
	nodes := make([]CategoryNode, 0, len(cats))
	index := map[uuid.UUID]int{}
	for _, c := range cats {
		if c.ParentID() == nil {
			index[c.ID()] = len(nodes)
			nodes = append(nodes, CategoryNode{Category: c, Children: []*domain.Category{}})
		}
	}
	for _, c := range cats {
		if c.ParentID() == nil {
			continue
		}
		if i, ok := index[*c.ParentID()]; ok {
			nodes[i].Children = append(nodes[i].Children, c)
		}
	}
	return nodes
}

func (s *Service) GetCategory(ctx context.Context, userID, id uuid.UUID) (*domain.Category, error) {
	return s.categories.Get(ctx, userID, id)
}

func (s *Service) CreateCategory(ctx context.Context, in CreateCategoryInput) (*domain.Category, error) {
	typ, err := domain.ParseTxType(in.Type)
	if err != nil {
		return nil, err
	}
	var parent *domain.Category
	if in.ParentID != nil {
		if parent, err = s.categories.Get(ctx, in.UserID, *in.ParentID); err != nil {
			return nil, err
		}
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	c, err := domain.NewCategory(domain.NewCategoryParams{
		ID: id, UserID: in.UserID, Parent: parent, Type: typ, Name: in.Name,
		Icon: in.Icon, Color: in.Color, Now: s.now(),
	})
	if err != nil {
		return nil, err
	}
	if err := s.categories.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) UpdateCategory(ctx context.Context, in UpdateCategoryInput) (*domain.Category, error) {
	c, err := s.categories.Get(ctx, in.UserID, in.ID)
	if err != nil {
		return nil, err
	}
	if err := c.Update(in.UserID, domain.CategoryUpdate{Name: in.Name, Icon: in.Icon, Color: in.Color, Now: s.now()}); err != nil {
		return nil, err
	}
	if err := s.categories.Update(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// DeleteCategory menghapus (soft) kategori custom. Kategori yang masih dipakai
// transaksi hanya bisa dihapus bila reassignTo diisi: semua transaksi dipindah
// ke kategori tujuan dalam DB transaction yang sama.
func (s *Service) DeleteCategory(ctx context.Context, userID, id uuid.UUID, reassignTo *uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		c, err := s.categories.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		if err := c.EnsureModifiableBy(userID); err != nil {
			return err
		}
		hasChildren, err := s.categories.HasChildren(ctx, userID, id)
		if err != nil {
			return err
		}
		if hasChildren {
			return domain.ErrCategoryHasChildren
		}
		now := s.now()
		if reassignTo != nil {
			target, err := s.categories.Get(ctx, userID, *reassignTo)
			if err != nil {
				return domain.ErrInvalidReassignTarget
			}
			if err := c.ValidateReassignTarget(userID, target); err != nil {
				return err
			}
			if err := s.categories.Reassign(ctx, userID, id, target.ID(), now); err != nil {
				return err
			}
		} else {
			inUse, err := s.categories.IsInUse(ctx, userID, id)
			if err != nil {
				return err
			}
			if inUse {
				return domain.ErrCategoryInUse
			}
		}
		return s.categories.SoftDelete(ctx, userID, id, now)
	})
}
