package plugins

import (
	"context"
	"fmt"
	"strings"
	"template-go/store"
	"time"

	"go.mau.fi/whatsmeow"
	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// Ctx is the unified message context passed to every plugin handler and Before hook.
type Ctx struct {
	Client *whatsmeow.Client
	Event  *events.Message

	// Identifiers
	Chat        types.JID
	SenderPhone string // cleaned phone number e.g. "6281234567890"
	SenderJID   types.JID
	IsGroup     bool
	IsFromMe    bool
	PushName    string

	// Content
	Text    string
	MsgType string

	// Quoted message info (nil if no quote)
	Quoted *QuotedInfo

	// Mentioned JIDs in the message
	MentionedJIDs []string

	// Timing
	Timestamp  time.Time
	ReceivedAt int64 // UnixMilli when the event was received by the handler

	// Command dispatch (set by handler after parsing)
	CommandName string
	Args        []string
	Q           string // args joined
	Prefix      string

	// Owner / bot identity
	IsOwner    bool
	OwnerPhone string
	BotPhone   string

	// Group metadata (nil for DMs)
	GroupMeta       *types.GroupInfo
	IsGroupAdmin    bool
	IsBotGroupAdmin bool
}

// QuotedInfo holds extracted info about a quoted (replied-to) message.
type QuotedInfo struct {
	ID          string
	Sender      string // phone number of the quoted message's author
	Participant string // raw participant JID from context info (for groups)
	FromMe      bool   // whether the quoted message was sent by this device
	Text        string
	MsgType     string
	Message     *waProto.Message // raw proto for re-downloading media
}

// ─── Send helpers ────────────────────────────────────────────────────────────

// Reply sends a text message quoted to the current message.
func (c *Ctx) Reply(text string) error {
	msg := buildQuotedText(text, c.Event)
	_, err := c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// ReplyID sends a quoted text message and returns the message ID.
func (c *Ctx) ReplyID(text string) (string, error) {
	msg := buildQuotedText(text, c.Event)
	resp, err := c.Client.SendMessage(context.Background(), c.Chat, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// Edit edits a previously sent message by ID.
func (c *Ctx) Edit(msgID, newText string) error {
	msg := &waProto.Message{
		ProtocolMessage: &waProto.ProtocolMessage{
			Key: &waCommon.MessageKey{
				ID:        proto.String(msgID),
				FromMe:    proto.Bool(true),
				RemoteJID: proto.String(c.Chat.String()),
			},
			Type: waProto.ProtocolMessage_MESSAGE_EDIT.Enum(),
			EditedMessage: &waProto.Message{
				Conversation: proto.String(newText),
			},
		},
	}
	_, err := c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// Replyf is like Reply but with fmt.Sprintf formatting.
func (c *Ctx) Replyf(format string, a ...any) error {
	return c.Reply(fmt.Sprintf(format, a...))
}

// ReplyMention sends a text message with @mentions, quoted to the current message.
func (c *Ctx) ReplyMention(text string, phones []string) error {
	mentions := make([]string, len(phones))
	for i, p := range phones {
		if strings.Contains(p, "@") {
			mentions[i] = p
		} else {
			mentions[i] = p + "@s.whatsapp.net"
		}
	}
	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(c.Event.Info.ID),
				Participant:   proto.String(c.SenderJID.String()),
				QuotedMessage: c.Event.Message,
				MentionedJID:  mentions,
			},
		},
	}
	_, err := c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// React sends an emoji reaction to the current message.
func (c *Ctx) React(emoji string) error {
	key := &waCommon.MessageKey{
		ID:        proto.String(c.Event.Info.ID),
		FromMe:    proto.Bool(c.Event.Info.IsFromMe),
		RemoteJID: proto.String(c.Chat.String()),
	}
	if c.IsGroup {
		key.Participant = proto.String(c.SenderJID.String())
	}
	msg := &waProto.Message{
		ReactionMessage: &waProto.ReactionMessage{
			Key:               key,
			Text:              proto.String(emoji),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
		},
	}
	_, err := c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// SendDocument uploads bytes and sends them as a document (file).
func (c *Ctx) SendDocument(data []byte, mime, filename string) error {
	uploaded, err := c.Client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	msg := &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(mime),
			FileName:      proto.String(filename),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(c.Event.Info.ID),
				Participant:   proto.String(c.SenderJID.String()),
				QuotedMessage: c.Event.Message,
			},
		},
	}
	_, err = c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// SendVideo uploads bytes and sends them as a video with an optional caption.
func (c *Ctx) SendVideo(data []byte, mime, caption string) error {
	uploaded, err := c.Client.Upload(context.Background(), data, whatsmeow.MediaVideo)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	msg := &waProto.Message{
		VideoMessage: &waProto.VideoMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(mime),
			Caption:       proto.String(caption),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(c.Event.Info.ID),
				Participant:   proto.String(c.SenderJID.String()),
				QuotedMessage: c.Event.Message,
			},
		},
	}
	_, err = c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// SendAudio uploads bytes and sends them as an audio/voice message.
func (c *Ctx) SendAudio(data []byte, mime string, ptt bool) error {
	uploaded, err := c.Client.Upload(context.Background(), data, whatsmeow.MediaAudio)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	msg := &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(mime),
			PTT:           proto.Bool(ptt),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(c.Event.Info.ID),
				Participant:   proto.String(c.SenderJID.String()),
				QuotedMessage: c.Event.Message,
			},
		},
	}
	_, err = c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// SendImage uploads bytes and sends them as an image with an optional caption.
