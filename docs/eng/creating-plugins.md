# Creating Go Plugins

A plugin is a `.go` file in `plugins/` that belongs to `package plugins` and calls `Register` from `init()`. Plugins are normally compiled into the bot binary.

```text
plugins/
├── registry.go       # Plugin registry
├── ctx.go            # Ctx fields and helper methods
├── info-menu.go      # Built-in example
└── info-example.go   # Your plugin
```

## Minimal Template

```go
// plugins/info-greet.go
package plugins

import "context"

func init() {
    Register(&Plugin{
        Command:     []string{"greet", "hello"},
        Description: "Reply with a greeting",
        Category:    "info",
        Handler:     greetHandler,
    })
}

func greetHandler(_ context.Context, c *Ctx) error {
    name := c.PushName
    if name == "" {
        name = c.SenderPhone
    }
    return c.Replyf("Hello %s! Type %smenu to see all commands.", name, c.Prefix)
}
```

Restart `make run` or rebuild with `make build`, then send `.greet`.

## Plugin Structure

```go
type Plugin struct {
    Command     []string
    Description string
    Category    string
    Handler     func(ctx context.Context, c *Ctx) error
    Before      func(ctx context.Context, c *Ctx) error
    Parallel    bool
}
```

| Field | Required | Description |
|---|---:|---|
| `Command` | Yes | Names without a prefix. The first entry is the primary command. |
| `Description` | Recommended | Human-readable description. |
| `Category` | Recommended | Menu category such as `info`, `utility`, `owner`, or `hidden`. |
| `Handler` | Yes | Runs after a matching command passes ban/spam checks. |
| `Before` | No | Runs for every accepted non-reaction/non-sticker message before command parsing. |
| `Parallel` | No | Always run in parallel, bypassing the per-contact command gate. |

Aliases point to the same plugin:

```go
Command: []string{"ping", "stats", "status"},
```

If a later registration reuses any command alias, the registry replaces the older plugin and all of that older plugin's aliases.

## Categories and Menu Behavior

| Category | Behavior |
|---|---|
| `info` | Included in the Information menu section. |
| `utility` | Included in the Information section by the current menu implementation. |
| `owner` | Shown only when the menu requester is the owner. Access must still be checked by the handler. |
| `hidden` | Never displayed; intended for `Before` hooks. |
| `ai`, `downloader`, custom | Automatically shown as a separate alphabetically sorted section. |

The category is presentation metadata. It does **not** enforce access control. Every owner plugin must check `c.IsOwner`.

## `Ctx` Reference

Every handler receives a `*Ctx` containing parsed message data and helpers.

### Connection and raw event

| Field | Type | Description |
|---|---|---|
| `Client` | `*whatsmeow.Client` | Raw whatsmeow client. |
| `Event` | `*events.Message` | Original message event and protobuf payload. |

### Identity and chat

| Field | Type | Description |
|---|---|---|
| `Chat` | `types.JID` | Destination chat JID. |
| `SenderPhone` | `string` | Clean phone number when LID mapping is available. |
| `SenderJID` | `types.JID` | Raw sender JID; it may use the `lid` server. |
| `IsGroup` | `bool` | Whether `Chat` is a group. |
| `IsFromMe` | `bool` | Whether the message was sent by this linked device. |
| `PushName` | `string` | WhatsApp display name. |
| `IsOwner` | `bool` | Owner/bot identity result using phone, from-me, config, and cached LID mappings. |
| `OwnerPhone` | `string` | Owner number from config. |
| `BotPhone` | `string` | Connected bot number from whatsmeow. |

### Message content

| Field | Type | Description |
|---|---|---|
| `Text` | `string` | Conversation text or image/video/document caption. |
| `MsgType` | `string` | Parsed type such as `conversation`, `imageMessage`, or `audioMessage`. |
| `Quoted` | `*QuotedInfo` | Parsed replied-to message, or `nil`. |
| `MentionedJIDs` | `[]string` | Mention JIDs from `ContextInfo`. |

