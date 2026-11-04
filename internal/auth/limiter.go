package auth

import (
	"sync"
	"time"
)

const (
	loginLockTime    = 15 * time.Minute
	maxLoginFailures = 5
	maximumAttempts  = 4096
)

type loginAttempt struct {
	Failures int
	Expires  time.Time
}

type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{attempts: make(map[string]loginAttempt)}
}

// Begin reserves an attempt before password hashing, including concurrent requests.
func (l *LoginLimiter) Begin(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for key, attempt := range l.attempts {
		if !now.Before(attempt.Expires) {
			delete(l.attempts, key)
		}
	}
	attempt, exists := l.attempts[ip]
	if attempt.Failures >= maxLoginFailures || (!exists && len(l.attempts) >= maximumAttempts) {
		return false
	}
	attempt.Failures++
	attempt.Expires = now.Add(loginLockTime)
	l.attempts[ip] = attempt
	return true
}

func (l *LoginLimiter) Success(ip string) {
	l.mu.Lock()
	delete(l.attempts, ip)
	l.mu.Unlock()
}
