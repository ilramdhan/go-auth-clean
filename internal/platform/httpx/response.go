// Package httpx berisi helper HTTP bersama: decode request, tulis response,
// dan format error standar.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"go-auth-clean/internal/platform/logger"
	"go-auth-clean/internal/platform/requestid"
)

// MaxBodyBytes adalah batas default ukuran body JSON (1 MiB).
const MaxBodyBytes int64 = 1 << 20

// DataResponse adalah envelope sukses: {"data": ...}.
type DataResponse struct {
	Data any `json:"data"`
}

// ListResponse adalah envelope list: {"data": [...], "meta": {...}}.
type ListResponse struct {
	Data any `json:"data"`
	Meta any `json:"meta"`
}

// ErrorBody adalah isi field "error" pada response gagal.
type ErrorBody struct {
	Code    string        `json:"code" example:"VALIDATION_FAILED"`
	Message string        `json:"message" example:"input tidak valid"`
	Details []FieldDetail `json:"details,omitempty"`
} //	@name	ErrorBody

// FieldDetail menjelaskan error per field input.
type FieldDetail struct {
	Field   string `json:"field" example:"email"`
	Message string `json:"message" example:"harus berupa email yang valid"`
} //	@name	FieldDetail

// ErrorResponse adalah envelope gagal: {"error": {...}, "request_id": "..."}.
// Dipakai juga sebagai tipe @Failure di anotasi swagger.
type ErrorResponse struct {
	Error     ErrorBody `json:"error"`
	RequestID string    `json:"request_id,omitempty" example:"0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6a"`
} //	@name	ErrorResponse

// Error adalah error yang sudah dipetakan ke protokol HTTP oleh adapter.
type Error struct {
	Status  int
	Code    string
	Message string
	Details []FieldDetail
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Error umum yang bisa dipakai ulang oleh semua adapter.
var (
	ErrUnsupportedMediaType = &Error{Status: http.StatusUnsupportedMediaType, Code: "UNSUPPORTED_MEDIA_TYPE", Message: "Content-Type harus application/json"}
	ErrBodyTooLarge         = &Error{Status: http.StatusRequestEntityTooLarge, Code: "BODY_TOO_LARGE", Message: "ukuran body melebihi batas"}
	ErrInvalidJSON          = &Error{Status: http.StatusBadRequest, Code: "INVALID_JSON", Message: "body JSON tidak valid"}
	ErrNotFound             = &Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "resource tidak ditemukan"}
	ErrRateLimited          = &Error{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "terlalu banyak request, coba lagi nanti"}
	ErrInternal             = &Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "terjadi kesalahan pada server"}
)

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Data(w http.ResponseWriter, status int, data any) {
	JSON(w, status, DataResponse{Data: data})
}

// List menulis envelope {"data": [...], "meta": meta} dengan status 200.
func List(w http.ResponseWriter, data, meta any) {
	JSON(w, http.StatusOK, ListResponse{Data: data, Meta: meta})
}

// WriteError menulis *Error apa adanya; error lain dianggap 500 dan di-log.
// Detail error internal tidak pernah dikirim ke client.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	httpErr, ok := errors.AsType[*Error](err)
	if !ok {
		logger.FromContext(r.Context()).ErrorContext(r.Context(), "internal error", slog.Any("error", err))
		httpErr = ErrInternal
	}
	JSON(w, httpErr.Status, ErrorResponse{
		Error:     ErrorBody{Code: httpErr.Code, Message: httpErr.Message, Details: httpErr.Details},
		RequestID: requestid.FromContext(r.Context()),
	})
}

// Decode membaca body JSON dengan batas MaxBodyBytes.
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	return DecodeLimit(w, r, dst, MaxBodyBytes)
}

// DecodeLimit membaca body JSON dengan batas ukuran tertentu (mis. 16 KiB untuk
// endpoint auth), mewajibkan Content-Type JSON, menolak field tak dikenal dan
// data sisa setelah objek pertama.
func DecodeLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || mt != "application/json" {
			return ErrUnsupportedMediaType
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return ErrBodyTooLarge
		}
		return &Error{Status: http.StatusBadRequest, Code: ErrInvalidJSON.Code, Message: "body hanya boleh berisi satu objek JSON"}
	}
	return nil
}

func decodeError(err error) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return ErrBodyTooLarge
	}
	if errors.Is(err, io.EOF) {
		return &Error{Status: http.StatusBadRequest, Code: ErrInvalidJSON.Code, Message: "body tidak boleh kosong"}
	}
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return &Error{
			Status: http.StatusBadRequest, Code: ErrInvalidJSON.Code, Message: "tipe data field tidak sesuai",
			Details: []FieldDetail{{Field: typeErr.Field, Message: "tipe harus " + typeErr.Type.String()}},
		}
	}
	// Pesan json decoder aman (tidak memuat data internal), cukup informatif untuk client.
	return &Error{Status: http.StatusBadRequest, Code: ErrInvalidJSON.Code, Message: "body tidak valid: " + err.Error()}
}
