package middleware

import (
	"sync"
	"time"
)

// repeatLogLimiter suppresses repeated identical log lines inside a window,
// reporting how many were dropped so the count is never lost.
//
// Why: the auth middlewares log once per request when a security check cannot
// be performed. During a Redis outage that is one ERROR line per authenticated
// request across the whole fleet — the outage then reads as a log storm and
// buries the one line that matters. The first line in each window is emitted
// with the suppressed count from the previous window attached.
type repeatLogLimiter struct {
	window time.Duration

	mu         sync.Mutex
	lastLogged time.Time
	suppressed int
}

func newRepeatLogLimiter(window time.Duration) *repeatLogLimiter {
	if window <= 0 {
		window = time.Minute
	}
	return &repeatLogLimiter{window: window}
}

// allow reports whether this occurrence should be logged. When it returns
// false, suppressed is the number of lines dropped so far in the current window
// and should be carried into the next logged line.
func (l *repeatLogLimiter) allow(now time.Time) (bool, int) {
	if l == nil {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lastLogged.IsZero() || now.Sub(l.lastLogged) >= l.window {
		dropped := l.suppressed
		l.lastLogged = now
		l.suppressed = 0
		return true, dropped
	}
	l.suppressed++
	return false, 0
}
