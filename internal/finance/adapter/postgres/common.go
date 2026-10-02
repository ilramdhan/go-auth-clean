// Package postgres berisi implementasi repository finance menggunakan pgx.
// Semua query di-scope per user_id dan memakai parameter ($n), tidak pernah
// string concat untuk nilai dari user.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/platform/database"
	"go-auth-clean/internal/shared/money"
)

const (
	pgUniqueViolation = "23505"
	pgCheckViolation  = "23514"
)

// base menyimpan pool dan helper conn (tx dari ctx bila ada).
type base struct{ db *pgxpool.Pool }

func (b base) conn(ctx context.Context) database.DBTX { return database.Conn(ctx, b.db) }

func pgCode(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	return ""
}

func currency(code string) (money.Currency, error) {
	c, err := money.ParseCurrency(strings.TrimSpace(code))
	if err != nil {
		return money.Currency{}, fmt.Errorf("unknown currency %q in database: %w", code, err)
	}
	return c, nil
}

// escapeLike meng-escape karakter wildcard LIKE agar input user diperlakukan literal.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// extraRow menambahkan kolom tambahan di akhir Scan (mis. role pada join).
type extraRow struct {
	row   interface{ Scan(dest ...any) error }
	extra []any
}

func (e extraRow) Scan(dest ...any) error { return e.row.Scan(append(dest, e.extra...)...) }