`QuotedInfo` contains:

| Field | Description |
|---|---|
| `ID` | Quoted message stanza ID. |
| `Sender` | Extracted sender identifier/phone component. |
| `Participant` | Raw participant JID. |
| `FromMe` | Reserved quoted-message identity flag. |
| `Text` | Extracted quoted text/caption. |
| `MsgType` | Quoted message type. |
| `Message` | Raw quoted protobuf, used for media download. |

### Command data

| Field | Type | Description |
|---|---|---|
| `CommandName` | `string` | Matched normalized command alias. |
| `Args` | `[]string` | Whitespace-split arguments. |
| `Q` | `string` | Arguments joined as one string. For `$`, this is the full shell text. |
| `Prefix` | `string` | Current configured prefix. |

These command fields are assigned after command parsing. Do not expect them to be populated inside a `Before` hook; use `c.Text` there.

### Group and timing data

| Field | Type | Description |
|---|---|---|
| `GroupMeta` | `*types.GroupInfo` | Cached metadata, or `nil` in DMs and during a cold async cache fill. |
| `IsGroupAdmin` | `bool` | Sender admin result from available metadata. |
| `IsBotGroupAdmin` | `bool` | Bot admin result from available metadata. |
| `Timestamp` | `time.Time` | WhatsApp message timestamp. |
| `ReceivedAt` | `int64` | Handler receive time in Unix milliseconds. |

Group metadata is cached for five minutes. An expired entry is returned while an asynchronous refresh runs. A completely cold cache may be `nil` for the first message.

## Text and Message Helpers

### Reply

```go
if err := c.Reply("Hello world"); err != nil {
    return err
}

return c.Replyf("Hello %s, prefix: %s", c.PushName, c.Prefix)
```

Replies quote the incoming message.

### Send and edit a progress message

```go
id, err := c.ReplyID("Processing...")
if err != nil {
    return err
}

// Do work...
return c.Edit(id, "Finished.")
```

`Edit` only applies to messages sent by the bot and expects the returned message ID.

### Reply with mentions

```go
phone := c.SenderPhone
return c.ReplyMention(
    fmt.Sprintf("Hello @%s", phone),
    []string{phone},
)
```

`ReplyMention` accepts plain phone numbers or full JID strings.

### React and delete

```go
_ = c.React("✅")

if c.IsGroup && !c.IsBotGroupAdmin {
    return c.Reply("The bot must be an admin to delete other users' messages.")
}
return c.Delete()
```

Delete an explicitly identified message with:

```go
return c.DeleteKey(messageID, remoteJID, participantJID, fromMe)
```

### Read receipts and presence

```go
c.MarkRead()

_ = c.SendPresenceUpdate("composing")
defer c.SendPresenceUpdate("paused")
```

Supported presence strings are `composing`, `paused`, and `recording`.

## Sending Media

Media helpers accept bytes, upload them through whatsmeow, and quote the current message.

```go
data, err := os.ReadFile("./document.pdf")
if err != nil {
    return err
}
return c.SendDocument(data, "application/pdf", "document.pdf")
```

```go
return c.SendImage(imageBytes, "image/jpeg", "Image caption")
return c.SendVideo(videoBytes, "video/mp4", "Video caption")
return c.SendAudio(audioBytes, "audio/ogg; codecs=opus", true) // true = PTT
```

To send raw whatsmeow messages, use `c.Client.SendMessage`:

```go
_, err := c.Client.SendMessage(ctx, c.Chat, &waProto.Message{
    Conversation: proto.String("Unquoted text"),
})
return err
```

## Downloading Media

Download image, video, audio, sticker, document, or PTV media:

```go
data, mime, err := c.DownloadCurrentMedia()
if err != nil {
    return c.Reply("Send the command as a media caption.")
}
return c.Replyf("Downloaded %d bytes (%s)", len(data), mime)
```

