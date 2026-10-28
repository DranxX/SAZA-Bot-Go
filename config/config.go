package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Config holds all runtime configuration loaded from config.json.
type Config struct {
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Bot         string `json:"bot"`
	Prefix      string `json:"prefix"`
	Status      string `json:"status"`      // public | ponly | gonly | self
	Autoread    string `json:"autoread"`    // enable | disable
	LoginMethod string `json:"loginMethod"` // qr | pairs
	Markdown    bool   `json:"markdown"`    // WA native markdown hint for plugins

	Antispam    AntiSpamConfig    `json:"antispam"`
	CommandGate CommandGateConfig `json:"commandGate"`
}

// AntiSpamConfig tunes the per-contact sliding-window spam limiter.
type AntiSpamConfig struct {
	Enabled  string `json:"enabled"`  // enable | disable
	WindowMs int64  `json:"windowMs"` // sliding window size in ms
	MaxWarn  int    `json:"maxWarn"`  // command count in window that triggers a warning
	MaxBan   int    `json:"maxBan"`   // command count in window that triggers a tempban
	BanMs    int64  `json:"banMs"`    // temporary ban duration in ms
}

// CommandGateConfig tunes the per-contact command execution gate.
type CommandGateConfig struct {
	Enabled           string `json:"enabled"`           // enable | disable
	MaxConcurrentFree int    `json:"maxConcurrentFree"` // parallel commands per free contact
	MaxQueue          int    `json:"maxQueue"`          // queued overflow commands
	FreeMode          string `json:"freeMode"`          // queue | reject
	QueueTimeoutMs    int64  `json:"queueTimeoutMs"`    // queue wait limit in ms
}

var (
	mu         sync.RWMutex
	current    Config
	configPath string
)

var statusAliases = map[string]string{
	"public": "public", "ponly": "ponly", "pconly": "ponly",
	"gonly": "gonly", "gconly": "gonly",
	"self": "self", "selfonly": "self",
}

// Load reads the config file and populates the global singleton.
// If the file is missing, it creates one with defaults and uses those.
func Load(path string) error {
	configPath = path
	data, err := os.ReadFile(path)
	if err != nil {
		current = defaultConfig()
		fmt.Printf("[CONFIG] Failed to read %s, using defaults: %v\n", path, err)
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	current = defaultConfig()
	if err = json.Unmarshal(data, &current); err != nil {
		return fmt.Errorf("config parse error: %w", err)
	}
	normalize(&current)
	return nil
}

// Get returns a copy of the current config (safe for concurrent reads).
func Get() Config {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Update atomically modifies and persists the config.
func Update(fn func(*Config)) {
	mu.Lock()
	defer mu.Unlock()
	fn(&current)
	normalize(&current)
	save()
}

func save() {
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		fmt.Println("[CONFIG] Marshal error:", err)
		return
	}
	data = append(data, '\n')
	if err = os.WriteFile(configPath, data, 0644); err != nil {
		fmt.Println("[CONFIG] Write error:", err)
	}
}

func normalize(c *Config) {
	if mapped, ok := statusAliases[c.Status]; ok {
		c.Status = mapped
	} else {
		c.Status = "public"
	}
	if c.Prefix == "" {
		c.Prefix = "."
	}
	if c.Name == "" {
		c.Name = "Template Go"
	}
	if c.LoginMethod != "qr" && c.LoginMethod != "pairs" {
		c.LoginMethod = "qr"
	}
	if c.Autoread != "enable" && c.Autoread != "disable" {
		c.Autoread = "enable"
	}
	if c.Antispam.Enabled != "disable" {
		c.Antispam.Enabled = "enable"
	}
	if c.Antispam.WindowMs <= 0 {
		c.Antispam.WindowMs = 1_000
	}
	if c.Antispam.MaxWarn <= 0 {
		c.Antispam.MaxWarn = 4
	}
	if c.Antispam.MaxBan <= c.Antispam.MaxWarn {
		c.Antispam.MaxBan = c.Antispam.MaxWarn + 1
	}
	if c.Antispam.BanMs <= 0 {
		c.Antispam.BanMs = 60_000
	}
	if c.CommandGate.Enabled != "disable" {
		c.CommandGate.Enabled = "enable"
	}
	if c.CommandGate.MaxConcurrentFree <= 0 {
		c.CommandGate.MaxConcurrentFree = 1
	}
	if c.CommandGate.FreeMode == "reject" {
		c.CommandGate.MaxQueue = 0
	} else {
		c.CommandGate.FreeMode = "queue"
		if c.CommandGate.MaxQueue <= 0 {
			c.CommandGate.MaxQueue = 3
		}
	}
	if c.CommandGate.QueueTimeoutMs <= 0 {
		c.CommandGate.QueueTimeoutMs = 30_000
	}
}

func defaultConfig() Config {
	return Config{
		Name:        "Template Go",
		Prefix:      ".",
		Status:      "public",
		Autoread:    "enable",
		LoginMethod: "qr",
		Markdown:    true,
	}
}
