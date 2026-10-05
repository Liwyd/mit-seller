package bot

import (
	"html"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/Liwyd/mit-seller/internal/tg"
)

// Telegram Premium custom emoji support.
//
// Telegram reports custom emoji as message entities (offsets in UTF-16 code
// units). The PHP bot stored only the plain text, so a premium emoji typed by
// the admin came back as its fallback character. Texts are sent with
// parse_mode HTML, so wrapping those ranges in <tg-emoji> keeps them.

const emojiIDButton = "🆔 شناسه ایموجی پریمیوم"

func customEmojis(ents []tg.Entity) []tg.Entity {
	var out []tg.Entity
	for _, e := range ents {
		if e.Type == "custom_emoji" && e.CustomEmojiID != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Offset < out[j].Offset })
	return out
}

// withCustomEmoji returns text with every custom emoji wrapped in <tg-emoji>.
func withCustomEmoji(text string, ents []tg.Entity) string {
	ce := customEmojis(ents)
	if len(ce) == 0 {
		return text
	}
	u := utf16.Encode([]rune(text))
	var b strings.Builder
	pos := 0
	for _, e := range ce {
		if e.Offset < pos || e.Offset+e.Length > len(u) {
			continue
		}
		b.WriteString(string(utf16.Decode(u[pos:e.Offset])))
		inner := string(utf16.Decode(u[e.Offset : e.Offset+e.Length]))
		b.WriteString(`<tg-emoji emoji-id="` + html.EscapeString(e.CustomEmojiID) + `">` + inner + `</tg-emoji>`)
		pos = e.Offset + e.Length
	}
	b.WriteString(string(utf16.Decode(u[pos:])))
	return b.String()
}

// leadingCustomEmoji splits a custom emoji at the start of a button label
// into an icon id and the remaining label.
func leadingCustomEmoji(text string, ents []tg.Entity) (string, string) {
	ce := customEmojis(ents)
	if len(ce) == 0 || ce[0].Offset != 0 {
		return "", text
	}
	u := utf16.Encode([]rune(text))
	if ce[0].Length > len(u) {
		return "", text
	}
	rest := strings.TrimSpace(string(utf16.Decode(u[ce[0].Length:])))
	if rest == "" {
		return "", text
	}
	return ce[0].CustomEmojiID, rest
}

// buttonTextKeys are textbot entries used as button labels (no HTML there).
var buttonTextKeys = map[string]bool{
	"text_sell": true, "text_usertest": true, "text_Purchased_services": true, "text_support": true,
	"text_help": true, "text_fq": true, "text_account": true, "text_Add_Balance": true,
	"text_Tariff_list": true, "text_Discount": true,
}

// saveBotText stores an admin-provided text for a textbot id. Labels keep a
// leading premium emoji as the button icon; message texts keep them inline.
func (c *Ctx) saveBotText(id string) {
	value := c.text
	ents := allowedEntities(c.db(), c.f.Entities)
	if buttonTextKeys[id] {
		if emoji, rest := leadingCustomEmoji(c.text, ents); emoji != "" {
			value = rest
			bc := LoadButtons(c.db())
			s := bc.Buttons[id]
			s.Emoji = emoji
			bc.Buttons[id] = s
			StoreButtons(c.db(), bc)
		}
	} else {
		value = withCustomEmoji(c.text, ents)
	}
	c.upd("textbot", "text", value, "id_text", id)
}

// emojiIDReply lists the custom emoji ids in the admin's message.
func (c *Ctx) emojiIDReply() {
	ents := c.f.Entities
	if len(ents) == 0 {
		ents = c.f.CaptionEntities
	}
	ce := customEmojis(ents)
	if len(ce) == 0 {
		c.sendHTML(c.fromID, "هیچ ایموجی پریمیومی در پیام نبود. یک یا چند ایموجی پریمیوم بفرستید.", kbBackAdmin())
		return
	}
	var b strings.Builder
	b.WriteString("🆔 شناسه ایموجی‌ها:\n\n")
	var ids []string
	seen := map[string]bool{}
	for _, e := range ce {
		if seen[e.CustomEmojiID] {
			continue
		}
		seen[e.CustomEmojiID] = true
		ids = append(ids, e.CustomEmojiID)
		b.WriteString(`<tg-emoji emoji-id="` + e.CustomEmojiID + `">⭐️</tg-emoji> <code>` + e.CustomEmojiID + "</code>\n")
	}
	// the packs they come from: Mit Panel allows emoji by pack
	var packs []string
	if st, err := c.b.TG.GetCustomEmojiStickers(ids); err == nil {
		got := map[string]bool{}
		for _, s := range st {
			if s.SetName != "" && !got[s.SetName] {
				got[s.SetName] = true
				packs = append(packs, s.SetName)
			}
		}
	}
	if len(packs) > 0 {
		b.WriteString("\n📦 پک:\n")
		for _, p := range packs {
			b.WriteString("https://t.me/addemoji/" + html.EscapeString(p) + "\n")
		}
	}
	b.WriteString("\nبرای مجازکردن در Mit Panel: ربات ← «پک‌های ایموجی پریمیوم»، لینک پک یا همین پیام را بگذارید و «افزودن» را بزنید. " +
		"بعد این ایموجی‌ها در «دکمه‌ها» (آیکون دکمه) و در متن‌ها قابل استفاده‌اند.")
	c.sendHTML(c.fromID, b.String(), kbAdmin())
	c.step("home")
}
