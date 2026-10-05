package bot

import (
	"encoding/json"
	"testing"

	"github.com/MHBehzadian/nexra-mirzabot/internal/tg"
)

func TestDecorateWith(t *testing.T) {
	exact := map[string]LabelStyle{"آلمان": {Style: "success", Emoji: "111"}, "🏠 بازگشت": {Style: "danger"}}
	loose := map[string]LabelStyle{menuKey("آلمان"): exact["آلمان"], menuKey("🏠 بازگشت"): exact["🏠 بازگشت"]}
	kept := tg.CB("سرویس ۱", "x")
	kept.Style = "primary"
	in := tg.Inline([]tg.Button{tg.CB("آلمان", "loc_1"), tg.CB("فرانسه", "loc_2")}, []tg.Button{tg.CB("بازگشت", "back"), kept})
	out, _ := json.Marshal(decorateWith(in, exact, loose))
	var got struct {
		InlineKeyboard [][]tg.Button `json:"inline_keyboard"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	k := got.InlineKeyboard
	if k[0][0].Style != "success" || k[0][0].IconCustomEmojiID != "111" || *k[0][0].CallbackData != "loc_1" {
		t.Errorf("exact label not styled: %+v", k[0][0])
	}
	if k[0][1].Style != "" || k[0][1].IconCustomEmojiID != "" {
		t.Errorf("other label styled: %+v", k[0][1])
	}
	if k[1][0].Style != "danger" {
		t.Errorf("label without its emoji not styled: %+v", k[1][0])
	}
	if k[1][1].Style != "primary" {
		t.Errorf("a button's own style was replaced: %+v", k[1][1])
	}
	// no styles stored: the markup is passed through as is
	if decorateWith(in, map[string]LabelStyle{}, nil) != any(in) {
		t.Error("markup changed with no styles")
	}
	// reply keyboards and JSON strings too
	rk := `{"keyboard":[[{"text":"آلمان"}]],"resize_keyboard":true}`
	b, _ := json.Marshal(decorateWith(rk, exact, loose))
	if string(b) != `{"keyboard":[[{"icon_custom_emoji_id":"111","style":"success","text":"آلمان"}]],"resize_keyboard":true}` {
		t.Errorf("reply keyboard: %s", b)
	}
}
