# Rich Messages — Referensi API Go

`lib/airich.go` menyediakan builder untuk pesan WhatsApp yang lebih kaya:

1. `RichComposer` — AI Rich Response ala Meta AI;
2. `FlowActionComposer` — native-flow button, URL/copy action, dan single-select;
3. `SimpleFlowComposer` — kartu quick reply ringkas;
4. `MessageCarouselComposer` — carousel dari interactive card.

Builder ini memakai protobuf dan native-flow node internal WhatsApp via whatsmeow. Formatnya dapat berubah mengikuti WhatsApp/whatsmeow.

## Import

```go
import "template-go/lib"
```

Di plugin, parameter umum:

- `ctx` — `context.Context` handler;
- `c.Client` — client whatsmeow;
- `c.Chat` — chat tujuan;
- `c.Event` — pesan sumber untuk quote/reply context.

## Quick Start AIRich

```go
func richHandler(ctx context.Context, c *Ctx) error {
    return lib.NewComposer(c.Client).
        WithHeader("Hasil Pencarian").
        WithBody("Hasil untuk *Saza-Bot*.").
        AppendText("Template ini memakai whatsmeow dan SQLite.").
        AppendCode("go", "fmt.Println(\"hello\")").
        AppendTable([][]string{
            {"File", "Fungsi"},
            {"main.go", "Entry point"},
            {"handler.go", "Pipeline pesan"},
        }).
        AppendTip("Gunakan .menu untuk melihat semua command.").
        AppendSuggestedPills([]string{"Menu", "Profile"}).
        DispatchMessage(ctx, c.Chat, c.Event)
}
```

Builder bersifat mutable. Buat builder baru untuk setiap pesan dan jangan dipakai bersama beberapa goroutine.

## `RichComposer`

### Metadata

| Method | Keterangan |
|---|---|
| `NewComposer(agent)` | Membuat composer dari client pengirim. |
| `WithHeader(text)` | Judul/heading. |
| `WithBody(text)` | Body utama. |
| `WithFooter(text)` | Footer. |
| `MentionJIDs(jids)` | Menambahkan mention JID ke context. |

### Teks, code, tabel

| Method | Keterangan |
|---|---|
| `AppendText(text)` | Menambah teks rich markdown. Teks panjang dipecah per paragraf. |
| `AppendCode(language, code)` | Code block dengan syntax highlighting. Urutan argumen: language, lalu code. |
| `AppendTable(rows)` | Tabel; baris pertama menjadi header. |
| `AppendTip(text)` | Tip/metadata text. |

```go
return lib.NewComposer(c.Client).
    WithHeader("Demo").
    AppendText("*Bold*, _italic_, ~strike~").
    AppendCode("go", `func main() {
    fmt.Println("Saza")
}`).
    AppendTable([][]string{
        {"Command", "Kategori"},
        {".menu", "info"},
        {".ping", "utility"},
    }).
    DispatchMessage(ctx, c.Chat, c.Event)
```

### Link, citation, dan LaTeX/image entity

`AppendText` mengenali pola:

```text
[Google](https://google.com)       hyperlink
[](https://example.com/source)     citation entity
[E=mc²|400|100]<https://...png>    image/LaTeX entity
```

Source citation section:

```go
rich.AppendCitations([][]string{
    {"https://example.com/icon.jpg", "https://go.dev", "Go"},
    {"https://example.com/icon.jpg", "https://github.com/tulir/whatsmeow", "whatsmeow"},
})
```

Format tiap baris: `[iconURL, sourceURL, label]`.

### Gambar dan video

```go
rich.AppendImages("https://example.com/image.png")
rich.AppendImages([]string{"https://example.com/1.png", "https://example.com/2.png"})
```

```go
rich.AppendVideos(map[string]any{
    "url":         "https://example.com/video.mp4",
    "mime_type":   "video/mp4",
    "duration":    float64(12),
    "file_length": float64(123456),
    "thumbnail":   "https://example.com/thumb.jpg",
})
```

