package plugins

import (
	"context"
	"fmt"
	"strings"
	"template-go/lib"
	"time"
)

func init() {
	Register(&Plugin{
		Command:     []string{"msgbuild", "airich"},
		Description: "Demo AI Rich Response & Interactive Messages",
		Category:    "info",
		Handler:     infoAirichHandler,
	})
}

func infoAirichHandler(ctx context.Context, c *Ctx) error {
	arg := ""
	if len(c.Args) > 0 {
		arg = strings.ToLower(strings.TrimSpace(c.Args[0]))
	}

	keys := []string{"text", "code", "table", "combined", "latex"}
	isDemoKey := false
	for _, k := range keys {
		if k == arg {
			isDemoKey = true
			break
		}
	}

	fullModes := []string{"button", "buttonv2", "carousel", "airich", "newlayout", "all"}
	isFullMode := false
	for _, fm := range fullModes {
		if fm == arg {
			isFullMode = true
			break
		}
	}

	if isFullMode {
		return runFullTest(ctx, c, arg)
	}

	if arg == "" || arg == "help" || !isDemoKey {
		ai := lib.NewComposer(c.Client).
			WithHeader("Saza-Bot Builder").
			AppendText("Demo fitur *AI Rich Response*.\n\n*Sub-perintah:*")
		for _, k := range keys {
			label := ""
			switch k {
			case "text":
				label = "Format Teks"
			case "code":
				label = "Syntax Highlight"
			case "table":
				label = "Tabel"
			case "combined":
				label = "Kombinasi"
			case "latex":
				label = "LaTeX Formula"
			}
			ai.AppendText(fmt.Sprintf("▸ %s%s %s — %s", c.Prefix, c.CommandName, k, label))
		}
		ai.AppendText(fmt.Sprintf("\n*Full test:* %s%s [button|buttonv2|carousel|airich|newlayout|all]", c.Prefix, c.CommandName))
		return ai.DispatchMessage(ctx, c.Chat, c.Event)
	}

	switch arg {
	case "text":
		return lib.NewComposer(c.Client).
			WithHeader("Format Teks").
			WithBody("*Ini teks tebal*\n_Ini teks miring_\n*_Ini tebal + miring_*\n~Ini teks dicoret~\n\nGunakan *bold* untuk penekanan,\n_italic_ untuk istilah asing.").
			DispatchMessage(ctx, c.Chat, c.Event)

	case "code":
		return lib.NewComposer(c.Client).
			WithHeader("Kode JavaScript").
			AppendCode("javascript", "function formatUptime(detik) {\n  const jam = Math.floor(detik / 3600);\n  const menit = Math.floor((detik % 3600) / 60);\n  const sisa = detik % 60;\n  return `${jam}j ${menit}m ${sisa}d`;\n}\n\nconsole.log(formatUptime(3661));").
			DispatchMessage(ctx, c.Chat, c.Event)

	case "table":
		return lib.NewComposer(c.Client).
			WithHeader("Daftar Command").
			AppendTable([][]string{
				{"Command", "Kategori", "Fungsi"},
				{".menu", "Info", "Tampilkan menu"},
				{".ping", "Utility", "Cek respon bot"},
				{".ai", "AI", "Chat dengan AI"},
			}).
			DispatchMessage(ctx, c.Chat, c.Event)

	case "combined":
		return lib.NewComposer(c.Client).
			WithHeader("Demo Kombinasi").
			AppendText("Pesan ini menggabungkan teks berformat dan code block:").
			AppendCode("javascript", "const sapa = (nama) =>\n  `Halo, ${nama}!`;\n\nconsole.log(sapa(\"Dunia\"));").
			AppendText("Semua dalam *satu* rich message.").
			DispatchMessage(ctx, c.Chat, c.Event)

	case "latex":
		return lib.NewComposer(c.Client).
			WithHeader("LaTeX Rendering").
			AppendText("AIRich mendukung render *LaTeX formula* via inline entity.\n\nContoh: [E=mc^{2}|400|100]<https://latex.codecogs.com/png.latex?\\dpi{150}\\bg{white}E%3Dmc^{2}>").
			AppendTip("Format: [label|width|height]<url PNG image> — LaTeX dirender sebagai gambar via codecogs.").
			DispatchMessage(ctx, c.Chat, c.Event)
	}

	return nil
}

