package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

// escapeLike meng-escape wildcard LIKE agar input user diperlakukan literal.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// List memakai keyset (created_at, id) DESC. Pencarian ILIKE substring dibantu
// index trigram (users_email_trgm_idx / users_full_name_trgm_idx).
func (r *UserRepository) List(ctx context.Context, f domain.UserFilter) ([]domain.User, error) {
	var (
		where = []string{"deleted_at IS NULL"}
		args  []any
	)
	add := func(cond string, v ...any) {
		for _, x := range v {
			args = append(args, x)
			cond = strings.Replace(cond, "?", fmt.Sprintf("$%d", len(args)), 1)
		}
		where = append(where, cond)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		p := "%" + escapeLike(q) + "%"
		add(`(email ILIKE ? OR full_name ILIKE ?)`, p, p)
	}
	if f.Status != "" {
		add("status = ?", f.Status)
	}
	if f.After != nil {
		add("(created_at, id) < (?, ?)", f.After.CreatedAt, f.After.ID)
	}
	args = append(args, f.Limit)
	q := `SELECT ` + userColumns + ` FROM users WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(args))
	rows, err := r.conn(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("userRepo.List: %w", err)
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("userRepo.List: %w", err)
		}
		out = append(out, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("userRepo.List: %w", err)
	}
	return out, nil
}

func (r *UserRepository) SetStatus(ctx context.Context, id uuid.UUID, from, to domain.UserStatus, now time.Time) error {
	const q = `UPDATE users SET status = $3, updated_at = $4
		WHERE id = $1 AND status = $2 AND deleted_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, id, from, to, now)
	if err != nil {
		return fmt.Errorf("userRepo.SetStatus: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	// Bedakan "tidak ada" dengan "status sudah berubah".
	if _, err := r.FindByID(ctx, id); err != nil {
		return err
	}
	return domain.ErrInvalidTransition
}
