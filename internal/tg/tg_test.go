package tg

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestCallRetriesWithoutExtras(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if strings.Contains(string(b), "icon_custom_emoji_id") || strings.Contains(string(b), "tg-emoji") {
			w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: BUTTON_ICON_INVALID"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Token: "t", HTTP: srv.Client()}
	kb := ReplyKeyboard{Keyboard: [][]Button{{{Text: "buy", Style: "success", IconCustomEmojiID: "123"}}}, ResizeKeyboard: true}
	r := c.Call("sendMessage", map[string]any{"chat_id": 1, "text": `hi <tg-emoji emoji-id="5">😀</tg-emoji>`, "parse_mode": "HTML", "reply_markup": kb})
	if !r.OK {
		t.Fatalf("expected the plain retry to succeed: %+v", r)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(bodies))
	}
	var second map[string]any
	_ = json.Unmarshal([]byte(bodies[1]), &second)
	if second["text"] != "hi 😀" {
		t.Fatalf("tg-emoji not reduced: %v", second["text"])
	}
	btn := second["reply_markup"].(map[string]any)["keyboard"].([]any)[0].([]any)[0].(map[string]any)
	if _, has := btn["style"]; has || btn["text"] != "buy" {
		t.Fatalf("button not stripped: %v", btn)
	}

	// a plain message that fails is not retried
	bodies = nil
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, "x")
		w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer srv2.Close()
	c2 := &Client{Base: srv2.URL, Token: "t", HTTP: srv2.Client()}
	if c2.Call("sendMessage", map[string]any{"chat_id": 1, "text": "x"}).OK || len(bodies) != 1 {
		t.Fatalf("plain failure should not be retried (%d calls)", len(bodies))
	}
}
