package auth

import (
	"testing"
	"time"
)

func TestExpiredSessionsAreRejected(t *testing.T) {
	for _, mode := range []string{"idle", "absolute"} {
		t.Run(mode, func(t *testing.T) {
			store := NewSessions()
			now := time.Now()
			entry := Session{CSRF: "csrf", Created: now, LastSeen: now}
			if mode == "idle" {
				entry.LastSeen = now.Add(-SessionIdleTimeout)
			} else {
				entry.Created = now.Add(-SessionLifetime)
			}
			store.entries["expired"] = entry
			if _, ok := store.Get("expired"); ok {
				t.Fatal("expired session accepted")
			}
			if len(store.entries) != 0 {
				t.Fatal("expired session retained")
			}
		})
	}
}