func (c *Ctx) SendImage(data []byte, mime, caption string) error {
	uploaded, err := c.Client.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	msg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(mime),
			Caption:       proto.String(caption),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(c.Event.Info.ID),
				Participant:   proto.String(c.SenderJID.String()),
				QuotedMessage: c.Event.Message,
			},
		},
	}
	_, err = c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// Delete deletes the current message (bot must be admin in groups for other people's messages).
func (c *Ctx) Delete() error {
	key := &waCommon.MessageKey{
		ID:        proto.String(c.Event.Info.ID),
		FromMe:    proto.Bool(c.Event.Info.IsFromMe),
		RemoteJID: proto.String(c.Chat.String()),
	}
	if c.IsGroup {
		key.Participant = proto.String(c.SenderJID.String())
	}
	msg := &waProto.Message{
		ProtocolMessage: &waProto.ProtocolMessage{
			Key:  key,
			Type: waProto.ProtocolMessage_REVOKE.Enum(),
		},
	}
	_, err := c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// DeleteKey deletes a message by an explicit key.
func (c *Ctx) DeleteKey(msgID, remoteJid, participant string, fromMe bool) error {
	key := &waCommon.MessageKey{
		ID:        proto.String(msgID),
		FromMe:    proto.Bool(fromMe),
		RemoteJID: proto.String(remoteJid),
	}
	if participant != "" {
		key.Participant = proto.String(participant)
	}
	msg := &waProto.Message{
		ProtocolMessage: &waProto.ProtocolMessage{
			Key:  key,
			Type: waProto.ProtocolMessage_REVOKE.Enum(),
		},
	}
	_, err := c.Client.SendMessage(context.Background(), c.Chat, msg)
	return err
}

// MarkRead marks the current message as read.
func (c *Ctx) MarkRead() {
	_ = c.Client.MarkRead(context.Background(), []types.MessageID{types.MessageID(c.Event.Info.ID)}, time.Now(), c.Chat, c.SenderJID)
}

// SendPresenceUpdate sends a presence update (like composing or paused) to the current chat.
func (c *Ctx) SendPresenceUpdate(state string) error {
	switch state {
	case "composing":
		return c.Client.SendChatPresence(context.Background(), c.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	case "paused":
		return c.Client.SendChatPresence(context.Background(), c.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	case "recording":
		return c.Client.SendChatPresence(context.Background(), c.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)
	default:
		return c.Client.SendChatPresence(context.Background(), c.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	}
}

// ─── Media download helpers ──────────────────────────────────────────────────

// DownloadCurrentMedia downloads the media from the current message.
func (c *Ctx) DownloadCurrentMedia() ([]byte, string, error) {
	return downloadFromMsg(c.Client, c.Event.Message)
}

// DownloadQuotedMedia downloads the media from the quoted message.
func (c *Ctx) DownloadQuotedMedia() ([]byte, string, error) {
	if c.Quoted == nil || c.Quoted.Message == nil {
		return nil, "", fmt.Errorf("no quoted message")
	}
	return downloadFromMsg(c.Client, c.Quoted.Message)
}

func downloadFromMsg(client *whatsmeow.Client, msg *waProto.Message) ([]byte, string, error) {
	if msg == nil {
		return nil, "", fmt.Errorf("nil message")
	}
	ctx := context.Background()
	switch {
	case msg.ImageMessage != nil:
		d, err := client.Download(ctx, msg.ImageMessage)
		return d, msg.ImageMessage.GetMimetype(), err
	case msg.VideoMessage != nil:
		d, err := client.Download(ctx, msg.VideoMessage)
		return d, msg.VideoMessage.GetMimetype(), err
	case msg.AudioMessage != nil:
		d, err := client.Download(ctx, msg.AudioMessage)
		return d, msg.AudioMessage.GetMimetype(), err
	case msg.StickerMessage != nil:
		d, err := client.Download(ctx, msg.StickerMessage)
		return d, msg.StickerMessage.GetMimetype(), err
	case msg.DocumentMessage != nil:
		d, err := client.Download(ctx, msg.DocumentMessage)
		return d, msg.DocumentMessage.GetMimetype(), err
	case msg.PtvMessage != nil:
		d, err := client.Download(ctx, msg.PtvMessage)
		return d, msg.PtvMessage.GetMimetype(), err
	}
	return nil, "", fmt.Errorf("no downloadable media")
}

// ─── Proto helpers (used by handler to build Ctx) ────────────────────────────

// GetMessageText extracts the text content from any message type.
func GetMessageText(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	switch {
	case msg.Conversation != nil:
		return *msg.Conversation
	case msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil:
		return *msg.ExtendedTextMessage.Text
	case msg.ImageMessage != nil:
		return msg.ImageMessage.GetCaption()
	case msg.VideoMessage != nil:
		return msg.VideoMessage.GetCaption()
	case msg.DocumentMessage != nil:
		return msg.DocumentMessage.GetCaption()
	case msg.DocumentWithCaptionMessage != nil:
		if inner := msg.DocumentWithCaptionMessage.Message; inner != nil {
			return inner.GetDocumentMessage().GetCaption()
		}
	}
	return ""
}

// GetMessageType returns a string label for the message type.
func GetMessageType(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	switch {
	case msg.Conversation != nil:
		return "conversation"
	case msg.ExtendedTextMessage != nil:
		return "extendedTextMessage"
	case msg.ImageMessage != nil:
		return "imageMessage"
	case msg.VideoMessage != nil:
		return "videoMessage"
	case msg.AudioMessage != nil:
		return "audioMessage"
	case msg.StickerMessage != nil:
		return "stickerMessage"
	case msg.DocumentMessage != nil:
		return "documentMessage"
	case msg.DocumentWithCaptionMessage != nil:
		return "documentWithCaptionMessage"
	case msg.ReactionMessage != nil:
		return "reactionMessage"
	case msg.ProtocolMessage != nil:
		return "protocolMessage"
	case msg.PtvMessage != nil:
		return "ptvMessage"
	case msg.EphemeralMessage != nil:
		return "ephemeralMessage"
	case msg.ViewOnceMessageV2 != nil:
		return "viewOnceMessageV2"
	}
	return "unknown"
}

// GetContextInfo extracts ContextInfo from any message type.
func GetContextInfo(msg *waProto.Message) *waProto.ContextInfo {
	if msg == nil {
		return nil
	}
	switch {
	case msg.ExtendedTextMessage != nil:
		return msg.ExtendedTextMessage.ContextInfo
	case msg.ImageMessage != nil:
		return msg.ImageMessage.ContextInfo
	case msg.VideoMessage != nil:
		return msg.VideoMessage.ContextInfo
	case msg.AudioMessage != nil:
		return msg.AudioMessage.ContextInfo
	case msg.StickerMessage != nil:
		return msg.StickerMessage.ContextInfo
	case msg.DocumentMessage != nil:
		return msg.DocumentMessage.ContextInfo
	case msg.PtvMessage != nil:
		return msg.PtvMessage.ContextInfo
	}
	return nil
}

// ExtractQuoted builds a QuotedInfo from a message's context info.
func ExtractQuoted(msg *waProto.Message, selfPhone string) *QuotedInfo {
	ci := GetContextInfo(msg)
	if ci == nil || ci.QuotedMessage == nil {
		return nil
	}
	quoted := ci.QuotedMessage
	if quoted.EphemeralMessage != nil {
		quoted = quoted.EphemeralMessage.Message
	}
	senderJID := ci.GetParticipant()
	senderPhone := strings.Split(strings.Split(senderJID, ":")[0], "@")[0]
	return &QuotedInfo{
		ID:          ci.GetStanzaID(),
		Sender:      senderPhone,
		Participant: senderJID,
		Text:        GetMessageText(quoted),
		MsgType:     GetMessageType(quoted),
		Message:     quoted,
	}
}

func buildQuotedText(text string, evt *events.Message) *waProto.Message {
	return &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(evt.Info.ID),
				Participant:   proto.String(evt.Info.Sender.String()),
				QuotedMessage: evt.Message,
			},
		},
	}
}

// PhoneFromJID extracts the cleaned phone number from a JID string.
func PhoneFromJID(jid string) string {
	s := strings.Split(jid, ":")[0]
	s = strings.Split(s, "@")[0]
	return s
}

// FormatDuration formats a duration in seconds to a human-readable string.
func FormatDuration(seconds int64) string {
	if seconds <= 0 {
		return "Permanent"
	}
	d := seconds / 86400
	h := (seconds % 86400) / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	var parts []string
	if d > 0 {
		parts = append(parts, fmt.Sprintf("%dd", d))
	}
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if s > 0 {
		parts = append(parts, fmt.Sprintf("%ds", s))
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}

func ResolveTargetPhone(c *Ctx) (string, string, error) {
	resolveLid := func(lidStr string) string {
		if c.Client != nil && c.Client.Store != nil && c.Client.Store.LIDs != nil {
			lidJID, err := types.ParseJID(lidStr + "@lid")
			if err == nil {
				pn, err := c.Client.Store.LIDs.GetPNForLID(context.Background(), lidJID)
				if err == nil && !pn.IsEmpty() {
					store.MapLidToPhone(lidStr, pn.User)
					return pn.User
				}
			}
		}
		return ""
	}

	// A reply always wins: resolve the quoted sender, never the chat itself.
	// Note: Participant keeps the raw JID (server suffix); Sender is digits only.
	if c.Quoted != nil {
		if c.Quoted.Sender != "" {
			raw := PhoneFromJID(c.Quoted.Sender)
			isLid := strings.HasSuffix(c.Quoted.Participant, "@lid")

			if c.GroupMeta != nil {
				for _, p := range c.GroupMeta.Participants {
					if p.LID.User == raw || p.JID.User == raw ||
						strings.HasPrefix(p.JID.String(), raw+"@") || strings.HasPrefix(p.LID.String(), raw+"@") {
						return p.PhoneNumber.User, p.LID.User, nil
					}
				}
			}

			if isLid {
				if phone := store.GetPhoneForLid(raw); phone != "" {
					return phone, raw, nil
				}
				if resolved := resolveLid(raw); resolved != "" {
					return resolved, raw, nil
				}
				return raw, raw, nil
			}
			return raw, "", nil
		}
		return resolveChatPartner(c, resolveLid)
	}

	if len(c.MentionedJIDs) > 0 {
		mentioned := c.MentionedJIDs[0]
		raw := PhoneFromJID(mentioned)
		if strings.HasSuffix(mentioned, "@lid") || (c.GroupMeta != nil) {
			if c.GroupMeta != nil {
				for _, p := range c.GroupMeta.Participants {
					if p.JID.User == raw || p.LID.User == raw ||
						strings.HasPrefix(p.JID.String(), raw+"@") || strings.HasPrefix(p.LID.String(), raw+"@") {
						return p.PhoneNumber.User, p.LID.User, nil
					}
				}
			}
		}
		if !strings.HasSuffix(mentioned, "@lid") {
			return raw, "", nil
		}
		if phone := store.GetPhoneForLid(raw); phone != "" {
			return phone, raw, nil
		}
		if resolved := resolveLid(raw); resolved != "" {
			return resolved, raw, nil
		}
		return raw, raw, nil
	}

	for _, arg := range c.Args {
		stripped := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, arg)
		if len(stripped) >= 7 {
			return stripped, "", nil
		}
	}

	// No reply, mention, or number: in a 1:1 chat the partner is the target.
	if !c.IsGroup {
		return resolveChatPartner(c, resolveLid)
	}

	return "", "", fmt.Errorf("no target")
}

// resolveChatPartner targets the other side of a 1:1 chat, mapping LID to phone when possible.
func resolveChatPartner(c *Ctx, resolveLid func(string) string) (string, string, error) {
	raw := PhoneFromJID(c.Chat.String())
	if strings.HasSuffix(c.Chat.String(), "@lid") {
		if phone := store.GetPhoneForLid(raw); phone != "" {
			return phone, raw, nil
		}
		if resolved := resolveLid(raw); resolved != "" {
			return resolved, raw, nil
		}
		return raw, raw, nil
	}
	return raw, "", nil
}

// IsTargetOwnerOrBot checks if the resolved phone belongs to the owner or bot.
func IsTargetOwnerOrBot(phone string, c *Ctx) bool {
	return phone == c.OwnerPhone || phone == c.BotPhone
}

// ParseDuration parses a duration string like "1h30m" into seconds.
func ParseDuration(s string) int64 {
	if s == "" {
		return 0
	}
	var total int64
	var num int64
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			num = num*10 + int64(r-'0')
		case r == 's':
			total += num
			num = 0
		case r == 'm':
			total += num * 60
			num = 0
		case r == 'h':
			total += num * 3600
			num = 0
		case r == 'd':
			total += num * 86400
			num = 0
		}
	}
	return total
}
