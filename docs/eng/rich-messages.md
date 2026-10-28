# Rich Messages — Go API Reference

`lib/airich.go` provides fluent builders for four WhatsApp message families:

1. `RichComposer` — Meta AI-style unified rich responses;
2. `FlowActionComposer` — native-flow buttons and single-selection lists;
3. `SimpleFlowComposer` — a compact quick-reply card;
4. `MessageCarouselComposer` — a carousel of interactive cards.

These builders use WhatsApp protobufs and native-flow nodes through whatsmeow. They are not part of WhatsApp's stable public Business API, so behavior can change with WhatsApp or whatsmeow updates.

## Import

```go
import "template-go/lib"
```

Inside a plugin handler, the common send arguments are:

- `ctx` — handler `context.Context`;
- `c.Client` — whatsmeow client;
- `c.Chat` — destination JID;
- `c.Event` — quoted source message.

Passing `nil` instead of `c.Event` sends without reply context.

## AIRich Quick Start

```go
func richHandler(ctx context.Context, c *Ctx) error {
    return lib.NewComposer(c.Client).
        WithHeader("Search Results").
        WithBody("Results for *Go WhatsApp bot*.").
        AppendText("The template uses whatsmeow and SQLite.").
        AppendCode("go", "fmt.Println(\"hello\")").
        AppendTable([][]string{
            {"File", "Purpose"},
            {"main.go", "Connection and event loop"},
            {"handler.go", "Message dispatch"},
        }).
        AppendTip("Use .menu to list commands.").
        AppendSuggestedPills([]string{"Menu", "Profile"}).
        DispatchMessage(ctx, c.Chat, c.Event)
}
```

Builders are mutable. Create a new instance for each outgoing message; do not reuse one instance concurrently.

## `RichComposer` Methods

### Metadata

| Method | Description |
|---|---|
| `NewComposer(agent)` | Create a composer from a whatsmeow-compatible `Messager`. |
| `WithHeader(text)` | Add a heading; represented as `# heading` in the rich text fallback. |
| `WithBody(text)` | Add the main body. |
| `WithFooter(text)` | Add an italic-style footer. |
| `MentionJIDs(jids)` | Add full JIDs to the rich message context. |

Header/body/footer are inserted before appended segments when the message is assembled.

### Text, code, and tables

| Method | Description |
|---|---|
| `AppendText(text)` | Add rich markdown text. Text above roughly 950 bytes is split at paragraph boundaries. |
| `AppendCode(language, code)` | Add a syntax-highlighted code block. Note the argument order: language first. |
| `AppendTable(rows)` | Add a table; the first row is treated as its heading row. |
| `AppendTip(text)` | Add metadata/tip text. |

```go
rich := lib.NewComposer(c.Client).
    AppendText("*Bold*, _italic_, ~strikethrough~").
    AppendCode("go", `func main() {
    fmt.Println("Saza")
}`).
    AppendTable([][]string{
        {"Command", "Category"},
        {".menu", "info"},
        {".ping", "utility"},
    })
```

Syntax tokenization contains language-specific keyword maps. Unknown/plain-text languages still produce a valid default code block.

### Links, citations, and inline entities

`AppendText` recognizes special inline forms:

```text
[Google](https://google.com)       hyperlink
[](https://example.com/source)     numbered citation-style entity
[label|400|100]<https://...png>    image/LaTeX-style inline entity
```

A URL prefixed with `!` in hyperlink syntax marks it as untrusted in metadata:

```text
[External](!https://example.com)
```

Add a visible source section with `AppendCitations`:

```go
rich.AppendCitations([][]string{
    {"https://example.com/favicon.jpg", "https://go.dev", "The Go Programming Language"},
    {"https://example.com/icon.jpg", "https://github.com/tulir/whatsmeow", "whatsmeow"},
})
```

Each source row is `[iconURL, sourceURL, displayName]`.

### Images and videos

```go
rich.AppendImages("https://example.com/image.png")
rich.AppendImages([]string{
    "https://example.com/one.png",
    "https://example.com/two.png",
})
```

```go
rich.AppendVideos("https://example.com/video.mp4")
rich.AppendVideos(map[string]any{
    "url":         "https://example.com/video.mp4",
    "mime_type":   "video/mp4",
    "duration":    float64(12),
    "file_length": float64(123456),
    "thumbnail":   "https://example.com/thumb.jpg",
})
```

Remote media is downloaded with a 15-second HTTP timeout and uploaded to WhatsApp newsletter media storage. Successful uploads are cached in memory by source URL.

### Products

```go
rich.AppendProduct(
    "Mechanical Keyboard",
    "Saza Store",
    "$80",
    "$65",
    "https://example.com/keyboard.jpg",
    "https://example.com/products/keyboard",
)
```

Argument order is `title, brand, price, salePrice, imageURL, productURL`.

### Reels

```go
rich.AppendReels([]map[string]any{
    {
        "username":     "creator",
        "profileIconUrl": "https://example.com/avatar.jpg",
        "thumbnailUrl": "https://example.com/thumb.jpg",
        "videoUrl":     "https://example.com/reel.mp4",
        "likes_count":  float64(100),
        "shares_count": float64(12),
        "view_count":   float64(900),
        "is_verified":  true,
    },
})
```

