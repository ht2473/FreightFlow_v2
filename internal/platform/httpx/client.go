package httpx

import (
	"context"
	"fmt"
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
// от доверенного обратного прокси (адреса trustedProxies). Адресом клиента считается крайний
// справа адрес цепочки, не принадлежащий доверенным прокси: левые элементы клиент может подделать.
func WithClient(trustedProxies ...netip.Prefix) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := Client{IP: remoteIP(r.RemoteAddr), UserAgent: r.UserAgent()}
			if c.IP.IsValid() && trusted(c.IP, trustedProxies) {
				if ip, ok := forwardedFor(r.Header.Values("X-Forwarded-For"), trustedProxies); ok {
					c.IP = ip
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey{}, c)))
		})
	}
}

// forwardedFor — крайний справа адрес X-Forwarded-For вне доверенных прокси.
func forwardedFor(values []string, trustedProxies []netip.Prefix) (netip.Addr, bool) {
	var hops []string
	for _, v := range values {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return netip.Addr{}, false
		}
		ip = ip.Unmap()
		if !trusted(ip, trustedProxies) {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

// ParsePrefixes разбирает список сетей и адресов (адрес — сеть из одного узла).
func ParsePrefixes(items []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(items))
	for _, raw := range items {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			p, err := netip.ParsePrefix(raw)
			if err != nil {
				return nil, fmt.Errorf("parse network %q: %w", raw, err)
			}
			out = append(out, p.Masked())
			continue
		}
		ip, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, fmt.Errorf("parse address %q: %w", raw, err)
		}
		ip = ip.Unmap()
		out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
	}
	return out, nil
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
