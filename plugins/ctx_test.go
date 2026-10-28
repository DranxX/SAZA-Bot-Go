package plugins

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
	"template-go/store"
)

// Regression suite for target resolution: reply must win over the chat
// fallback, and LID detection must use the raw participant JID (Sender is
// digits-only), otherwise bans/unbans by reply silently miss the real phone.
func TestResolveTargetPhone(t *testing.T) {
	meta := &types.GroupInfo{Participants: []types.GroupParticipant{
		{JID: types.NewJID("628111111111", types.DefaultUserServer), PhoneNumber: types.NewJID("628111111111", types.DefaultUserServer)},
		{JID: types.NewJID("628222222222", types.DefaultUserServer), LID: types.NewJID("111000111222333", "lid"), PhoneNumber: types.NewJID("628222222222", types.DefaultUserServer)},
	}}
	store.MapLidToPhone("999000999111222", "628333333333")

	tests := []struct {
		name      string
		c         *Ctx
		wantPhone string
		wantLid   string
		wantErr   bool
	}{
		{
			name:      "group reply pn participant",
			c:         &Ctx{IsGroup: true, Chat: types.NewJID("120000000000000", types.GroupServer), GroupMeta: meta, Quoted: &QuotedInfo{Sender: "628111111111", Participant: "628111111111@s.whatsapp.net"}},
			wantPhone: "628111111111",
		},
		{
			name:      "group reply lid member resolved via meta",
			c:         &Ctx{IsGroup: true, Chat: types.NewJID("120000000000000", types.GroupServer), GroupMeta: meta, Quoted: &QuotedInfo{Sender: "111000111222333", Participant: "111000111222333@lid"}},
			wantPhone: "628222222222",
			wantLid:   "111000111222333",
		},
		{
			name:      "group reply lid resolved via store mapping",
			c:         &Ctx{IsGroup: true, Chat: types.NewJID("120000000000000", types.GroupServer), Quoted: &QuotedInfo{Sender: "999000999111222", Participant: "999000999111222@lid"}},
			wantPhone: "628333333333",
			wantLid:   "999000999111222",
		},
		{
			name:      "group reply unmapped lid falls back to lid digits",
			c:         &Ctx{IsGroup: true, Chat: types.NewJID("120000000000000", types.GroupServer), Quoted: &QuotedInfo{Sender: "888000888111222", Participant: "888000888111222@lid"}},
			wantPhone: "888000888111222",
			wantLid:   "888000888111222",
		},
		{
			name:      "mention pn",
			c:         &Ctx{IsGroup: true, Chat: types.NewJID("120000000000000", types.GroupServer), MentionedJIDs: []string{"628111111111@s.whatsapp.net"}},
			wantPhone: "628111111111",
		},
		{
			name:      "number argument",
			c:         &Ctx{IsGroup: true, Chat: types.NewJID("120000000000000", types.GroupServer), Args: []string{"628444444444"}},
			wantPhone: "628444444444",
		},
		{
			name:    "group without any target errors",
			c:       &Ctx{IsGroup: true, Chat: types.NewJID("120000000000000", types.GroupServer)},
			wantErr: true,
		},
		{
			name:      "dm without target resolves chat partner",
			c:         &Ctx{Chat: types.NewJID("628555555555", types.DefaultUserServer)},
			wantPhone: "628555555555",
		},
		{
			name:      "dm reply without participant resolves chat partner",
			c:         &Ctx{Chat: types.NewJID("628555555555", types.DefaultUserServer), Quoted: &QuotedInfo{}},
			wantPhone: "628555555555",
		},
		{
			name:      "dm reply with participant targets quoted sender",
			c:         &Ctx{Chat: types.NewJID("628555555555", types.DefaultUserServer), Quoted: &QuotedInfo{Sender: "628666666666", Participant: "628666666666@s.whatsapp.net"}},
			wantPhone: "628666666666",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phone, lid, err := ResolveTargetPhone(tt.c)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got phone=%q lid=%q", phone, lid)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if phone != tt.wantPhone || lid != tt.wantLid {
				t.Fatalf("got phone=%q lid=%q, want phone=%q lid=%q", phone, lid, tt.wantPhone, tt.wantLid)
			}
		})
	}
}

func TestFindCreditAmount(t *testing.T) {
	if n, ok := findCreditAmount([]string{"abc", "10"}); !ok || n != 10 {
		t.Fatalf("findCreditAmount = %d, %v, want 10, true", n, ok)
	}
	if n, _ := findCreditAmount([]string{"2000000"}); n != maxCreditAmount {
		t.Fatalf("amount not capped: %d", n)
	}
	if _, ok := findCreditAmount([]string{"-5", "abc"}); ok {
		t.Fatal("negative amounts must be rejected")
	}
	if _, ok := findCreditAmount([]string{"abc"}); ok {
		t.Fatal("non-numeric args must not resolve")
	}
}
