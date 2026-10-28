package lib

import (
	"context"
	"errors"
	"sync"
)

// ErrQueueFull is returned when the per-contact queue is full.
var ErrQueueFull = errors.New("command queue full")

// CommandGate serializes command execution per contact, tier-aware.
// Free tier: at most MaxConcurrent commands at once; overflow waits in a bounded
// queue (the ctx governs the wait) or is rejected when maxQueue == 0.
// Premium tier bypasses the gate entirely — commands run in parallel, no queue.
// Anti-spam stays independent of this gate.
type CommandGate struct {
	mu            sync.Mutex
	maxConcurrent int
	maxQueue      int
	lanes         map[string]*gateLane
}

type gateLane struct {
	running int
	queue   []chan struct{} // grant = send on channel; waiter consumes exactly once
}

// NewCommandGate creates a gate. Call Configure to apply config values.
func NewCommandGate(maxConcurrent, maxQueue int) *CommandGate {
	g := &CommandGate{lanes: make(map[string]*gateLane)}
	g.Configure(maxConcurrent, maxQueue)
	return g
}

// Configure updates the limits (safe to call per message; cheap).
// maxQueue == 0 means overflow is rejected immediately with ErrQueueFull.
func (g *CommandGate) Configure(maxConcurrent, maxQueue int) {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if maxQueue < 0 {
		maxQueue = 0
	}
	g.mu.Lock()
	g.maxConcurrent = maxConcurrent
	g.maxQueue = maxQueue
	g.mu.Unlock()
}

// Run executes fn for contact. free=false runs fn immediately (parallel).
// The provided ctx only governs the queue wait; fn itself is called without it.
func (g *CommandGate) Run(ctx context.Context, contact string, free bool, fn func() error) error {
	if !free {
		return fn()
	}

	g.mu.Lock()
	l := g.lanes[contact]
	if l == nil {
		l = &gateLane{}
		g.lanes[contact] = l
	}
	if l.running >= g.maxConcurrent {
		if len(l.queue) >= g.maxQueue {
			g.mu.Unlock()
			return ErrQueueFull
		}
		ch := make(chan struct{}, 1)
		l.queue = append(l.queue, ch)
		g.mu.Unlock()

		select {
		case <-ch: // slot granted; the running count already belongs to us
		case <-ctx.Done():
			g.mu.Lock()
			granted := true
			for i, c := range l.queue {
				if c == ch {
					l.queue = append(l.queue[:i], l.queue[i+1:]...)
					granted = false
					break
				}
			}
			g.mu.Unlock()
			if !granted {
				return ctx.Err()
			}
			<-ch // granted between wakeups — honor the slot
			defer g.release(contact, l)
			return fn()
		}
	} else {
		l.running++
		g.mu.Unlock()
	}

	defer g.release(contact, l)
	return fn()
}

// Note: a granted queue slot keeps the lane's running count; release() hands it over.
func (g *CommandGate) release(contact string, l *gateLane) {
	g.mu.Lock()
	if len(l.queue) > 0 {
		ch := l.queue[0]
		l.queue = l.queue[1:]
		g.mu.Unlock()
		ch <- struct{}{}
		return
	}
	l.running--
	if l.running <= 0 && len(l.queue) == 0 {
		delete(g.lanes, contact)
	}
	g.mu.Unlock()
}
