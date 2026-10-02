package database_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go-auth-clean/internal/platform/database"
	"go-auth-clean/internal/platform/database/dbtest"
)

// ---- fakes ----

// fakeTx meng-embed pgx.Tx (nil) supaya cukup override method yang dipakai.
type fakeTx struct {
	pgx.Tx
	committed  bool
	rolledBack bool
	commitErr  error
}

func (f *fakeTx) Commit(context.Context) error {
	f.committed = true
	return f.commitErr
}

func (f *fakeTx) Rollback(context.Context) error {
	if f.committed {
		return pgx.ErrTxClosed
	}
	f.rolledBack = true
	return nil
}

type fakeBeginner struct {
	tx       *fakeTx
	beginErr error
	begins   int
}

func (b *fakeBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	b.begins++
	if b.beginErr != nil {
		return nil, b.beginErr
	}
	return b.tx, nil
}

type fakeDB struct{ database.DBTX }

func (fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func TestWithinTx(t *testing.T) {
	errBoom := errors.New("boom")

	tests := []struct {
		name         string
		beginErr     error
		commitErr    error
		fnErr        error
		wantErr      error
		wantCommit   bool
		wantRollback bool
	}{
		{name: "success commits", wantCommit: true},
		{name: "fn error rolls back", fnErr: errBoom, wantErr: errBoom, wantRollback: true},
		{name: "begin error", beginErr: errBoom, wantErr: errBoom},
		{name: "commit error", commitErr: errBoom, wantErr: errBoom, wantCommit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTx{commitErr: tt.commitErr}
			b := &fakeBeginner{tx: tx, beginErr: tt.beginErr}
			m := database.NewTxManager(b)

			var sawTx bool
			err := m.WithinTx(context.Background(), func(ctx context.Context) error {
				got, ok := database.TxFromContext(ctx)
				sawTx = ok && got == tx
				return tt.fnErr
			})

			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.beginErr == nil && !sawTx {
				t.Error("fn tidak menerima tx di context")
			}
			if tx.committed != tt.wantCommit {
				t.Errorf("committed = %v, want %v", tx.committed, tt.wantCommit)
			}
			if tx.rolledBack != tt.wantRollback {
				t.Errorf("rolledBack = %v, want %v", tx.rolledBack, tt.wantRollback)
			}
		})
	}
}

func TestWithinTx_NestedJoinsOuter(t *testing.T) {
	b := &fakeBeginner{tx: &fakeTx{}}
	m := database.NewTxManager(b)

	err := m.WithinTx(context.Background(), func(ctx context.Context) error {
		return m.WithinTx(ctx, func(context.Context) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.begins != 1 {
		t.Fatalf("begins = %d, want 1", b.begins)
	}
}

func TestWithinTx_PanicRollsBack(t *testing.T) {
	tx := &fakeTx{}
	m := database.NewTxManager(&fakeBeginner{tx: tx})

	defer func() {
		if recover() == nil {
			t.Fatal("panic harus diteruskan")
		}
		if !tx.rolledBack {
			t.Fatal("tx harus di-rollback saat panic")
		}
	}()
	_ = m.WithinTx(context.Background(), func(context.Context) error { panic("x") })
}

func TestConn(t *testing.T) {
	pool := fakeDB{}
	if got := database.Conn(context.Background(), pool); got != pool {
		t.Fatal("tanpa tx harus mengembalikan fallback")
	}

	tx := &fakeTx{}
	m := database.NewTxManager(&fakeBeginner{tx: tx})
	_ = m.WithinTx(context.Background(), func(ctx context.Context) error {
		if got := database.Conn(ctx, pool); got != tx {
			t.Fatal("di dalam tx harus mengembalikan tx")
		}
		return nil
	})
}

// ---- integration ----

func TestMain(m *testing.M) { dbtest.Main(m) }

func TestWithinTx_Integration(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS tx_probe (v INT)`); err != nil {
		t.Fatal(err)
	}
	dbtest.Truncate(t, pool, "tx_probe")
	m := database.NewTxManager(pool)

	insert := func(ctx context.Context, v int) error {
		_, err := database.Conn(ctx, pool).Exec(ctx, `INSERT INTO tx_probe (v) VALUES ($1)`, v)
		return err
	}

	if err := m.WithinTx(ctx, func(ctx context.Context) error { return insert(ctx, 1) }); err != nil {
		t.Fatal(err)
	}
	errBoom := errors.New("boom")
	err := m.WithinTx(ctx, func(ctx context.Context) error {
		if err := insert(ctx, 2); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tx_probe`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (insert kedua harus di-rollback)", n)
	}
}
