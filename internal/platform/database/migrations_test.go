package database_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // driver pgx5://
	_ "github.com/golang-migrate/migrate/v4/source/file"     // source file://
	"github.com/jackc/pgx/v5"

	"go-auth-clean/internal/platform/database/dbtest"
)

// TestMigrations_UpDownUp menjalankan siklus penuh 1→N→0→N di PostgreSQL
// kosong (container khusus), satu langkah demi satu langkah, untuk memastikan
// setiap file .down.sql benar-benar membalik .up.sql pasangannya.
func TestMigrations_UpDownUp(t *testing.T) {
	dsn := dbtest.NewEmptyDSN(t)
	dir, err := dbtest.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	total := countUpMigrations(t, dir)

	m, err := migrate.New("file://"+filepath.ToSlash(dir), strings.Replace(dsn, "postgres://", "pgx5://", 1))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })

	ctx := t.Context()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	stepAll(t, m, +1, total)
	assertVersion(t, m, uint(total))
	first := schemaFingerprint(t, conn)
	if len(first) == 0 {
		t.Fatal("skema kosong setelah up")
	}

	stepAll(t, m, -1, total)
	if _, _, err := m.Version(); !errors.Is(err, migrate.ErrNilVersion) {
		t.Fatalf("setelah down semua, version err = %v; want ErrNilVersion", err)
	}
	if left := schemaFingerprint(t, conn); len(left) != 0 {
		t.Fatalf("objek tersisa setelah down semua:\n%s", strings.Join(left, "\n"))
	}

	if err := m.Up(); err != nil {
		t.Fatalf("up ulang: %v", err)
	}
	assertVersion(t, m, uint(total))
	if second := schemaFingerprint(t, conn); strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Fatalf("skema setelah up kedua berbeda dari up pertama (%d vs %d objek)", len(first), len(second))
	}
}

func countUpMigrations(t *testing.T, dir string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("tidak ada migrasi di %s: %v", dir, err)
	}
	for _, f := range files {
		down := strings.TrimSuffix(f, ".up.sql") + ".down.sql"
		if _, err := os.Stat(down); err != nil {
			t.Fatalf("pasangan down hilang untuk %s", filepath.Base(f))
		}
	}
	return len(files)
}

// stepAll menjalankan migrasi satu per satu supaya error menunjuk versi yang tepat.
func stepAll(t *testing.T, m *migrate.Migrate, dir, n int) {
	t.Helper()
	for i := range n {
		if err := m.Steps(dir); err != nil {
			v, dirty, _ := m.Version()
			t.Fatalf("step %d (dir %+d) gagal di version %d dirty=%v: %v", i+1, dir, v, dirty, err)
		}
	}
}

func assertVersion(t *testing.T, m *migrate.Migrate, want uint) {
	t.Helper()
	v, dirty, err := m.Version()
	if err != nil || dirty || v != want {
		t.Fatalf("version = %d dirty=%v err=%v; want %d clean", v, dirty, err, want)
	}
}

// schemaFingerprint mendaftar objek user di schema public (tabel, index,
// sequence, view, tipe, fungsi, constraint) kecuali milik extension dan tabel
// schema_migrations. Kosong = database bersih.
func schemaFingerprint(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	const q = `
WITH ext AS (SELECT objid FROM pg_depend WHERE deptype = 'e')
SELECT 'rel:' || c.relkind::text || ':' || c.relname
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relname NOT LIKE 'schema_migrations%'
   AND c.oid NOT IN (SELECT objid FROM ext)
UNION ALL
SELECT 'type:' || t.typname
  FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
 WHERE n.nspname = 'public' AND t.typtype IN ('e', 'd')
   AND t.oid NOT IN (SELECT objid FROM ext)
UNION ALL
SELECT 'func:' || p.proname
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
 WHERE n.nspname = 'public' AND p.oid NOT IN (SELECT objid FROM ext)
UNION ALL
SELECT 'con:' || cl.relname || '.' || co.conname
  FROM pg_constraint co JOIN pg_class cl ON cl.oid = co.conrelid
  JOIN pg_namespace n ON n.oid = cl.relnamespace
 WHERE n.nspname = 'public' AND cl.relname NOT LIKE 'schema_migrations%'
ORDER BY 1`
	rows, err := conn.Query(t.Context(), q)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return out
}
