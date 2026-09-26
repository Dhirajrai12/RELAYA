package ratelimit

import (
	"testing"
	"time"
)

func clock(l *Limiter) *time.Time {
	t := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return t }
	return &t
}

func TestBurstThenRefill(t *testing.T) {
	l := New(60, time.Minute, 3) // 1/s, burst 3
	now := clock(l)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d should pass", i+1)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait != time.Second {
		t.Fatalf("4th: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("keys are independent")
	}
	*now = now.Add(1500 * time.Millisecond)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a token refills after 1s")
	}
	if ok, wait := l.Allow("a"); ok || wait != 500*time.Millisecond {
		t.Fatalf("half a token left: ok=%v wait=%v", ok, wait)
	}
}

func TestCheckAndTakeChargeOnlyFailures(t *testing.T) {
	l := New(2, time.Hour, 2)
	clock(l)
	for i := 0; i < 5; i++ {
		if ok, _ := l.Check("user"); !ok {
			t.Fatal("Check must not consume")
		}
	}
	l.Take("user")
	l.Take("user")
	l.Take("user") // stays at zero
	if ok, wait := l.Check("user"); ok || wait != 30*time.Minute {
		t.Fatalf("after 2 failures: ok=%v wait=%v", ok, wait)
	}
}

func TestGCDropsFullBuckets(t *testing.T) {
	l := New(60, time.Minute, 5)
	now := clock(l)
	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	*now = now.Add(2 * time.Minute)
	l.Allow("d") // triggers gc
	if l.Len() != 1 {
		t.Fatalf("tracked keys: %d", l.Len())
	}
}

func TestNilIsUnlimited(t *testing.T) {
	var l *Limiter
	if ok, _ := l.Allow("x"); !ok {
		t.Fatal("nil limiter must allow")
	}
	l.Take("x")
	if ok, _ := l.Check("x"); !ok {
		t.Fatal("nil limiter must allow")
	}
}
