package bot

import (
	"encoding/json"
	"regexp"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"github.com/MHBehzadian/nexra-mirzabot/internal/tg"
)

// Premium emoji allow-list. Nexra Panel's owner chooses which emoji packs the
// bots may use and pushes their emoji ids here (nexra_kv "emoji_allowed").
// Unset means no restriction (as before the panel managed it); once set,
// only those emoji are kept, as button icons or inside texts.

const emojiAllowKey = "emoji_allowed"

// EmojiAllowList is the allowed set, and whether a list is in force.
func EmojiAllowList(d *db.DB) (map[string]bool, bool) {
	raw, ok := d.KVOk(emojiAllowKey)
	if !ok || raw == "" {
		return nil, false
	}
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) != nil {
		return nil, false
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, true
}

// SetEmojiAllowList stores the allowed ids; restricted=false lifts the limit.
func SetEmojiAllowList(d *db.DB, ids []string, restricted bool) {
	if !restricted {
		d.Exec("DELETE FROM nexra_kv WHERE k = ?", emojiAllowKey)
		return
	}
	clean := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if isDigits(id) && id != "" && !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	b, _ := json.Marshal(clean)
	d.SetKV(emojiAllowKey, string(b))
}

// EmojiAllowed reports whether a custom emoji id may be used.
func EmojiAllowed(d *db.DB, id string) bool {
	set, restricted := EmojiAllowList(d)
	return !restricted || set[id]
}

var tgEmojiID = regexp.MustCompile(`<tg-emoji\s+emoji-id="([0-9]+)"`)

// DisallowedEmojiInText returns the first <tg-emoji> id in s that is not
// allowed, or "".
func DisallowedEmojiInText(d *db.DB, s string) string {
	set, restricted := EmojiAllowList(d)
	if !restricted {
		return ""
	}
	for _, m := range tgEmojiID.FindAllStringSubmatch(s, -1) {
		if !set[m[1]] {
			return m[1]
		}
	}
	return ""
}

// allowedEntities drops custom emoji entities outside the allow-list, so a
// text typed in Telegram keeps those as their plain fallback emoji.
func allowedEntities(d *db.DB, ents []tg.Entity) []tg.Entity {
	set, restricted := EmojiAllowList(d)
	if !restricted {
		return ents
	}
	out := make([]tg.Entity, 0, len(ents))
	for _, e := range ents {
		if e.Type == "custom_emoji" && !set[e.CustomEmojiID] {
			continue
		}
		out = append(out, e)
	}
	return out
}
