package bot

import (
	"encoding/json"
	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"math"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

// The PHP bot ran its periodic jobs from crontab (curl .../cron/*.php). They
// run inside the Go process instead, with the same intervals and on/off
// switches (stored in nexra_kv).

// RunCrons starts the schedulers; stop closes them.
func (b *Bot) RunCrons(stop <-chan struct{}) {
	every := func(d time.Duration, name string, fn func()) {
		go func() {
			t := time.NewTicker(d)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					b.safe(name, fn)
				}
			}
		}()
	}
	every(time.Minute, "sendmessage", b.cronSendMessage)
	every(time.Minute, "cronvolume", func() {
		if b.CronOn("volume") {
			b.cronVolume()
		}
	})
	every(time.Minute, "cronday", func() {
		if b.CronOn("time") {
			b.cronDay()
		}
	})
	every(time.Minute, "removeexpire", func() {
		if b.CronOn("remove") {
			b.cronRemoveExpire()
		}
	})
	every(15*time.Minute, "configtest", func() {
		if b.CronOn("test") {
			b.cronConfigTest()
		}
	})
	// "auto-confirm without review": every receipt about a minute after it
	// arrives (the PHP crontab ran this every 4 minutes)
	every(20*time.Second, "croncard", func() {
		if b.CronOn("card") {
			b.cronCard()
		}
	})
}

func (b *Bot) safe(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			b.Log.Printf("cron %s panic: %v", name, r)
		}
	}()
	fn()
}

// cronConfigTest removes expired trial accounts (cron/configtest.php).
func (b *Bot) cronConfigTest() {
	d := b.DB
	for _, r := range d.MustQuery("SELECT * FROM invoice WHERE status = 'active' AND name_product = 'usertest' LIMIT 10") {
		username := trimSpace(r.S("username"))
		if d.Select("marzban_panel", "*", "name_panel", r.S("Service_location")) == nil {
			continue
		}
		st := b.PM.DataUser(r.S("Service_location"), r.S("username")).S("status")
		switch st {
		case "active", "on_hold", "Unsuccessful", "disabled":
			continue
		}
		b.PM.RemoveUser(r.S("Service_location"), username)
		d.Update("invoice", "status", "disabled", "username", username)
		k := ik(row(cb(T("users.cron.textbuy"), "buy")))
		b.TG.SendMessage(r.S("id_user"), sprintf("users.cron.crontest", username), k, "HTML")
	}
}

// cronDay warns about services that expire within ~2 days (cron/cronday.php).
func (b *Bot) cronDay() {
	d := b.DB
	for _, r := range d.MustQuery("SELECT * FROM invoice WHERE (status = 'active' OR status = 'end_of_volume') AND name_product != 'usertest' ORDER BY RAND() LIMIT 5") {
		if d.Select("marzban_panel", "*", "name_panel", r.S("Service_location")) == nil {
			continue
		}
		out := b.PM.DataUser(r.S("Service_location"), r.S("username"))
		st := out.S("status")
		if st == "Unsuccessful" || (st != "active" && st != "on_hold") {
			continue
		}
		left := out.F("expire") - float64(time.Now().Unix())
		day := math.Floor(left/86400) + 1
		service := d.Select("textbot", "text", "id_text", "text_Purchased_services").S("text")
		k := ik(row(cb(T("users.extend.title"), "extend_"+r.S("username"))))
		if left <= 167000 && left > 0 {
			b.TG.SendMessage(r.S("id_user"), sprintf("users.cron.cronday", r.S("username"), php.FloatToString(day), service), k, "HTML")
			if r.S("Status") == "end_of_volume" {
				d.Update("invoice", "Status", "sendedwarn", "username", r.S("username"))
			} else {
				d.Update("invoice", "Status", "end_of_time", "username", r.S("username"))
			}
		}
	}
}

// cronVolume warns when less than 1 GB is left (cron/cronvolume.php).
func (b *Bot) cronVolume() {
	d := b.DB
	for _, r := range d.MustQuery("SELECT * FROM invoice WHERE (status = 'active' OR status = 'end_of_time') AND name_product != 'usertest' ORDER BY RAND() LIMIT 5") {
		line := trimSpace(r.S("username"))
		out := b.PM.DataUser(r.S("Service_location"), r.S("username"))
		if out == nil || out.S("status") == "Unsuccessful" {
			continue
		}
		left := out.F("data_limit") - out.F("used_traffic")
		service := d.Select("textbot", "text", "id_text", "text_Purchased_services").S("text")
		k := ik(row(cb(T("users.extend.title"), "extend_"+r.S("username"))))
		if left <= math.Pow(1024, 3) && left > 0 && out.S("status") == "active" {
			b.TG.SendMessage(r.S("id_user"), sprintf("users.cron.cronvolume", line, formatBytes(left), service), k, "HTML")
			if r.S("Status") == "end_of_time" {
				d.Update("invoice", "Status", "sendedwarn", "username", line)
			} else {
				d.Update("invoice", "Status", "end_of_volume", "username", line)
			}
		}
	}
}

