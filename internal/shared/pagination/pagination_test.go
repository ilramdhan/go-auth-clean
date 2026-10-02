package pagination_test

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/shared/pagination"
)

func TestCursorRoundTrip(t *testing.T) {
	c := pagination.Cursor{Time: time.Date(2026, 9, 30, 10, 0, 0, 123, time.UTC), ID: uuid.New(), Filter: pagination.FilterHash("acc", "2026-09")}
	got, err := pagination.Decode(c.Encode(), c.Filter)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Time.Equal(c.Time) || got.ID != c.ID || got.Filter != c.Filter {
		t.Fatalf("got %+v, want %+v", got, c)
	}
}

func TestDecode(t *testing.T) {
	valid := pagination.Cursor{Time: time.Now(), ID: uuid.New(), Filter: "f1"}.Encode()
	tests := []struct {
		name    string
		in      string
		filter  string
		wantNil bool
		wantErr bool
	}{
		{"empty means first page", "", "f1", true, false},
		{"valid", valid, "f1", false, false},
		{"filter mismatch", valid, "f2", false, true},
		{"not base64", "%%%", "f1", false, true},
		{"not json", base64.RawURLEncoding.EncodeToString([]byte("nope")), "f1", false, true},
		{"missing id", base64.RawURLEncoding.EncodeToString([]byte(`{"t":"2026-01-01T00:00:00Z"}`)), "", false, true},
		{"too long", string(make([]byte, 2000)), "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pagination.Decode(tt.in, tt.filter)
			if tt.wantErr {
				if !errors.Is(err, pagination.ErrInvalidCursor) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || (got == nil) != tt.wantNil {
				t.Fatalf("got %v, err %v", got, err)
			}
		})
	}
}

func TestFilterHash(t *testing.T) {
	if pagination.FilterHash("a", "b") == pagination.FilterHash("ab") {
		t.Fatal("hash harus memisahkan part")
	}
	if a, b := pagination.FilterHash("a"), pagination.FilterHash("a"); a != b || len(a) != 16 {
		t.Fatal("hash harus deterministik")
	}
}

func TestClampAndParseLimit(t *testing.T) {
	clamp := map[int]int{-1: 20, 0: 20, 1: 1, 50: 50, 100: 100, 101: 100}
	for in, want := range clamp {
		if got := pagination.ClampLimit(in); got != want {
			t.Errorf("ClampLimit(%d) = %d", in, got)
		}
	}
	parse := []struct {
		in      string
		want    int
		wantErr bool
	}{{"", 20, false}, {"5", 5, false}, {"500", 100, false}, {"0", 0, true}, {"-1", 0, true}, {"abc", 0, true}}
	for _, tt := range parse {
		got, err := pagination.ParseLimit(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseLimit(%q) = %d, %v", tt.in, got, err)
		}
	}
}

type item struct {
	at time.Time
	id uuid.UUID
}

func TestPage(t *testing.T) {
	key := func(i item) (time.Time, uuid.UUID) { return i.at, i.id }
	now := time.Now().UTC()
	mk := func(n int) []item {
		out := make([]item, n)
		for i := range out {
			out[i] = item{now.Add(-time.Duration(i) * time.Minute), uuid.New()}
		}
		return out
	}

	got, meta := pagination.Page[item](nil, 2, "f", key)
	if got == nil || len(got) != 0 || meta.HasMore || meta.NextCursor != "" {
		t.Fatalf("empty page = %v %+v", got, meta)
	}

	items := mk(2)
	got, meta = pagination.Page(items, 2, "f", key)
	if len(got) != 2 || meta.HasMore || meta.Limit != 2 {
		t.Fatalf("exact page = %d %+v", len(got), meta)
	}

	items = mk(3)
	got, meta = pagination.Page(items, 2, "f", key)
	if len(got) != 2 || !meta.HasMore {
		t.Fatalf("more page = %d %+v", len(got), meta)
	}
	c, err := pagination.Decode(meta.NextCursor, "f")
	if err != nil || c.ID != items[1].id || !c.Time.Equal(items[1].at) {
		t.Fatalf("next cursor = %+v %v", c, err)
	}
}