For replied-to media:

```go
data, mime, err := c.DownloadQuotedMedia()
if err != nil {
    return c.Reply("Reply to a media message.")
}
return c.SendDocument(data, mime, "downloaded-file")
```

## Target Resolution

`ResolveTargetPhone` applies the same target rules used by built-in ban and premium plugins:

1. the other participant for a private chat with no explicit target;
2. quoted sender;
3. first mention;
4. a numeric argument containing at least seven digits.

It also checks group participants, the in-memory LID map, and whatsmeow's LID store.

```go
phone, lid, err := ResolveTargetPhone(c)
if err != nil {
    return c.Reply("Reply, mention, or enter a phone number.")
}
if IsTargetOwnerOrBot(phone, c) {
    return c.Reply("That target is protected.")
}
return c.Replyf("Resolved phone=%s lid=%s", phone, lid)
```

Duration helpers are also available:

```go
seconds := ParseDuration("1d12h30m")
human := FormatDuration(seconds)
```

Supported units are `s`, `m`, `h`, and `d`. Invalid characters are ignored by the parser; validate user input if strict syntax matters.

## Premium Integration

Import the store because plugins and stores are separate packages:

```go
import "template-go/store"
```

```go
func premiumAIHandler(_ context.Context, c *Ctx) error {
    if c.Q == "" {
        return c.Replyf("Usage: %sask <question>", c.Prefix)
    }

    if !c.IsOwner {
        profile := store.GetPremiumProfile(c.SenderPhone, c.SenderJID.User)
        if !profile.IsPremium {
            return c.Reply("This command is premium-only.")
        }

        result := store.ConsumePremiumCredit(c.SenderPhone)
        if !result.OK {
            return c.Reply("No premium credits remaining.")
        }
    }

    return c.Reply("Your API result goes here.")
}
```

The store provides:

- `GetPremiumProfile(phone, lid)`;
- `ConsumePremiumCredit(phone)`;
- `SetPremiumCredits(phone, amount)`;
- `AddPremiumCredits(phone, amount)`;
- `RemovePremiumCredits(phone, amount)`;
- owner-management functions used by the built-in plugins.

## Owner-only Plugins

`Category: "owner"` only affects menu visibility. Always enforce authorization:

```go
func ownerSayHandler(_ context.Context, c *Ctx) error {
    if !c.IsOwner {
        return nil
    }
    if c.Q == "" {
        return c.Replyf("Usage: %ssay <text>", c.Prefix)
    }
    return c.Reply(c.Q)
}
```

## `Before` Hooks

A `Before` hook runs after message parsing and status filtering, but before prefix detection, plugin lookup, ban checks, and spam checks.

```go
package plugins

import (
    "context"
    "strings"
)

func init() {
    Register(&Plugin{
        Command:     []string{"automod"},
        Description: "Automatic moderation",
        Category:    "hidden",
        Handler:     func(context.Context, *Ctx) error { return nil },
        Before: func(_ context.Context, c *Ctx) error {
            if c.IsOwner {
                return nil
            }
            if strings.Contains(strings.ToLower(c.Text), "spamword") {
                if !c.IsGroup || c.IsBotGroupAdmin {
                    _ = c.Delete()
                }
            }
            return nil
        },
    })
}
```

Important behavior:

- reaction, sticker, and protocol messages return before hooks run;
- the current handler ignores hook errors (`_ = p.Before(...)`), so handle/report failures inside the hook when needed;
- hooks run sequentially in plugin registration order;
- banned users can still reach hooks because the ban check happens after a command is recognized;
- avoid slow network work in hooks, or launch bounded background work carefully.

## Command Parsing Notes

