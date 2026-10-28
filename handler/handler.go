package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"template-go/config"
	"template-go/lib"
	"template-go/plugins"
	"template-go/store"
	"time"
	"unicode"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"golang.org/x/text/unicode/norm"
)

// ─── Spam / rate-limit + command gate state ──────────────────────────────────

var (
	groupCacheMu   sync.RWMutex
	groupCache     = make(map[types.JID]*types.GroupInfo)
	groupCacheTime = make(map[types.JID]time.Time)
	antiSpam       = lib.NewAntiSpam()
	cmdGate        = lib.NewCommandGate(1, 3)
	metaFlights    = lib.NewFlightCache()
	msgDedup       = lib.NewMsgDedup(2 * 60 * 1000) // > stale-message window so redeliveries stay covered
)

func init() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			antiSpam.Sweep(config.Get().Antispam.WindowMs)
		}
	}()
}

// ─── Blocklist cache ──────────────────────────────────────────────────────────

var (
	blocklistMu    sync.Mutex
	blocklistCache = map[string]bool{}
	blocklistTTL   int64
)

// FetchBlocklist fetches the blocklist from WhatsApp and updates the cache.
func FetchBlocklist(client *whatsmeow.Client) (map[string]bool, error) {
	bl, err := client.GetBlocklist(context.Background())
	if err != nil {
		return nil, err
	}
	blocklistMu.Lock()
	blocklistCache = make(map[string]bool, len(bl.JIDs))
	for _, jid := range bl.JIDs {
		blocklistCache[jid.User] = true
	}
	blocklistTTL = time.Now().UnixMilli()
	blocklistMu.Unlock()
	return blocklistCache, nil
}

// ClearGroupCache removes a group from the metadata cache.
func ClearGroupCache(chat types.JID) {
	groupCacheMu.Lock()
	delete(groupCache, chat)
	delete(groupCacheTime, chat)
	groupCacheMu.Unlock()
}

func mapGroupParticipantsLids(groupMeta *types.GroupInfo) {
	if groupMeta == nil {
		return
	}
	for _, p := range groupMeta.Participants {
		cleanLid := p.LID.User
		cleanPhone := p.PhoneNumber.User
		if cleanLid != "" && cleanPhone != "" {
			store.MapLidToPhone(cleanLid, cleanPhone)
		}
	}
}

// FetchGroupMetadata fetches group metadata from WhatsApp and caches it.
// Concurrent fetches for the same JID collapse into a single network call.
func FetchGroupMetadata(client *whatsmeow.Client, chat types.JID) (*types.GroupInfo, error) {
	val, err := metaFlights.Do(chat.String(), 0, func() (any, error) {
		return client.GetGroupInfo(context.Background(), chat)
	})
	if err != nil {
		return nil, err
	}
	newInfo := val.(*types.GroupInfo)
	mapGroupParticipantsLids(newInfo)
	groupCacheMu.Lock()
	groupCache[chat] = newInfo
	groupCacheTime[chat] = time.Now()
	groupCacheMu.Unlock()
	return newInfo, nil
}

func isBlocked(client *whatsmeow.Client, phone string) bool {
	blocklistMu.Lock()
	now := time.Now().UnixMilli()
	if now-blocklistTTL > 60_000 {
		blocklistTTL = now
		blocklistMu.Unlock()
		go func() { _, _ = FetchBlocklist(client) }()
	} else {
		blocklistMu.Unlock()
	}
	blocklistMu.Lock()
	blocked := blocklistCache[phone]
	blocklistMu.Unlock()
	return blocked
}

