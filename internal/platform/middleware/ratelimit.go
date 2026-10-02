package middleware

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/platform/logger"
)

// RateLimiter adalah port rate limit. Implementasi MVP in-memory (single
// instance); nanti bisa diganti Redis tanpa mengubah middleware/handler.
type RateLimiter interface {
	// Allow mengonsumsi satu token untuk key. retryAfter > 0 bila ditolak.
	Allow(ctx context.Context, key string) (ok bool, retryAfter time.Duration)
}

// RateConfig adalah konfigurasi token bucket: Limit request per Period, Burst maksimum.
type RateConfig struct {
	Limit  int
	Period time.Duration
	Burst  int
}

// every mengonversi ke rate.Limit (token per detik); Limit<=0 berarti tanpa batas.
func (l RateConfig) every() rate.Limit {
	if l.Limit <= 0 || l.Period <= 0 {
		return rate.Inf
	}
	return rate.Limit(float64(l.Limit) / l.Period.Seconds())
}

type visitor struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// MemoryLimiter: token bucket per key di memory, dengan janitor yang membuang
// key yang idle lebih lama dari ttl (mencegah memory leak).
type MemoryLimiter struct {
	cfg      RateConfig
	ttl      time.Duration
	now      func() time.Time
	mu       sync.Mutex
	visitors map[string]*visitor
}

var _ RateLimiter = (*MemoryLimiter)(nil)

// NewMemoryLimiter membuat limiter dan menjalankan janitor sampai ctx dibatalkan.
func NewMemoryLimiter(ctx context.Context, cfg RateConfig, ttl time.Duration) *MemoryLimiter {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	m := &MemoryLimiter{cfg: cfg, ttl: ttl, now: time.Now, visitors: make(map[string]*visitor)}
	go m.janitor(ctx)
	return m
}

// Allow mengonsumsi satu token untuk key.
func (m *MemoryLimiter) Allow(_ context.Context, key string) (bool, time.Duration) {
	now := m.now()
	m.mu.Lock()
	v, ok := m.visitors[key]
	if !ok {
		burst := max(m.cfg.Burst, 1)
		v = &visitor{lim: rate.NewLimiter(m.cfg.every(), burst)}
		m.visitors[key] = v
	}
	v.lastSeen = now
	m.mu.Unlock()

	r := v.lim.ReserveN(now, 1)
	if !r.OK() {
		return false, m.cfg.Period
	}
	delay := r.DelayFrom(now)
	if delay == 0 {
		return true, 0
	}
	r.CancelAt(now) // jangan konsumsi token bila request ditolak
	return false, delay
}

// Len mengembalikan jumlah key yang dilacak (untuk test/metrics).
func (m *MemoryLimiter) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.visitors)
}

// Cleanup membuang key yang idle lebih lama dari ttl.
func (m *MemoryLimiter) Cleanup() {
	cutoff := m.now().Add(-m.ttl)
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.visitors {
		if v.lastSeen.Before(cutoff) {
			delete(m.visitors, k)
		}
	}
}

func (m *MemoryLimiter) janitor(ctx context.Context) {
	t := time.NewTicker(m.ttl / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Cleanup()
		}
	}
}

// KeyFunc menghasilkan key rate limit dari request. Kembalikan "" untuk melewati limit.
type KeyFunc func(r *http.Request) string

// KeyByIP memakai IP klien (hasil middleware ClientIP) sebagai key.
func KeyByIP(prefix string) KeyFunc {
	return func(r *http.Request) string { return prefix + ":ip:" + ClientIPFromRequest(r) }
}

// RateLimit menolak request dengan 429 RATE_LIMITED + Retry-After
// (format error httpx) bila limiter menolak.
func RateLimit(l RateLimiter, key KeyFunc) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if k == "" {
				next.ServeHTTP(w, r)
				return
			}
			ok, retry := l.Allow(r.Context(), k)
			if !ok {
				secs := int(math.Ceil(retry.Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
				logger.FromContext(r.Context()).WarnContext(r.Context(), "rate limit exceeded",
					"path", r.URL.Path)
				httpx.WriteError(w, r, httpx.ErrRateLimited)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
