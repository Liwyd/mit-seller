// Package tg is a small Telegram Bot API client covering what the bot uses.
package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

type Client struct {
	Token string
	Base  string // https://api.telegram.org
	HTTP  *http.Client
	Log   *log.Logger
	// Decorate, if set, may rewrite every reply_markup before it is sent
	// (the bot uses it to colour buttons by their text).
	Decorate func(markup any) any
}

func (c *Client) decorate(params map[string]any) map[string]any {
	m, ok := params["reply_markup"]
	if c.Decorate == nil || !ok || m == nil {
		return params
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}
	out["reply_markup"] = c.Decorate(m)
	return out
}

func New(token, base, proxy string) *Client {
	tr := &http.Transport{MaxIdleConnsPerHost: 32, IdleConnTimeout: 90 * time.Second}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &Client{Token: token, Base: base, HTTP: &http.Client{Timeout: 60 * time.Second, Transport: tr}}
}

// Response is the Bot API envelope.
type Response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

func (c *Client) logf(format string, a ...any) {
	if c.Log != nil {
		c.Log.Printf(format, a...)
	}
}

// Call sends a method with JSON parameters. Nil values are dropped, the way
// an unset PHP array entry would be.
func (c *Client) Call(method string, params map[string]any) Response {
	params = c.decorate(params)
	r := c.call(method, params)
	if !r.OK && r.ErrorCode == 400 {
		// Coloured buttons, premium-emoji button icons and <tg-emoji> in text
		// are extras: if Telegram refuses them (owner without Premium, a stale
		// emoji id, an older server) send the message plain rather than not at all.
		if plain, changed := withoutExtras(params); changed {
			c.logf("tg %s: retrying without button styles/custom emoji (%s)", method, r.Description)
			return c.call(method, plain)
		}
	}
	return r
}

var tgEmojiRe = regexp.MustCompile(`(?s)<tg-emoji emoji-id="[0-9]+">(.*?)</tg-emoji>`)

// withoutExtras copies params without button style/icon fields and with
// <tg-emoji> tags reduced to their fallback emoji.
func withoutExtras(params map[string]any) (map[string]any, bool) {
	out := make(map[string]any, len(params))
	changed := false
	for k, v := range params {
		out[k] = v
		switch k {
		case "reply_markup":
			b, err := json.Marshal(v)
			if err != nil {
				continue
			}
			var m any
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			if stripKeys(m, "style", "icon_custom_emoji_id") {
				out[k] = m
				changed = true
			}
		case "text", "caption":
			if s, ok := v.(string); ok && tgEmojiRe.MatchString(s) {
				out[k] = tgEmojiRe.ReplaceAllString(s, "$1")
				changed = true
			}
		}
	}
	return out, changed
}

func stripKeys(v any, keys ...string) bool {
	changed := false
	switch x := v.(type) {
	case map[string]any:
		for _, k := range keys {
			if _, ok := x[k]; ok {
				delete(x, k)
				changed = true
			}
		}
		for _, e := range x {
			if stripKeys(e, keys...) {
				changed = true
			}
		}
	case []any:
		for _, e := range x {
			if stripKeys(e, keys...) {
				changed = true
			}
		}
	}
	return changed
}

func (c *Client) call(method string, params map[string]any) Response {
	clean := map[string]json.RawMessage{}
	for k, v := range params {
		if v == nil {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil || string(b) == "null" {
			continue
		}
		clean[k] = b
	}
	body, err := json.Marshal(clean)
	if err != nil {
		c.logf("tg %s marshal: %v", method, err)
		return Response{}
	}
	return c.do(method, "application/json", bytes.NewReader(body))
}

func (c *Client) do(method, contentType string, body io.Reader) Response {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.Base+"/bot"+c.Token+"/"+method, body)
	if err != nil {
		return Response{}
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.logf("tg %s: %v", method, err)
		return Response{}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var r Response
	_ = json.Unmarshal(raw, &r)
	if !r.OK {
		c.logf("tg %s failed: %s", method, string(raw))
	}
	return r
}

// Upload sends a method with one file field plus string fields.
func (c *Client) Upload(method string, fields map[string]any, fileField, fileName string, data []byte) Response {
	fields = c.decorate(fields)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if v == nil {
			continue
		}
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case int64:
			s = strconv.FormatInt(x, 10)
		case int:
			s = strconv.Itoa(x)
		case bool:
			s = strconv.FormatBool(x)
		default:
			b, _ := json.Marshal(x)
			if string(b) == "null" {
				continue
			}
			s = string(b)
		}
		_ = w.WriteField(k, s)
	}
	fw, _ := w.CreateFormFile(fileField, fileName)
	_, _ = fw.Write(data)
	_ = w.Close()
	return c.do(method, w.FormDataContentType(), &buf)
}

