package cache

import (
	"testing"
	"time"
)

func TestTTL(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	c := New[int](time.Minute, 2)
	c.now = func() time.Time { return now }
	c.Set("a", 1)
	now = now.Add(10 * time.Second)
	c.Set("b", 2)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("a = %d, %v", v, ok)
	}
	// Третья запись вытесняет ту, что истекает раньше всех.
	now = now.Add(5 * time.Second)
	c.Set("c", 3)
	if _, ok := c.Get("a"); ok {
		t.Error("a not evicted")
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Errorf("b = %d, %v", v, ok)
	}
	// Просроченное значение не возвращается: b истекла, c — ещё нет.
	now = now.Add(57 * time.Second)
	if _, ok := c.Get("b"); ok {
		t.Error("b not expired")
	}
	if v, ok := c.Get("c"); !ok || v != 3 {
		t.Errorf("c = %d, %v", v, ok)
	}
}
