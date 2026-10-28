package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"plugin"
	"strings"
	"sync"
	"syscall"
	"template-go/config"
	"template-go/handler"
	"template-go/plugins"
	"template-go/store"
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	sqlite "modernc.org/sqlite"
)

const (
	sessionDB    = "db/session/template-session.db"
	sessionWAL   = "db/session/template-session.db-wal"
	sessionSHM   = "db/session/template-session.db-shm"
	maxQRRetries = 5
)

var (
	client            *whatsmeow.Client
	reconnectMu       sync.Mutex
	reconnectAttempt  int
	isConnecting      bool
	startTime         = time.Now()
	sqlContainer      *sqlstore.Container
	reconnectTimer    *time.Timer
	lastReconnectTime time.Time
)

func init() {
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		queries := []string{
			"PRAGMA foreign_keys = ON;",
			"PRAGMA journal_mode = WAL;",
			"PRAGMA busy_timeout = 5000;",
			"PRAGMA synchronous = NORMAL;",
		}
		for _, q := range queries {
			if _, err := conn.ExecContext(context.Background(), q, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func main() {
	if err := config.Load("config.json"); err != nil {
		fmt.Println("[CONFIG]", err)
		os.Exit(1)
	}
	cfg := config.Get()

	isGoRun := func() bool {
		execPath, err := os.Executable()
		if err != nil {
			return false
		}
		return strings.Contains(execPath, "go-build") || strings.HasPrefix(execPath, os.TempDir())
	}

	if isGoRun() {
		go func() {
			getModuleName := func() (string, error) {
				data, err := os.ReadFile("go.mod")
				if err != nil {
					return "", err
				}
				lines := strings.Split(string(data), "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "module ") {
						return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
					}
				}
				return "", fmt.Errorf("module name not found in go.mod")
			}

			modName, err := getModuleName()
			if err != nil {
				return
			}

			modTimes := make(map[string]time.Time)
			files, err := os.ReadDir("plugins")
			if err != nil {
				return
			}

			for _, entry := range files {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
					continue
				}
				if entry.Name() == "ctx.go" || entry.Name() == "registry.go" || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				path := filepath.Join("plugins", entry.Name())
				if info, err := entry.Info(); err == nil {
					modTimes[path] = info.ModTime()
				}
			}

			reloadingPlugins := make(map[string]bool)
			var reloadMu sync.Mutex

			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()

			var counter int

			for range ticker.C {
				files, err := os.ReadDir("plugins")
				if err != nil {
					continue
				}

				for _, entry := range files {
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
						continue
					}
					if entry.Name() == "ctx.go" || entry.Name() == "registry.go" || strings.HasPrefix(entry.Name(), ".") {
						continue
					}
					path := filepath.Join("plugins", entry.Name())
					info, err := entry.Info()
					if err != nil {
						continue
					}

					lastMod, exists := modTimes[path]
					if !exists || info.ModTime().After(lastMod) {
						modTimes[path] = info.ModTime()

						reloadMu.Lock()
						if reloadingPlugins[path] {
							reloadMu.Unlock()
							continue
						}
						reloadingPlugins[path] = true
						reloadMu.Unlock()

						counter++
						go func(cPath, cName string, cnt int) {
							defer func() {
								reloadMu.Lock()
								delete(reloadingPlugins, cPath)
								reloadMu.Unlock()
							}()

							fmt.Printf("[SYSTEM] Reloading plugin: %s...\n", cName)
							data, err := os.ReadFile(cPath)
							if err != nil {
								return
							}
							content := string(data)
							content = strings.Replace(content, "package plugins", fmt.Sprintf("package main\nimport . \"%s/plugins\"", modName), 1)
							content += "\nfunc main() {}\n"
							tempDir := os.TempDir()
							tempSrc := filepath.Join(tempDir, fmt.Sprintf("reload_%d.go", cnt))
							err = os.WriteFile(tempSrc, []byte(content), 0644)
							if err != nil {
								return
							}
							defer os.Remove(tempSrc)
							tempSo := filepath.Join(tempDir, fmt.Sprintf("reload_%d.so", cnt))
							cmd := exec.Command("go", "build", "-buildmode=plugin", "-o", tempSo, tempSrc)
							var errOut strings.Builder
							cmd.Stderr = &errOut
							if err := cmd.Run(); err != nil {
								fmt.Printf("[SYSTEM] Gagal mengkompilasi %s: %v\nDetail:\n%s\n", cName, err, errOut.String())
								return
							}
							defer os.Remove(tempSo)
							_, err = plugin.Open(tempSo)
							if err != nil {
								fmt.Printf("[SYSTEM] Gagal memuat %s: %v\n", cName, err)
								return
							}
							fmt.Printf("[SYSTEM] ✓ Reloaded plugin: %s\n", cName)
						}(path, entry.Name(), counter)
					}
				}
			}
		}()
	}

	_ = os.MkdirAll("db/session", 0755)
	_ = os.MkdirAll("db/banned", 0755)
	_ = os.MkdirAll("db/premium", 0755)
	store.InitBanned(filepath.Join("db", "banned", "banned.db"))
	store.InitPremium(filepath.Join("db", "premium", "premium.db"))

	fmt.Println()
	fmt.Println("╭──────────────────────────────╮")
	fmt.Printf("│  %-28s│\n", cfg.Name)
	fmt.Printf("│  %-28s│\n", "DranxX Creative")
	fmt.Println("├──────────────────────────────┤")
	fmt.Printf("│  • Status   : %-15s│\n", cfg.Status)
	fmt.Printf("│  • Prefix   : %-15s│\n", cfg.Prefix)
	fmt.Printf("│  • Owner    : %-15s│\n", cfg.Owner)
	fmt.Printf("│  • Login    : %-15s│\n",
		map[bool]string{true: "Pairing Code", false: "QR Code"}[cfg.LoginMethod == "pairs"])
	fmt.Println("╰──────────────────────────────╯")
	fmt.Println()

	connectToWhatsApp()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	fmt.Println("\n[SYSTEM] Shutting down...")
	if client != nil {
		client.Disconnect()
	}
	if sqlContainer != nil {
		sqlContainer.Close()
	}
}

