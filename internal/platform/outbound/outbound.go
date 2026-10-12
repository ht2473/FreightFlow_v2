// Package outbound — HTTP-клиент для внешних источников данных: только разрешённые хосты,
// таймауты, повторы с паузой, условные запросы, запрет обращений во внутреннюю сеть.
// Российские государственные сайты запрашиваются напрямую (в обход системного прокси)
// с доверием к корневому сертификату Минцифры.
package outbound

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Ошибки клиента.
var (
	ErrHostNotAllowed = errors.New("outbound: host is not in the allowlist")
	ErrPrivateAddress = errors.New("outbound: private network address is not allowed")
	ErrNotModified    = errors.New("outbound: not modified")
)

// Config — параметры клиента одного источника.
type Config struct {
	// Hosts — разрешённые хосты: точное имя или «*.домен» для поддоменов.
	Hosts []string
	// Direct — соединение в обход системного прокси (российские госсайты).
	Direct bool
	// RootCAFile — дополнительный корневой сертификат (PEM), например Минцифры.
	RootCAFile string
	// Timeout — предел на один запрос, включая чтение тела; 0 — 60 секунд. Для больших
	// файлов задаётся с запасом на всю загрузку.
	Timeout time.Duration
	// Retries — число повторов при сетевых сбоях и ответах 429/502/503/504; 0 — 3.
	Retries int
	// UserAgent — заголовок User-Agent.
	UserAgent string
	// AllowPrivate разрешает адреса локальной сети (только для своих служб и тестов).
	AllowPrivate bool
}

// Client — HTTP-клиент источника.
type Client struct {
	http      *http.Client
	retries   int
	userAgent string
	sleep     func(context.Context, time.Duration) error
}

// New создаёт клиент. Контекст ограничивает определение адресов системного прокси.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if len(cfg.Hosts) == 0 {
		return nil, errors.New("outbound: allowlist is empty")
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.RootCAFile != "" {
		pool, err := rootPool(cfg.RootCAFile)
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = pool
	}

	proxy := http.ProxyFromEnvironment
	if cfg.Direct {
		proxy = nil
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	if !cfg.AllowPrivate {
		dialer.Control = publicOnly(proxyAddrs(ctx))
	}
	transport := &http.Transport{
		Proxy:                 proxy,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       tlsCfg,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	retries := cfg.Retries
	if retries == 0 {
		retries = 3
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "FreightFlow"
	}
	return &Client{
		http: &http.Client{
			Transport: allowlist{hosts: cfg.Hosts, next: transport},
			Timeout:   timeout,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("outbound: too many redirects")
				}
				return nil
			},
		},
		retries:   retries,
		userAgent: ua,
		sleep:     sleepCtx,
	}, nil
}

// Validators — значения для условного запроса из предыдущего ответа.
type Validators struct {
	ETag         string
	LastModified string
}

// Response — ответ с телом; тело закрывает вызывающий.
type Response struct {
	*http.Response
	Validators Validators
}

// Get выполняет GET с повторами. При совпадении валидаторов возвращает ErrNotModified.
func (c *Client) Get(ctx context.Context, rawURL string, v Validators) (*Response, error) {
	var last error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, backoff(attempt, last)); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("outbound: %w", err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		if v.ETag != "" {
			req.Header.Set("If-None-Match", v.ETag)
		}
		if v.LastModified != "" {
			req.Header.Set("If-Modified-Since", v.LastModified)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			if !retryableErr(err) || ctx.Err() != nil {
				return nil, err
			}
			last = err
			continue
		}
		switch {
		case resp.StatusCode == http.StatusNotModified:
			drain(resp)
			return nil, ErrNotModified
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return &Response{Response: resp, Validators: Validators{
				ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"),
			}}, nil
		case retryableStatus(resp.StatusCode):
			last = &statusError{code: resp.StatusCode, retryAfter: retryAfter(resp)}
			drain(resp)
		default:
			drain(resp)
			return nil, &statusError{code: resp.StatusCode}
		}
	}
	return nil, fmt.Errorf("outbound: %s failed after %d attempts: %w", redact(rawURL), c.retries+1, last)
}

// statusError — неуспешный код ответа.
type statusError struct {
	code       int
	retryAfter time.Duration
}

func (e *statusError) Error() string { return "outbound: unexpected status " + strconv.Itoa(e.code) }

// StatusCode возвращает код ответа из ошибки клиента, если он есть.
func StatusCode(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.code
	}
	return 0
}

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout
}

func retryableErr(err error) bool {
	return !errors.Is(err, ErrHostNotAllowed) && !errors.Is(err, ErrPrivateAddress)
}

// backoff — пауза перед повтором: Retry-After сервера или 1, 2, 4… секунд (до 30).
func backoff(attempt int, last error) time.Duration {
	var se *statusError
	if errors.As(last, &se) && se.retryAfter > 0 {
		return min(se.retryAfter, 2*time.Minute)
	}
	return min(time.Duration(1<<(attempt-1))*time.Second, 30*time.Second)
}

func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// redact убирает параметры запроса из адреса для журналов и ошибок (там бывают ключи API).
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid url"
	}
	u.RawQuery = ""
	u.User = nil
	return u.String()
}

// allowlist пропускает запросы только к разрешённым хостам, в том числе при перенаправлениях.
type allowlist struct {
	hosts []string
	next  http.RoundTripper
}

func (a allowlist) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
		return nil, fmt.Errorf("%w: scheme %s", ErrHostNotAllowed, req.URL.Scheme)
	}
	if !hostAllowed(a.hosts, req.URL.Hostname()) {
		return nil, fmt.Errorf("%w: %s", ErrHostNotAllowed, req.URL.Hostname())
	}
	return a.next.RoundTrip(req)
}

func hostAllowed(hosts []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range hosts {
		h = strings.ToLower(h)
		if suffix, ok := strings.CutPrefix(h, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == h {
			return true
		}
	}
	return false
}

// publicOnly запрещает соединения с адресами локальной сети. Исключение — адреса
// системного прокси: через него идут запросы, если источник не помечен как прямой.
func publicOnly(proxies map[string]bool) func(network, address string, _ syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		if proxies[address] {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return err
		}
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified() || ip.IsMulticast() || cgnat.Contains(ip) {
			return fmt.Errorf("%w: %s", ErrPrivateAddress, ip)
		}
		return nil
	}
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// proxyAddrs — адреса системного прокси из окружения (host:port).
func proxyAddrs(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		u, err := url.Parse(v)
		if err != nil || u.Host == "" {
			continue
		}
		host, port := u.Hostname(), u.Port()
		if port == "" {
			port = "80"
		}
		lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ips, err := net.DefaultResolver.LookupHost(lctx, host)
		cancel()
		if err != nil {
			continue
		}
		for _, ip := range ips {
			out[net.JoinHostPort(ip, port)] = true
		}
	}
	return out
}

// rootPool — системные корневые сертификаты плюс сертификаты из файла.
func rootPool(file string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	pem, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read root CA %s: %w", file, err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("root CA %s: no PEM certificates", file)
	}
	return pool, nil
}
