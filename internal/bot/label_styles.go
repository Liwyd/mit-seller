package bot

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/Liwyd/mit-seller/internal/db"
)

// Colours and premium icons for any button, by its text: categories,
// products, locations, payment methods, back buttons… Every keyboard the bot
// sends passes through decorateMarkup, which gives a button the style stored
// for its label unless the button already has one (the main menu's own
// settings win). Nothing is stored → keyboards go out untouched.

const kvLabelStyles = "label_styles"

// MaxLabelStyles bounds the list an admin can keep.
const MaxLabelStyles = 1000

type LabelStyle struct {
	Style string `json:"style,omitempty"`
	Emoji string `json:"emoji,omitempty"`
}

func validStyle(s string) bool {
	switch s {
	case "", "primary", "success", "danger":
		return true
	}
	return false
}

// LoadLabelStyles reads the stored styles, dropping icons outside the
// allowed emoji packs.
func LoadLabelStyles(d *db.DB) map[string]LabelStyle {
	out := map[string]LabelStyle{}
	raw := d.KV(kvLabelStyles)
	if raw == "" {
		return out
	}
	var m map[string]LabelStyle
	if json.Unmarshal([]byte(raw), &m) != nil {
		return out
	}
	allowed, restricted := EmojiAllowList(d)
	for label, s := range m {
		label = strings.TrimSpace(label)
		if label == "" || !validStyle(s.Style) {
			continue
		}
		if s.Emoji != "" && (!isDigits(s.Emoji) || (restricted && !allowed[s.Emoji])) {
			s.Emoji = ""
		}
		if s.Style != "" || s.Emoji != "" {
			out[label] = s
		}
	}
	return out
}

// StoreLabelStyles saves the styles (empty entries are dropped).
func (b *Bot) StoreLabelStyles(m map[string]LabelStyle) {
	clean := map[string]LabelStyle{}
	for label, s := range m {
		label = strings.TrimSpace(label)
		if label != "" && (s.Style != "" || s.Emoji != "") {
			clean[label] = s
		}
	}
	raw, _ := json.Marshal(clean)
	b.DB.SetKV(kvLabelStyles, string(raw))
	b.labels.mu.Lock()
	b.labels.at = time.Time{}
	b.labels.mu.Unlock()
}

type labelCache struct {
	mu    sync.Mutex
	at    time.Time
	exact map[string]LabelStyle
	loose map[string]LabelStyle // by menuKey: the label without emoji/joiners
}

func (b *Bot) labelStyles() (map[string]LabelStyle, map[string]LabelStyle) {
	b.labels.mu.Lock()
	defer b.labels.mu.Unlock()
	if time.Since(b.labels.at) > 30*time.Second {
		b.labels.exact = LoadLabelStyles(b.DB)
		b.labels.loose = map[string]LabelStyle{}
		for label, s := range b.labels.exact {
			if k := menuKey(label); k != "" {
				b.labels.loose[k] = s
			}
		}
		b.labels.at = time.Now()
	}
	return b.labels.exact, b.labels.loose
}

// decorateMarkup gives the buttons of a reply_markup their stored style.
func (b *Bot) decorateMarkup(markup any) any {
	exact, loose := b.labelStyles()
	return decorateWith(markup, exact, loose)
}

func decorateWith(markup any, exact, loose map[string]LabelStyle) any {
	if len(exact) == 0 || markup == nil {
		return markup
	}
	var raw []byte
	if s, ok := markup.(string); ok {
		raw = []byte(s)
	} else {
		var err error
		if raw, err = json.Marshal(markup); err != nil {
			return markup
		}
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return markup
	}
	changed := false
	for _, field := range []string{"inline_keyboard", "keyboard"} {
		rows, _ := m[field].([]any)
		for _, r := range rows {
			btns, _ := r.([]any)
			for _, x := range btns {
				btn, _ := x.(map[string]any)
				text, _ := btn["text"].(string)
				if text == "" || btn["style"] != nil || btn["icon_custom_emoji_id"] != nil {
					continue
				}
				s, ok := exact[strings.TrimSpace(text)]
				if !ok {
					if k := menuKey(text); k != "" {
						s, ok = loose[k]
					}
				}
				if !ok {
					continue
				}
				if s.Style != "" {
					btn["style"] = s.Style
				}
				if s.Emoji != "" {
					btn["icon_custom_emoji_id"] = s.Emoji
				}
				changed = true
			}
		}
	}
	if !changed {
		return markup
	}
	return m
}

// labelSuggestionKeys are the fixed buttons users see most, offered in Mit
// Panel next to the categories, products and locations.
var labelSuggestionKeys = []string{
	"users.backhome", "users.backmenu", "users.closelist",
	"users.buy.payandGet", "users.buy.discount",
	"users.moeny.cart_to_Cart_btn", "users.moeny.nowpaymentbtn", "users.moeny.currency_rial_gateway", "users.moeny.mr_payment_gateway",
	"users.status.backservice", "users.status.backlist", "users.status.manageService",
	"users.extend.title", "users.Extra_volume.sellextra", "users.removeconfig.btnremoveuser",
	"users.page.next", "users.page.previous", "users.rulesaccept", "users.sendnumber",
	"users.help.btninlinebuy", "users.customusername", "users.customidAndRandom",
}

// LabelSuggestions lists button labels this bot actually shows, by group.
func (b *Bot) LabelSuggestions() map[string][]string {
	d := b.DB
	col := func(q string) []string {
		seen := map[string]bool{}
		out := []string{}
		for _, r := range d.MustQuery(q) {
			v := strings.TrimSpace(r.S("v"))
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
		return out
	}
	fixed := []string{}
	seen := map[string]bool{}
	for _, k := range labelSuggestionKeys {
		v := strings.TrimSpace(T(k))
		if v != "" && v != k && !seen[v] && len([]rune(v)) <= 40 && !strings.Contains(v, "\n") {
			seen[v] = true
			fixed = append(fixed, v)
		}
	}
	return map[string][]string{
		"categories": col("SELECT remark AS v FROM category ORDER BY id"),
		"products":   col("SELECT name_product AS v FROM product ORDER BY id"),
		"locations":  col("SELECT name_panel AS v FROM marzban_panel ORDER BY id"),
		"fixed":      fixed,
	}
}
