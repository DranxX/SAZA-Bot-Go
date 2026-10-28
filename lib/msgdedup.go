package lib

import (
	"sync"
	"time"
)

// MsgDedup drops re-delivered messages by key within the TTL, so reconnect
// retries are processed once instead of counting as fresh hits.
// Note: in-memory only — a restart forgets recent IDs; the stale-message filter covers that gap.
type MsgDedup struct {
	mu   sync.Mutex
	seen map[string]int64
	ttl  int64 // ms
}

// NewMsgDedup creates a tracker that forgets keys after ttlMs.
// Non-positive ttl falls back to 120s so the guard can never silently no-op.
func NewMsgDedup(ttlMs int64) *MsgDedup {
	if ttlMs <= 0 {
		ttlMs = 120_000
	}
	return &MsgDedup{seen: make(map[string]int64), ttl: ttlMs}
}

// Seen records key and reports whether it was already seen inside the TTL.
// Warning: past 8192 live keys the map is dropped wholesale (fail-open) to bound memory.
func (d *MsgDedup) Seen(key string) bool {
	if key == "" {
		return false
	}
	now := time.Now().UnixMilli()
	d.mu.Lock()
	defer d.mu.Unlock()
	if ts, dup := d.seen[key]; dup && now-ts <= d.ttl {
		return true
	}
	d.seen[key] = now
	if len(d.seen) > 8192 {
		d.seen = make(map[string]int64)
		return false
	}
	if len(d.seen) > 4096 {
		for k, ts := range d.seen {
			if now-ts > d.ttl {
				delete(d.seen, k)
			}
		}
	}
	return false
}
