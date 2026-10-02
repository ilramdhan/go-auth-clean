// Package mailer mengirim email. Message tidak bergantung pada protokol;
// Sender bisa SMTP (production / mailpit di dev) atau LogSender (test).
// Pengiriman async (goroutine/outbox) adalah urusan pemanggil.
package mailer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"time"

	gomail "github.com/wneessen/go-mail"

	"go-auth-clean/internal/platform/logger"
)

// Message adalah email yang akan dikirim. Minimal salah satu Text/HTML terisi.
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

// ErrInvalidMessage dikembalikan bila Message tidak lengkap.
var ErrInvalidMessage = errors.New("mailer: invalid message")

// Validate memeriksa penerima, subject, dan body.
func (m Message) Validate() error {
	if len(m.To) == 0 {
		return fmt.Errorf("%w: recipient kosong", ErrInvalidMessage)
	}
	for _, to := range m.To {
		if _, err := mail.ParseAddress(to); err != nil {
			return fmt.Errorf("%w: recipient tidak valid", ErrInvalidMessage)
		}
	}
	if strings.TrimSpace(m.Subject) == "" || strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("%w: subject tidak valid", ErrInvalidMessage)
	}
	if m.Text == "" && m.HTML == "" {
		return fmt.Errorf("%w: body kosong", ErrInvalidMessage)
	}
	return nil
}

// Sender adalah port pengirim email (didefinisikan juga oleh consumer bila perlu).
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// SMTPConfig adalah konfigurasi SMTP.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// TLS: true = wajib STARTTLS (production); false = opportunistic (mailpit dev).
	RequireTLS bool
	Timeout    time.Duration
}

// SMTPSender mengirim email lewat SMTP memakai wneessen/go-mail.
type SMTPSender struct {
	cfg SMTPConfig
}

var _ Sender = (*SMTPSender)(nil)

// NewSMTPSender memvalidasi konfigurasi lalu membuat sender.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Host == "" || cfg.Port <= 0 {
		return nil, errors.New("mailer: SMTP host/port wajib diisi")
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("mailer: SMTP from tidak valid: %w", err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &SMTPSender{cfg: cfg}, nil
}

// Send membangun MIME message (multipart bila Text+HTML) lalu mengirimnya.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	m, err := s.build(msg)
	if err != nil {
		return err
	}

	opts := []gomail.Option{
		gomail.WithPort(s.cfg.Port),
		gomail.WithTimeout(s.cfg.Timeout),
	}
	if s.cfg.RequireTLS {
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	} else {
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSOpportunistic))
	}
	if s.cfg.Username != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
			gomail.WithUsername(s.cfg.Username),
			gomail.WithPassword(s.cfg.Password),
		)
	}
	client, err := gomail.NewClient(s.cfg.Host, opts...)
	if err != nil {
		return fmt.Errorf("mailer.Send client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, m); err != nil {
		return fmt.Errorf("mailer.Send: %w", err)
	}
	return nil
}

func (s *SMTPSender) build(msg Message) (*gomail.Msg, error) {
	m := gomail.NewMsg()
	if err := m.From(s.cfg.From); err != nil {
		return nil, fmt.Errorf("mailer.build from: %w", err)
	}
	if err := m.To(msg.To...); err != nil {
		return nil, fmt.Errorf("mailer.build to: %w", err)
	}
	m.Subject(msg.Subject)
	m.SetDate()
	m.SetMessageID()
	switch {
	case msg.Text != "" && msg.HTML != "":
		m.SetBodyString(gomail.TypeTextPlain, msg.Text)
		m.AddAlternativeString(gomail.TypeTextHTML, msg.HTML)
	case msg.HTML != "":
		m.SetBodyString(gomail.TypeTextHTML, msg.HTML)
	default:
		m.SetBodyString(gomail.TypeTextPlain, msg.Text)
	}
	return m, nil
}

// LogSender tidak mengirim apa pun: hanya mencatat metadata (TANPA body, karena
// body bisa berisi OTP/link reset) dan menyimpan pesan untuk diperiksa test.
type LogSender struct {
	mu   sync.Mutex
	sent []Message
	// Err, bila diisi, dikembalikan oleh Send (simulasi kegagalan di test).
	Err error
}

var _ Sender = (*LogSender)(nil)

// NewLogSender membuat LogSender kosong.
func NewLogSender() *LogSender { return &LogSender{} }

// Send menyimpan pesan dan mencatat penerima & subject.
func (l *LogSender) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	if l.Err != nil {
		return l.Err
	}
	l.mu.Lock()
	l.sent = append(l.sent, msg)
	l.mu.Unlock()
	logger.FromContext(ctx).InfoContext(ctx, "email sent (log sender)",
		slog.Int("recipients", len(msg.To)), slog.String("subject", msg.Subject))
	return nil
}

// Sent mengembalikan salinan semua pesan yang terkirim.
func (l *LogSender) Sent() []Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Message, len(l.sent))
	copy(out, l.sent)
	return out
}

// Last mengembalikan pesan terakhir (ok=false bila belum ada).
func (l *LogSender) Last() (Message, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.sent) == 0 {
		return Message{}, false
	}
	return l.sent[len(l.sent)-1], true
}