func runFullTest(ctx context.Context, c *Ctx, mode string) error {
	if mode == "button" || mode == "all" {
		btn := lib.NewActionComposer(c.Client).
			WithTitle("Title Message").
			WithSubtitle("Subtitle Message").
			WithBody("Body Message").
			WithFooter("Footer Message").
			WithImageMedia("https://files.catbox.moe/5da9um.png").
			AddReplyAction("Menu", ".menu", map[string]any{"icon": "DEFAULT"}).
			AddReplyAction("Profile", ".profile", map[string]any{"icon": "REVIEW"}).
			AddURLAction("Website", "https://example.com", true, map[string]any{"icon": "PROMOTION"}).
			AddCopyAction("Copy Code", "SAZA-2026", map[string]any{"icon": "DOCUMENT"}).
			AddSingleSelection("Pilih Kategori").
			CreateSection("Main Menu").
			CreateRow("HOT", "Downloader", "Download social media", ".dl").
			CreateRow("FAST", "AI Chat", "Chat dengan AI", ".ai")
		if err := btn.DispatchInteractive(ctx, c.Chat, c.Event); err != nil {
			return err
		}
		if mode != "all" {
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	if mode == "buttonv2" || mode == "all" {
		btnV2 := lib.NewSimpleFlow(c.Client).
			WithTitle("Title Message").
			WithSubtitle("Subtitle Message").
			WithBody("Body Message").
			WithFooter("Footer Message").
			WithThumbnail("https://files.catbox.moe/5da9um.png").
			AddQuickReply("Menu", ".menu").
			AddQuickReply("Profile", ".profile")
		if err := btnV2.DispatchFlow(ctx, c.Chat, c.Event); err != nil {
			return err
		}
		if mode != "all" {
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	if mode == "carousel" || mode == "all" {
		b1, err1 := lib.NewActionComposer(c.Client).
			WithTitle("Kwetiau").
			WithBody("Kwetiau sodap").
			WithFooter("$5").
			WithImageMedia("https://files.catbox.moe/5da9um.png").
			AddReplyAction("Buy", ".buy kwetiau").
			RenderInteractiveCard(ctx)

		b2, err2 := lib.NewActionComposer(c.Client).
			WithTitle("Mie Gepeng").
			WithBody("Mie gepeng rebus").
			WithFooter("$7").
			WithImageMedia("https://files.catbox.moe/5da9um.png").
			AddReplyAction("Buy", ".buy mie gepeng").
			RenderInteractiveCard(ctx)

		if err1 != nil {
			return err1
		}
		if err2 != nil {
			return err2
		}
		car := lib.NewCarouselComposer(c.Client).
			WithBody("Product List").
			WithFooter("Swipe untuk lihat").
			AppendCard(b1).
			AppendCard(b2)
		if err := car.DispatchCarousel(ctx, c.Chat, c.Event); err != nil {
			return err
		}
		if mode != "all" {
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	if mode == "airich" || mode == "all" {
		err := lib.NewComposer(c.Client).
			WithHeader("Saza-Bot / DranxX Creative").
			WithFooter("SazaBot Core").
			AppendSuggestedPills([]string{"Bot Info", "All Categories", "Active Settings"}).
			AppendTip("Pro Tip: Tap suggestions below to navigate quickly!").
			AppendText("# Message Builder Showcase\n## Dynamic GenAI Layout\n### Interactive Elements\n* **Hyperlink**: Visit [Google](https://google.com)\n* **Citation**: [](https://github.com/dranxx)").
			AppendCode("javascript", "class SazaBot {\n  static init() {\n    console.log(\"Premium WhatsApp Bot active!\");\n  }\n}").
			AppendTable([][]string{{"DEVELOPER", "ROLE", "LINK"}, {"SazaBot", "Core Engine", "[GitHub](https://github.com)"}}).
			AppendCitations([][]string{{"https://files.catbox.moe/5da9um.png", "https://github.com", "GitHub Source"}}).
			AppendImages("https://files.catbox.moe/5da9um.png").
			DispatchMessage(ctx, c.Chat, c.Event)
		if err != nil {
			return err
		}
		if mode == "all" {
			time.Sleep(1 * time.Second)
		} else {
			return nil
		}
	}

	if mode == "newlayout" || mode == "all" {
		rich := lib.NewComposer(c.Client).
			WithHeader("DranxX Layout Engine").
			WithFooter("SazaBot Core").
			AppendTextSegment("Manual text via ComposeLayout").
			AppendLayoutSection(lib.ComposeLayout("Single", map[string]any{
				"text":       "# Manual Text Section\nAdded via ComposeLayout.",
				"__typename": "GenAIMarkdownTextUXPrimitive",
			}, nil))

		codeBlocks, unifiedBlocks := lib.DecomposeCode("rich.AppendLayoutSection(lib.ComposeLayout(\"Single\", data, nil))", "javascript")
		rich.AppendCodeSegment("javascript", codeBlocks).
			AppendLayoutSection(lib.ComposeLayout("Single", map[string]any{
				"language":    "javascript",
				"code_blocks": unifiedBlocks,
				"__typename":  "GenAICodeUXPrimitive",
			}, nil))

		_, tableRows, unifiedRows := lib.ConvertTableToMetadata([][]string{
			{"METHOD", "LAYOUT", "USE CASE"},
			{"ComposeLayout(\"Single\")", "Single", "Text, Code"},
			{"ComposeLayout(\"HScroll\")", "HScroll", "Carousel"},
			{"ComposeLayout(\"ActionRow\")", "ActionRow", "Suggestions"},
		})
		rich.AppendTableSegment("", tableRows).
			AppendLayoutSection(lib.ComposeLayout("Single", map[string]any{
				"rows":       unifiedRows,
				"__typename": "GenATableUXPrimitive",
			}, nil)).
			AppendLayoutSection(lib.ComposeLayout("ActionRow", []any{
				map[string]any{
					"prompt_text": "Single",
					"prompt_type": "SUGGESTED_PROMPT",
					"__typename":  "GenAIFollowUpSuggestionPillPrimitive",
				},
				map[string]any{
					"prompt_text": "HScroll",
					"prompt_type": "SUGGESTED_PROMPT",
					"__typename":  "GenAIFollowUpSuggestionPillPrimitive",
				},
				map[string]any{
					"prompt_text": "ActionRow",
					"prompt_type": "SUGGESTED_PROMPT",
					"__typename":  "GenAIFollowUpSuggestionPillPrimitive",
				},
			}, nil))

		return rich.DispatchMessage(ctx, c.Chat, c.Event)
	}

	return nil
}
