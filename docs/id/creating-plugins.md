# Membuat Plugin Go

Plugin adalah file `.go` di folder `plugins/`, memakai `package plugins`, dan mendaftarkan diri lewat `Register()` di dalam `init()`. Berbeda dari template JS/Bun yang melakukan scan file dinamis, template Go memakai registrasi compile-time: tambah file plugin, lalu restart `make run` atau build ulang.

```text
plugins/
├── registry.go       # Registry plugin
├── ctx.go            # Context + helper pesan/media
├── info-menu.go      # Plugin bawaan
└── info-contoh.go    # Plugin kamu
```

## Template Minimal

```go
// plugins/info-sapa.go
package plugins

import "context"

func init() {
    Register(&Plugin{
        Command:     []string{"sapa", "halo"},
        Description: "Membalas dengan sapaan",
        Category:    "info",
        Handler:     sapaHandler,
    })
}

func sapaHandler(_ context.Context, c *Ctx) error {
    name := c.PushName
    if name == "" {
        name = c.SenderPhone
    }
    return c.Replyf("Halo %s! Ketik %smenu untuk melihat command.", name, c.Prefix)
}
```

Simpan file, jalankan ulang bot, lalu kirim `.sapa`.

## Struktur Plugin

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

| Field | Wajib | Keterangan |
|---|---:|---|
| `Command` | Ya | Nama command tanpa prefix. Multi-alias: `[]string{"ping", "stats"}`. |
| `Description` | Disarankan | Keterangan yang muncul di menu. |
| `Category` | Disarankan | `info`, `utility`, `owner`, `hidden`, `ai`, `downloader`, atau kategori custom. |
| `Handler` | Ya | Fungsi utama command. |
| `Before` | Tidak | Hook yang berjalan pada setiap pesan sebelum command dispatch. |
| `Parallel` | Tidak | Selalu paralel, melewati command gate per kontak. |

Jika command alias bentrok dengan plugin lama, registry mengganti plugin lama beserta semua aliasnya.

## Kategori

| Kategori | Perilaku |
|---|---|
| `info` | Masuk section Information. |
| `utility` | Saat ini ikut digabung ke section Information. |
| `owner` | Hanya ditampilkan di menu owner. Handler tetap harus cek `c.IsOwner`. |
| `hidden` | Tidak tampil di menu; cocok untuk `Before` hook. |
| `ai`, `downloader`, custom | Ditampilkan sebagai section tambahan. |

`Category` hanya metadata tampilan, bukan security boundary.

## Referensi `Ctx`

Setiap handler menerima `*Ctx`.

### Koneksi dan event

| Field | Tipe | Keterangan |
|---|---|---|
| `Client` | `*whatsmeow.Client` | Client whatsmeow mentah. |
| `Event` | `*events.Message` | Event pesan asli. |

### Identitas

| Field | Tipe | Keterangan |
|---|---|---|
| `Chat` | `types.JID` | JID chat tujuan. |
| `SenderPhone` | `string` | Nomor bersih pengirim, hasil resolusi LID jika tersedia. |
| `SenderJID` | `types.JID` | JID pengirim mentah. Bisa server `lid`. |
| `IsGroup` | `bool` | Apakah chat grup. |
| `IsFromMe` | `bool` | Pesan dari device bot sendiri. |
| `PushName` | `string` | Nama WhatsApp pengirim. |
| `IsOwner` | `bool` | Hasil deteksi owner/bot. |
| `OwnerPhone` | `string` | Nomor owner dari config. |
| `BotPhone` | `string` | Nomor bot yang login. |

### Konten pesan

| Field | Tipe | Keterangan |
|---|---|---|
| `Text` | `string` | Teks atau caption pesan. |
| `MsgType` | `string` | Contoh: `conversation`, `imageMessage`, `videoMessage`. |
| `Quoted` | `*QuotedInfo` | Informasi pesan yang di-reply. `nil` jika tidak ada. |
| `MentionedJIDs` | `[]string` | JID mention dari context info. |

`QuotedInfo` berisi `ID`, `Sender`, `Participant`, `FromMe`, `Text`, `MsgType`, dan `Message` mentah untuk download media.

### Command

| Field | Tipe | Keterangan |
|---|---|---|
| `CommandName` | `string` | Alias command yang cocok. |
| `Args` | `[]string` | Argumen setelah command. |
| `Q` | `string` | Semua argumen digabung. Untuk `$`, berisi teks shell penuh. |
| `Prefix` | `string` | Prefix aktif. |

