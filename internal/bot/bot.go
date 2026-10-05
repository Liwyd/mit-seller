// Package bot is the Go version of index.php + admin.php: one Ctx per
// Telegram update, holding what the PHP script kept in globals, and handlers
// that run in the same order as the PHP file so behaviour is unchanged.
package bot

import (
	"fmt"
	"log"
	"regexp"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/Liwyd/mit-seller/internal/config"
	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/text"
	"github.com/Liwyd/mit-seller/internal/tg"
)

// Version is shown on the admin login message ($version in PHP).
var Version = "7.0.0-go"

type Bot struct {
	Cfg *config.Config
	DB  *db.DB
	TG  *tg.Client
	PM  *panels.Manager
	Log *log.Logger

	labels labelCache // button styles by label (label_styles.go)

	locks sync.Map // per-user mutex: updates of one user run one at a time
	queue chan *tg.Update
	wg    sync.WaitGroup
}

func New(cfg *config.Config, d *db.DB, t *tg.Client, logger *log.Logger) *Bot {
	b := &Bot{Cfg: cfg, DB: d, TG: t, PM: panels.New(d), Log: logger}
	if t != nil {
		t.Decorate = b.decorateMarkup
	}
	return b
}

// T is $textbotlang lookup.
func T(key string) string { return text.T(key) }

// Start launches the update workers.
func (b *Bot) Start() {
	b.queue = make(chan *tg.Update, 4096)
	for i := 0; i < b.Cfg.Workers; i++ {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			for u := range b.queue {
				b.Handle(u)
			}
		}()
	}
}

// Enqueue hands an update to the workers (the HTTP handler answers Telegram
// right away, as fastcgi_finish_request() did).
func (b *Bot) Enqueue(u *tg.Update) {
	select {
	case b.queue <- u:
	default:
		go b.Handle(u)
	}
}

// Stop waits for in-flight updates to finish.
func (b *Bot) Stop() {
	if b.queue != nil {
		close(b.queue)
		b.wg.Wait()
	}
}

