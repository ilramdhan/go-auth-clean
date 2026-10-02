// Package dbtest menyediakan PostgreSQL sungguhan (testcontainers) untuk
// integration test repository. Satu container per test package (process),
// semua migrations/*.up.sql diterapkan berurutan saat container pertama dibuat.
//
// Pemakaian:
//
//	func TestMain(m *testing.M) { dbtest.Main(m) } // opsional: matikan container di akhir
//
//	func TestUserRepo(t *testing.T) {
//		pool := dbtest.New(t)               // skip otomatis bila -short / docker tidak ada
//		dbtest.Truncate(t, pool, "users")   // isolasi antar test
//		...
//	}
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"go-auth-clean/internal/platform/database"
)

// Image adalah versi Postgres yang sama dengan docker-compose.
const Image = "postgres:17-alpine"

var (
	once      sync.Once
	pool      *pgxpool.Pool
	container *tcpostgres.PostgresContainer
	setupErr  error
)

// Main dipanggil dari TestMain: menjalankan test lalu mematikan container.
// Tanpa Main pun container tetap dibersihkan oleh reaper testcontainers (Ryuk).
func Main(m *testing.M) {
	code := m.Run()
	Shutdown()
	os.Exit(code)
}

// Shutdown menutup pool dan menghentikan container (aman dipanggil berkali-kali).
func Shutdown() {
	if pool != nil {
		pool.Close()
	}
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
}

// New mengembalikan pool ke database test yang sudah dimigrasi.
// Test di-skip saat `go test -short` atau Docker tidak tersedia.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("dbtest: integration test di-skip pada mode -short")
	}
	once.Do(func() { pool, setupErr = start() })
	if setupErr != nil {
		if errors.Is(setupErr, errDockerUnavailable) {
			t.Skipf("dbtest: %v", setupErr)
		}
		t.Fatalf("dbtest: %v", setupErr)
	}
	return pool
}

var errDockerUnavailable = errors.New("docker tidak tersedia")

// NewEmptyDSN menjalankan container PostgreSQL BARU yang masih kosong (tanpa
// migrasi) khusus untuk satu test, dan mengembalikan DSN-nya. Container
// dihentikan lewat t.Cleanup. Dipakai mis. untuk menguji siklus migrasi
// up/down tanpa mengganggu database bersama milik New.
func NewEmptyDSN(t testing.TB) string {
	t.Helper()
	if testing.Short() {
		t.Skip("dbtest: integration test di-skip pada mode -short")
	}
	dsn, c, err := startEmpty()
	if c != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	}
	if err != nil {
		if errors.Is(err, errDockerUnavailable) {
			t.Skipf("dbtest: %v", err)
		}
		t.Fatalf("dbtest: %v", err)
	}
	return dsn
}

func startEmpty() (dsn string, c *tcpostgres.PostgresContainer, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", errDockerUnavailable, r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if !dockerAvailable(ctx) {
		return "", nil, errDockerUnavailable
	}
	c, err = runContainer(ctx, "app_migrate")
	if err != nil {
		return "", c, err
	}
	dsn, err = c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", c, fmt.Errorf("connection string: %w", err)
	}
	return dsn, c, nil
}

func runContainer(ctx context.Context, dbName string) (*tcpostgres.PostgresContainer, error) {
	c, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return c, fmt.Errorf("start postgres container: %w", err)
	}
	return c, nil
}

func start() (p *pgxpool.Pool, err error) {
	// testcontainers bisa panic bila provider docker tidak ditemukan.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", errDockerUnavailable, r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if !dockerAvailable(ctx) {
		return nil, errDockerUnavailable
	}

	c, err := runContainer(ctx, "app_test")
	if c != nil {
		container = c
	}
	if err != nil {
		return nil, err
	}

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("connection string: %w", err)
	}
	p, err = database.NewPool(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := applyMigrations(ctx, p); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// configureDockerHost membaca endpoint dari `docker context` aktif bila DOCKER_HOST
// kosong (Colima/OrbStack tidak memakai /var/run/docker.sock di host).
func configureDockerHost(ctx context.Context) {
	if os.Getenv("DOCKER_HOST") != "" {
		return
	}
	out, err := exec.CommandContext(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}").Output()
	if err != nil {
		return
	}
	host := strings.TrimSpace(string(out))
	if host == "" || host == "unix:///var/run/docker.sock" {
		return
	}
	_ = os.Setenv("DOCKER_HOST", host)
	if os.Getenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE") == "" {
		// Di dalam VM docker, socket tetap berada di lokasi standar (untuk container Ryuk).
		_ = os.Setenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE", "/var/run/docker.sock")
	}
}

func dockerAvailable(ctx context.Context) bool {
	configureDockerHost(ctx)
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return false
	}
	defer func() { _ = provider.Close() }()
	return provider.Health(ctx) == nil
}

// applyMigrations menjalankan semua *.up.sql berurutan nama file (prefix -seq).
func applyMigrations(ctx context.Context, p *pgxpool.Pool) error {
	dir, err := MigrationsDir()
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(files)
	for _, f := range files {
		sqlBytes, err := os.ReadFile(f) //nolint:gosec // path berasal dari repo sendiri
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(f), err)
		}
		// Exec tanpa argumen memakai simple protocol: multi-statement diizinkan.
		if _, err := p.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply %s: %w", filepath.Base(f), err)
		}
	}
	return nil
}

// MigrationsDir mencari folder migrations dengan berjalan ke atas sampai go.mod.
func MigrationsDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "migrations"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod tidak ditemukan")
		}
		dir = parent
	}
}

// Truncate mengosongkan tabel yang disebut (RESTART IDENTITY CASCADE).
// Sengaja eksplisit: tabel reference data hasil seed migration (mis. currencies)
// tidak boleh ikut terhapus.
func Truncate(t testing.TB, p *pgxpool.Pool, tables ...string) {
	t.Helper()
	if len(tables) == 0 {
		return
	}
	quoted := make([]string, len(tables))
	for i, tb := range tables {
		quoted[i] = pgx.Identifier{tb}.Sanitize()
	}
	q := "TRUNCATE " + strings.Join(quoted, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := p.Exec(context.Background(), q); err != nil {
		t.Fatalf("dbtest.Truncate: %v", err)
	}
}