func normalizeCmd(s string) string {
	t := norm.NFD.String(s)
	var b strings.Builder
	for _, r := range t {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Handle processes a single incoming WhatsApp message event.
func Handle(client *whatsmeow.Client, evt *events.Message) {
	receivedAt := time.Now().UnixMilli()
	cfg := config.Get()

	if evt.Info.Chat.Server == "broadcast" {
		return
	}

	// Redelivered messages (reconnect retries) must count once, not per delivery.
	// Messages without an ID are never deduped — the key would collide across sends.
	if evt.Info.ID != "" && msgDedup.Seen(evt.Info.Chat.String()+"\x00"+evt.Info.ID) {
		fmt.Printf("[DEDUP] drop repeat %s | %s\n", evt.Info.ID, evt.Info.PushName)
		return
	}

	rawMsg := evt.Message
	if rawMsg.GetEphemeralMessage() != nil {
		rawMsg = rawMsg.GetEphemeralMessage().Message
	}
	if rawMsg.GetViewOnceMessageV2() != nil {
		rawMsg = rawMsg.GetViewOnceMessageV2().Message
	}
	if rawMsg.GetDocumentWithCaptionMessage() != nil {
		rawMsg = rawMsg.GetDocumentWithCaptionMessage().Message
	}

	chat := evt.Info.Chat
	isGroup := chat.Server == types.GroupServer
	senderJID := evt.Info.Sender
	senderPhone := senderJID.User
	senderLid := ""
	if senderJID.Server == "lid" {
		senderLid = senderJID.User
		if mappedPhone := store.GetPhoneForLid(senderJID.User); mappedPhone != "" {
			senderPhone = mappedPhone
		}
	}
	pushName := evt.Info.PushName
	text := plugins.GetMessageText(rawMsg)
	msgType := plugins.GetMessageType(rawMsg)
	quoted := plugins.ExtractQuoted(rawMsg, client.Store.ID.User)

	botPhone := ""
	if client.Store.ID != nil {
		botPhone = client.Store.ID.User
	}
	ownerPhone := cfg.Owner
	configBot := cfg.Bot

	isOwner := evt.Info.IsFromMe ||
		senderPhone == ownerPhone ||
		senderPhone == botPhone ||
		(configBot != "" && senderPhone == configBot) ||
		store.IsOwnerPhone(senderPhone, ownerPhone)

	var groupMeta *types.GroupInfo
	var isGroupAdmin, isBotGroupAdmin bool

	if isGroup {
		if isBlocked(client, senderPhone) {
			return
		}
		groupCacheMu.RLock()
		info, exists := groupCache[chat]
		cacheTime, timeExists := groupCacheTime[chat]
		groupCacheMu.RUnlock()

		if exists && timeExists && time.Since(cacheTime) < 5*time.Minute {
			groupMeta = info
		} else if !exists {
			go func() { _, _ = FetchGroupMetadata(client, chat) }()
		} else {
			groupMeta = info
			go func() { _, _ = FetchGroupMetadata(client, chat) }()
		}

		if groupMeta != nil {
			for _, p := range groupMeta.Participants {
				if p.IsAdmin || p.IsSuperAdmin {
					if p.JID.User == senderPhone {
						isGroupAdmin = true
					}
					if client.Store.ID != nil && p.JID.User == client.Store.ID.User {
						isBotGroupAdmin = true
					}
				}
			}
		}
	}

	c := &plugins.Ctx{
		Client:          client,
		Event:           evt,
		Chat:            chat,
		SenderPhone:     senderPhone,
		SenderJID:       senderJID,
		IsGroup:         isGroup,
		IsFromMe:        evt.Info.IsFromMe,
		PushName:        pushName,
		Text:            text,
		MsgType:         msgType,
		Quoted:          quoted,
		Timestamp:       evt.Info.Timestamp,
		ReceivedAt:      receivedAt,
		Prefix:          cfg.Prefix,
		IsOwner:         isOwner,
		OwnerPhone:      ownerPhone,
		BotPhone:        botPhone,
		GroupMeta:       groupMeta,
		IsGroupAdmin:    isGroupAdmin,
		IsBotGroupAdmin: isBotGroupAdmin,
	}

	if ci := plugins.GetContextInfo(rawMsg); ci != nil {
		c.MentionedJIDs = ci.MentionedJID
	}

	// Log: [tag] preview | sender
	if msgType == "reactionMessage" || msgType == "stickerMessage" || msgType == "protocolMessage" {
		return
	}
	tag := "msg"
	tags := map[string]string{
		"conversation": "msg", "extendedTextMessage": "msg",
		"imageMessage": "img", "videoMessage": "vid",
		"stickerMessage": "sticker", "reactionMessage": "react",
		"audioMessage": "audio", "documentMessage": "doc",
		"documentWithCaptionMessage": "doc",
	}
	if evt.Info.IsFromMe {
		tag = "self"
	} else if t, ok := tags[msgType]; ok {
		tag = t
	}
	preview := text
	if len(preview) > 80 {
		preview = preview[:80]
	}
	fmt.Printf("[%s] %s | %s\n", tag, preview, pushName)

	switch cfg.Status {
	case "ponly":
		if isGroup {
			return
		}
	case "gonly":
		if !isGroup {
			return
		}
	case "self":
		if senderPhone != botPhone && senderPhone != ownerPhone {
			return
		}
	}

	for _, p := range plugins.All() {
		if p.Before != nil {
			_ = p.Before(context.Background(), c)
		}
	}

	prefix := cfg.Prefix
	isCmd := strings.HasPrefix(text, prefix)

	if !isCmd && !strings.HasPrefix(text, "$ ") {
		return
	}

	var cmdName, q string
	var args []string

	switch {
	case strings.HasPrefix(text, "$ "):
		cmdName = "$"
		q = strings.TrimPrefix(text, "$ ")
		args = strings.Fields(q)

	case isCmd:
		body := strings.TrimSpace(text[len(prefix):])
		if body != "" {
			parts := strings.Fields(body)
			cmdName = normalizeCmd(parts[0])
			args = parts[1:]
			q = strings.Join(args, " ")
		}
	}

	if cmdName == "" {
		return
	}

	plugin := plugins.Get(cmdName)
	if plugin == nil {
		return
	}

	if !isOwner && store.IsBanned(senderPhone, senderLid) {
		fmt.Printf("[BAN] %s tried %s%s while banned\n", senderPhone, prefix, cmdName)
		return
	}

	if !isOwner {
		if blocked := checkSpam(c, cmdName); blocked {
			return
		}
	}

	c.CommandName = cmdName
	c.Args = args
	c.Q = q

	fmt.Printf("[%s] %s%s %s | %s\n", cmdName, prefix, cmdName, strings.Join(args, " "), pushName)

	if cfg.Autoread == "enable" {
		go func() {
			_ = client.MarkRead(context.Background(), []types.MessageID{types.MessageID(evt.Info.ID)}, time.Now(), chat, senderJID)
		}()
	}

	runErr := runCommand(cfg, c, plugin, senderPhone, senderLid, isOwner)
	switch {
	case runErr == nil:
	case errors.Is(runErr, lib.ErrQueueFull):
		_ = c.Reply("⏳ Your command queue is full — wait for the current command to finish first.")
	case errors.Is(runErr, context.DeadlineExceeded):
		_ = c.Replyf("⏳ Timed out waiting in the command queue (%s). Try again.",
			plugins.FormatDuration(cfg.CommandGate.QueueTimeoutMs/1000))
	default:
		fmt.Printf("[PLUGIN ERROR] %s: %v\n", cmdName, runErr)
		_ = c.Replyf("Error: %v", runErr)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// checkSpam applies the per-contact sliding-window anti-spam rule. Returns true when blocked.
func checkSpam(c *plugins.Ctx, cmdName string) bool {
	cfg := config.Get()
	if cfg.Antispam.Enabled == "disable" {
		return false
	}
	dec := antiSpam.Check(c.SenderPhone, c.Event.Info.Timestamp.UnixMilli(), lib.AntiSpamConfig{
		WindowMs: cfg.Antispam.WindowMs,
		MaxWarn:  cfg.Antispam.MaxWarn,
		MaxBan:   cfg.Antispam.MaxBan,
	})
	if dec.Allowed {
		return false
	}
	lid := ""
	if c.SenderJID.Server == "lid" {
		lid = c.SenderJID.User
	}
	if dec.Ban {
		store.AddBan(c.SenderPhone, lid, "spam "+cmdName, cfg.Antispam.BanMs)
		fmt.Printf("[SPAM] BAN %s — %d cmds in %ds (%s%s) → tempban %s\n",
			c.SenderPhone, dec.Count, cfg.Antispam.WindowMs/1000, cfg.Prefix, cmdName,
			plugins.FormatDuration(cfg.Antispam.BanMs/1000))
		_ = c.Replyf("You've been temporarily banned for spamming %s. Try again in %s.",
			cmdName, plugins.FormatDuration(cfg.Antispam.BanMs/1000))
		antiSpam.Reset(c.SenderPhone)
		return true
	}
	if dec.Warn {
		fmt.Printf("[SPAM] WARN %s — %d cmds in %ds (%s%s)\n",
			c.SenderPhone, dec.Count, cfg.Antispam.WindowMs/1000, cfg.Prefix, cmdName)
		_ = c.Replyf("Please don't spam %s. Max %d commands per %s. Continued spam will result in a temporary ban.",
			cmdName, cfg.Antispam.MaxWarn, plugins.FormatDuration(cfg.Antispam.WindowMs/1000))
	}
	return true
}

// runCommand executes the plugin handler under the tier-aware per-contact gate.
// Owner and premium run in parallel without a queue; free contacts are serialized.
func runCommand(cfg config.Config, c *plugins.Ctx, plugin *plugins.Plugin, phone, lid string, isOwner bool) error {
	if cfg.CommandGate.Enabled == "disable" || isOwner || plugin.Parallel {
		return plugin.Handler(context.Background(), c)
	}
	if store.GetPremiumProfile(phone, lid).IsPremium {
		return plugin.Handler(context.Background(), c)
	}
	cmdGate.Configure(cfg.CommandGate.MaxConcurrentFree, cfg.CommandGate.MaxQueue)
	qctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.CommandGate.QueueTimeoutMs)*time.Millisecond)
	defer cancel()
	return cmdGate.Run(qctx, phone, true, func() error {
		return plugin.Handler(context.Background(), c)
	})
}
