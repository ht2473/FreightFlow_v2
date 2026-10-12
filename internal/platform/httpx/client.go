package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Client — сведения о клиенте запроса.
type Client struct {
	IP        netip.Addr
	UserAgent string
}

type clientKey struct{}

// ClientFrom возвращает сведения о клиенте текущего запроса.
func ClientFrom(ctx context.Context) Client {
	c, _ := ctx.Value(clientKey{}).(Client)
	return c
}

// WithClient определяет адрес клиента. Заголовок X-Forwarded-For учитывается только для запросов
// от доверенного обратного прокси (адреса trustedProxies).
func WithClient(trustedProxies ...netip.Prefix) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := Client{IP: remoteIP(r.RemoteAddr), UserAgent: r.UserAgent()}
			if c.IP.IsValid() && trusted(c.IP, trustedProxies) {
				if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					first := strings.TrimSpace(strings.Split(xff, ",")[0])
					if ip, err := netip.ParseAddr(first); err == nil {
						c.IP = ip
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey{}, c)))
		})
	}
}

func remoteIP(addr string) netip.Addr {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return ip.Unmap()
}

func trusted(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
