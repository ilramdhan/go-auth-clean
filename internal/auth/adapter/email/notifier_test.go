package email

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go-auth-clean/internal/platform/mailer"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestNotifier_SendsAsync(t *testing.T) {
	s := mailer.NewLogSender()
	n := NewNotifier(s, quietLog(), Options{AppName: "Test", FrontendURL: "https://app.example.com"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx) }()

	if err := n.EmailVerification(t.Context(), "budi@example.com", "O'Brien <b>", "123456", 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := n.PasswordReset(t.Context(), "budi@example.com", "Budi", "654321", 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := n.PasswordChanged(t.Context(), "budi@example.com", "Budi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(s.Sent()) == 3 })

	byCode := map[string]mailer.Message{}
	for _, m := range s.Sent() {
		switch {
		case strings.Contains(m.Text, "123456"):
			byCode["verify"] = m
		case strings.Contains(m.Text, "654321"):
			byCode["reset"] = m
		default:
			byCode["changed"] = m
		}
	}
	v := byCode["verify"]
	if !strings.HasPrefix(v.Subject, "Test: ") || !strings.Contains(v.Text, "10 menit") ||
		!strings.Contains(v.Text, "https://app.example.com/verify-email?email=budi%40example.com") {
		t.Fatalf("verify mail: %+v", v)
	}
	if !strings.Contains(v.Text, "O'Brien <b>") {
		t.Fatal("text body tidak boleh di-escape HTML")
	}
	if strings.Contains(v.HTML, "<b>") || !strings.Contains(v.HTML, "123456") {
		t.Fatal("html harus escape nama dan memuat kode")
	}
	for _, m := range s.Sent() {
		for _, line := range strings.Split(m.Text, "\n") {
			if strings.Contains(line, "http") && (strings.Contains(line, "123456") || strings.Contains(line, "654321")) {
				t.Fatalf("kode OTP tidak boleh ada di URL: %s", line)
			}
		}
	}
	if !strings.Contains(byCode["reset"].Text, "/reset-password") || !strings.Contains(byCode["changed"].Text, "/forgot-password") {
		t.Fatal("links")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := n.PasswordChanged(t.Context(), "budi@example.com", "Budi"); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
}

func TestNotifier_QueueFullAndInvalid(t *testing.T) {
	n := NewNotifier(mailer.NewLogSender(), quietLog(), Options{QueueSize: 1})
	if err := n.PasswordChanged(t.Context(), "a@b.co", "A"); err != nil {
		t.Fatal(err)
	}
	if n.Pending() != 1 {
		t.Fatal("pending")
	}
	if err := n.PasswordChanged(t.Context(), "a@b.co", "A"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want queue full, got %v", err)
	}
	if err := n.PasswordChanged(t.Context(), "bukan-email", "A"); !errors.Is(err, mailer.ErrInvalidMessage) {
		t.Fatalf("invalid: %v", err)
	}
	// Tanpa FrontendURL: tidak ada link.
	if got := n.link("/x", "a@b.co"); got != "" {
		t.Fatal(got)
	}
}

// blockingSender menahan Send sampai release ditutup.
type blockingSender struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	sent    int
}

func (b *blockingSender) Send(ctx context.Context, _ mailer.Message) error {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	b.mu.Lock()
	b.sent++
	b.mu.Unlock()
	return nil
}

func TestNotifier_DrainOnShutdown(t *testing.T) {
	s := mailer.NewLogSender()
	n := NewNotifier(s, quietLog(), Options{Workers: 1, QueueSize: 10})
	for range 5 {
		if err := n.PasswordChanged(t.Context(), "a@b.co", "A"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // shutdown langsung: semua email harus terkirim lewat drain
	if err := n.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.Sent()) != 5 || n.Pending() != 0 {
		t.Fatalf("sent=%d pending=%d", len(s.Sent()), n.Pending())
	}
}

func TestNotifier_DrainTimeoutDrops(t *testing.T) {
	b := &blockingSender{started: make(chan struct{}, 1), release: make(chan struct{})}
	n := NewNotifier(b, quietLog(), Options{Workers: 1, QueueSize: 10, DrainTimeout: 20 * time.Millisecond, SendTimeout: time.Second})
	for range 3 {
		_ = n.PasswordChanged(t.Context(), "a@b.co", "A")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	if err := n.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("drain harus dibatasi DrainTimeout")
	}
	if n.Pending() != 0 || b.sent != 0 {
		t.Fatalf("pending=%d sent=%d", n.Pending(), b.sent)
	}
}

func TestNotifier_SendErrorLogged(t *testing.T) {
	s := mailer.NewLogSender()
	s.Err = errors.New("smtp down")
	n := NewNotifier(s, quietLog(), Options{})
	_ = n.PasswordChanged(t.Context(), "a@b.co", "A")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := n.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.Sent()) != 0 {
		t.Fatal("must fail")
	}
}
