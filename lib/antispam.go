package lib

import (
	"sync"
	"time"
)

// AntiSpamConfig controls the per-contact sliding window.
type AntiSpamConfig struct {
	WindowMs int64 // window size in ms
	MaxWarn  int   // command count in window that triggers a warning
	MaxBan   int   // command count in window that triggers a tempban
}

// AntiSpamDecision is the verdict for one command attempt.
type AntiSpamDecision struct {
	Allowed bool
	Warn    bool // send a warning (true at most once per window)
	Ban     bool // apply a temporary ban
	Count   int  // command count inside the window
}

// AntiSpam is a per-contact sliding-window rate limiter with warn/ban levels.
// Pure decision engine: applying bans and sending replies is the caller's job.
type AntiSpam struct {
	mu       sync.Mutex
	hits     map[string][]int64
	warnedAt map[string]int64
}

// NewAntiSpam creates an empty tracker.
func NewAntiSpam() *AntiSpam {
	return &AntiSpam{
		hits:     make(map[string][]int64),
		warnedAt: make(map[string]int64),
	}
}

// Check records a command attempt at message timestamp tsMs (server clock) and
// returns the verdict. Window math runs on message timestamps, so redelivered
// or stale messages can never inflate the current window.
// Exempted senders (owner/premium) must not be passed here at all.
func (a *AntiSpam) Check(contact string, tsMs int64, cfg AntiSpamConfig) AntiSpamDecision {
	a.mu.Lock()
	defer a.mu.Unlock()

	arr := a.hits[contact][:0]
	for _, t := range a.hits[contact] {
		if tsMs-t <= cfg.WindowMs {
			arr = append(arr, t)
		}
	}
	arr = append(arr, tsMs)
	a.hits[contact] = arr
	n := len(arr)

	switch {
	case n >= cfg.MaxBan:
		return AntiSpamDecision{Allowed: false, Ban: true, Count: n}
	case n >= cfg.MaxWarn:
		warn := tsMs-a.warnedAt[contact] > cfg.WindowMs
		if warn {
			a.warnedAt[contact] = tsMs
		}
		return AntiSpamDecision{Allowed: false, Warn: warn, Count: n}
	default:
		return AntiSpamDecision{Allowed: true, Count: n}
	}
}

// Reset clears tracking for one contact (e.g. after a ban was applied).
func (a *AntiSpam) Reset(contact string) {
	a.mu.Lock()
	delete(a.hits, contact)
	delete(a.warnedAt, contact)
	a.mu.Unlock()
}

// Sweep drops tracking for contacts with no recent activity.
// Note: wall-clock based cleanup only — decisions never depend on it.
func (a *AntiSpam) Sweep(windowMs int64) {
	now := time.Now().UnixMilli()
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, arr := range a.hits {
		if len(arr) == 0 || now-arr[len(arr)-1] > windowMs*2 {
			delete(a.hits, k)
			delete(a.warnedAt, k)
		}
	}
}
