package wire

import (
	"sync"
	"time"
)

// limiter is a token bucket. Pairing codes cannot be guessed, so this exists
// only to bound the work an unauthenticated peer can make the box do.
type limiter struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

func newLimiter(perMinute, burst int) *limiter {
	return &limiter{rate: float64(perMinute) / 60, burst: float64(burst), tokens: float64(burst)}
}

func (l *limiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.last.IsZero() {
		l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