Recognized aliases include `profile_url`/`profile`, `thumbnail`, `url`, `like`, `share`, `view`, `source`, and `verified`.

### Social posts

```go
rich.AppendPost(map[string]any{
    "title":               "Release announcement",
    "username":            "sazabot",
    "profile_picture_url": "https://example.com/avatar.jpg",
    "thumbnail_url":       "https://example.com/post.jpg",
    "post_caption":        "Version 1.0 is available.",
    "likes_count":         float64(250),
    "comments_count":      float64(30),
    "shares_count":        float64(20),
    "post_url":            "https://example.com/post/1",
    "source_app":          "INSTAGRAM",
    "post_type":           "IMAGE",
})
```

Pass either `map[string]any` or `[]map[string]any`. Multiple posts use a horizontal layout.

### Suggested pills

```go
rich.AppendSuggestedPills("Show menu")
rich.AppendSuggestedPills([]string{"Show menu", "Open profile", "Bot status"})
```

A single pill uses a Single layout; multiple pills use HScroll.

### Manual layouts and segments

For advanced use:

| Method/function | Purpose |
|---|---|
| `AppendCustomLayout(name, data, extra)` | Build and append a layout directly. |
| `AppendLayoutSection(section)` | Append a prebuilt section. |
| `ComposeLayout(name, data, extra)` | Return a layout map without adding it. |
| `AppendTextSegment(text)` | Add raw text protobuf fallback. |
| `AppendCodeSegment(lang, blocks)` | Add pre-tokenized code metadata. |
| `AppendTableSegment(title, rows)` | Add prebuilt table metadata. |
| `DecomposeCode(code, lang)` | Produce protobuf and unified code blocks. |
| `ConvertTableToMetadata(rows)` | Produce protobuf and unified table rows. |

```go
rich := lib.NewComposer(c.Client).
    AppendTextSegment("Fallback text").
    AppendLayoutSection(lib.ComposeLayout("Single", map[string]any{
        "text":       "# Manual section",
        "__typename": "GenAIMarkdownTextUXPrimitive",
    }, nil))

return rich.DispatchMessage(ctx, c.Chat, c.Event)
```

Common layout names are `Single`, `HScroll`, and `ActionRow`. Manual layouts expose internal schemas; malformed data may fail to render without producing a transport error.

### Build and send

| Method | Description |
|---|---|
| `AssembleMessage(evt)` | Build a `*waProto.Message` without sending. |
| `DispatchMessage(ctx, to, evt)` | Process media, build, and send with required native-flow nodes. |

Prefer `DispatchMessage` when media is present because it uploads and rewrites external media URLs with a real context/client.

## Native-flow Buttons — `FlowActionComposer`

```go
return lib.NewActionComposer(c.Client).
    WithTitle("Saza-Bot").
    WithSubtitle("Main menu").
    WithBody("Choose an action.").
    WithFooter("DranxX Creative").
    WithImageMedia("https://example.com/header.png").
    AddReplyAction("Menu", c.Prefix+"menu").
    AddURLAction("Website", "https://example.com", true).
    AddCopyAction("Copy code", "SAZA-2026").
    DispatchInteractive(ctx, c.Chat, c.Event)
```

### Card metadata and media

| Method | Description |
|---|---|
| `WithTitle(text)` | Header title. |
| `WithSubtitle(text)` | Header subtitle. |
| `WithBody(text)` | Card body. |
| `WithFooter(text)` | Card footer. |
| `WithImageMedia(source)` | Image header from URL, local path, or `[]byte`. |
| `WithVideoMedia(source)` | Video header. |
| `WithDocumentMedia(source)` | Document header. |
| `WithMediaData(map)` | Set the raw attachment map. |
| `WithParams(map)` | Set native-flow message params JSON. |

Only one attachment type is rendered; image takes precedence over video, then document when multiple keys are supplied.

### Actions

```go
builder.
    AddReplyAction("Profile", c.Prefix+"profile", map[string]any{"icon": "REVIEW"}).
    AddURLAction("Docs", "https://example.com/docs", true, map[string]any{"icon": "PROMOTION"}).
    AddCopyAction("Copy token", "ABC-123", map[string]any{"icon": "DOCUMENT"})
```

The optional map is merged into button parameters. For an arbitrary native-flow action:

```go
builder.RegisterAction("quick_reply", map[string]any{
    "display_text": "Menu",
    "id":           c.Prefix + "menu",
})
```

Use `ResetActions()` to clear all accumulated actions.

### Single-selection lists

`CreateSection` and `CreateRow` apply to the most recently added single-selection action:

```go
builder.
    AddSingleSelection("Choose a category").
    CreateSection("Main menu", "POPULAR").
    CreateRow("FAST", "Utilities", "Ping and status", c.Prefix+"menu info").
    CreateRow("OWNER", "Owner commands", "Administration", c.Prefix+"menu owner").
    CreateSection("Other").
    CreateRow("HELP", "Help", "Show all commands", c.Prefix+"help")
```

