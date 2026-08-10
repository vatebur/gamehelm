package main

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

const (
	maxLoginFailures = 5
	loginLockTime    = 15 * time.Minute
)

type session struct {
	CSRF string
}

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]session)}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *sessionStore) create() (string, session, error) {
	token, err := randomToken()
	if err != nil {
		return "", session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", session{}, err
	}
	sess := session{CSRF: csrf}
	s.mu.Lock()
	s.sessions[token] = sess
	s.mu.Unlock()
	return token, sess, nil
}

func (s *sessionStore) get(token string) (session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[token]
	return sess, ok
}

func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

type loginAttempt struct {
	Failures    int
	LockedUntil time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	now      func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string]loginAttempt), now: time.Now}
}

func (l *loginLimiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.attempts[ip]
	if a.LockedUntil.IsZero() || !l.now().Before(a.LockedUntil) {
		if !a.LockedUntil.IsZero() {
			delete(l.attempts, ip)
		}
		return true
	}
	return false
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.attempts[ip]
	a.Failures++
	if a.Failures >= maxLoginFailures {
		a.LockedUntil = l.now().Add(loginLockTime)
	}
	l.attempts[ip] = a
}

func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	delete(l.attempts, ip)
	l.mu.Unlock()
}
