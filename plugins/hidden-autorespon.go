package plugins

import (
	"context"
	"strings"
)

func init() {
	Register(&Plugin{
		Command:     []string{"autorespon"},
		Description: "Auto-react to certain keywords",
		Category:    "hidden",
		Handler:     func(_ context.Context, _ *Ctx) error { return nil },
		Before:      autoReactBefore,
	})
}

func autoReactBefore(_ context.Context, c *Ctx) error {
	text := strings.ToLower(c.Text)
	if text == "" {
		return nil
	}

	// Laughing
	if strings.Contains(text, "wkwk") || strings.Contains(text, "haha") || strings.Contains(text, "lol") || strings.Contains(text, "😂") {
		_ = c.React("😂")
	}
	// Sad
	if strings.Contains(text, "sedih") || strings.Contains(text, "sad") || strings.Contains(text, "😢") {
		_ = c.React("😢")
	}
	// Thanks
	if strings.Contains(text, "makasih") || strings.Contains(text, "thanks") || strings.Contains(text, "thx") || strings.Contains(text, "terima kasih") {
		_ = c.React("❤️")
	}
	// Surprised / amazed
	if strings.Contains(text, "anjay") || strings.Contains(text, "gila") || strings.Contains(text, "wow") {
		_ = c.React("🗿")
	}

	return nil
}
