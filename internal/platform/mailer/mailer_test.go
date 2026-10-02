package mailer_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"go-auth-clean/internal/platform/mailer"
)

func TestMessageValidate(t *testing.T) {
	tests := []struct {
		name string
		msg  mailer.Message
		ok   bool
	}{
		{"valid text", mailer.Message{To: []string{"a@b.com"}, Subject: "Hi", Text: "x"}, true},
		{"valid html", mailer.Message{To: []string{"Ani <a@b.com>"}, Subject: "Hi", HTML: "<p>x</p>"}, true},
		{"no recipient", mailer.Message{Subject: "Hi", Text: "x"}, false},
		{"bad recipient", mailer.Message{To: []string{"nope"}, Subject: "Hi", Text: "x"}, false},
		{"header injection", mailer.Message{To: []string{"a@b.com"}, Subject: "Hi\r\nBcc: x@y.com", Text: "x"}, false},
		{"empty body", mailer.Message{To: []string{"a@b.com"}, Subject: "Hi"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v", err)
			}
			if err != nil && !errors.Is(err, mailer.ErrInvalidMessage) {
				t.Fatalf("err harus ErrInvalidMessage: %v", err)
			}
		})
	}
}

func TestLogSender(t *testing.T) {
	s := mailer.NewLogSender()
	if _, ok := s.Last(); ok {
		t.Fatal("harus kosong")
	}
	msg := mailer.Message{To: []string{"a@b.com"}, Subject: "OTP", Text: "123456"}
	if err := s.Send(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	last, ok := s.Last()
	if !ok || last.Text != "123456" || len(s.Sent()) != 1 {
		t.Fatalf("last = %+v", last)
	}
	if err := s.Send(t.Context(), mailer.Message{}); err == nil {
		t.Fatal("pesan invalid harus ditolak")
	}
	s.Err = errors.New("smtp down")
	if err := s.Send(t.Context(), msg); err == nil {
		t.Fatal("harus mengembalikan Err")
	}
}

func TestNewSMTPSender_Validation(t *testing.T) {
	if _, err := mailer.NewSMTPSender(mailer.SMTPConfig{Port: 25, From: "a@b.com"}); err == nil {
		t.Error("host kosong harus error")
	}
	if _, err := mailer.NewSMTPSender(mailer.SMTPConfig{Host: "x", Port: 25, From: "bad"}); err == nil {
		t.Error("from invalid harus error")
	}
}

// fakeSMTP adalah server SMTP minimal untuk menguji SMTPSender end-to-end.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	data strings.Builder
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn)
		}
	}()
	return s
}

func (s *fakeSMTP) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	write := func(l string) { _, _ = conn.Write([]byte(l + "\r\n")) }
	write("220 fake ESMTP")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if line == ".\r\n" {
				inData = false
				write("250 OK")
				continue
			}
			s.mu.Lock()
			s.data.WriteString(line)
			s.mu.Unlock()
			continue
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			write("250 fake")
		case strings.HasPrefix(cmd, "DATA"):
			inData = true
			write("354 go ahead")
		case strings.HasPrefix(cmd, "QUIT"):
			write("221 bye")
			return
		default:
			write("250 OK")
		}
	}
}

func (s *fakeSMTP) received() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.String()
}

func TestSMTPSender_Send(t *testing.T) {
	srv := startFakeSMTP(t)
	addr := srv.ln.Addr().(*net.TCPAddr)

	sender, err := mailer.NewSMTPSender(mailer.SMTPConfig{
		Host: "127.0.0.1", Port: addr.Port, From: "App <no-reply@example.com>", Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err = sender.Send(ctx, mailer.Message{
		To: []string{"budi@example.com"}, Subject: "Verifikasi email", Text: "kode 123456", HTML: "<b>123456</b>",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	got := srv.received()
	for _, want := range []string{"Subject: Verifikasi email", "budi@example.com", "multipart/alternative"} {
		if !strings.Contains(got, want) {
			t.Errorf("pesan tidak berisi %q", want)
		}
	}

	if err := sender.Send(ctx, mailer.Message{}); !errors.Is(err, mailer.ErrInvalidMessage) {
		t.Fatalf("invalid msg err = %v", err)
	}
	for _, m := range []mailer.Message{
		{To: []string{"a@b.com"}, Subject: "html", HTML: "<p>x</p>"},
		{To: []string{"a@b.com"}, Subject: "text", Text: "x"},
	} {
		if err := sender.Send(ctx, m); err != nil {
			t.Fatalf("send %s: %v", m.Subject, err)
		}
	}
}
