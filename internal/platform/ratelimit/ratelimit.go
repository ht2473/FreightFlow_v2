// Package ratelimit ограничивает частоту запросов по ключу (адрес клиента, учётная запись, токен).
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter — набор «вёдер токенов» по ключам. Неиспользуемые ключи вытесняются.
type Limiter struct {
	mu      sync.Mutex
	every   rate.Limit
	burst   int
	buckets map[string]*bucket
	maxKeys int
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// New создаёт ограничитель: не более burst запросов подряд и в среднем один запрос за interval.
func New(interval time.Duration, burst int) *Limiter {
	return &Limiter{
		every:   rate.Every(interval),
		burst:   burst,
		buckets: make(map[string]*bucket),
		maxKeys: 10000,
	}
}

// Allow сообщает, разрешён ли очередной запрос для ключа.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.prune(now)
		}
		b = &bucket{lim: rate.NewLimiter(l.every, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	return b.lim.AllowN(now, 1)
}

// prune удаляет ключи, не использовавшиеся дольше времени полного восстановления ведра.
func (l *Limiter) prune(now time.Time) {
	idle := time.Duration(float64(l.burst)/float64(l.every)) * time.Second
	if idle < time.Minute {
		idle = time.Minute
	}
	for k, b := range l.buckets {
		if now.Sub(b.seen) > idle {
			delete(l.buckets, k)
		}
	}
}
