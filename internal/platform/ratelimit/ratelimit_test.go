package ratelimit

import (
	"testing"
	"time"
)

func TestAllowN(t *testing.T) {
	t.Parallel()
	l := New(time.Hour, 10)
	if !l.AllowN("u", 8) || l.AllowN("u", 3) || !l.Allow("u") || !l.Allow("u") || l.Allow("u") {
		t.Error("bucket of 10: 8 + 1 + 1 allowed, more denied")
	}
	if !l.AllowN("v", 10) {
		t.Error("keys are independent")
	}
}
