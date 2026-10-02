// Package middleware berisi HTTP middleware lintas modul.
package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/platform/logger"
	"go-auth-clean/internal/platform/requestid"
)

// Middleware membungkus http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain menerapkan middleware dari luar ke dalam: Chain(h, a, b) == a(b(h)).
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// RequestIDFromContext mengambil request ID dari context (alias requestid.FromContext).
func RequestIDFromContext(ctx context.Context) string { return requestid.FromContext(ctx) }

// RequestID memberi setiap request ID unik dan menyimpan logger berisi
// request_id ke context, sehingga semua log dari request ini bisa dikorelasikan.
func RequestID(base *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if !validRequestID(id) {
				id = uuid.NewString()
			}
			w.Header().Set("X-Request-ID", id)

			ctx := requestid.WithContext(r.Context(), id)
			ctx = logger.WithContext(ctx, base.With(slog.String("request_id", id)))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// validRequestID menolak ID kosong, terlalu panjang, atau berisi karakter aneh
// (mencegah log injection lewat header X-Request-ID).
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		ok := c == '-' || c == '_' || c == '.' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

// Unwrap dipakai http.ResponseController (Flush, SetWriteDeadline, dll.).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// AccessLog mencatat satu baris log per request (log di edge, bukan di setiap layer).
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		}
		logger.FromContext(r.Context()).Log(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

// Recover mencegah panic mematikan server dan mengembalikan 500.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				logger.FromContext(r.Context()).ErrorContext(r.Context(), "panic recovered",
					slog.Any("panic", v), slog.String("stack", string(debug.Stack())))
				httpx.JSON(w, http.StatusInternalServerError, httpx.ErrorResponse{
					Error:     httpx.ErrorBody{Code: httpx.ErrInternal.Code, Message: httpx.ErrInternal.Message},
					RequestID: requestid.FromContext(r.Context()),
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
