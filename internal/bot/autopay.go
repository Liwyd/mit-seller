package bot

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

// Automatic confirmation of card-to-card payments (autopaylib.php). Each
// order is quoted base price + 1..300 toman; the companion Android app
// forwards bank SMS and an exact amount match confirms the order through
// DirectPayment, like the admin's approve button.

const (
	autopayOffsetMin = 1
	autopayOffsetMax = 300
	autopayHoldHours = 24
	autopayReuseHour = 24
)

func now() string { return php.DateNow("Y-m-d H:i:s") }

// AutopaySettings is autopay_settings(): the single row, created on demand.
func (b *Bot) AutopaySettings() db.Row {
	r := b.DB.One("SELECT * FROM autopay LIMIT 1")
	if r == nil {
		b.DB.Exec("INSERT INTO autopay (id, status, device_key, last_seen, created_at) VALUES (1, 'off', ?, NULL, ?)", randHex(24), now())
		r = b.DB.One("SELECT * FROM autopay LIMIT 1")
	}
	return r
}

func (b *Bot) AutopayEnabled() bool { return b.AutopaySettings().S("status") == "on" }

// AutopaySet is autopay_set(); a nil value stores NULL.
func (b *Bot) AutopaySet(field string, value any) {
	b.AutopaySettings()
	switch field {
	case "status", "device_key", "last_seen", "device_info":
		b.DB.Exec("UPDATE autopay SET "+field+" = ? WHERE id = 1", value)
	}
}

func autopayDigits(s string) string {
	r := strings.NewReplacer(
		"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
		"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
		"٫", ".", "،", "", ",", "",
	)
	return r.Replace(s)
}

// ParsedSMS is autopay_parse_sms()'s result.
type ParsedSMS struct {
	Direction  string
	Amount     int64
	Candidates []int64
	Card       string
}

var (
	reSpace   = regexp.MustCompile(`\s+`)
	reBalance = regexp.MustCompile(`(?i)(?:مانده|موجودی|balance)[^\d]{0,20}\d+`)
	reDeposit = regexp.MustCompile(`(?:واریز|وار.ز|بستانکار|افزایش|افزايش)[^\d]{0,20}(\d{3,})`)
	reNum3    = regexp.MustCompile(`\d{3,}`)
	reCard    = regexp.MustCompile(`(\d{4})\s*\*{2,}|\*{2,}\s*(\d{4})`)
)

// ParseSMS pulls direction, amount candidates and card tail out of a bank SMS.
func ParseSMS(raw string) ParsedSMS {
	flat := reSpace.ReplaceAllString(autopayDigits(raw), " ")
	out := ParsedSMS{Direction: "unknown", Candidates: []int64{}}
	for _, w := range []string{"برداشت", "بدهکار", "خريد", "خرید", "انتقال به", "کارمزد", "withdraw", "debit", "purchase"} {
		if strings.Contains(flat, w) {
			out.Direction = "out"
			break
		}
	}
	if out.Direction == "unknown" {
		for _, w := range []string{"واریز", "بستانکار", "افزايش", "افزایش", "انتقال از", "deposit", "credit"} {
			if strings.Contains(flat, w) {
				out.Direction = "in"
				break
			}
		}
	}
	hunting := reBalance.ReplaceAllString(flat, " ")
	var amount int64
	if m := reDeposit.FindStringSubmatch(hunting); m != nil {
		amount = php.Intval(m[1])
	}
	if amount == 0 {
		for _, n := range reNum3.FindAllString(hunting, -1) {
			if len(n) >= 14 {
				continue
			}
			if v := php.Intval(n); v > amount {
				amount = v
			}
		}
	}
	if amount > 0 {
		if amount%10 == 0 {
			out.Candidates = append(out.Candidates, amount/10)
		}
		out.Candidates = append(out.Candidates, amount)
	}
	if len(out.Candidates) > 0 {
		out.Amount = out.Candidates[0]
	}
	if m := reCard.FindStringSubmatch(flat); m != nil {
		out.Card = m[1]
		if out.Card == "" {
			out.Card = m[2]
		}
	}
	return out
}

func (b *Bot) autopayReleaseOld() {
	b.DB.Exec("UPDATE autopay_order SET status = 'expired', closed_at = ? WHERE status = 'open' AND created_at < ?",
		now(), php.Date("Y-m-d H:i:s", time.Now().Unix()-autopayHoldHours*3600))
}