func connectToWhatsApp() {
	reconnectMu.Lock()
	if isConnecting {
		reconnectMu.Unlock()
		return
	}
	isConnecting = true
	reconnectMu.Unlock()
	defer func() {
		reconnectMu.Lock()
		isConnecting = false
		reconnectMu.Unlock()
	}()

	cfg := config.Get()

	if sqlContainer != nil {
		sqlContainer.Close()
		sqlContainer = nil
	}

	container, err := sqlstore.New(context.Background(), "sqlite", "file:"+sessionDB+"?_foreign_keys=on", waLog.Noop)
	if err != nil {
		fmt.Println("[SESSION] Failed to open session DB:", err)
		scheduleReconnect(5*time.Second, false)
		return
	}
	sqlContainer = container
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		fmt.Println("[SESSION] Failed to get device:", err)
		scheduleReconnect(5*time.Second, false)
		return
	}

	client = whatsmeow.NewClient(deviceStore, waLog.Noop)
	client.AddEventHandler(eventHandler)

	if client.Store.ID == nil {
		if cfg.LoginMethod == "pairs" {
			if err = client.Connect(); err != nil {
				fmt.Println("[SESSION] Connect error:", err)
				scheduleReconnect(reconnectDelay(), false)
				return
			}
			phone := cleanPhone(firstNonEmpty(cfg.Bot, cfg.Owner))
			if phone == "" {
				phone = cleanPhone(askInput("[PAIRING] Enter phone number (e.g. 6281234567890): "))
			}
			fmt.Printf("[PAIRING] Requesting code for +%s...\n", phone)
			code, pErr := client.PairPhone(context.Background(), phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
			if pErr != nil {
				fmt.Println("[PAIRING] Failed:", pErr)
				return
			}
			fmt.Println()
			fmt.Println("╭─────────────────────────────────────╮")
			fmt.Printf("│  Pairing Code: %-21s│\n", code)
			fmt.Println("╰─────────────────────────────────────╯")
			fmt.Println("[PAIRING] Enter this in WhatsApp → Linked Devices → Link a Device")
		} else {
			qrChan, _ := client.GetQRChannel(context.Background())
			if err = client.Connect(); err != nil {
				fmt.Println("[SESSION] Connect error:", err)
				scheduleReconnect(reconnectDelay(), false)
				return
			}
			fmt.Println("[SESSION] Scan the QR code below:")
			qrCount := 0
			for evt := range qrChan {
				if evt.Event == "code" {
					qrCount++
					if qrCount > maxQRRetries {
						fmt.Printf("[SESSION] QR expired after %d retries. Restarting...\n", maxQRRetries)
						client.Disconnect()
						clearSession()
						scheduleReconnect(3*time.Second, true)
						return
					}
					fmt.Printf("\n[QR] %d/%d\n", qrCount, maxQRRetries)
					qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				} else {
					fmt.Println("[QR] Result:", evt.Event)
				}
			}
		}
	} else {
		reconnectMu.Lock()
		attempt := reconnectAttempt
		reconnectMu.Unlock()
		fmt.Printf("[SESSION] Connecting... (attempt %d)\n", attempt+1)
		if err = client.Connect(); err != nil {
			fmt.Println("[SESSION] Connect error:", err)
			reconnectMu.Lock()
			reconnectAttempt++
			reconnectMu.Unlock()
			scheduleReconnect(reconnectDelay(), false)
		}
	}
}

