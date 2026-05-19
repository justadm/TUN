package core

import (
	"sync"
	"time"
)

const (
	defaultHandshakeReplayTTL      = 2 * time.Minute
	defaultHandshakeReplayCapacity = 4096
)

type clientHelloReplayCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	entries  map[[16]byte]time.Time
}

func newClientHelloReplayCache(ttl time.Duration, capacity int) *clientHelloReplayCache {
	if ttl <= 0 {
		ttl = defaultHandshakeReplayTTL
	}
	if capacity <= 0 {
		capacity = defaultHandshakeReplayCapacity
	}
	return &clientHelloReplayCache{
		ttl:      ttl,
		capacity: capacity,
		entries:  make(map[[16]byte]time.Time, capacity),
	}
}

func (c *clientHelloReplayCache) seenOrAdd(nonce [16]byte, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.evictExpiredLocked(now)
	if ts, ok := c.entries[nonce]; ok && now.Sub(ts) <= c.ttl {
		return true
	}
	if len(c.entries) >= c.capacity {
		c.evictOldestLocked()
	}
	c.entries[nonce] = now
	return false
}

func (c *clientHelloReplayCache) evictExpiredLocked(now time.Time) {
	for nonce, ts := range c.entries {
		if now.Sub(ts) > c.ttl {
			delete(c.entries, nonce)
		}
	}
}

func (c *clientHelloReplayCache) evictOldestLocked() {
	var (
		oldestNonce [16]byte
		oldestTime  time.Time
		found       bool
	)
	for nonce, ts := range c.entries {
		if !found || ts.Before(oldestTime) {
			oldestNonce = nonce
			oldestTime = ts
			found = true
		}
	}
	if found {
		delete(c.entries, oldestNonce)
	}
}
