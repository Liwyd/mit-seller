package bot

import (
	"github.com/Liwyd/mit-seller/internal/php"
)

// How a card-to-card payment gets confirmed. Exactly one is in effect at a
// time and it decides two things together: what the customer is told about
// the receipt, and which state the bot waits in afterwards. Splitting those
// apart is what produced messages that asked for a receipt in one sentence
// and said no receipt was needed in the next.
type PayMode string

const (
	// ModeManual: an admin approves each receipt from the bot.
	ModeManual PayMode = "manual"
	// ModeAuto: every receipt is approved about a minute later, unchecked.
	ModeAuto PayMode = "auto"
	// ModeApp: the phone app forwards the bank SMS and the exact amount
	// confirms the payment; no receipt is needed.
	ModeApp PayMode = "application"
)

// autopayStaleAfter is how long the phone may go quiet before it counts as
// disconnected. Quoting a unique amount while it is away would leave the
// customer with a payment that no receipt and no SMS can confirm, so the
// mode falls back to manual instead.
const autopayStaleAfter = 12 * 3600

func (m PayMode) Label() string {
	switch m {
	case ModeApp:
		return "برنامه (تأیید خودکار با پیامک)"
	case ModeAuto:
		return "خودکار (تأیید بدون بازبینی)"
	}
	return "دستی (تأیید توسط ادمین)"
}

// appConnected reports whether the phone app is on and has pinged recently.
func appConnected(lastSeen string, now int64) bool {
	ts, ok := php.Strtotime(lastSeen)
	if !ok || ts <= 0 {
		return false
	}
	ago := now - ts
	// last_seen is stamped by this very process from this process's clock,
	// so a ping in the same second we are asking must count as connected
	return ago >= 0 && ago <= autopayStaleAfter
}

// payModeFor picks the mode from the raw switches. The application only
// counts when it is on *and* the phone is reachable; a phone that never
// connected, or that stopped forwarding, must not be allowed to promise an
// automatic confirmation nobody can deliver.
func payModeFor(status, lastSeen string, cronCard bool, now int64) PayMode {
	if status == "on" && appConnected(lastSeen, now) {
		return ModeApp
	}
	if cronCard {
		return ModeAuto
	}
	return ModeManual
}

func (b *Bot) PayMode() PayMode {
	s := b.AutopaySettings()
	return payModeFor(s.S("status"), s.S("last_seen"), b.CronOn("card"), nowUnix())
}

// AppConnected is the single question "can the phone confirm a payment
// right now?", answered for the admin panel and the API.
func (b *Bot) AppConnected() bool {
	s := b.AutopaySettings()
	return s.S("status") == "on" && appConnected(s.S("last_seen"), nowUnix())
}

// lastSeenLabel renders the phone's last ping for an admin.
func lastSeenLabel(lastSeen string) string {
	ts, ok := php.Strtotime(lastSeen)
	if !ok || ts <= 0 {
		return "هرگز ⛔️"
	}
	switch ago := nowUnix() - ts; {
	case ago < 0:
		return "—"
	case ago < 120:
		return "همین الان ✅"
	case ago < 3600:
		if ago > 900 {
			return itoa(ago/60) + " دقیقه پیش ⚠️"
		}
		return itoa(ago/60) + " دقیقه پیش ✅"
	case ago < 86400:
		return itoa(ago/3600) + " ساعت پیش ⚠️"
	default:
		return itoa(ago/86400) + " روز پیش ⚠️"
	}
}

// cardMessage builds what the customer is told about a card-to-card payment.
// The mode picks the instruction and only that instruction: manual and
// automatic both need the receipt, application mode does not, and no mode
// ever receives both sentences.
func cardMessage(mode PayMode, amount int64, card string) string {
	head := sprintf("users.moeny.carttext_head", nf(float64(amount)), card)
	var mid string
	switch mode {
	case ModeApp:
		mid = sprintf("users.moeny.carttext_app", nf(float64(amount)))
	case ModeAuto:
		mid = T("users.moeny.carttext_auto")
	default:
		mid = T("users.moeny.carttext_manual")
	}
	return head + mid + T("users.moeny.carttext_warn")
}

// waitsForReceipt reports which state a mode leaves the bot in. Application
// mode waits for the bank SMS; the other two wait for the receipt photo, and
// a photo sent in application mode is still accepted as an extra.
func (m PayMode) waitsForReceipt() bool { return m != ModeApp }
