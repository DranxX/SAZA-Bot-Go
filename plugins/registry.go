package plugins

import (
	"context"
	"sync"
)

// Plugin defines a command handler. Before is called on every message (like the JS `before` hook).
type Plugin struct {
	Command     []string
	Description string
	Category    string
	Handler     func(ctx context.Context, c *Ctx) error
	Before      func(ctx context.Context, c *Ctx) error // optional, runs on every message
	Parallel    bool                                    // optional: bypass the per-contact command gate (always parallel)
}

var global = &Registry{
	commands: make(map[string]*Plugin),
}

// Registry is a thread-safe command → plugin map.
type Registry struct {
	mu       sync.RWMutex
	commands map[string]*Plugin
	list     []*Plugin
}

// Register adds a plugin. Call from init() only — not goroutine-safe by design.
func Register(p *Plugin) {
	global.mu.Lock()
	defer global.mu.Unlock()
	for _, cmd := range p.Command {
		if old, exists := global.commands[cmd]; exists {
			for i, item := range global.list {
				if item == old {
					global.list = append(global.list[:i], global.list[i+1:]...)
					break
				}
			}
			for oldCmd, oldP := range global.commands {
				if oldP == old {
					delete(global.commands, oldCmd)
				}
			}
		}
	}
	for _, cmd := range p.Command {
		global.commands[cmd] = p
	}
	global.list = append(global.list, p)
}

// Get looks up a plugin by command name.
func Get(cmd string) *Plugin {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.commands[cmd]
}

// All returns a copy of all registered plugins (in registration order).
func All() []*Plugin {
	global.mu.RLock()
	defer global.mu.RUnlock()
	out := make([]*Plugin, len(global.list))
	copy(out, global.list)
	return out
}