func eventHandler(rawEvt interface{}) {
	switch evt := rawEvt.(type) {

	case *events.Connected:
		reconnectMu.Lock()
		reconnectAttempt = 0
		reconnectMu.Unlock()
		user := ""
		if client.Store.ID != nil {
			user = client.Store.ID.User
		}
		fmt.Printf("[SESSION] ✓ Connected as +%s\n", user)
		cfg := config.Get()
		if cfg.Owner != "" {
			go resolveOwnerLid(cfg.Owner)
		}
		go populateInitialCaches(client)

	case *events.Disconnected:
		fmt.Println("[SESSION] Disconnected")
		reconnectMu.Lock()
		reconnectAttempt++
		reconnectMu.Unlock()
		scheduleReconnect(reconnectDelay(), false)

	case *events.StreamReplaced:
		fmt.Println("[SESSION] Stream replaced — disconnecting old, reconnecting in 10s...")
		if client != nil {
			client.Disconnect()
		}
		scheduleReconnect(10*time.Second, false)

	case *events.LoggedOut:
		fmt.Println("[SESSION] Logged out!")
		clearSession()
		scheduleReconnect(3*time.Second, true)

	case *events.Message:
		if time.Since(evt.Info.Timestamp) > 60*time.Second || evt.Info.Timestamp.Before(startTime) {
			return
		}
		if plugins.GetMessageType(evt.Message) == "protocolMessage" {
			return
		}
		go handler.Handle(client, evt)

	case *events.GroupInfo:
		handler.ClearGroupCache(evt.JID)
		fmt.Printf("[CACHE] Group info updated, invalidated cache for %s\n", evt.JID)

	case *events.CallOffer:
		if evt.GroupJID.IsEmpty() && client != nil {
			_ = client.RejectCall(context.Background(), evt.From, evt.CallID)
			go func() {
				_, _ = client.SendMessage(
					context.Background(),
					evt.From,
					&waProto.Message{
						Conversation: proto.String("Cannot receive calls. Please send a message instead."),
					},
				)
			}()
		}
	}
}

func scheduleReconnect(delay time.Duration, resetAttempt bool) {
	reconnectMu.Lock()
	if resetAttempt {
		reconnectAttempt = 0
	}
	if reconnectTimer != nil {
		reconnectTimer.Stop()
	}
	if !lastReconnectTime.IsZero() && time.Since(lastReconnectTime) < 2*time.Second {
		delay = 5 * time.Second
	}
	lastReconnectTime = time.Now().Add(delay)
	reconnectTimer = time.AfterFunc(delay, connectToWhatsApp)
	reconnectMu.Unlock()
}

func reconnectDelay() time.Duration {
	reconnectMu.Lock()
	a := reconnectAttempt
	reconnectMu.Unlock()
	d := 3 * time.Second
	for i := 0; i < a; i++ {
		d = time.Duration(float64(d) * 1.5)
		if d > 60*time.Second {
			d = 60 * time.Second
			break
		}
	}
	return d
}

func clearSession() {
	_ = os.MkdirAll("db/session", 0755)
	for _, f := range []string{sessionDB, sessionWAL, sessionSHM} {
		_ = os.Remove(f)
	}
}

func cleanPhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func askInput(prompt string) string {
	fmt.Print(prompt)
	s := bufio.NewScanner(os.Stdin)
	if s.Scan() {
		return strings.TrimSpace(s.Text())
	}
	return ""
}

func resolveOwnerLid(ownerPhone string) {
	if client == nil {
		return
	}
	results, err := client.IsOnWhatsApp(context.Background(), []string{ownerPhone + "@s.whatsapp.net"})
	if err != nil || len(results) == 0 {
		return
	}
	if results[0].IsIn {
		store.SetOwnerLid(results[0].JID.String())
		fmt.Printf("[OWNER] LID resolved: %s\n", results[0].JID)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func populateInitialCaches(cli *whatsmeow.Client) {
	_, _ = handler.FetchBlocklist(cli)
	groups, err := cli.GetJoinedGroups(context.Background())
	if err == nil {
		for _, g := range groups {
			go func(jid types.JID) {
				_, _ = handler.FetchGroupMetadata(cli, jid)
			}(g.JID)
		}
	}
}
