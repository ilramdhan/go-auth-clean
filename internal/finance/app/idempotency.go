package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// IdempotentRequest mengidentifikasi satu request POST yang bisa di-retry.
type IdempotentRequest struct {
	UserID uuid.UUID
	Key    string
	Method string
	Path   string
	Body   []byte
}

// ValidateIdempotencyKey: 8-128 karakter ASCII yang tercetak.
func ValidateIdempotencyKey(key string) error {
	if key == "" {
		return ErrIdempotencyKeyRequired
	}
	if len(key) < 8 || len(key) > 128 {
		return ErrInvalidIdempotencyKey
	}
	for i := range len(key) {
		if key[i] < 0x21 || key[i] > 0x7e {
			return ErrInvalidIdempotencyKey
		}
	}
	return nil
}

// RequestHash = sha256(method + path + body). Body sudah dikanonisasi oleh
// pemanggil (mis. hasil re-marshal DTO) supaya beda spasi tidak dianggap beda.
func RequestHash(method, path string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(strings.ToUpper(method)))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(body)
	return h.Sum(nil)
}

// Idempotent menjalankan fn tepat sekali per (user, key) dalam satu DB
// transaction: klaim key, jalankan use case, simpan response. Bila key sudah
// selesai dengan request yang sama, response tersimpan dikembalikan (replayed
// = true) tanpa menjalankan fn. Request berbeda dengan key sama ditolak.
// Bila fn gagal, tx rollback sehingga key tidak tersimpan dan client boleh retry.
func (s *Service) Idempotent(ctx context.Context, req IdempotentRequest, fn func(ctx context.Context) (StoredResponse, error)) (resp StoredResponse, replayed bool, err error) {
	if err := ValidateIdempotencyKey(req.Key); err != nil {
		return StoredResponse{}, false, err
	}
	hash := RequestHash(req.Method, req.Path, req.Body)
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		now := s.now()
		stored, err := s.idem.Claim(ctx, IdempotencyRecord{
			UserID: req.UserID, Key: req.Key, Method: req.Method, Path: req.Path,
			RequestHash: hash, Now: now, ExpiresAt: now.Add(IdempotencyTTL),
		})
		if err != nil {
			return err
		}
		if stored != nil {
			resp, replayed = *stored, true
			return nil
		}
		resp, err = fn(ctx)
		if err != nil {
			return err
		}
		if err := s.idem.Complete(ctx, req.UserID, req.Key, resp); err != nil {
			return fmt.Errorf("finance.Idempotent complete: %w", err)
		}
		return nil
	})
	if err != nil {
		return StoredResponse{}, false, err
	}
	return resp, replayed, nil
}

// MatchHash dipakai implementasi store untuk membandingkan hash request.
func MatchHash(a, b []byte) bool { return bytes.Equal(a, b) }

// PurgeExpiredIdempotencyKeys menghapus key kedaluwarsa (job harian).
func (s *Service) PurgeExpiredIdempotencyKeys(ctx context.Context) (int64, error) {
	n, err := s.idem.DeleteExpired(ctx, s.now())
	if err != nil {
		return 0, fmt.Errorf("finance.PurgeExpiredIdempotencyKeys: %w", err)
	}
	return n, nil
}
