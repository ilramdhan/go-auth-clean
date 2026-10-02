package app

import (
	"context"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

type UpdateTagInput struct {
	UserID uuid.UUID
	ID     uuid.UUID
	Name   *string
	Color  *string
}

func (s *Service) CreateTag(ctx context.Context, userID uuid.UUID, name, color string) (*domain.Tag, error) {
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	t, err := domain.NewTag(id, userID, name, color, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.tags.Create(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) ListTags(ctx context.Context, userID uuid.UUID) ([]*domain.Tag, error) {
	return s.tags.List(ctx, userID)
}

func (s *Service) UpdateTag(ctx context.Context, in UpdateTagInput) (*domain.Tag, error) {
	t, err := s.tags.Get(ctx, in.UserID, in.ID)
	if err != nil {
		return nil, err
	}
	if err := t.Update(in.Name, in.Color, s.now()); err != nil {
		return nil, err
	}
	if err := s.tags.Update(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// DeleteTag menghapus tag; relasi ke transaksi ikut hilang.
func (s *Service) DeleteTag(ctx context.Context, userID, id uuid.UUID) error {
	return s.tags.Delete(ctx, userID, id)
}

// SetTransactionTags mengganti seluruh tag transaksi (maks 10). Tag milik user
// lain / tidak ada -> ErrTagNotFound (IDOR = 404).
func (s *Service) SetTransactionTags(ctx context.Context, userID, txID uuid.UUID, tagIDs []uuid.UUID) ([]*domain.Tag, error) {
	var out []*domain.Tag
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.transactions.Get(ctx, userID, txID); err != nil {
			return err
		}
		tags, err := s.attachTags(ctx, userID, txID, tagIDs)
		out = tags
		return err
	})
	return out, err
}

func (s *Service) attachTags(ctx context.Context, userID, txID uuid.UUID, tagIDs []uuid.UUID) ([]*domain.Tag, error) {
	ids, err := domain.NormalizeTagIDs(tagIDs)
	if err != nil {
		return nil, err
	}
	tags, err := s.tags.GetMany(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	if len(tags) != len(ids) {
		return nil, domain.ErrTagNotFound
	}
	if err := s.tags.SetForTransaction(ctx, txID, ids); err != nil {
		return nil, err
	}
	return tags, nil
}

// TransactionTags memuat tag untuk banyak transaksi dalam satu query (anti N+1).
func (s *Service) TransactionTags(ctx context.Context, userID uuid.UUID, txIDs []uuid.UUID) (map[uuid.UUID][]*domain.Tag, error) {
	return s.tags.ListForTransactions(ctx, userID, txIDs)
}