Calling `CreateRow` before `AddSingleSelection` and `CreateSection` is a no-op.

### Render, assemble, dispatch

| Method | Return/use |
|---|---|
| `RenderInteractiveCard(ctx)` | Build `*waProto.InteractiveMessage`; useful as a carousel card. |
| `AssembleInteractive(ctx, evt)` | Wrap a card in `*waProto.Message`. |
| `DispatchInteractive(ctx, to, evt)` | Build and send the card. |

## Compact Buttons — `SimpleFlowComposer`

```go
return lib.NewSimpleFlow(c.Client).
    WithTitle("Quick actions").
    WithSubtitle("Saza-Bot").
    WithBody("Select one.").
    WithFooter("Footer").
    WithThumbnail("https://example.com/thumb.png").
    AddQuickReply("Menu", c.Prefix+"menu").
    AddQuickReply("Profile", c.Prefix+"profile").
    DispatchFlow(ctx, c.Chat, c.Event)
```

`AddQuickReply` accepts an optional ID. If omitted, a random UUID is generated:

```go
builder.AddQuickReply("Informational label")
```

Methods:

| Method | Description |
|---|---|
| `WithTitle`, `WithSubtitle`, `WithBody`, `WithFooter` | Set card text. |
| `WithThumbnail(source)` | Add an image header. |
| `AddQuickReply(label, optionalID...)` | Add a quick-reply button. |
| `AssembleFlow(ctx, evt)` | Build without sending. |
| `DispatchFlow(ctx, to, evt)` | Build and send. |

## Carousel

Create cards with `RenderInteractiveCard`, then append them to a carousel:

```go
card1, err := lib.NewActionComposer(c.Client).
    WithTitle("Starter").
    WithBody("Basic package").
    WithFooter("$5").
    WithImageMedia("https://example.com/starter.jpg").
    AddReplyAction("Buy", c.Prefix+"buy starter").
    RenderInteractiveCard(ctx)
if err != nil {
    return err
}

card2, err := lib.NewActionComposer(c.Client).
    WithTitle("Pro").
    WithBody("Advanced package").
    WithFooter("$10").
    WithImageMedia("https://example.com/pro.jpg").
    AddReplyAction("Buy", c.Prefix+"buy pro").
    RenderInteractiveCard(ctx)
if err != nil {
    return err
}

return lib.NewCarouselComposer(c.Client).
    WithBody("Available packages").
    WithFooter("Swipe to view").
    AppendCard(card1).
    AppendCard(card2).
    DispatchCarousel(ctx, c.Chat, c.Event)
```

| Method | Description |
|---|---|
| `WithBody(text)` | Carousel body. |
| `WithFooter(text)` | Carousel footer. |
| `AppendCard(card)` | Add a rendered interactive card; `nil` is ignored. |
| `AssembleCarousel(evt)` | Build without sending. |
| `DispatchCarousel(ctx, to, evt)` | Build and send. |

## Media Sources and Caching

Interactive header media accepts:

- an HTTP/HTTPS URL;
- a local filesystem path;
- `[]byte`.

AIRich image/video methods currently accept URL-oriented values. The implementation downloads remote content before upload and caches successful upload responses in a process-local `sync.Map`.

Operational considerations:

- no explicit download-size limit is enforced by the builder;
- remote HTTP status must be `200 OK`;
- remote downloads time out after 15 seconds;
- cached entries last until process exit;
- MIME values for interactive headers use builder defaults (`image/png`, `video/mp4`, `application/pdf`).

Validate and size-limit untrusted URLs before passing them to these builders.

## Troubleshooting

### Message sends but rich content does not render

- Update the client WhatsApp application.
- Confirm the project uses the whatsmeow version from `go.mod`.
- Start with `AppendText` and gradually add advanced layouts.
- Internal primitive names and native-flow formats can change upstream.

### Media fails

- Verify the URL is directly downloadable and returns `200` within 15 seconds.
- Prefer HTTPS and common image/video formats.
- For interactive media, try local `[]byte` to separate download failure from upload failure.

### Buttons do not trigger bot commands

The action ID should contain the actual command text, including the active prefix, for example `.menu`. If users can change the prefix, construct IDs with `c.Prefix + "menu"` rather than hardcoding `.`.

### Duplicate content when reusing builders

Composer methods append internal segments and assembly adds metadata segments. Create a fresh builder for every message instead of dispatching the same instance multiple times.

## Recommended Practices

1. Create a new builder per outgoing message.
2. Return dispatch errors from plugin handlers.
3. Use `c.Prefix` in button command IDs.
4. Limit remote media size and use HTTP timeouts before invoking the builder.
5. Keep a plain-text fallback for critical information.
6. Treat manual layouts as experimental and test after dependency upgrades.
7. Render carousel cards first and check every render error.
8. Do not mutate or reuse a builder from multiple goroutines.

See [Creating Go Plugins](creating-plugins.md) for the complete handler and `Ctx` reference.