func (b *Bot) userLock(id int64) *sync.Mutex {
	v, _ := b.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Handle processes one update synchronously.
func (b *Bot) Handle(u *tg.Update) {
	f := u.Flatten()
	if f.FromID != 0 {
		l := b.userLock(f.FromID)
		l.Lock()
		defer l.Unlock()
	}
	defer func() {
		if r := recover(); r != nil {
			b.Log.Printf("panic handling update %d: %v\n%s", u.UpdateID, r, debug.Stack())
		}
	}()
	c := b.newCtx(f)
	c.run()
}

// Ctx is one update being processed.
type Ctx struct {
	b *Bot
	f tg.Fields

	fromID          string
	chatType        string
	text            string
	textCallback    string
	messageID       int64
	photo           bool
	photoID         string
	caption         string
	video           bool
	videoID         string
	datain          string
	username        string
	userPhone       string
	contactID       string
	firstName       string
	callbackQueryID string

	user      db.Row // $user
	usersStep string // $users['step'] when keyboard.php ran
	setting   db.Row
	adminIDs  []string
	isAdmin   bool
	texts     map[string]string // $datatextbot
	channels  db.Row
	textIn    string // $text as it arrived (keyboard.php used it)

	// $dataget from the last matching preg_match
	dataget []string
}

func (b *Bot) newCtx(f tg.Fields) *Ctx {
	c := &Ctx{
		b: b, f: f,
		fromID:          strconv.FormatInt(f.FromID, 10),
		chatType:        f.ChatType,
		text:            f.Text,
		textCallback:    f.TextCallback,
		messageID:       f.MessageID,
		photo:           f.HasPhoto,
		photoID:         f.PhotoID,
		caption:         f.Caption,
		video:           f.HasVideo,
		videoID:         f.VideoID,
		datain:          f.Datain,
		username:        f.Username,
		userPhone:       f.UserPhone,
		firstName:       f.FirstName,
		callbackQueryID: f.CallbackQueryID,
	}
	if f.ContactID != 0 {
		c.contactID = strconv.FormatInt(f.ContactID, 10)
	} else {
		c.contactID = "0"
	}
	c.textIn = c.text
	return c
}

func (c *Ctx) db() *db.DB { return c.b.DB }

// --------------------------------------------------------------------------
// Telegram helpers named after the PHP ones

func (c *Ctx) send(chat any, txt string, markup tg.Markup, parse string) tg.Response {
	return c.b.TG.SendMessage(chat, txt, markup, parse)
}

func (c *Ctx) sendHTML(chat any, txt string, markup tg.Markup) tg.Response {
	return c.b.TG.SendMessage(chat, txt, markup, "HTML")
}

func (c *Ctx) edit(txt string, markup tg.Markup) {
	c.b.TG.EditMessageText(c.fromID, c.messageID, txt, markup)
}

func (c *Ctx) del() { c.b.TG.DeleteMessage(c.fromID, c.messageID) }

func (c *Ctx) alert(txt string) { c.b.TG.AnswerCallback(c.callbackQueryID, txt, true) }

func (c *Ctx) step(s string) { c.db().Step(s, c.fromID) }

func (c *Ctx) stepOf(id, s string) { c.db().Step(s, id) }

func (c *Ctx) upd(table, field string, v any, wf string, wv any) {
	c.db().Update(table, field, v, wf, wv)
}

func (c *Ctx) setUser(field string, v any) { c.db().Update("user", field, v, "id", c.fromID) }

func (c *Ctx) report(txt string) {
	if ch := c.setting.S("Channel_Report"); ch != "" {
		c.sendHTML(ch, txt, nil)
	}
}

// match is preg_match($re, $datain, $dataget) on callback data. Callback data
// written before the rebrand is retried under its current spelling, so a
// button on an old message still works; the data as it arrived is always
// tried first, so a value the admin typed is never rewritten.
func (c *Ctx) match(re *regexp.Regexp) bool {
	if m := re.FindStringSubmatch(c.datain); m != nil {
		c.dataget = m
		return true
	}
	if alt := preRebrand(c.datain); alt != c.datain {
		if m := re.FindStringSubmatch(alt); m != nil {
			c.dataget = m
			return true
		}
	}
	return false
}

func (c *Ctx) g(i int) string {
	if i < len(c.dataget) {
		return c.dataget[i]
	}
	return ""
}

var reCache sync.Map

// re compiles (and caches) a pattern.
func re(p string) *regexp.Regexp {
	if v, ok := reCache.Load(p); ok {
		return v.(*regexp.Regexp)
	}
	r := regexp.MustCompile(p)
	reCache.Store(p, r)
	return r
}

func (c *Ctx) m(p string) bool { return c.match(re(p)) }

func (c *Ctx) stepIs(s string) bool { return c.user.S("step") == s }

func (c *Ctx) isAdminID(id string) bool {
	for _, a := range c.adminIDs {
		if php.LooseEq(a, id) {
			return true
		}
	}
	return false
}

// inColumn is in_array($v, select($table,$col,...,"FETCH_COLUMN")): exact
// (PHP-loose) comparison in Go, independent of the column's SQL collation.
func (c *Ctx) inColumn(table, col, v string) bool {
	for _, x := range c.db().Column(table, col) {
		if php.LooseEq(x, v) {
			return true
		}
	}
	return false
}

func (c *Ctx) logf(format string, a ...any) { c.b.Log.Printf(format, a...) }

func nowUnix() int64 { return time.Now().Unix() }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func nf(f float64) string { return php.NumberFormat(f, 0) }

func nfs(s string) string { return php.NumberFormatS(s) }

func sprintf(key string, a ...any) string { return php.Sprintf(T(key), a...) }

var _ = fmt.Sprint
