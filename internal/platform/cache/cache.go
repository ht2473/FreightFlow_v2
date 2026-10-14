// Package cache — короткая память процесса для повторов одинаковых запросов.
package cache

import (
	"sync"
	"time"
)

// TTL хранит значения по ключу заданное время; сверх max записей вытесняются старейшие.
type TTL[V any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	max   int
	items map[string]entry[V]
	now   func() time.Time
}

type entry[V any] struct {
	v       V
	expires time.Time
}

// New создаёт кеш: значение живёт ttl, записей не больше max.
func New[V any](ttl time.Duration, maxItems int) *TTL[V] {
	return &TTL[V]{ttl: ttl, max: maxItems, items: make(map[string]entry[V]), now: time.Now}
}

// Get возвращает действующее значение.
func (c *TTL[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || !c.now().Before(e.expires) {
		var zero V
		return zero, false
	}
	return e.v, true
}

// Set сохраняет значение.
func (c *TTL[V]) Set(key string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if _, ok := c.items[key]; !ok && len(c.items) >= c.max {
		c.evict(now)
	}
	c.items[key] = entry[V]{v: v, expires: now.Add(c.ttl)}
}

// evict удаляет просроченные записи, а если их нет — запись, которая истекает раньше всех.
func (c *TTL[V]) evict(now time.Time) {
	oldest, first := "", time.Time{}
	for k, e := range c.items {
		if !now.Before(e.expires) {
			delete(c.items, k)
			continue
		}
		if oldest == "" || e.expires.Before(first) {
			oldest, first = k, e.expires
		}
	}
	if len(c.items) >= c.max && oldest != "" {
		delete(c.items, oldest)
	}
}