Media remote di-download dengan timeout 15 detik dan di-upload ke storage WhatsApp. Hasil upload di-cache selama proses hidup.

### Produk, reels, post

```go
rich.AppendProduct(
    "Keyboard", "Saza Store", "$80", "$65",
    "https://example.com/keyboard.jpg",
    "https://example.com/products/keyboard",
)
```

```go
rich.AppendReels([]map[string]any{{
    "username":     "creator",
    "thumbnailUrl": "https://example.com/thumb.jpg",
    "videoUrl":     "https://example.com/reel.mp4",
    "view_count":   float64(900),
}})
```

```go
rich.AppendPost(map[string]any{
    "username":      "sazabot",
    "thumbnail_url": "https://example.com/post.jpg",
    "post_caption":  "Version 1.0 tersedia.",
    "post_url":      "https://example.com/post/1",
    "source_app":    "INSTAGRAM",
})
```

### Suggested pills

```go
rich.AppendSuggestedPills("Tampilkan menu")
rich.AppendSuggestedPills([]string{"Menu", "Profile", "Status bot"})
```

### Layout manual

| Method/fungsi | Keterangan |
|---|---|
| `AppendCustomLayout(name, data, extra)` | Menambah layout manual. |
| `AppendLayoutSection(section)` | Menambah section prebuilt. |
| `ComposeLayout(name, data, extra)` | Membuat layout map. |
| `AppendTextSegment(text)` | Menambah fallback text protobuf. |
| `AppendCodeSegment(lang, blocks)` | Menambah code segment prebuilt. |
| `AppendTableSegment(title, rows)` | Menambah table segment prebuilt. |
| `DecomposeCode(code, lang)` | Membuat metadata code. |
| `ConvertTableToMetadata(rows)` | Membuat metadata tabel. |

```go
rich := lib.NewComposer(c.Client).
    AppendTextSegment("Fallback text").
    AppendLayoutSection(lib.ComposeLayout("Single", map[string]any{
        "text":       "# Manual Section",
        "__typename": "GenAIMarkdownTextUXPrimitive",
    }, nil))

return rich.DispatchMessage(ctx, c.Chat, c.Event)
```

Gunakan layout manual hanya jika memahami schema internal (`Single`, `HScroll`, `ActionRow`).

### Build dan kirim

| Method | Fungsi |
|---|---|
| `AssembleMessage(evt)` | Membuat `*waProto.Message` tanpa kirim. |
| `DispatchMessage(ctx, to, evt)` | Memproses media, build, dan kirim. |

## Native-flow Button — `FlowActionComposer`

```go
return lib.NewActionComposer(c.Client).
    WithTitle("Saza-Bot").
    WithSubtitle("Main menu").
    WithBody("Pilih tindakan.").
    WithFooter("DranxX Creative").
    WithImageMedia("https://example.com/header.png").
    AddReplyAction("Menu", c.Prefix+"menu").
    AddURLAction("Website", "https://example.com", true).
    AddCopyAction("Salin kode", "SAZA-2026").
    DispatchInteractive(ctx, c.Chat, c.Event)
```

| Method | Keterangan |
|---|---|
| `WithTitle`, `WithSubtitle`, `WithBody`, `WithFooter` | Mengatur teks card. |
| `WithImageMedia`, `WithVideoMedia`, `WithDocumentMedia` | Header media dari URL/path/bytes. |
| `WithParams(map)` | Message params JSON. |
| `ResetActions()` | Menghapus action. |
| `RegisterAction(name, params)` | Action native-flow mentah. |
| `AddReplyAction(label, id, opts...)` | Quick reply. |
| `AddURLAction(label, url, webview, opts...)` | Tombol URL. |
| `AddCopyAction(label, code, opts...)` | Tombol copy. |

Single select:

```go
builder.
    AddSingleSelection("Pilih kategori").
    CreateSection("Main Menu", "HOT").
    CreateRow("FAST", "Utility", "Ping dan status", c.Prefix+"menu info").
    CreateRow("OWNER", "Owner", "Administrasi", c.Prefix+"menu owner")
```

