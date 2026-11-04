package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

const (
	SessionIdleTimeout = 30 * time.Minute
	SessionLifetime    = 12 * time.Hour
	maximumSessions    = 1024
)

type Session struct {
	CSRF     string
	Created  time.Time
	LastSeen time.Time
}

type Sessions struct {
	mu      sync.Mutex
	entries map[string]Session
}

func NewSessions() *Sessions { return &Sessions{entries: make(map[string]Session)} }

func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func sessionExpired(s Session, now time.Time) bool {
	return now.Sub(s.LastSeen) >= SessionIdleTimeout || now.Sub(s.Created) >= SessionLifetime
}

func (s *Sessions) Create() (string, Session, error) {
	token, err := RandomToken()
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := RandomToken()
	if err != nil {
		return "", Session{}, err
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.entries {
		if sessionExpired(entry, now) {
			delete(s.entries, key)
		}
	}
	if len(s.entries) >= maximumSessions {
		return "", Session{}, errors.New("登录会话已满，请稍后重试")
	}
	entry := Session{CSRF: csrf, Created: now, LastSeen: now}
	s.entries[token] = entry
	return token, entry, nil
}

func (s *Sessions) Get(token string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	if !ok {
		return Session{}, false
	}
	now := time.Now()
	if sessionExpired(entry, now) {
		delete(s.entries, token)
		return Session{}, false
	}
	entry.LastSeen = now
	s.entries[token] = entry
	return entry, true
}

func (s *Sessions) Delete(token string) {
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
}
