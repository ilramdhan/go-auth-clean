// Package email mengimplementasikan port app.Notifier di atas platform mailer.
//
// Pengiriman dibuat asynchronous lewat antrean ber-kapasitas tetap + worker:
//   - latency SMTP tidak ikut ke response (resend/forgot jadi seragam waktunya,
//     mencegah user enumeration lewat timing);
//   - jumlah goroutine terbatas (tidak ada goroutine per request);
//   - saat shutdown, sisa antrean tetap dikirim dengan batas waktu.
//
// Trade-off: email bisa hilang bila proses crash sebelum terkirim. User bisa
// meminta kirim ulang; versi berikutnya bisa memakai outbox table.
package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/url"
	"sync"
	texttemplate "text/template"
	"time"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/platform/mailer"
	"go-auth-clean/internal/platform/requestid"
)

var _ app.Notifier = (*Notifier)(nil)

var (
	ErrQueueFull = errors.New("email queue full")
	ErrClosed    = errors.New("email notifier closed")
)

type Options struct {
	AppName     string
	FrontendURL string
	QueueSize   int
	Workers     int
	SendTimeout time.Duration
	// DrainTimeout: batas waktu mengirim sisa antrean saat shutdown.
	DrainTimeout time.Duration
}

type job struct {
	msg       mailer.Message
	kind      string
	requestID string
}

type Notifier struct {
	sender mailer.Sender
	log    *slog.Logger
	opt    Options
	queue  chan job

	mu     sync.RWMutex
	closed bool
}

func NewNotifier(sender mailer.Sender, log *slog.Logger, opt Options) *Notifier {
	if opt.AppName == "" {
		opt.AppName = "Go Auth"
	}
	if opt.QueueSize <= 0 {
		opt.QueueSize = 256
	}
	if opt.Workers <= 0 {
		opt.Workers = 2
	}
	if opt.SendTimeout <= 0 {
		opt.SendTimeout = 15 * time.Second
	}
	if opt.DrainTimeout <= 0 {
		opt.DrainTimeout = 10 * time.Second
	}
	return &Notifier{sender: sender, log: log, opt: opt, queue: make(chan job, opt.QueueSize)}
}

// Run menjalankan worker sampai ctx dibatalkan, lalu mengirim sisa antrean.
// Semua pengiriman setelah ctx dibatalkan (termasuk yang sedang berjalan)
// dibatasi DrainTimeout. Setelah Run selesai, enqueue mengembalikan ErrClosed.
func (n *Notifier) Run(ctx context.Context) error {
	// sendCtx tetap hidup selama operasi normal dan berakhir DrainTimeout
	// setelah sinyal shutdown.
	sendCtx, cancelSend := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelSend()
	var timer *time.Timer
	var timerMu sync.Mutex
	stop := context.AfterFunc(ctx, func() {
		timerMu.Lock()
		timer = time.AfterFunc(n.opt.DrainTimeout, cancelSend)
		timerMu.Unlock()
	})
	defer func() {
		stop()
		timerMu.Lock()
		if timer != nil {
			timer.Stop()
		}
		timerMu.Unlock()
	}()

	var wg sync.WaitGroup
	for range n.opt.Workers {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case j := <-n.queue:
					n.send(sendCtx, j)
				}
			}
		})
	}
	<-ctx.Done()
	wg.Wait()

	n.mu.Lock()
	n.closed = true
	n.mu.Unlock()

	for {
		select {
		case j := <-n.queue:
			if sendCtx.Err() != nil {
				n.log.WarnContext(sendCtx, "email dropped on shutdown", slog.String("kind", j.kind))
				continue
			}
			n.send(sendCtx, j)
		default:
			return nil
		}
	}
}

func (n *Notifier) send(ctx context.Context, j job) {
	ctx, cancel := context.WithTimeout(ctx, n.opt.SendTimeout)
	defer cancel()
	if err := n.sender.Send(ctx, j.msg); err != nil {
		// Jangan log isi email (berisi OTP); cukup jenis & request id.
		n.log.ErrorContext(ctx, "send email failed", slog.String("kind", j.kind),
			slog.String("request_id", j.requestID), slog.Any("error", err))
		return
	}
	n.log.DebugContext(ctx, "email sent", slog.String("kind", j.kind), slog.String("request_id", j.requestID))
}