// AutopayReserve reserves an amount nobody else is waiting on.
func (b *Bot) AutopayReserve(userID, basePrice string) (int64, bool) {
	base := php.Intval(basePrice)
	if base <= 0 {
		return 0, false
	}
	b.autopayReleaseOld()
	taken := map[int64]bool{}
	for _, r := range b.DB.MustQuery("SELECT amount FROM autopay_order WHERE status = 'open'") {
		taken[r.I("amount")] = true
	}
	for _, r := range b.DB.MustQuery("SELECT amount FROM autopay_order WHERE created_at > ?", php.Date("Y-m-d H:i:s", time.Now().Unix()-autopayReuseHour*3600)) {
		taken[r.I("amount")] = true
	}
	offsets := make([]int64, 0, autopayOffsetMax)
	for o := int64(autopayOffsetMin); o <= autopayOffsetMax; o++ {
		offsets = append(offsets, o)
	}
	rand.Shuffle(len(offsets), func(i, j int) { offsets[i], offsets[j] = offsets[j], offsets[i] })
	var chosen int64
	for _, o := range offsets {
		if !taken[base+o] {
			chosen = o
			break
		}
	}
	if chosen == 0 {
		for _, o := range offsets {
			if b.DB.Count("SELECT COUNT(*) FROM autopay_order WHERE amount = ? AND status = 'open'", base+o) == 0 {
				chosen = o
				break
			}
		}
	}
	if chosen == 0 {
		return 0, false
	}
	b.DB.Exec("INSERT INTO autopay_order (id_user, base_price, amount, status, id_order, created_at) VALUES (?, ?, ?, 'open', '', ?)", userID, base, base+chosen, now())
	return base + chosen, true
}

func (b *Bot) AutopayOpenOrder(userID string) db.Row {
	return b.DB.One("SELECT * FROM autopay_order WHERE id_user = ? AND status = 'open' ORDER BY id DESC LIMIT 1", userID)
}

func (b *Bot) AutopayAttach(id, order string) {
	b.DB.Exec("UPDATE autopay_order SET id_order = ? WHERE id = ?", order, id)
}

// AutopayCloseByOrder takes a manually handled payment out of the phone's reach.
func (b *Bot) AutopayCloseByOrder(order, status string) {
	b.DB.Exec("UPDATE autopay_order SET status = ?, closed_at = ? WHERE id_order = ? AND status = 'open'", status, now(), order)
}

// autopayConfirm confirms a matched order (with or without a receipt).
func (b *Bot) autopayConfirm(order db.Row, smsID int64) (map[string]any, bool) {
	d := b.DB
	idOrder := order.S("id_order")
	if idOrder == "" {
		idOrder = randHex(5)
		// Paid without sending a receipt. If the customer was in the middle
		// of buying (not just topping up), finish that purchase, as the
		// receipt would have.
		tag := "0|0"
		if u := d.Select("user", "*", "id", order.S("id_user")); u.S("Processing_value_tow") == "getconfigafterpay" {
			tag = "getconfigafterpay|" + u.S("Processing_value_one")
		}
		d.Exec("INSERT INTO Payment_report (id_user, id_order, time, price, payment_Status, Payment_Method, invoice) VALUES (?, ?, ?, ?, 'waiting', 'cart to cart', ?)",
			order.S("id_user"), idOrder, php.DateNow("Y/m/d H:i:s"), order.S("amount"), tag)
		d.Exec("UPDATE autopay_order SET id_order = ? WHERE id = ?", idOrder, order.S("id"))
	}
	handled := true
	b.lockOrder(idOrder, func() {
		rep := d.Select("Payment_report", "*", "id_order", idOrder)
		if rep == nil || rep.S("payment_Status") == "paid" || rep.S("payment_Status") == "reject" {
			return
		}
		handled = false
		b.DirectPayment(idOrder, nil)
		// DirectPayment marks plain top-ups paid; a purchase-after-payment is
		// marked here, the way the admin's approve button did.
		d.Update("Payment_report", "payment_Status", "paid", "id_order", idOrder)
		d.Update("user", "Processing_value", "0", "id", order.S("id_user"))
		d.Update("user", "Processing_value_one", "0", "id", order.S("id_user"))
		d.Update("user", "Processing_value_tow", "0", "id", order.S("id_user"))
	})
	if handled {
		d.Exec("UPDATE autopay_order SET status = 'manual', closed_at = ? WHERE id = ?", now(), order.S("id"))
		return map[string]any{"reason": "already handled"}, false
	}
	d.Exec("UPDATE autopay_order SET status = 'paid', closed_at = ?, sms_id = ? WHERE id = ?", now(), smsID, order.S("id"))
	d.Exec("UPDATE autopay_sms SET status = 'matched', id_order = ? WHERE id = ?", idOrder, smsID)
	return map[string]any{"id_order": idOrder, "id_user": order.S("id_user"), "amount": order.I("amount")}, true
}

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// AutopayHandleSMS stores one forwarded SMS and tries to match it.
func (b *Bot) AutopayHandleSMS(raw, sender, sentAt string) map[string]any {
	d := b.DB
	sum := sha256.Sum256([]byte(strings.TrimSpace(sender) + "|" + strings.TrimSpace(raw) + "|" + strings.TrimSpace(sentAt)))
	hash := hex.EncodeToString(sum[:])
	if r := d.One("SELECT * FROM autopay_sms WHERE hash = ? LIMIT 1", hash); r != nil {
		return map[string]any{"result": "duplicate", "status": r.S("status")}
	}
	p := ParseSMS(raw)
	res, err := d.Exec("INSERT INTO autopay_sms (hash, sender, body, amount, direction, card, sent_at, received_at, status, id_order) VALUES (?,?,?,?,?,?,?,?,?,'')",
		hash, sender, trunc(raw, 900), p.Amount, p.Direction, p.Card, sentAt, now(), "new")
	if err != nil {
		// a concurrent copy of the same SMS won the unique hash
		return map[string]any{"result": "duplicate", "status": "new"}
	}
	smsID, _ := res.LastInsertId()
	if p.Direction != "in" {
		d.Exec("UPDATE autopay_sms SET status = 'ignored' WHERE id = ?", smsID)
		return map[string]any{"result": "ignored", "reason": "not a deposit", "amount": p.Amount}
	}
	if p.Amount <= 0 {
		d.Exec("UPDATE autopay_sms SET status = 'unreadable' WHERE id = ?", smsID)
		return map[string]any{"result": "unreadable"}
	}
	b.autopayReleaseOld()
	var order db.Row
	for _, cand := range p.Candidates {
		order = d.One("SELECT * FROM autopay_order WHERE amount = ? AND status = 'open' ORDER BY id ASC LIMIT 1", cand)
		if order != nil {
			d.Exec("UPDATE autopay_sms SET amount = ? WHERE id = ?", cand, smsID)
			break
		}
	}
	if order == nil {
		d.Exec("UPDATE autopay_sms SET status = 'unmatched' WHERE id = ?", smsID)
		b.autopayTellUnmatched(p.Amount)
		return map[string]any{"result": "unmatched", "amount": p.Amount}
	}
	done, ok := b.autopayConfirm(order, smsID)
	if !ok {
		d.Exec("UPDATE autopay_sms SET status = 'skipped' WHERE id = ?", smsID)
		return map[string]any{"result": "skipped", "reason": done["reason"]}
	}
	b.autopayTellPaid(done)
	return map[string]any{"result": "paid", "id_order": done["id_order"], "amount": done["amount"]}
}