// cronRemoveExpire deletes services expired/limited for removedayc days.
func (b *Bot) cronRemoveExpire() {
	d := b.DB
	setting := d.Setting()
	for _, r := range d.MustQuery("SELECT * FROM invoice WHERE (status = 'active' OR status = 'end_of_time' OR status = 'end_of_volume' OR status = 'sendedwarn') AND name_product != 'usertest' ORDER BY RAND() LIMIT 10") {
		line := trimSpace(r.S("username"))
		if d.Select("marzban_panel", "*", "name_panel", r.S("Service_location")) == nil {
			continue
		}
		out := b.PM.DataUser(r.S("Service_location"), r.S("username"))
		st := out.S("status")
		if st != "limited" && st != "expired" {
			continue
		}
		day := math.Floor((out.F("expire") - float64(time.Now().Unix())) / 86400)
		if day <= -float64(php.Intval(setting.S("removedayc"))) {
			b.TG.SendMessage(r.S("id_user"), sprintf("users.cron.removeexpire", r.S("username")), nil, "HTML")
			d.Update("invoice", "status", "removeTime", "username", line)
			b.PM.RemoveUser(r.S("Service_location"), line)
			if ch := setting.S("Channel_Report"); ch != "" {
				b.TG.SendMessage(ch, sprintf("Admin.Report.reportremovecron", line, statusLabel(st)), nil, "HTML")
			}
		}
	}
}

// cronCard is "automatic confirmation without review": a card-to-card
// receipt still waiting a minute after it was sent (and at most an hour) is
// accepted, and the bot's admins are told to look at it themselves. When the
// SMS check (autopay) is on it decides instead, so this stands aside.
const cardAutoDelay = 60 // seconds after the receipt

func (b *Bot) cronCard() {
	d := b.DB
	if b.AutopaySettings().S("status") == "on" {
		return
	}
	setting := d.Setting()
	for _, r := range d.MustQuery("SELECT * FROM Payment_report WHERE payment_Status = 'waiting' AND Payment_Method = 'cart to cart'") {
		ts, ok := php.Strtotime(r.S("time"))
		age := time.Now().Unix() - ts
		if !ok || age >= 3600 || age < cardAutoDelay {
			continue
		}
		b.lockOrder(r.S("id_order"), func() {
			rep := d.Select("Payment_report", "*", "id_order", r.S("id_order"))
			if rep.S("payment_Status") != "waiting" {
				return
			}
			buyer := d.Select("user", "*", "id", rep.S("id_user"))
			d.Update("Payment_report", "payment_Status", "paid", "id_order", rep.S("id_order"))
			d.Update("Payment_report", "dec_not_confirmed", "Confirmed by robot", "id_order", rep.S("id_order"))
			b.AutopayCloseByOrder(rep.S("id_order"), "manual")
			b.DirectPayment(rep.S("id_order"), nil)
			if ch := setting.S("Channel_Report"); ch != "" {
				b.TG.SendMessage(ch, sprintf("Admin.Report.autocart", buyer.S("id"), rep.S("price")), nil, "HTML")
			}
			b.notifyAutoCard(rep, buyer)
		})
	}
}

// notifyAutoCard tells every admin that a receipt went through unchecked,
// with the receipt photo when the bot kept it.
func (b *Bot) notifyAutoCard(rep, buyer db.Row) {
	uname := buyer.S("username")
	if uname == "" || uname == "NOT_USERNAME" || uname == "none" {
		uname = "—"
	} else {
		uname = "@" + uname
	}
	text := sprintf("Admin.Report.autocartadmin", nfs(rep.S("price")), rep.S("id_user"), uname, rep.S("id_order"), rep.S("time"))
	photo := b.DB.KV("receipt_" + rep.S("id_order"))
	for _, a := range b.DB.AdminIDs() {
		if photo != "" {
			if r := b.TG.SendPhotoID(a, photo, text, nil, "HTML"); r.OK {
				continue
			}
		}
		b.TG.SendMessage(a, text, nil, "HTML")
	}
}

// ---- bulk message (cron/sendmessage.php: 20 users a minute)

// StartBroadcast queues info (JSON {"text":..,"id_admin":..}) for every active user.
func (b *Bot) StartBroadcast(info string) int64 {
	d := b.DB
	d.Exec("DELETE FROM nexra_broadcast")
	d.Exec("INSERT INTO nexra_broadcast (user_id) SELECT id FROM user WHERE User_Status = 'Active'")
	d.SetKV("broadcast_info", info)
	return d.Count("SELECT COUNT(*) FROM nexra_broadcast")
}

func (b *Bot) CancelBroadcast() {
	b.DB.Exec("DELETE FROM nexra_broadcast")
	b.DB.SetKV("broadcast_info", "")
}

// BroadcastPending reports how many recipients are left.
func (b *Bot) BroadcastPending() int64 { return b.DB.Count("SELECT COUNT(*) FROM nexra_broadcast") }

func (b *Bot) cronSendMessage() {
	d := b.DB
	raw := d.KV("broadcast_info")
	if raw == "" {
		return
	}
	var info map[string]any
	_ = json.Unmarshal([]byte(raw), &info)
	text, _ := info["text"].(string)
	rows := d.MustQuery("SELECT id, user_id FROM nexra_broadcast ORDER BY id LIMIT 20")
	if len(rows) == 0 {
		if admin := php.ToString(info["id_admin"]); admin != "" {
			b.TG.SendMessage(admin, T("users.cron.sendedmessage"), nil, "HTML")
		}
		d.SetKV("broadcast_info", "")
		return
	}
	for _, r := range rows {
		b.TG.SendMessage(r.S("user_id"), text, nil, "HTML")
		d.Exec("DELETE FROM nexra_broadcast WHERE id = ?", r.S("id"))
	}
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t' || s[0] == '\r' || s[0] == 0 || s[0] == '\v') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c == ' ' || c == '\n' || c == '\t' || c == '\r' || c == 0 || c == '\v' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}
