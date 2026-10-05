package api

import (
	"net/http"

	"github.com/Liwyd/mit-seller/internal/bot"
	"regexp"
	"strings"
	"sync"
)

// Premium (custom) emoji for the button editor: a still preview of an emoji
// id, and the emoji of a pack so they can be picked instead of typed.

var (
	emojiID   = regexp.MustCompile(`^[0-9]{5,25}$`)
	packName  = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	emojiMu   sync.Mutex
	emojiImgs = map[string]emojiImg{}
)

type emojiImg struct {
	data  []byte
	ctype string
}

func (a *API) emojiPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !emojiID.MatchString(id) {
		fail(w, 400, "not a custom emoji id")
		return
	}
	emojiMu.Lock()
	img, ok := emojiImgs[id]
	emojiMu.Unlock()
	if !ok {
		st, err := a.B.TG.GetCustomEmojiStickers([]string{id})
		if err != nil || len(st) == 0 {
			fail(w, 404, "unknown custom emoji")
			return
		}
		fid := st[0].PreviewFileID()
		if fid == "" {
			fail(w, 404, "this emoji has no still preview")
			return
		}
		data, ctype, err := a.B.TG.DownloadFile(fid)
		if err != nil || len(data) == 0 {
			fail(w, 502, "could not download the emoji")
			return
		}
		if ctype == "" || ctype == "application/octet-stream" {
			ctype = "image/webp"
			if len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8 {
				ctype = "image/jpeg"
			}
		}
		img = emojiImg{data, ctype}
		emojiMu.Lock()
		if len(emojiImgs) > 2000 {
			emojiImgs = map[string]emojiImg{}
		}
		emojiImgs[id] = img
		emojiMu.Unlock()
	}
	w.Header().Set("Content-Type", img.ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(img.data)
}

func (a *API) emojiPack(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if !packName.MatchString(name) {
		fail(w, 400, "give the pack link (t.me/addemoji/NAME) or its name")
		return
	}
	set, err := a.B.TG.GetStickerSet(name)
	if err != nil {
		fail(w, 404, "pack not found")
		return
	}
	if set.StickerType != "custom_emoji" {
		fail(w, 400, "this is a sticker pack, not an emoji pack")
		return
	}
	out := []map[string]any{}
	for _, s := range set.Stickers {
		if s.CustomEmojiID != "" {
			out = append(out, map[string]any{"id": s.CustomEmojiID, "emoji": s.Emoji, "preview": s.PreviewFileID() != ""})
		}
	}
	ok(w, map[string]any{"name": set.Name, "title": set.Title, "emojis": out})
}

func (a *API) getEmojiAllow(w http.ResponseWriter, r *http.Request) {
	set, restricted := bot.EmojiAllowList(a.B.DB)
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	ok(w, map[string]any{"restricted": restricted, "count": len(ids), "ids": ids})
}

// putEmojiAllow is called by Mit Panel when its owner changes the packs.
func (a *API) putEmojiAllow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Restricted *bool    `json:"restricted"`
		IDs        []string `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	restricted := in.Restricted == nil || *in.Restricted
	if len(in.IDs) > 20000 {
		fail(w, 400, "too many emoji")
		return
	}
	bot.SetEmojiAllowList(a.B.DB, in.IDs, restricted)
	a.getEmojiAllow(w, r)
}

// emojiSets maps custom emoji ids (?ids=1,2,…) to the packs they belong to,
// so a pack can be allowed from just the ids of its emoji.
func (a *API) emojiSets(w http.ResponseWriter, r *http.Request) {
	var ids []string
	seen := map[string]bool{}
	for _, id := range strings.FieldsFunc(r.URL.Query().Get("ids"), func(c rune) bool { return c == ',' || c == ' ' }) {
		if emojiID.MatchString(id) && !seen[id] && len(ids) < 200 {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		fail(w, 400, "give custom emoji ids")
		return
	}
	st, err := a.B.TG.GetCustomEmojiStickers(ids)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	sets := map[string]string{}
	for _, s := range st {
		if s.SetName != "" {
			sets[s.CustomEmojiID] = s.SetName
		}
	}
	unknown := []string{}
	for _, id := range ids {
		if sets[id] == "" {
			unknown = append(unknown, id)
		}
	}
	ok(w, map[string]any{"sets": sets, "unknown": unknown})
}

// Colours and icons of the other buttons (categories, products, locations,
// payment methods…), by button text.
func (a *API) getLabelStyles(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"styles": bot.LoadLabelStyles(a.B.DB), "suggestions": a.B.LabelSuggestions()})
}

func (a *API) putLabelStyles(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Styles map[string]bot.LabelStyle `json:"styles"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if len(in.Styles) > bot.MaxLabelStyles {
		fail(w, 400, "too many buttons")
		return
	}
	for label, s := range in.Styles {
		switch {
		case strings.TrimSpace(label) == "" || len([]rune(label)) > 200:
			fail(w, 400, "a button text is empty or too long")
			return
		case s.Style != "" && s.Style != "primary" && s.Style != "success" && s.Style != "danger":
			fail(w, 400, label+": unknown colour "+s.Style)
			return
		case s.Emoji != "" && !emojiID.MatchString(s.Emoji):
			fail(w, 400, label+": not a custom emoji id")
			return
		case s.Emoji != "" && !bot.EmojiAllowed(a.B.DB, s.Emoji):
			fail(w, 400, label+": this premium emoji is not in the allowed packs")
			return
		}
	}
	a.B.StoreLabelStyles(in.Styles)
	a.getLabelStyles(w, r)
}
