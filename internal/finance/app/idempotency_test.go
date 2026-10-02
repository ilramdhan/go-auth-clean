package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go-auth-clean/internal/finance/domain"
)

func TestValidateIdempotencyKey(t *testing.T) {
	cases := map[string]error{
		"":                       ErrIdempotencyKeyRequired,
		"short":                  ErrInvalidIdempotencyKey,
		strings.Repeat("a", 129): ErrInvalidIdempotencyKey,
		"has space inside":       ErrInvalidIdempotencyKey,
		"kunci-ünicode":          ErrInvalidIdempotencyKey,
		"0192f-abc-def":          nil,
	}
	for k, want := range cases {
		if err := ValidateIdempotencyKey(k); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", k, err, want)
		}
	}
	h1 := RequestHash("post", "/a", []byte("{}"))
	if !MatchHash(h1, RequestHash("POST", "/a", []byte("{}"))) || MatchHash(h1, RequestHash("POST", "/b", []byte("{}"))) {
		t.Fatal("hash")
	}
}

func TestIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	calls := 0
	run := func(body, key string) (StoredResponse, bool, error) {
		return f.svc.Idempotent(ctx, IdempotentRequest{UserID: f.user, Key: key, Method: "POST", Path: "/x", Body: []byte(body)},
			func(context.Context) (StoredResponse, error) {
				calls++
				return StoredResponse{Status: 201, Body: []byte(`{"n":1}`)}, nil
			})
	}
	r, replayed, err := run("a", "key-12345")
	if err != nil || replayed || r.Status != 201 || calls != 1 {
		t.Fatal(err)
	}
	r, replayed, err = run("a", "key-12345")
	if err != nil || !replayed || string(r.Body) != `{"n":1}` || calls != 1 {
		t.Fatal("replay harus tanpa eksekusi ulang")
	}
	_, _, err = run("b", "key-12345")
	wantErr(t, err, ErrIdempotencyKeyReused)
	_, _, err = run("a", "x")
	wantErr(t, err, ErrInvalidIdempotencyKey)
	// fn gagal -> klaim di-rollback, retry boleh
	_, _, err = f.svc.Idempotent(ctx, IdempotentRequest{UserID: f.user, Key: "key-fail-1", Method: "POST", Path: "/x"},
		func(context.Context) (StoredResponse, error) { return StoredResponse{}, domain.ErrInsufficientBalance })
	wantErr(t, err, domain.ErrInsufficientBalance)
	if _, replayed, err := run("", "key-fail-1"); err != nil || replayed {
		t.Fatal("retry setelah gagal harus dieksekusi")
	}
	// in progress (klaim belum complete)
	f.db.idem[f.user.String()+"|key-progress"] = idemRow{hash: RequestHash("POST", "/x", []byte("a"))}
	_, _, err = run("a", "key-progress")
	wantErr(t, err, ErrIdempotencyInProgress)
	f.db.failOn["idem.complete"] = errBoom
	_, _, err = run("a", "key-complete")
	wantErr(t, err, errBoom)
}