// ---------------------------------------------------------------------------
// keyboards

// CopyText is the copy_text button payload.
type CopyText struct {
	Text string `json:"text"`
}

// Button covers both reply and inline keyboard buttons.
type Button struct {
	Text              string    `json:"text"`
	CallbackData      *string   `json:"callback_data,omitempty"`
	URL               string    `json:"url,omitempty"`
	CopyText          *CopyText `json:"copy_text,omitempty"`
	RequestContact    bool      `json:"request_contact,omitempty"`
	Style             string    `json:"style,omitempty"`
	IconCustomEmojiID string    `json:"icon_custom_emoji_id,omitempty"`
}

// Markup is any reply_markup object; nil means none.
type Markup any

// CB builds an inline button with callback data.
func CB(text, data string) Button { d := data; return Button{Text: text, CallbackData: &d} }

// URLBtn builds an inline URL button.
func URLBtn(text, u string) Button { return Button{Text: text, URL: u} }

// Txt builds a reply keyboard button.
func Txt(text string) Button { return Button{Text: text} }

type InlineKeyboard struct {
	InlineKeyboard [][]Button `json:"inline_keyboard"`
	// a few PHP keyboards carried this on inline markup; Telegram ignores it
	ResizeKeyboard bool `json:"resize_keyboard,omitempty"`
}

type ReplyKeyboard struct {
	Keyboard       [][]Button `json:"keyboard"`
	ResizeKeyboard bool       `json:"resize_keyboard"`
}

// Inline makes an inline keyboard from rows.
func Inline(rows ...[]Button) *InlineKeyboard {
	if rows == nil {
		rows = [][]Button{}
	}
	return &InlineKeyboard{InlineKeyboard: rows}
}

// Reply makes a resized reply keyboard from rows.
func Reply(rows ...[]Button) *ReplyKeyboard {
	if rows == nil {
		rows = [][]Button{}
	}
	return &ReplyKeyboard{Keyboard: rows, ResizeKeyboard: true}
}

// Row is a convenience for one keyboard row.
func Row(b ...Button) []Button { return b }

// ---------------------------------------------------------------------------
// methods

func (c *Client) SendMessage(chatID any, text string, markup Markup, parseMode string) Response {
	return c.Call("sendMessage", map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
		"reply_markup":             markup,
		"parse_mode":               emptyNil(parseMode),
	})
}

func (c *Client) EditMessageText(chatID any, messageID int64, text string, markup Markup) Response {
	return c.Call("editMessageText", map[string]any{
		"chat_id":      chatID,
		"message_id":   messageID,
		"text":         text,
		"reply_markup": markup,
		"parse_mode":   "html",
	})
}

func (c *Client) EditMessageCaption(chatID any, messageID int64, caption string, markup Markup) Response {
	return c.Call("editMessageCaption", map[string]any{
		"chat_id":      chatID,
		"message_id":   messageID,
		"caption":      caption,
		"reply_markup": markup,
	})
}

