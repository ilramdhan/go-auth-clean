package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

type fakePromoter struct {
	err   error
	email string
}

func (f *fakePromoter) PromoteByEmail(_ context.Context, email string) (*domain.User, error) {
	f.email = email
	if f.err != nil {
		return nil, f.err
	}
	return &domain.User{ID: uuid.Nil, Email: email}, nil
}

func connectWith(p promoter, err error) func(context.Context) (promoter, func(), error) {
	return func(context.Context) (promoter, func(), error) { return p, func() {}, err }
}

func TestRun(t *testing.T) {
	var out bytes.Buffer
	fp := &fakePromoter{}
	if err := run(t.Context(), []string{"-email", "a@b.io"}, &out, connectWith(fp, nil)); err != nil {
		t.Fatal(err)
	}
	if fp.email != "a@b.io" || !strings.Contains(out.String(), "OK: a@b.io") {
		t.Fatalf("out = %s", out.String())
	}

	if err := run(t.Context(), nil, &out, connectWith(fp, nil)); err == nil {
		t.Fatal("missing -email must fail")
	}
	if err := run(t.Context(), []string{"-nope"}, &out, connectWith(fp, nil)); err == nil {
		t.Fatal("bad flag must fail")
	}
	if err := run(t.Context(), []string{"-email", "x@y.io"}, &out, connectWith(nil, errors.New("db"))); err == nil {
		t.Fatal("connect error must propagate")
	}
	err := run(t.Context(), []string{"-email", "x@y.io"}, &out, connectWith(&fakePromoter{err: domain.ErrUserNotFound}, nil))
	if err == nil || !strings.Contains(err.Error(), "tidak ditemukan") {
		t.Fatalf("err = %v", err)
	}
	if err := run(t.Context(), []string{"-email", "x@y.io"}, &out, connectWith(&fakePromoter{err: errors.New("boom")}, nil)); err == nil {
		t.Fatal("service error must propagate")
	}
}
