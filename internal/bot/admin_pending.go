package bot

import (
	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/tg"
)

// The "unapproved receipts" section: everything a customer paid that the bot
// has not finished crediting, whichever of the three flows it came through.
// A receipt the automatic cron never reached, a receipt whose delivery failed
// and is waiting for a retry, and an application-mode order the phone never
// matched all land here, each with the one button it needs.
//
// Before this existed an application-mode payment that no SMS ever matched
// could only be finished by editing the database by hand.

const pendingCap = 20

// Pending is what is waiting for an admin, shared with the management API so
// the two cannot disagree about it.
type Pending struct {
	Mode      PayMode
	Phone     string
	Receipts  []db.Row
	Orders    []db.Row
	Unmatched []db.Row
}

func (b *Bot) Pending() Pending {
	s := b.AutopaySettings()
	lim := itoa(pendingCap)
	return Pending{
		Mode:      b.PayMode(),
		Phone:     lastSeenLabel(s.S("last_seen")),
		Receipts:  b.DB.MustQuery("SELECT * FROM Payment_report WHERE payment_Status = 'waiting' ORDER BY id DESC LIMIT " + lim),
		Orders:    b.DB.MustQuery("SELECT * FROM autopay_order WHERE status = 'open' ORDER BY id DESC LIMIT " + lim),
		Unmatched: b.DB.MustQuery("SELECT * FROM autopay_sms WHERE status = 'unmatched' ORDER BY id DESC LIMIT " + lim),
	}
}

func (p Pending) Empty() bool {
	return len(p.Receipts) == 0 && len(p.Orders) == 0 && len(p.Unmatched) == 0
}

// ageLabel renders how long ago something happened, for an admin scanning a
// list.
func ageLabel(ts string) string {
	tsUnix, ok := php.Strtotime(ts)
	if !ok {
		return "—"
	}
	switch d := nowUnix() - tsUnix; {
	case d < 60:
		return "چند لحظه پیش"
	case d < 3600:
		return itoa(d/60) + " دقیقه پیش"
	case d < 86400:
		return itoa(d/3600) + " ساعت پیش"
	default:
		return itoa(d/86400) + " روز پیش"
	}
}

func (b *Bot) pendingText() string { return b.pendingTextOf(b.Pending()) }

func (b *Bot) pendingTextOf(p Pending) string {
	t := sprintf("Admin.pending.mode", p.Mode.Label()) + "\n"
	if p.Mode == ModeApp {
		t += sprintf("Admin.pending.phone", p.Phone) + "\n"
	}
	t += "\n📷 رسید در انتظار: " + itoa(int64(len(p.Receipts))) +
		"\n🤖 پرداخت برنامه در انتظار: " + itoa(int64(len(p.Orders))) +
		"\n⚠️ واریز جورنشده: " + itoa(int64(len(p.Unmatched)))
	if p.Empty() {
		return t + "\n\n" + T("Admin.pending.empty")
	}
	return t
}

// sendPendingItems sends each group that has something, so the admin sees
// the receipt itself rather than only a row number.
func (b *Bot) sendPendingItems(c *Ctx, p Pending) {
	if len(p.Receipts) > 0 {
		b.TG.SendMessage(c.fromID, T("Admin.pending.receipts_title"), nil, "HTML")
	}
	for i, r := range p.Receipts {
		if i >= pendingPhotoCap {
			break
		}
		b.sendPendingReceipt(c, r)
	}
	if len(p.Orders) > 0 {
		b.TG.SendMessage(c.fromID, b.pendingOrdersText(p.Orders), b.pendingOrdersKeyboard(p.Orders), "HTML")
	}
	if len(p.Unmatched) > 0 {
		b.TG.SendMessage(c.fromID, b.pendingUnmatchedText(p.Unmatched), b.pendingUnmatchedKeyboard(p.Unmatched), "HTML")
	}
}

// pendingPhotoCap is how many receipt photos go out in one sitting; the rest
// are still in the list below and in the API.
const pendingPhotoCap = 10

func (b *Bot) sendPendingReceipt(c *Ctx, r db.Row) {
	k := ik(row(cb(T("users.Balance.Confirmpaying"), "Confirm_pay_"+r.S("id_order")),
		cb(T("users.Balance.reject_pay"), "reject_pay_"+r.S("id_order"))))
	cap := sprintf("Admin.pending.receipt", r.S("id_user"), nfs(r.S("price")), r.S("id_order"), ageLabel(r.S("time")))
	if photo := b.DB.KV("receipt_" + r.S("id_order")); photo != "" {
		if res := b.TG.SendPhotoID(c.fromID, photo, cap, k, "HTML"); res.OK {
			return
		}
	}
	b.TG.SendMessage(c.fromID, cap, k, "HTML")
}

