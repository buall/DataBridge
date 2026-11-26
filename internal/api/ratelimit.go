package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type loginAttemptBucket struct {
	count       int
	windowStart time.Time
}

type loginRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]loginAttemptBucket
	limit   int
	window  time.Duration
}

func newLoginRateLimiter(limit int, window time.Duration) *loginRateLimiter {
	return &loginRateLimiter{
		buckets: make(map[string]loginAttemptBucket),
		limit:   limit,
		window:  window,
	}
}

func (l *loginRateLimiter) Allow(key string) bool {
	if l == nil || key == "" {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.buckets) > 1024 {
		for k, b := range l.buckets {
			if now.Sub(b.windowStart) >= l.window {
				delete(l.buckets, k)
			}
		}
	}

	bucket, ok := l.buckets[key]
	if !ok || now.Sub(bucket.windowStart) >= l.window {
		l.buckets[key] = loginAttemptBucket{count: 1, windowStart: now}
		return true
	}
	if bucket.count >= l.limit {
		return false
	}
	bucket.count++
	l.buckets[key] = bucket
	return true
}

func (l *loginRateLimiter) Reset(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
