package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// IPResolver menentukan IP klien. X-Forwarded-For HANYA dipercaya bila request
// datang langsung dari proxy tepercaya (TRUSTED_PROXIES); jika kosong, selalu
// memakai RemoteAddr (header bisa dipalsukan oleh klien).
type IPResolver struct {
	trusted []netip.Prefix
}

// NewIPResolver menerima daftar CIDR atau IP tunggal (mis. "10.0.0.0/8", "127.0.0.1").
func NewIPResolver(trustedProxies []string) (*IPResolver, error) {
	res := &IPResolver{}
	for _, raw := range trustedProxies {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q: %w", raw, err)
			}
			res.trusted = append(res.trusted, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", raw, err)
		}
		res.trusted = append(res.trusted, p.Masked())
	}
	return res, nil
}

func (r *IPResolver) isTrusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ClientIP mengembalikan IP klien dari request. Algoritma: mulai dari RemoteAddr;
// selama hop saat ini adalah proxy tepercaya, mundur ke entri XFF paling kanan.
func (r *IPResolver) ClientIP(req *http.Request) string {
	remote := remoteAddr(req)
	addr, err := netip.ParseAddr(remote)
	if err != nil || len(r.trusted) == 0 || !r.isTrusted(addr) {
		return remote
	}

	hops := strings.Split(strings.Join(req.Header.Values("X-Forwarded-For"), ","), ",")
	client := addr
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break // entri rusak: berhenti di hop tepercaya terakhir
		}
		client = hop.Unmap()
		if !r.isTrusted(client) {
			break
		}
	}
	return client.String()
}

func remoteAddr(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return a.Unmap().String()
	}
	return host
}

type clientIPKey struct{}

// ClientIP middleware menyimpan IP klien yang sudah di-resolve ke context.
func ClientIP(res *IPResolver) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), clientIPKey{}, res.ClientIP(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIPFromRequest mengambil IP dari context (diisi middleware ClientIP);
// fallback ke RemoteAddr bila middleware tidak terpasang (mis. di unit test).
func ClientIPFromRequest(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok && ip != "" {
		return ip
	}
	return remoteAddr(r)
}
