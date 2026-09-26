// Package ratelimit is an in-memory token bucket per key (an IP, an email,
// a webhook token, an API caller). It is per process, which is right for a
// single server; several servers would share limits through Redis instead.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Limiter allows Burst requests at once per key, refilled at Rate per second.
type Limiter struct {
	rate  float64 // tokens per second
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

// New allows n requests per period per key, with bursts up to burst.
func New(n int, per time.Duration, burst int) *Limiter {
	return &Limiter{
		rate:    float64(n) / per.Seconds(),
		burst:   float64(max(burst, 1)),
		now:     time.Now,
		buckets: map[string]*bucket{},
	}
}

// refill returns the key's bucket brought up to date. Callers hold mu.
func (l *Limiter) refill(key string, now time.Time) *bucket {
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
		return b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.at).Seconds()*l.rate)
	b.at = now
	return b
}

// wait is how long until one token is available.
func (l *Limiter) wait(b *bucket) time.Duration {
	return time.Duration(math.Ceil((1 - b.tokens) / l.rate * float64(time.Second)))
}

// Allow takes a token for key. When none is left it reports how long to wait.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	b := l.refill(key, now)
	if b.tokens < 1 {
		return false, l.wait(b)
	}
	b.tokens--
	return true, 0
}

// Check reports whether key has a token left, without taking one. Use it
// with Take to charge only for failures (e.g. wrong passwords).
func (l *Limiter) Check(key string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key, l.now())
	if b.tokens < 1 {
		return false, l.wait(b)
	}
	return true, 0
}

// Take charges key one token, going no lower than zero.
func (l *Limiter) Take(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	b := l.refill(key, now)
	b.tokens = math.Max(0, b.tokens-1)
}

// gc drops buckets that have refilled completely (they carry no state).
// Callers hold mu.
func (l *Limiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < time.Minute {
		return
	}
	l.lastGC = now
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.at) >= full {
			delete(l.buckets, k)
		}
	}
}

// Len is the number of keys being tracked (for tests and metrics).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
