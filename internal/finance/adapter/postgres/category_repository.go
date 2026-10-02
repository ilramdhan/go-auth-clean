package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/domain"
)

var _ domain.CategoryRepository = (*CategoryRepository)(nil)

type CategoryRepository struct{ base }

func NewCategoryRepository(db *pgxpool.Pool) *CategoryRepository {
	return &CategoryRepository{base{db}}
}

const categoryCols = `id, user_id, parent_id, type, name, icon, color, created_at, updated_at`

func scanCategory(row pgx.Row) (*domain.Category, error) {
	var (
		s   domain.CategoryState
		typ string
	)
	err := row.Scan(&s.ID, &s.UserID, &s.ParentID, &typ, &s.Name, &s.Icon, &s.Color, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrCategoryNotFound
	}
	if err != nil {
		return nil, err
	}
	s.Type = domain.TxType(typ)
	return domain.RehydrateCategory(s), nil
}

func (r *CategoryRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Category, error) {
	q := `SELECT ` + categoryCols + ` FROM categories
		WHERE id = $1 AND (user_id = $2 OR user_id IS NULL) AND deleted_at IS NULL`
	c, err := scanCategory(r.conn(ctx).QueryRow(ctx, q, id, userID))
	if err != nil && !errors.Is(err, domain.ErrCategoryNotFound) {
		return nil, fmt.Errorf("categoryRepo.Get: %w", err)
	}
	return c, err
}

func (r *CategoryRepository) List(ctx context.Context, userID uuid.UUID, typ *domain.TxType) ([]*domain.Category, error) {
	q := `SELECT ` + categoryCols + ` FROM categories
		WHERE (user_id = $1 OR user_id IS NULL) AND deleted_at IS NULL
		  AND ($2::text IS NULL OR type = $2)
		ORDER BY type, (user_id IS NOT NULL), name, id`
	var t *string
	if typ != nil {
		s := string(*typ)
		t = &s
	}
	rows, err := r.conn(ctx).Query(ctx, q, userID, t)
	if err != nil {
		return nil, fmt.Errorf("categoryRepo.List: %w", err)
	}
	defer rows.Close()
	out := []*domain.Category{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, fmt.Errorf("categoryRepo.List scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CategoryRepository) Create(ctx context.Context, c *domain.Category) error {
	const q = `INSERT INTO categories (` + categoryCols + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.conn(ctx).Exec(ctx, q, c.ID(), c.UserID(), c.ParentID(), string(c.Type()), c.Name(),
		c.Icon(), c.Color(), c.CreatedAt(), c.UpdatedAt())
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrDuplicateName
	}
	if err != nil {
		return fmt.Errorf("categoryRepo.Create: %w", err)
	}
	return nil
}

// Update hanya untuk kategori milik user (system tidak pernah ter-update).
func (r *CategoryRepository) Update(ctx context.Context, c *domain.Category) error {
	if c.UserID() == nil {
		return domain.ErrCategoryReadOnly
	}
	const q = `UPDATE categories SET name = $3, icon = $4, color = $5, updated_at = $6
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, c.ID(), *c.UserID(), c.Name(), c.Icon(), c.Color(), c.UpdatedAt())
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrDuplicateName
	}
	if err != nil {
		return fmt.Errorf("categoryRepo.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrCategoryNotFound
	}
	return nil
}

func (r *CategoryRepository) exists(ctx context.Context, name, q string, args ...any) (bool, error) {
	var ok bool
	if err := r.conn(ctx).QueryRow(ctx, q, args...).Scan(&ok); err != nil {
		return false, fmt.Errorf("categoryRepo.%s: %w", name, err)
	}
	return ok, nil
}

func (r *CategoryRepository) HasChildren(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	return r.exists(ctx, "HasChildren", `SELECT EXISTS (SELECT 1 FROM categories
		WHERE parent_id = $1 AND user_id = $2 AND deleted_at IS NULL)`, id, userID)
}

func (r *CategoryRepository) IsInUse(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	return r.exists(ctx, "IsInUse", `SELECT EXISTS (SELECT 1 FROM transactions
		WHERE user_id = $1 AND category_id = $2 AND deleted_at IS NULL)`, userID, id)
}

// Reassign memindahkan transaksi aktif user; version dinaikkan agar edit
// bersamaan dari tab lain terdeteksi sebagai konflik.
func (r *CategoryRepository) Reassign(ctx context.Context, userID, from, to uuid.UUID, at time.Time) error {
	const q = `UPDATE transactions SET category_id = $3, updated_at = $4, version = version + 1
		WHERE user_id = $1 AND category_id = $2 AND deleted_at IS NULL`
	if _, err := r.conn(ctx).Exec(ctx, q, userID, from, to, at); err != nil {
		return fmt.Errorf("categoryRepo.Reassign: %w", err)
	}
	return nil
}

func (r *CategoryRepository) SoftDelete(ctx context.Context, userID, id uuid.UUID, at time.Time) error {
	const q = `UPDATE categories SET deleted_at = $3, updated_at = $3
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, id, userID, at)
	if err != nil {
		return fmt.Errorf("categoryRepo.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrCategoryNotFound
	}
	return nil
}
