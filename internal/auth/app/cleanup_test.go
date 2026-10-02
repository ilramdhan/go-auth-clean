package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestCleaner_RunsUntilCancel(t *testing.T) {
	var calls atomic.Int32
	c := &Cleaner{
		run: func(context.Context) (CleanupResult, error) {
			if calls.Add(1) == 2 {
				return CleanupResult{}, errors.New("db")
			}
			return CleanupResult{Sessions: 1}, nil
		},
		interval: 5 * time.Millisecond,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.After(2 * time.Second)
	for calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatal("cleaner did not tick")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNewCleaner_DefaultInterval(t *testing.T) {
	c := NewCleaner(newEnv(t).svc, 0, slog.Default())
	if c.interval != time.Hour {
		t.Fatal(c.interval)
	}
}
