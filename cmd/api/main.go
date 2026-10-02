// Command api adalah entry point HTTP server. File ini hanya bertugas sebagai
// composition root: membaca config, merakit dependency, lalu menjalankan server.
//
//	@title						Go Auth Clean API
//	@version					1.0
//	@description				REST API untuk autentikasi (register, login, refresh token, session) dan personal finance tracker.
//	@description				Semua response sukses dibungkus {"data": ...}; list memakai {"data": [...], "meta": {...}}; error memakai {"error": {code, message, details}, "request_id"}.
//	@description				Nominal uang dikirim sebagai string desimal major unit (mis. "35000" untuk IDR, "12.34" untuk USD).
//	@contact.name				Backend Team
//	@BasePath					/api/v1
//	@schemes					http https
//	@accept						json
//	@produce					json
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Type 'Bearer <token>' (access token dari /auth/login).
//	@securityDefinitions.apikey	ApiKeyAuth
//	@in							header
//	@name						X-API-Key
//	@description				API key (gac_<env>_<prefix>_<secret>) dari POST /users/me/api-keys. Hanya untuk endpoint yang mencantumkan ApiKeyAuth.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/crypto/bcrypt"

	authemail "go-auth-clean/internal/auth/adapter/email"
	authpg "go-auth-clean/internal/auth/adapter/postgres"
	"go-auth-clean/internal/auth/adapter/security"
	authapp "go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/finance"
	"go-auth-clean/internal/platform/clock"
	"go-auth-clean/internal/platform/config"
	"go-auth-clean/internal/platform/database"
	"go-auth-clean/internal/platform/logger"
	"go-auth-clean/internal/platform/mailer"
)

// version diisi saat build via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.ErrorContext(context.Background(), "application stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

// run dipisah dari main supaya bisa memakai defer dan return error dengan normal.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.AppEnv, cfg.LogLevel)
	slog.SetDefault(log)

	// ctx dibatalkan saat menerima SIGINT/SIGTERM (Ctrl+C atau docker stop).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, cfg.DB.DSN())
	if err != nil {
		return err
	}
	defer pool.Close()

	hasher, err := security.NewBcryptHasher(bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	jwtIssuer := security.NewJWTIssuer(cfg.JWT.Secret, cfg.JWT.Issuer, cfg.JWT.AccessTokenTTL)
	clk := clock.System{}
	txm := database.NewTxManager(pool)

	authCfg, err := loadAuthEnv(cfg)
	if err != nil {
		return err
	}
	otpCodec, err := security.NewHMACOTPCodec(cfg.Security.EncryptionKey)
	if err != nil {
		return err
	}
	sender, err := mailer.NewSMTPSender(mailer.SMTPConfig{
		Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, Username: cfg.SMTP.Username, Password: cfg.SMTP.Password,
		From: cfg.SMTP.From, RequireTLS: cfg.SMTP.RequireTLS,
	})
	if err != nil {
		return err
	}
	notifier := authemail.NewNotifier(sender, log, authemail.Options{FrontendURL: cfg.FrontendURL})

	authDeps := authapp.Deps{
		Users:    authpg.NewUserRepository(pool),
		Sessions: authpg.NewSessionRepository(pool),
		OTPs:     authpg.NewOTPRepository(pool),
		Audit:    authpg.NewAuditRepository(pool),
		Hasher:   hasher,
		Tokens:   jwtIssuer,
		OTP:      otpCodec,
		Notifier: notifier,
		Clock:    clk,
		Tx:       txm,
	}
	sealer, err := wireAuthP2(ctx, cfg, authCfg, pool, &authDeps)
	if err != nil {
		return err
	}
	authSvc := authapp.NewService(authDeps, authCfg.Service)

	// Worker latar belakang: berhenti saat ctx dibatalkan; ditunggu sebelum pool ditutup.
	// bgCtx terpisah supaya worker baru berhenti SETELAH HTTP server selesai
	// (request in-flight masih bisa meng-enqueue email).
	bgCtx, stopBg := context.WithCancel(context.WithoutCancel(ctx))
	var bg sync.WaitGroup
	defer func() {
		stopBg()
		bg.Wait()
	}()
	bg.Go(func() { _ = notifier.Run(bgCtx) })
	bg.Go(func() { _ = authapp.NewCleaner(authSvc, authCfg.CleanupInterval, log).Run(bgCtx) })

	fin := finance.NewModule(finance.Deps{
		Pool: pool, TxManager: txm, Clock: clk, Logger: log,
		UserDirectory: userDirectory{users: authpg.NewUserRepository(pool)},
		Config: finance.Config{
			DefaultCurrency:   cfg.Finance.DefaultCurrency,
			DefaultTimezone:   cfg.Finance.DefaultTimezone,
			RecurringInterval: cfg.Finance.RecurringWorkerInterval,
		},
	})
	fin.StartWorkers(bgCtx)
	bg.Go(fin.Wait)

	var draining atomic.Bool
	handler, err := newRouter(ctx, routerDeps{
		cfg: cfg, log: log, pool: pool,
		authSvc: authSvc, tokens: jwtIssuer, clock: clk, auth: authCfg, sealer: sealer,
		finance: fin, draining: &draining,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	errCh := make(chan error, 1)
	go func() {
		log.InfoContext(ctx, "http server started", slog.String("addr", cfg.HTTPAddr), slog.String("version", version), slog.Bool("swagger", cfg.Swagger.Enabled))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// /readyz 503 dulu supaya LB berhenti routing sebelum listener ditutup.
	draining.Store(true)
	log.InfoContext(ctx, "shutting down, waiting for in-flight requests")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.InfoContext(shutdownCtx, "http server stopped, draining background workers")
	return nil
}