func (n *Notifier) enqueue(ctx context.Context, kind string, msg mailer.Message) error {
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("email.%s: %w", kind, err)
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed {
		return ErrClosed
	}
	select {
	case n.queue <- job{msg: msg, kind: kind, requestID: requestid.FromContext(ctx)}:
		return nil
	default:
		return ErrQueueFull
	}
}

// Pending mengembalikan jumlah email di antrean (untuk test/metrics).
func (n *Notifier) Pending() int { return len(n.queue) }

func (n *Notifier) EmailVerification(ctx context.Context, to, name, code string, ttl time.Duration) error {
	return n.otpMail(ctx, "email_verification", to, name, code, ttl,
		"Kode verifikasi email", "Gunakan kode berikut untuk memverifikasi email Anda", "/verify-email")
}

func (n *Notifier) PasswordReset(ctx context.Context, to, name, code string, ttl time.Duration) error {
	return n.otpMail(ctx, "password_reset", to, name, code, ttl,
		"Kode reset password", "Gunakan kode berikut untuk mereset password Anda. Abaikan email ini jika Anda tidak memintanya", "/reset-password")
}

func (n *Notifier) PasswordChanged(ctx context.Context, to, name string) error {
	data := mailData{
		AppName: n.opt.AppName, Name: name,
		Intro: "Password akun Anda baru saja diubah. Jika ini bukan Anda, segera reset password dan hubungi kami.",
		Link:  n.link("/forgot-password", to),
	}
	return n.render(ctx, "password_changed", to, n.opt.AppName+": password diubah", data)
}

func (n *Notifier) otpMail(ctx context.Context, kind, to, name, code string, ttl time.Duration, subject, intro, path string) error {
	data := mailData{
		AppName: n.opt.AppName, Name: name, Intro: intro, Code: code,
		Minutes: int(ttl.Round(time.Minute) / time.Minute),
		Link:    n.link(path, to),
	}
	return n.render(ctx, kind, to, n.opt.AppName+": "+subject, data)
}

// link membangun URL frontend; kode OTP sengaja TIDAK dimasukkan ke URL
// (bisa bocor via history/referrer/prefetch email client).
func (n *Notifier) link(path, email string) string {
	if n.opt.FrontendURL == "" {
		return ""
	}
	return n.opt.FrontendURL + path + "?email=" + url.QueryEscape(email)
}

type mailData struct {
	AppName string
	Name    string
	Intro   string
	Code    string
	Minutes int
	Link    string
}

var (
	textTmpl = texttemplate.Must(texttemplate.New("text").Parse(`Halo {{.Name}},

{{.Intro}}.
{{if .Code}}
Kode: {{.Code}}
Berlaku {{.Minutes}} menit. Jangan bagikan kode ini kepada siapa pun.
{{end}}{{if .Link}}
{{.Link}}
{{end}}
- {{.AppName}}
`))
	htmlTmpl = template.Must(template.New("html").Parse(`<!doctype html><html><body style="font-family:sans-serif">
<p>Halo {{.Name}},</p><p>{{.Intro}}.</p>
{{if .Code}}<p style="font-size:28px;font-weight:bold;letter-spacing:6px">{{.Code}}</p>
<p>Berlaku {{.Minutes}} menit. Jangan bagikan kode ini kepada siapa pun.</p>{{end}}
{{if .Link}}<p><a href="{{.Link}}">{{.Link}}</a></p>{{end}}
<p>- {{.AppName}}</p></body></html>`))
)

func (n *Notifier) render(ctx context.Context, kind, to, subject string, d mailData) error {
	var txt, html bytes.Buffer
	// Bagian text memakai text/template (tanpa HTML escaping); bagian HTML
	// memakai html/template agar nama user ter-escape.
	if err := textTmpl.Execute(&txt, d); err != nil {
		return fmt.Errorf("email.render: %w", err)
	}
	if err := htmlTmpl.Execute(&html, d); err != nil {
		return fmt.Errorf("email.render: %w", err)
	}
	return n.enqueue(ctx, kind, mailer.Message{To: []string{to}, Subject: subject, Text: txt.String(), HTML: html.String()})
}
