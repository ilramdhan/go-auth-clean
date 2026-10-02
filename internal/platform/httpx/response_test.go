package httpx_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/platform/requestid"
)

func TestWriteError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"http error", &httpx.Error{Status: 409, Code: "CONFLICT", Message: "x"}, 409, "CONFLICT"},
		{"wrapped http error", errors.Join(errors.New("ctx"), httpx.ErrNotFound), 404, "NOT_FOUND"},
		{"internal error hidden", errors.New("pq: secret detail"), 500, "INTERNAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(requestid.WithContext(req.Context(), "rid-1"))
			rec := httptest.NewRecorder()
			httpx.WriteError(rec, req, tt.err)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "secret") {
				t.Fatal("detail internal bocor ke client")
			}
			var body httpx.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tt.wantCode || body.RequestID != "rid-1" {
				t.Fatalf("body = %+v", body)
			}
		})
	}
}

func TestDataAndList(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.Data(rec, http.StatusCreated, map[string]int{"a": 1})
	if rec.Code != 201 || strings.TrimSpace(rec.Body.String()) != `{"data":{"a":1}}` {
		t.Fatalf("Data = %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	httpx.List(rec, []int{1}, map[string]bool{"has_more": false})
	if strings.TrimSpace(rec.Body.String()) != `{"data":[1],"meta":{"has_more":false}}` {
		t.Fatalf("List = %s", rec.Body)
	}
}

func TestDecode(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	tests := []struct {
		name        string
		body        string
		contentType string
		limit       int64
		wantStatus  int
	}{
		{"valid", `{"name":"a","age":1}`, "application/json", 0, 0},
		{"valid with charset", `{"name":"a"}`, "application/json; charset=utf-8", 0, 0},
		{"no content type allowed", `{"name":"a"}`, "", 0, 0},
		{"wrong content type", `{"name":"a"}`, "text/plain", 0, 415},
		{"empty body", ``, "application/json", 0, 400},
		{"unknown field", `{"x":1}`, "application/json", 0, 400},
		{"wrong type", `{"age":"x"}`, "application/json", 0, 400},
		{"trailing data", `{"name":"a"}{"name":"b"}`, "application/json", 0, 400},
		{"malformed", `{"name":`, "application/json", 0, 400},
		{"too large", `{"name":"` + strings.Repeat("a", 100) + `"}`, "application/json", 10, 413},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			var dst payload
			var err error
			if tt.limit > 0 {
				err = httpx.DecodeLimit(httptest.NewRecorder(), req, &dst, tt.limit)
			} else {
				err = httpx.Decode(httptest.NewRecorder(), req, &dst)
			}
			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("unexpected err %v", err)
				}
				return
			}
			httpErr, ok := errors.AsType[*httpx.Error](err)
			if !ok || httpErr.Status != tt.wantStatus {
				t.Fatalf("err = %v, want status %d", err, tt.wantStatus)
			}
		})
	}
}
