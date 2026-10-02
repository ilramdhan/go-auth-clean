package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX dipenuhi oleh *pgxpool.Pool, *pgx.Conn, dan pgx.Tx. Repository cukup
// bergantung pada interface ini sehingga bisa berjalan di dalam atau di luar tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TxBeginner adalah sumber transaksi (biasanya *pgxpool.Pool).
type TxBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

type txKey struct{}

// TxFromContext mengembalikan tx aktif di context (jika ada).
func TxFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// Conn mengembalikan tx dari context bila ada, selain itu fallback (pool).
// Pola pemakaian di repository:
//
//	func (r *Repo) Save(ctx context.Context, x *X) error {
//		_, err := database.Conn(ctx, r.pool).Exec(ctx, q, ...)
//		...
//	}
func Conn(ctx context.Context, fallback DBTX) DBTX {
	if tx, ok := TxFromContext(ctx); ok {
		return tx
	}
	return fallback
}

// TxManager menjalankan fungsi di dalam satu DB transaction. Batas transaksi
// diputuskan di app layer (use case), repository hanya "ikut" lewat Conn.
type TxManager struct {
	db   TxBeginner
	opts pgx.TxOptions
}

// NewTxManager membuat TxManager dengan isolation READ COMMITTED (default Postgres).
func NewTxManager(db TxBeginner) *TxManager {
	return &TxManager{db: db, opts: pgx.TxOptions{IsoLevel: pgx.ReadCommitted}}
}

// WithinTx menjalankan fn dalam satu transaction: commit jika fn sukses,
// rollback jika fn mengembalikan error atau panic. Nested call memakai tx yang sama.
//
//	err := txm.WithinTx(ctx, func(ctx context.Context) error {
//		if err := users.UpdatePassword(ctx, ...); err != nil { return err }
//		return sessions.RevokeAllByUser(ctx, ...)
//	})
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := TxFromContext(ctx); ok {
		return fn(ctx) // sudah di dalam tx: join
	}

	tx, err := m.db.BeginTx(ctx, m.opts)
	if err != nil {
		return fmt.Errorf("database.WithinTx begin: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			// WithoutCancel: rollback tetap jalan walau ctx request sudah dibatalkan.
			if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("database.WithinTx rollback: %w", rbErr))
			}
		}
	}()

	err = fn(context.WithValue(ctx, txKey{}, tx))
	if err != nil {
		return err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("database.WithinTx commit: %w", err)
	}
	return nil
}