Field command baru diisi setelah parsing command. Di `Before` hook, gunakan `c.Text`.

### Grup dan waktu

| Field | Tipe | Keterangan |
|---|---|---|
| `GroupMeta` | `*types.GroupInfo` | Metadata grup cache. Bisa `nil` saat cache baru diisi. |
| `IsGroupAdmin` | `bool` | Apakah pengirim admin. |
| `IsBotGroupAdmin` | `bool` | Apakah bot admin. |
| `Timestamp` | `time.Time` | Timestamp WhatsApp. |
| `ReceivedAt` | `int64` | Waktu masuk handler dalam Unix millisecond. |

Metadata grup di-cache 5 menit dan di-refresh async.

## Helper Pesan

### Reply dan format

```go
return c.Reply("Halo dunia")
return c.Replyf("Halo %s", c.PushName)
```

### Progress + edit

```go
id, err := c.ReplyID("Sedang diproses...")
if err != nil {
    return err
}
return c.Edit(id, "Selesai.")
```

### Mention

```go
return c.ReplyMention(
    fmt.Sprintf("Halo @%s", c.SenderPhone),
    []string{c.SenderPhone},
)
```

### React, delete, read, presence

```go
_ = c.React("✅")
c.MarkRead()
_ = c.SendPresenceUpdate("composing")
defer c.SendPresenceUpdate("paused")

if !c.IsGroup || c.IsBotGroupAdmin {
    _ = c.Delete()
}
```

Presence yang didukung: `composing`, `paused`, `recording`.

## Mengirim Media

```go
data, err := os.ReadFile("./file.pdf")
if err != nil {
    return err
}
return c.SendDocument(data, "application/pdf", "file.pdf")
```

```go
return c.SendImage(imgBytes, "image/jpeg", "Caption gambar")
return c.SendVideo(videoBytes, "video/mp4", "Caption video")
return c.SendAudio(audioBytes, "audio/ogg; codecs=opus", true) // voice note
```

Untuk pesan mentah:

```go
_, err := c.Client.SendMessage(ctx, c.Chat, &waProto.Message{
    Conversation: proto.String("Pesan tanpa quote"),
})
return err
```

## Download Media

```go
data, mime, err := c.DownloadCurrentMedia()
if err != nil {
    return c.Reply("Kirim command sebagai caption media.")
}
return c.Replyf("Downloaded %d bytes (%s)", len(data), mime)
```

Untuk media yang di-reply:

```go
data, mime, err := c.DownloadQuotedMedia()
if err != nil {
    return c.Reply("Reply sebuah pesan media.")
}
return c.SendDocument(data, mime, "downloaded-file")
```

## Resolusi Target

Gunakan `ResolveTargetPhone(c)` untuk command yang membutuhkan target seperti ban, premium, info user, dan sebagainya. Urutan target:

1. lawan chat di DM jika tidak ada target eksplisit;
2. sender pesan yang di-reply;
3. mention pertama;
4. argumen angka minimal 7 digit.

```go
phone, lid, err := ResolveTargetPhone(c)
if err != nil {
    return c.Reply("Reply, mention, atau masukkan nomor target.")
}
if IsTargetOwnerOrBot(phone, c) {
    return c.Reply("Target tersebut dilindungi.")
}
return c.Replyf("Target: %s (lid: %s)", phone, lid)
```

Helper durasi:

```go
seconds := ParseDuration("1d12h30m")
text := FormatDuration(seconds)
```

Unit: `s`, `m`, `h`, `d`.

## Integrasi Premium

```go
import "template-go/store"

func premiumCommand(_ context.Context, c *Ctx) error {
    if !c.IsOwner {
        profile := store.GetPremiumProfile(c.SenderPhone, c.SenderJID.User)
        if !profile.IsPremium {
            return c.Reply("Command ini khusus premium.")
        }
        result := store.ConsumePremiumCredit(c.SenderPhone)
        if !result.OK {
            return c.Reply("Kredit premium habis.")
        }
    }
    return c.Reply("Fitur premium berjalan.")
}
```

Store premium menyediakan `GetPremiumProfile`, `ConsumePremiumCredit`, `SetPremiumCredits`, `AddPremiumCredits`, dan `RemovePremiumCredits`.

## Command Owner

```go
func ownerSayHandler(_ context.Context, c *Ctx) error {
    if !c.IsOwner {
        return nil
    }
    if c.Q == "" {
        return c.Replyf("Penggunaan: %ssay <teks>", c.Prefix)
    }
    return c.Reply(c.Q)
}
```