Render dan kirim:

| Method | Fungsi |
|---|---|
| `RenderInteractiveCard(ctx)` | Membuat card untuk carousel atau manual send. |
| `AssembleInteractive(ctx, evt)` | Membungkus card ke `waProto.Message`. |
| `DispatchInteractive(ctx, to, evt)` | Build dan kirim. |

## Button Ringkas — `SimpleFlowComposer`

```go
return lib.NewSimpleFlow(c.Client).
    WithTitle("Quick actions").
    WithSubtitle("Saza-Bot").
    WithBody("Pilih salah satu.").
    WithFooter("Footer").
    WithThumbnail("https://example.com/thumb.png").
    AddQuickReply("Menu", c.Prefix+"menu").
    AddQuickReply("Profile", c.Prefix+"profile").
    DispatchFlow(ctx, c.Chat, c.Event)
```

Jika ID tidak diberikan pada `AddQuickReply`, builder membuat UUID acak.

## Carousel

```go
card1, err := lib.NewActionComposer(c.Client).
    WithTitle("Starter").
    WithBody("Paket basic").
    WithFooter("$5").
    WithImageMedia("https://example.com/starter.jpg").
    AddReplyAction("Beli", c.Prefix+"buy starter").
    RenderInteractiveCard(ctx)
if err != nil {
    return err
}

card2, err := lib.NewActionComposer(c.Client).
    WithTitle("Pro").
    WithBody("Paket advanced").
    WithFooter("$10").
    WithImageMedia("https://example.com/pro.jpg").
    AddReplyAction("Beli", c.Prefix+"buy pro").
    RenderInteractiveCard(ctx)
if err != nil {
    return err
}

return lib.NewCarouselComposer(c.Client).
    WithBody("Paket tersedia").
    WithFooter("Geser untuk melihat").
    AppendCard(card1).
    AppendCard(card2).
    DispatchCarousel(ctx, c.Chat, c.Event)
```

| Method | Keterangan |
|---|---|
| `WithBody(text)` | Body carousel. |
| `WithFooter(text)` | Footer carousel. |
| `AppendCard(card)` | Menambah card; `nil` diabaikan. |
| `AssembleCarousel(evt)` | Build tanpa kirim. |
| `DispatchCarousel(ctx, to, evt)` | Build dan kirim. |

## Media dan Cache

Header media interactive menerima URL HTTP/HTTPS, path lokal, atau `[]byte`. Builder AIRich image/video lebih berorientasi URL. Download remote memiliki timeout 15 detik, status harus `200 OK`, dan tidak ada limit ukuran eksplisit.

Validasi URL tidak tepercaya sebelum diberikan ke builder.

## Troubleshooting

### Rich content tidak tampil

- Update aplikasi WhatsApp.
- Pastikan versi whatsmeow sesuai `go.mod`.
- Test dari `AppendText` dulu, lalu tambah layout kompleks.

### Media gagal

- Pastikan URL direct download, HTTPS, dan `200 OK`.
- Coba sumber lokal/`[]byte` untuk membedakan error download dan upload.

### Button tidak memicu command

ID action harus berisi command lengkap beserta prefix, misalnya `.menu`. Jika prefix bisa berubah, gunakan `c.Prefix + "menu"`.

### Konten duplikat

Jangan reuse builder yang sama untuk beberapa dispatch. Buat instance baru untuk setiap pesan.

## Best Practices

1. Buat builder baru per pesan.
2. Return error dari `Dispatch...` ke handler.
3. Gunakan `c.Prefix` untuk ID command button.
4. Batasi dan validasi media remote.
5. Simpan fallback teks untuk informasi penting.
6. Test layout manual setelah upgrade dependency.
7. Periksa error saat render card carousel.
8. Jangan akses satu builder dari beberapa goroutine.

Lihat [Membuat Plugin Go](creating-plugins.md) untuk referensi `Ctx` dan handler.
