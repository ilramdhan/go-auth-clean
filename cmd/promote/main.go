// Command promote memberi role admin ke user berdasarkan email (bootstrap admin
// pertama). Idempotent dan tercatat di audit log (actor=cli).
//
//	go run ./cmd/promote -email budi@example.com
//	make promote EMAIL=budi@example.com
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	authpg "go-auth-clean/internal/auth/adapter/postgres"
	authapp "go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/clock"
	"go-auth-clean/internal/platform/config"
	"go-auth-clean/internal/platform/database"
)

// promoter adalah port yang dibutuhkan CLI (dipenuhi *authapp.Service).
type promoter interface {
	PromoteByEmail(ctx context.Context, email string) (*domain.User, error)
}

var _ promoter = (*authapp.Service)(nil)

func main() {
	os.Exit(mainCode())
}

func mainCode() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, connect); err != nil {
		slog.ErrorContext(ctx, "promote failed", slog.Any("error", err))
		return 1
	}
	return 0
}

// connect merakit Service minimal (users, roles, audit, tx) dari config env.
func connect(ctx context.Context) (promoter, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	pool, err := database.NewPool(ctx, cfg.DB.DSN())
	if err != nil {
		return nil, nil, err
	}
	svc := authapp.NewService(authapp.Deps{
		Users: authpg.NewUserRepository(pool),
		Roles: authpg.NewRoleRepository(pool),
		Audit: authpg.NewAuditRepository(pool),
		Tx:    database.NewTxManager(pool),
		Clock: clock.System{},
	}, authapp.Config{})
	return svc, pool.Close, nil
}

func run(ctx context.Context, args []string, out io.Writer, connectFn func(context.Context) (promoter, func(), error)) error {
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	fs.SetOutput(out)
	email := fs.String("email", "", "email user yang dijadikan admin (wajib)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		fs.Usage()
		return errors.New("flag -email wajib diisi")
	}
	svc, closeFn, err := connectFn(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	u, err := svc.PromoteByEmail(ctx, *email)
	if errors.Is(err, domain.ErrUserNotFound) {
		return fmt.Errorf("user dengan email %q tidak ditemukan", *email)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "OK: %s (%s) sekarang admin. Role berlaku setelah user refresh token / login ulang.\n", u.Email, u.ID)
	return err
}