func (c *Client) DeleteMessage(chatID any, messageID int64) Response {
	return c.Call("deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID})
}

func (c *Client) ForwardMessage(fromChat any, messageID int64, toChat any) Response {
	return c.Call("forwardMessage", map[string]any{"from_chat_id": fromChat, "message_id": messageID, "chat_id": toChat})
}

// SendPhotoID sends an existing photo by file_id/URL.
func (c *Client) SendPhotoID(chatID any, photo, caption string, markup Markup, parseMode string) Response {
	return c.Call("sendPhoto", map[string]any{
		"chat_id":      chatID,
		"photo":        photo,
		"caption":      caption,
		"reply_markup": markup,
		"parse_mode":   emptyNil(parseMode),
	})
}

// SendPhotoFile uploads PNG bytes.
func (c *Client) SendPhotoFile(chatID any, png []byte, caption string, markup Markup) Response {
	return c.Upload("sendPhoto", map[string]any{
		"chat_id":      chatID,
		"caption":      caption,
		"reply_markup": markup,
		"parse_mode":   "HTML",
	}, "photo", "qr.png", png)
}

func (c *Client) SendVideo(chatID any, video, caption string) Response {
	return c.Call("sendVideo", map[string]any{"chat_id": chatID, "video": video, "caption": caption})
}

func (c *Client) SendDocument(chatID any, name string, data []byte, caption string) Response {
	return c.Upload("sendDocument", map[string]any{"chat_id": chatID, "caption": caption}, "document", name, data)
}

func (c *Client) AnswerCallback(id, text string, alert bool) Response {
	return c.Call("answerCallbackQuery", map[string]any{
		"callback_query_id": id,
		"text":              text,
		"show_alert":        alert,
		"cache_time":        5,
	})
}

// ChatMemberStatus returns the member status, ok=false when the call failed.
func (c *Client) ChatMemberStatus(chat string, userID any) (string, bool) {
	r := c.Call("getChatMember", map[string]any{"chat_id": chat, "user_id": userID})
	if !r.OK {
		return "", false
	}
	var m struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(r.Result, &m)
	return m.Status, true
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

func (c *Client) GetMe() (User, error) {
	r := c.Call("getMe", nil)
	if !r.OK {
		return User{}, fmt.Errorf("getMe: %s", r.Description)
	}
	var u User
	err := json.Unmarshal(r.Result, &u)
	return u, err
}

// DownloadFile fetches a file the bot has seen (getFile + file download).
func (c *Client) DownloadFile(fileID string) ([]byte, string, error) {
	r := c.Call("getFile", map[string]any{"file_id": fileID})
	if !r.OK {
		return nil, "", fmt.Errorf("getFile: %s", r.Description)
	}
	var f struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(r.Result, &f)
	res, err := c.HTTP.Get(c.Base + "/file/bot" + c.Token + "/" + f.FilePath)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 20<<20))
	return b, res.Header.Get("Content-Type"), err
}

func (c *Client) SetWebhook(u, secret string) Response {
	p := map[string]any{"url": u, "max_connections": 40, "allowed_updates": []string{"message", "callback_query"}}
	if secret != "" {
		p["secret_token"] = secret
	}
	return c.Call("setWebhook", p)
}

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Sticker is the part of a sticker (custom emoji) the panel needs.
type Sticker struct {
	FileID        string `json:"file_id"`
	Emoji         string `json:"emoji"`
	CustomEmojiID string `json:"custom_emoji_id"`
	SetName       string `json:"set_name"`
	IsAnimated    bool   `json:"is_animated"`
	IsVideo       bool   `json:"is_video"`
	Thumbnail     *struct {
		FileID string `json:"file_id"`
	} `json:"thumbnail"`
}

// PreviewFileID is a still image of the sticker, if Telegram has one.
func (s Sticker) PreviewFileID() string {
	if s.Thumbnail != nil && s.Thumbnail.FileID != "" {
		return s.Thumbnail.FileID
	}
	if !s.IsAnimated && !s.IsVideo {
		return s.FileID // static stickers are .webp images already
	}
	return ""
}

// GetCustomEmojiStickers resolves custom emoji ids (at most 200).
func (c *Client) GetCustomEmojiStickers(ids []string) ([]Sticker, error) {
	r := c.Call("getCustomEmojiStickers", map[string]any{"custom_emoji_ids": ids})
	if !r.OK {
		return nil, fmt.Errorf("getCustomEmojiStickers: %s", r.Description)
	}
	var out []Sticker
	err := json.Unmarshal(r.Result, &out)
	return out, err
}

// StickerSet is a sticker or custom emoji pack.
type StickerSet struct {
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	StickerType string    `json:"sticker_type"`
	Stickers    []Sticker `json:"stickers"`
}

// GetStickerSet loads a pack by its short name (t.me/addemoji/<name>).
func (c *Client) GetStickerSet(name string) (StickerSet, error) {
	r := c.Call("getStickerSet", map[string]any{"name": name})
	if !r.OK {
		return StickerSet{}, fmt.Errorf("getStickerSet: %s", r.Description)
	}
	var out StickerSet
	err := json.Unmarshal(r.Result, &out)
	return out, err
}
