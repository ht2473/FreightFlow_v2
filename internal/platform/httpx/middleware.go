package httpx

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/problem"
)

// Middleware оборачивает обработчик.
type Middleware func(http.Handler) http.Handler

// Chain применяет промежуточные обработчики в порядке перечисления: первый — внешний.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey int

const (
	requestIDKey ctxKey = iota
	nonceKey
)

// RequestID возвращает идентификатор текущего запроса.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// CSPNonce возвращает nonce для встроенных скриптов текущей страницы.
func CSPNonce(ctx context.Context) string {
	n, _ := ctx.Value(nonceKey).(string)
	return n
}

// WithRequestID присваивает запросу идентификатор и возвращает его в заголовке X-Request-ID.
func WithRequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" || len(id) > 64 {
				id = ids.NewString()
			}
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		})
	}
}

// Recover перехватывает панику обработчика, пишет её в журнал и отвечает ошибкой 500.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer recoverPanic(log, w, r)
			next.ServeHTTP(w, r)
		})
	}
}

// recoverPanic вызывается отложенно: recover работает только непосредственно в defer-функции.
func recoverPanic(log *slog.Logger, w http.ResponseWriter, r *http.Request) {
	v := recover()
	if v == nil {
		return
	}
	if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
		panic(v)
	}
	log.ErrorContext(r.Context(), "panic in handler", "request_id", RequestID(r.Context()),
		"path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
	problem.Write(w, r, problem.Internal)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog пишет в журнал строку на каждый запрос.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			}
			log.LogAttrs(r.Context(), level, "http request",
				slog.String("request_id", RequestID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

// SecurityHeaders задаёт заголовки безопасности и политику CSP с nonce для страниц.
// strictTransport включает HSTS (только при работе за HTTPS).
func SecurityHeaders(strictTransport bool, extraConnect ...string) Middleware {
	connect := "'self'"
	for _, c := range extraConnect {
		connect += " " + c
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce := newNonce()
			h := w.Header()
			h.Set("Content-Security-Policy", "default-src 'self'; "+
				"script-src 'self' 'nonce-"+nonce+"'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: blob:; "+
				"font-src 'self'; "+
				"worker-src 'self' blob:; "+
				"connect-src "+connect+"; "+
				"frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(self), payment=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			if strictTransport {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceKey, nonce)))
		})
	}
}

// CrossOrigin отклоняет межсайтовые изменяющие запросы (защита от CSRF по Sec-Fetch-Site и Origin).
func CrossOrigin(trusted ...string) (Middleware, error) {
	cop := http.NewCrossOriginProtection()
	for _, o := range trusted {
		if err := cop.AddTrustedOrigin(o); err != nil {
			return nil, err
		}
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		problem.Write(w, r, problem.Forbidden.WithDetail("Межсайтовый запрос отклонён"))
	}))
	return cop.Handler, nil
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}
