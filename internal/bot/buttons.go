package bot

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/tg"
)

// Main-menu customisation (not in the PHP bot): button order, visibility,
// colour (Bot API "style": primary / success / danger) and a custom emoji
// icon (icon_custom_emoji_id, shown when the bot owner has Telegram Premium).
// Button labels themselves stay in the textbot table, as before.

// ButtonStyle is one button's look.
type ButtonStyle struct {
	Style  string `json:"style,omitempty"`  // "", "primary", "success", "danger"
	Emoji  string `json:"emoji,omitempty"`  // custom emoji id
	Hidden bool   `json:"hidden,omitempty"` // left out of the menu (still works if typed)
}

// ButtonConfig is what mit_kv["buttons"] holds.
type ButtonConfig struct {
	Layout  [][]string             `json:"layout"`
	Buttons map[string]ButtonStyle `json:"buttons"`
	// Mode is how the main menu is shown: "" (the PHP bot's keyboard under
	// the text field) or MenuInline (buttons attached to the bot's message).
	Mode string `json:"mode,omitempty"`
}

// MenuInline shows the main menu as inline ("glass") buttons.
const MenuInline = "inline"

// menuTapPrefix starts the callback data of an inline main-menu button.
const menuTapPrefix = "msm_"

// legacyMenuTapPrefix is menuTapPrefix before the rebrand; menus sent before
// it still carry it and must keep working.
const legacyMenuTapPrefix = "nxm_"

// menuTapKey strips the main-menu prefix from callback data, accepting both
// spellings, and returns "" when the tap is not a main-menu button.
func menuTapKey(data string) string {
	if k := strings.TrimPrefix(data, menuTapPrefix); k != data {
		return k
	}
	if k := strings.TrimPrefix(data, legacyMenuTapPrefix); k != data {
		return k
	}
	return ""
}

// MainButtonKeys are the menu buttons and where their label comes from.
// textbot ids for most, a fixed text for affiliates and the admin entry.
var MainButtonKeys = []string{
	"text_sell", "text_usertest", "text_Purchased_services", "text_Tariff_list",
	"text_account", "text_Add_Balance", "affiliates", "text_support", "text_help",
}

// Inline buttons that can be styled too (labels from textbot / fixed texts).
var ExtraStyleKeys = []string{"admin", "text_Discount", "text_fq", "support_message", "rules_accept", "back_home"}

// DefaultLayout is the PHP keyboard.php main menu.
func DefaultLayout() [][]string {
	return [][]string{
		{"text_sell", "text_usertest"},
		{"text_Purchased_services", "text_Tariff_list"},
		{"text_account", "text_Add_Balance"},
		{"affiliates"},
		{"text_support", "text_help"},
	}
}

// LoadButtons reads the customisation (defaults when unset or broken).
func LoadButtons(d *db.DB) ButtonConfig {
	cfg := ButtonConfig{Buttons: map[string]ButtonStyle{}}
	if raw := d.KV("buttons"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.Buttons == nil {
		cfg.Buttons = map[string]ButtonStyle{}
	}
	if !validLayout(cfg.Layout) {
		cfg.Layout = DefaultLayout()
	}
	// icons outside the panel's allowed packs are not shown
	if set, restricted := EmojiAllowList(d); restricted {
		for k, s := range cfg.Buttons {
			if s.Emoji != "" && !set[s.Emoji] {
				s.Emoji = ""
				cfg.Buttons[k] = s
			}
		}
	}
	return cfg
}

// StoreButtons sanitises and saves the customisation.
func StoreButtons(d *db.DB, bc ButtonConfig) ButtonConfig {
	bc = SanitizeButtons(bc)
	b, _ := json.Marshal(bc)
	d.SetKV("buttons", string(b))
	return bc
}

func validLayout(l [][]string) bool {
	if len(l) == 0 {
		return false
	}
	known := map[string]bool{}
	for _, k := range MainButtonKeys {
		known[k] = true
	}
	for _, row := range l {
		for _, k := range row {
			if !known[k] {
				return false
			}
		}
	}
	return true
}

// SanitizeButtons cleans user input before it is stored: unknown keys and
// styles are dropped, missing main buttons are appended so none disappears.
func SanitizeButtons(in ButtonConfig) ButtonConfig {
	out := ButtonConfig{Buttons: map[string]ButtonStyle{}}
	if in.Mode == MenuInline {
		out.Mode = MenuInline
	}
	known := map[string]bool{}
	for _, k := range MainButtonKeys {
		known[k] = true
	}
	allowed := map[string]bool{}
	for _, k := range append(append([]string{}, MainButtonKeys...), ExtraStyleKeys...) {
		allowed[k] = true
	}
	seen := map[string]bool{}
	for _, row := range in.Layout {
		var r []string
		for _, k := range row {
			if known[k] && !seen[k] && len(r) < 4 {
				r = append(r, k)
				seen[k] = true
			}
		}
		if len(r) > 0 {
			out.Layout = append(out.Layout, r)
		}
	}
	for _, k := range MainButtonKeys {
		if !seen[k] {
			out.Layout = append(out.Layout, []string{k})
		}
	}
	for k, s := range in.Buttons {
		if !allowed[k] {
			continue
		}
		switch s.Style {
		case "", "primary", "success", "danger":
		default:
			s.Style = ""
		}
		s.Emoji = strings.TrimSpace(s.Emoji)
		if !isDigits(s.Emoji) {
			s.Emoji = ""
		}
		if s.Style != "" || s.Emoji != "" || s.Hidden {
			out.Buttons[k] = s
		}
	}
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// styled applies a stored style to a button.
func (bc ButtonConfig) styled(key string, b tg.Button) tg.Button {
	if s, ok := bc.Buttons[key]; ok {
		b.Style = s.Style
		b.IconCustomEmojiID = s.Emoji
	}
	return b
}

// ValidateButtons reports the first thing in a submitted customisation that
// SanitizeButtons would silently drop, so an API caller gets a clear error.
func ValidateButtons(in ButtonConfig) error {
	allowed := map[string]bool{}
	for _, k := range append(append([]string{}, MainButtonKeys...), ExtraStyleKeys...) {
		allowed[k] = true
	}
	main := map[string]bool{}
	for _, k := range MainButtonKeys {
		main[k] = true
	}
	switch in.Mode {
	case "", "reply", MenuInline:
	default:
		return fmt.Errorf("mode must be reply or inline")
	}
	for _, row := range in.Layout {
		if len(row) > 4 {
			return fmt.Errorf("a row can hold at most 4 buttons")
		}
		for _, k := range row {
			if !main[k] {
				return fmt.Errorf("unknown main-menu button %q", k)
			}
		}
	}
	for k, s := range in.Buttons {
		if !allowed[k] {
			return fmt.Errorf("unknown button %q", k)
		}
		switch s.Style {
		case "", "primary", "success", "danger":
		default:
			return fmt.Errorf("button %q: style must be primary, success, danger or empty", k)
		}
		if e := strings.TrimSpace(s.Emoji); e != "" && !isDigits(e) {
			return fmt.Errorf("button %q: emoji must be a custom emoji id (digits)", k)
		}
	}
	return nil
}
