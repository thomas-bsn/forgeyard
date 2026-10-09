package auth

import (
	"sync"
	"time"
)

// LoginLimiter blocks a key (an IP address) after too many failed logins within a window.
type LoginLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	failures map[string][]time.Time
}

// NewLoginLimiter allows at most max failures per key within window.
func NewLoginLimiter(max int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{max: max, window: window, failures: make(map[string][]time.Time)}
}

// Allowed reports whether key may attempt to log in now.
func (l *LoginLimiter) Allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key)) < l.max
}

// Fail records a failed attempt for key.
func (l *LoginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[key] = append(l.recent(key), time.Now())
}

// Reset forgets the failures of key, after a successful login.
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// recent drops failures older than the window. The caller must hold l.mu.
func (l *LoginLimiter) recent(key string) []time.Time {
	cutoff := time.Now().Add(-l.window)
	kept := l.failures[key][:0]
	for _, t := range l.failures[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = kept
	return kept
}