func (b *Bot) pendingOrdersText(rows []db.Row) string {
	t := T("Admin.pending.orders_title") + "\n"
	for _, r := range rows {
		t += "\n" + sprintf("Admin.pending.order", r.S("id_user"), nf(r.F("amount")), nf(r.F("base_price")),
			r.S("id_order"), ageLabel(r.S("created_at"))) + "\n"
	}
	return t
}

func (b *Bot) pendingOrdersKeyboard(rows []db.Row) *tg.InlineKeyboard {
	var rowsK [][]B
	for _, r := range rows {
		rowsK = append(rowsK, row(
			cb(T("Admin.pending.approve_order"), "autopay_approve_"+r.S("id")),
			cb(T("Admin.pending.cancel_order"), "autopay_cancel_"+r.S("id"))))
	}
	rowsK = append(rowsK, row(cb(T("Admin.pending.button"), "pending_home")))
	return ik(rowsK...)
}

func (b *Bot) pendingUnmatchedText(rows []db.Row) string {
	t := T("Admin.pending.unmatched_title") + "\n"
	for _, r := range rows {
		t += "\n" + sprintf("Admin.pending.unmatched", nf(r.F("amount")), ageLabel(r.S("sent_at")), r.S("id")) + "\n"
	}
	return t
}

func (b *Bot) pendingUnmatchedKeyboard(rows []db.Row) *tg.InlineKeyboard {
	var rowsK [][]B
	for _, r := range rows {
		rowsK = append(rowsK, row(cb(T("Admin.pending.match"), "autopay_match_"+r.S("id"))))
	}
	return ik(rowsK...)
}

// matchPickerKeyboard lists the open orders a deposit can be bound to.
func (b *Bot) matchPickerKeyboard(smsID string) *tg.InlineKeyboard {
	var rowsK [][]B
	for _, r := range b.AutopayOpenOrdersList() {
		label := nf(r.F("amount")) + " — " + r.S("id_user")
		rowsK = append(rowsK, row(cb(label, "autopay_bind_"+smsID+"_"+r.S("id"))))
	}
	rowsK = append(rowsK, row(cb(T("Admin.pending.button"), "pending_home")))
	return ik(rowsK...)
}

func (b *Bot) matchPickerText(smsID string) string {
	sms := b.DB.Select("autopay_sms", "*", "id", smsID)
	if sms == nil || sms.S("status") != "unmatched" {
		return T("Admin.pending.empty")
	}
	return "🧩 <b>واریز " + nf(sms.F("amount")) + " تومان</b>\n\n" +
		"پرداختی که با آن جور می‌شود را انتخاب کن:"
}

func (c *Ctx) admPending() bool {
	b := c.b
	if c.text == T("Admin.pending.button") {
		p := b.Pending()
		c.sendHTML(c.fromID, b.pendingTextOf(p), nil)
		b.sendPendingItems(c, p)
		c.step("home")
		return true
	}
	switch {
	case c.datain == "pending_home":
		c.edit(b.pendingText(), nil)
	case c.m(`^autopay_approve_(\d+)$`):
		switch b.ApproveAutopayOrder(c.g(1)) {
		case PaymentApplied:
			c.alert(T("Admin.pending.approve_confirm"))
		case PaymentFailed:
			c.alert(T("Admin.Payment.deliverfailed"))
		default:
			c.alert(T("Admin.Payment.reviewedpayment"))
		}
		c.edit(b.pendingText(), nil)
	case c.m(`^autopay_cancel_(\d+)$`):
		if b.CancelAutopayOrder(c.g(1), T("Admin.pending.cancel_reason")) {
			c.alert(T("Admin.pending.cancel_confirm"))
		} else {
			c.alert(T("Admin.Payment.reviewedpayment"))
		}
		c.edit(b.pendingText(), nil)
	case c.m(`^autopay_match_(\d+)$`):
		c.edit(b.matchPickerText(c.g(1)), b.matchPickerKeyboard(c.g(1)))
	case c.m(`^autopay_bind_(\d+)_(\d+)$`):
		done, matched := b.MatchUnmatchedSMS(c.g(1), c.g(2))
		switch {
		case matched:
			c.alert(T("Admin.pending.match_confirm"))
		case done != nil && done["reason"] == AutopayReasonDeliverFail:
			c.alert(T("Admin.Payment.deliverfailed"))
		default: // the deposit or the order was taken by someone else first
			c.alert(T("Admin.Payment.reviewedpayment"))
		}
		c.edit(b.pendingText(), nil)
	}
	return false
}
