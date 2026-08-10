package main

import (
	"testing"
	"time"
)

func TestLoginLimiterLocksAfterFiveFailures(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	for range 5 {
		if !limiter.allowed("192.0.2.1") {
			t.Fatal("locked too early")
		}
		limiter.fail("192.0.2.1")
	}
	if limiter.allowed("192.0.2.1") {
		t.Fatal("expected address to be locked")
	}
	now = now.Add(15 * time.Minute)
	if !limiter.allowed("192.0.2.1") {
		t.Fatal("lock should expire after 15 minutes")
	}
}

func TestSessionTokensAreDistinct(t *testing.T) {
	store := newSessionStore()
	tokenA, sessionA, err := store.create()
	if err != nil {
		t.Fatal(err)
	}
	tokenB, sessionB, err := store.create()
	if err != nil {
		t.Fatal(err)
	}
	if tokenA == tokenB || sessionA.CSRF == sessionB.CSRF {
		t.Fatal("session or CSRF tokens must be unique")
	}
	store.delete(tokenA)
	if _, ok := store.get(tokenA); ok {
		t.Fatal("deleted session remains available")
	}
}