func (b *Bot) autopayTellPaid(done map[string]any) {
	amount, _ := done["amount"].(int64)
	msg := "✅ <b>پرداخت خودکار تأیید شد</b>\n\n" +
		"کاربر: <code>" + done["id_user"].(string) + "</code>\n" +
		"مبلغ: " + nf(float64(amount)) + " تومان\n" +
		"کد پیگیری: <code>" + done["id_order"].(string) + "</code>"
	for _, a := range b.DB.AdminIDs() {
		b.TG.SendMessage(a, msg, nil, "HTML")
	}
}

func (b *Bot) autopayTellUnmatched(amount int64) {
	var waiting string
	for _, r := range b.DB.MustQuery("SELECT id_user, amount FROM autopay_order WHERE status = 'open' ORDER BY id DESC LIMIT 5") {
		waiting += "\n• " + nf(r.F("amount")) + " تومان — کاربر <code>" + r.S("id_user") + "</code>"
	}
	msg := "⚠️ <b>واریزی که با هیچ پرداختِ در انتظاری جور نشد</b>\n\n" +
		"مبلغ واریزشده: " + nf(float64(amount)) + " تومان\n\n"
	if waiting == "" {
		msg += "در حال حاضر هیچ پرداختی در انتظار واریز نیست؛ یعنی یا این واریز شخصی بوده، یا قبلاً همان پرداخت تأیید شده است."
	} else {
		msg += "پرداخت‌هایی که منتظرشانیم:" + waiting + "\n\nاگر مشتری مبلغ را رُند کرده یا اشتباه زده، دستی تأییدش کن."
	}
	for _, a := range b.DB.AdminIDs() {
		b.TG.SendMessage(a, msg, nil, "HTML")
	}
}

// AutopayPriceNote is what the customer is told to pay.
func AutopayPriceNote(amount int64) string {
	return "\n\n⚠️ مبلغ را <b>دقیقاً</b> " + nf(float64(amount)) + " تومان واریز کنید — نه کمتر، نه بیشتر." +
		"\nاین عدد مخصوص سفارش شماست و پرداخت با همین عدد شناسایی می‌شود." +
		"\n\n🤖 واریزی شما به‌صورت هوشمند و خودکار بررسی و ظرف چند ثانیه تأیید می‌شود؛" +
		" <b>نیازی به فرستادن رسید نیست</b>." +
		"\nاگر تا ۱۵ دقیقه بعد از واریز تأیید نشد، به پشتیبانی پیام بدهید."
}

// AutopayPairing is the "NXP1." string the Android app is paired with.
func (b *Bot) AutopayPairing() string {
	s := b.AutopaySettings()
	return PairingString("https://"+b.Cfg.Domain+"/autopay.php", s.S("device_key"))
}

// PairingString is 'NXP1.' + unpadded base64url of {"u":..,"k":..}.
func PairingString(u, key string) string {
	j := `{"u":"` + strings.ReplaceAll(u, "/", `\/`) + `","k":"` + key + `"}`
	enc := base64.URLEncoding.EncodeToString([]byte(j))
	return "NXP1." + strings.TrimRight(enc, "=")
}

var _ = strconv.Itoa
