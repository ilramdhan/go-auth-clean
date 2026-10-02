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

var _ domain.TagRepository = (*TagRepository)(nil)

type TagRepository struct{ base }

func NewTagRepository(db *pgxpool.Pool) *TagRepository {
	return &TagRepository{base{db}}
}

const tagCols = `id, user_id, name::text, color, created_at, updated_at`

func scanTag(row pgx.Row, extra ...any) (*domain.Tag, error) {
	var (
		id, userID           uuid.UUID
		name, color          string
		createdAt, updatedAt time.Time
	)
	err := row.Scan(append([]any{&id, &userID, &name, &color, &createdAt, &updatedAt}, extra...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrTagNotFound
	}
	if err != nil {
		return nil, err
	}
	return domain.RehydrateTag(id, userID, name, color, createdAt, updatedAt), nil
}

func (r *TagRepository) Create(ctx context.Context, t *domain.Tag) error {
	_, err := r.conn(ctx).Exec(ctx, `INSERT INTO tags (id, user_id, name, color, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, t.ID(), t.UserID(), t.Name(), t.Color(), t.CreatedAt(), t.UpdatedAt())
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrDuplicateName
	}
	if err != nil {
		return fmt.Errorf("tagRepo.Create: %w", err)
	}
	return nil
}

func (r *TagRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Tag, error) {
	t, err := scanTag(r.conn(ctx).QueryRow(ctx, `SELECT `+tagCols+` FROM tags WHERE id = $1 AND user_id = $2`, id, userID))
	if err != nil && !errors.Is(err, domain.ErrTagNotFound) {
		return nil, fmt.Errorf("tagRepo.Get: %w", err)
	}
	return t, err
}

func (r *TagRepository) query(ctx context.Context, op, q string, args ...any) ([]*domain.Tag, error) {
	rows, err := r.conn(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("tagRepo.%s: %w", op, err)
	}
	defer rows.Close()
	out := []*domain.Tag{}
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, fmt.Errorf("tagRepo.%s scan: %w", op, err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *TagRepository) List(ctx context.Context, userID uuid.UUID) ([]*domain.Tag, error) {
	return r.query(ctx, "List", `SELECT `+tagCols+` FROM tags WHERE user_id = $1 ORDER BY name, id`, userID)
}

func (r *TagRepository) GetMany(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) ([]*domain.Tag, error) {
	if len(ids) == 0 {
		return []*domain.Tag{}, nil
	}
	return r.query(ctx, "GetMany", `SELECT `+tagCols+` FROM tags WHERE user_id = $1 AND id = ANY($2) ORDER BY name, id`, userID, ids)
}

func (r *TagRepository) Update(ctx context.Context, t *domain.Tag) error {
	tag, err := r.conn(ctx).Exec(ctx, `UPDATE tags SET name = $3, color = $4, updated_at = $5
		WHERE id = $1 AND user_id = $2`, t.ID(), t.UserID(), t.Name(), t.Color(), t.UpdatedAt())
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrDuplicateName
	}
	if err != nil {
		return fmt.Errorf("tagRepo.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTagNotFound
	}
	return nil
}

// Delete menghapus tag permanen; relasi transaction_tags ikut terhapus (CASCADE).
func (r *TagRepository) Delete(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := r.conn(ctx).Exec(ctx, `DELETE FROM tags WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("tagRepo.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTagNotFound
	}
	return nil
}

func (r *TagRepository) SetForTransaction(ctx context.Context, txID uuid.UUID, tagIDs []uuid.UUID) error {
	c := r.conn(ctx)
	if _, err := c.Exec(ctx, `DELETE FROM transaction_tags WHERE transaction_id = $1`, txID); err != nil {
		return fmt.Errorf("tagRepo.SetForTransaction delete: %w", err)
	}
	if len(tagIDs) == 0 {
		return nil
	}
	_, err := c.Exec(ctx, `INSERT INTO transaction_tags (transaction_id, tag_id)
		SELECT $1, unnest($2::uuid[])`, txID, tagIDs)
	if err != nil {
		return fmt.Errorf("tagRepo.SetForTransaction insert: %w", err)
	}
	return nil
}

func (r *TagRepository) ListForTransactions(ctx context.Context, userID uuid.UUID, txIDs []uuid.UUID) (map[uuid.UUID][]*domain.Tag, error) {
	out := make(map[uuid.UUID][]*domain.Tag, len(txIDs))
	if len(txIDs) == 0 {
		return out, nil
	}
	rows, err := r.conn(ctx).Query(ctx, `SELECT g.id, g.user_id, g.name::text, g.color, g.created_at, g.updated_at, tt.transaction_id
		FROM transaction_tags tt JOIN tags g ON g.id = tt.tag_id
		WHERE tt.transaction_id = ANY($1) AND g.user_id = $2 ORDER BY g.name, g.id`, txIDs, userID)
	if err != nil {
		return nil, fmt.Errorf("tagRepo.ListForTransactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var txID uuid.UUID
		t, err := scanTag(rows, &txID)
		if err != nil {
			return nil, fmt.Errorf("tagRepo.ListForTransactions scan: %w", err)
		}
		out[txID] = append(out[txID], t)
	}
	return out, rows.Err()
}