Jangan hanya mengandalkan `Category: "owner"`.

## `Before` Hook

Hook berjalan pada pesan normal sebelum command dispatch.

```go
func init() {
    Register(&Plugin{
        Command:     []string{"automod"},
        Description: "Moderasi otomatis",
        Category:    "hidden",
        Handler:     func(context.Context, *Ctx) error { return nil },
        Before: func(_ context.Context, c *Ctx) error {
            if c.IsOwner {
                return nil
            }
            text := strings.ToLower(c.Text)
            if strings.Contains(text, "spamword") {
                if !c.IsGroup || c.IsBotGroupAdmin {
                    _ = c.Delete()
                }
            }
            return nil
        },
    })
}
```

Catatan:

- reaction, sticker, dan protocol message dilewati sebelum hook;
- error hook diabaikan oleh central handler, jadi log sendiri jika perlu;
- hook berjalan berurutan sesuai registrasi;
- user yang sudah diban tetap bisa melewati `Before` hook karena ban check baru dilakukan setelah command dikenali;
- hindari operasi network lambat di hook, atau jalankan background work yang dibatasi dengan hati-hati.

## Parsing Command

- Command biasa dimulai dengan prefix config.
- `$ ` dikenali terpisah dari prefix dan dipakai plugin shell exec.
- Nama command dinormalisasi Unicode NFD, combining mark dihapus, dan lower-case.
- Argumen memakai `strings.Fields`; gunakan `c.Q` untuk teks gabungan.

## Build dan Reload

Workflow aman:

```bash
gofmt -w plugins/info-sapa.go
go test ./...
make run
```

Saat dijalankan dengan `go run`, ada watcher plugin eksperimental yang mencoba compile file berubah dengan `-buildmode=plugin`. Untuk produksi, gunakan rebuild binary biasa.

## Runtime Guards (Anti-Spam, Command Gate, Cache)

Handler menerapkan tiga guard bawaan di sekeliling plugin handler kamu.

### Anti-spam (per contact)

Sliding window menghitung command per pengirim berdasarkan timestamp pesan. Mencapai `maxWarn` dalam window → satu warning dan command berikutnya di-drop; mencapai `maxBan` → ban sementara selama `banMs`.

```json
"antispam": { "enabled": "enable", "windowMs": 1000, "maxWarn": 4, "maxBan": 5, "banMs": 60000 }
```

### Command gate (antre vs paralel per tier)

- Free: maksimal `maxConcurrentFree` command jalan bersamaan; kelebihan menunggu di antrean terbatas (`maxQueue`, `queueTimeoutMs`) atau ditolak dengan pesan saat `freeMode` `"reject"`.
- Owner dan premium: langsung paralel tanpa antre. Anti-spam tetap berlaku di semua tier.
- Set `Parallel: true` di plugin untuk selalu melewati gate (command ringan seperti ping).

```json
"commandGate": { "enabled": "enable", "maxConcurrentFree": 1, "maxQueue": 3, "freeMode": "queue", "queueTimeoutMs": 30000 }
```

### Single-flight + TTL cache

`template-go/lib` menyediakan `FlightCache` — menggabungkan fetch konkuren per key jadi satu panggilan, cache hasil dengan TTL, dan menyajikan nilai lama saat refresh gagal. Handler memakainya untuk dedupe fetch group metadata. Gunakan untuk lookup mahal milikmu sendiri per contact:

```go
var fc = lib.NewFlightCache()

val, err := fc.Do("lookup:"+phone, 5*time.Minute, func() (any, error) {
    return fetchSomething(phone)
})
```

`ttl == 0` artinya dedupe saja (tanpa cache). State guard hidup di memori; semuanya bisa di-tune dari `config.json`.

## Best Practices

1. Satu fitur per file.
2. Gunakan prefix file `info-`, `utility-`, `owner-`, `hidden-`.
3. Cek `c.IsOwner` untuk command admin.
4. Gunakan `c.SenderPhone` untuk identitas user.
5. Gunakan `ResolveTargetPhone` untuk target user.
6. Antisipasi `c.GroupMeta == nil`.
7. Lindungi state global dari concurrent access.
8. Gunakan timeout untuk HTTP/API/shell.
9. Return error untuk error tak terduga; reply ramah untuk validasi user.
10. Jalankan `gofmt` dan `go test ./...`.

Lihat [Rich Messages](rich-messages.md) untuk AIRich, button, dan carousel.