- Normal commands start with the configured prefix.
- `$ ` is recognized independently of the normal prefix; the `$` handler itself checks owner access.
- Command names are normalized with Unicode NFD, combining marks are removed, and letters are lowercased. This makes command lookup diacritic-insensitive.
- Arguments use `strings.Fields`, so repeated whitespace is collapsed and quoted shell-like arguments are not preserved as separate syntax. `c.Q` preserves the joined remainder for normal commands and the raw trimmed shell text for `$`.

## Errors and Concurrency

Messages are handled in goroutines. Shared plugin state must be protected with a mutex, atomic operation, channel, or another concurrency-safe design.

Return errors from handlers:

```go
func handler(_ context.Context, c *Ctx) error {
    value, err := callService()
    if err != nil {
        return fmt.Errorf("service request failed: %w", err)
    }
    return c.Reply(value)
}
```

The central handler logs `[PLUGIN ERROR]` and sends `Error: ...` to the chat. For expected user errors, reply directly and return that reply result rather than exposing internal details.

## Reloading and Building

Plugins are compile-time registrations. The standard workflow is:

```bash
gofmt -w plugins/info-greet.go
go test ./...
make run
```

When started through `go run`, `main.go` watches plugin files and attempts to compile changed files with `-buildmode=plugin`. This is an experimental development convenience with limitations:

- Go plugins are not natively supported on Windows;
- new imports or dependencies may require a normal rebuild;
- deleting a source file does not unload an already registered plugin;
- production deployment should use a normal rebuilt binary.

## Runtime Guards (Anti-Spam, Command Gate, Cache)

The handler applies three built-in guards around your plugin handler.

### Anti-spam (per contact)

A sliding window counts commands per sender based on message timestamps. At `maxWarn` in the window the sender gets one warning and further commands are dropped; at `maxBan` the sender receives a temporary ban for `banMs`.

```json
"antispam": { "enabled": "enable", "windowMs": 1000, "maxWarn": 4, "maxBan": 5, "banMs": 60000 }
```

### Command gate (queue vs parallel per tier)

- Free contacts: at most `maxConcurrentFree` commands run at once; overflow waits in a bounded queue (`maxQueue`, `queueTimeoutMs`) or is rejected with a hint when `freeMode` is `"reject"`.
- Owner and premium: run immediately in parallel — no queue. Anti-spam still applies to every tier.
- Set `Parallel: true` on a plugin to always bypass the gate (light commands like ping).

```json
"commandGate": { "enabled": "enable", "maxConcurrentFree": 1, "maxQueue": 3, "freeMode": "queue", "queueTimeoutMs": 30000 }
```

### Single-flight + TTL cache

`template-go/lib` ships `FlightCache` — it collapses concurrent fetches per key into one call, caches results with a TTL, and serves the stale value when a refresh fails. The handler uses it to dedupe group-metadata fetches. Use it for your own expensive per-contact lookups:

```go
var fc = lib.NewFlightCache()

val, err := fc.Do("lookup:"+phone, 5*time.Minute, func() (any, error) {
    return fetchSomething(phone)
})
```

`ttl == 0` means dedupe-only (no caching). Guard state lives in memory; everything is tuned from `config.json`.

## Best Practices

1. Keep one feature per file and use aliases in one `Plugin`.
2. Prefix filenames with their category: `info-`, `utility-`, `owner-`, or `hidden-`.
3. Never rely on `Category` for authorization; check `c.IsOwner`.
4. Prefer `c.SenderPhone` over raw `c.SenderJID.User` for user identity.
5. Use `ResolveTargetPhone` for reply/mention/number commands.
6. Expect `c.GroupMeta` to be `nil` during a cold cache fill.
7. Protect package-level maps and counters from concurrent access.
8. Use timeouts for HTTP, shell, and external API calls.
9. Return errors for unexpected failures and use friendly replies for validation failures.
10. Keep `Before` hooks fast and idempotent.
11. Use `context.Context` passed to the handler when calling APIs that accept it.
12. Run `gofmt` and `go test ./...` before building.

See [Rich Messages](rich-messages.md) for AIRich, native-flow button, and carousel examples.
