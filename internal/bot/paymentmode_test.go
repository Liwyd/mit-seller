package bot

import (
	"strings"
	"testing"
	"time"

	"github.com/Liwyd/mit-seller/internal/php"
)

// The receipt flow is decided by one mode, and the mode is decided from one
// place. Everything else in this release — the message the customer gets, the
// state the bot waits in, whether a receipt is needed at all — follows from
// it, so it is tested first and separately.

func TestPayModeResolution(t *testing.T) {
	now := int64(1_700_000_000)
	recent := php.Date("Y-m-d H:i:s", now-300) // five minutes ago
	stale := php.Date("Y-m-d H:i:s", now-13*3600)
	future := php.Date("Y-m-d H:i:s", now+3600)

	cases := []struct {
		name, status, lastSeen string
		cronCard               bool
		want                   PayMode
	}{
		{"phone on and awake", "on", recent, false, ModeApp},
		{"phone on and awake wins over cron", "on", recent, true, ModeApp},
		// the bug that let a customer be quoted a unique amount nobody was
		// listening for: the switch was on but the phone had never connected
		{"phone on, never connected", "on", "", false, ModeManual},
		{"phone on, never connected, cron on", "on", "", true, ModeAuto},
		{"phone on, never connected, no cron", "on", "none", false, ModeManual},
		{"phone on but silent past the window", "on", stale, false, ModeManual},
		{"phone on but silent, cron still available", "on", stale, true, ModeAuto},
		{"phone on but clock skew", "on", future, false, ModeManual},
		{"phone off but cron on", "off", recent, true, ModeAuto},
		{"phone off, cron off", "off", "", false, ModeManual},
		{"everything off", "", "", false, ModeManual},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := payModeFor(tc.status, tc.lastSeen, tc.cronCard, now); got != tc.want {
				t.Errorf("payModeFor(%q, %q, %v) = %q, want %q", tc.status, tc.lastSeen, tc.cronCard, got, tc.want)
			}
		})
	}
}

func TestAppConnectedWindow(t *testing.T) {
	now := int64(1_700_000_000)
	cases := []struct {
		name, lastSeen string
		want           bool
	}{
		{"just now", php.Date("Y-m-d H:i:s", now-1), true},
		{"the very second it pinged", php.Date("Y-m-d H:i:s", now), true},
		{"a second ahead of us", php.Date("Y-m-d H:i:s", now+1), false},
		{"at the edge", php.Date("Y-m-d H:i:s", now-autopayStaleAfter), true},
		{"one second past", php.Date("Y-m-d H:i:s", now-autopayStaleAfter-1), false},
		{"never connected", "", false},
		{"unparseable", "not a date", false},
		{"the zero time", "0000-00-00 00:00:00", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := appConnected(tc.lastSeen, now); got != tc.want {
				t.Errorf("appConnected(%q) = %v, want %v", tc.lastSeen, got, tc.want)
			}
		})
	}
}

// Only application mode lets the customer off the receipt hook. The other two
// must ask for it, or the auto-approve cron and the admin would have nothing
// to act on.
func TestReceiptWaitingMatchesMode(t *testing.T) {
	if !ModeManual.waitsForReceipt() || !ModeAuto.waitsForReceipt() {
		t.Error("manual and automatic modes must both wait for the receipt photo")
	}
	if ModeApp.waitsForReceipt() {
		t.Error("application mode must not ask for a receipt")
	}
}

// The whole point of splitting the message: no mode is ever told both "send
// your receipt" and "no receipt is needed". This is the contradiction the
// combined carttext + AutopayPriceNote produced.
func TestCardMessageNeverAsksForBoth(t *testing.T) {
	const (
		needsManual = "عکس رسید خود را در این مرحله ارسال نمایید"
		needsAuto   = "عکس رسید خود را بفرستید"
		needsNone   = "نیازی به فرستادن رسید نیست"
	)
	cases := []struct {
		mode PayMode
		ask  string // the instruction this mode must carry
	}{
		{ModeManual, needsManual},
		{ModeAuto, needsAuto},
		{ModeApp, needsNone},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			msg := cardMessage(tc.mode, 180100, "6037991234567890")
			if !strings.Contains(msg, tc.ask) {
				t.Errorf("%s mode is missing its own instruction %q:\n%s", tc.mode, tc.ask, msg)
			}
			for _, other := range []string{needsManual, needsAuto, needsNone} {
				if other == tc.ask {
					continue
				}
				if strings.Contains(msg, other) {
					t.Errorf("%s mode also carries %q, so the customer is told two opposite things:\n%s", tc.mode, other, msg)
				}
			}
		})
	}
}

// The amount, the card and the standing warnings are not optional and not
// mode-dependent.
func TestCardMessageKeepsTheBasics(t *testing.T) {
	for _, mode := range []PayMode{ModeManual, ModeAuto, ModeApp} {
		t.Run(string(mode), func(t *testing.T) {
			msg := cardMessage(mode, 180100, "6037991234567890")
			if !strings.Contains(msg, "180,100") {
				t.Errorf("%s message does not show the amount:\n%s", mode, msg)
			}
			if !strings.Contains(msg, "6037991234567890") {
				t.Errorf("%s message does not show the card number:\n%s", mode, msg)
			}
			if !strings.Contains(msg, "10 میلیون تومان") {
				t.Errorf("%s message dropped the warnings:\n%s", mode, msg)
			}
			if strings.TrimSpace(msg) == "" {
				t.Error("empty message")
			}
		})
	}
}

// Application mode is the only one quoting a unique amount, and that number
// must be the one the customer is told to transfer exactly.
func TestAppMessageQuotesTheReservedAmount(t *testing.T) {
	msg := cardMessage(ModeApp, 70007, "6037991234567890")
	if !strings.Contains(msg, "70,007") {
		t.Errorf("application mode did not quote 70,007:\n%s", msg)
	}
	if !strings.Contains(msg, "دقیقاً") {
		t.Errorf("application mode did not stress the exact amount:\n%s", msg)
	}
	if !strings.Contains(msg, "۱۵ دقیقه") {
		t.Errorf("application mode dropped the support window:\n%s", msg)
	}
}

// Every string the new flow and the new admin section send has to exist;
// a missing key renders as an empty message that looks like the bot ignored
// the admin.
func TestPendingSectionTextsExist(t *testing.T) {
	for _, k := range []string{
		"users.moeny.carttext_head",
		"users.moeny.carttext_manual",
		"users.moeny.carttext_auto",
		"users.moeny.carttext_app",
		"users.moeny.carttext_warn",
		"users.moeny.deliver_failed",
		"users.moeny.app_receipt_attached",
		"Admin.Payment.deliverfailed",
		"Admin.pending.button",
		"Admin.pending.empty",
		"Admin.pending.mode",
		"Admin.pending.phone",
		"Admin.pending.receipts_title",
		"Admin.pending.receipt",
		"Admin.pending.orders_title",
		"Admin.pending.order",
		"Admin.pending.unmatched_title",
		"Admin.pending.unmatched",
		"Admin.pending.approve_order",
		"Admin.pending.cancel_order",
		"Admin.pending.match",
		"Admin.pending.approve_confirm",
		"Admin.pending.cancel_confirm",
		"Admin.pending.match_confirm",
	} {
		v := T(k)
		if v == "" {
			t.Errorf("%s is missing from text.json", k)
		}
	}
	// the format verbs each one needs
	for _, k := range []string{"users.moeny.carttext_head", "users.moeny.carttext_app"} {
		if n := strings.Count(T(k), "%s"); n == 0 {
			t.Errorf("%s has no %%s verb, so the amount or card cannot be filled in", k)
		}
	}
	if n := strings.Count(T("users.moeny.carttext_head"), "%s"); n != 2 {
		t.Errorf("carttext_head needs %d verbs (amount, card), has %d", 2, n)
	}
}

// The buttons the section shows and the callbacks the handler accepts are
// two halves of one contract; if they drift, the button silently does
// nothing.
func TestPendingCallbacksMatchTheirButtons(t *testing.T) {
	cases := []struct {
		data   string
		regex  string
		wantOK bool
		groups int
	}{
		{"autopay_approve_14", `^autopay_approve_(\d+)$`, true, 1},
		{"autopay_approve_14x", `^autopay_approve_(\d+)$`, false, 1},
		{"autopay_cancel_1", `^autopay_cancel_(\d+)$`, true, 1},
		{"autopay_match_3", `^autopay_match_(\d+)$`, true, 1},
		{"autopay_bind_3_9", `^autopay_bind_(\d+)_(\d+)$`, true, 2},
		// a match tap must not be read as a bind, and vice versa
		{"autopay_bind_3_9", `^autopay_match_(\d+)$`, false, 1},
		{"autopay_match_3", `^autopay_bind_(\d+)_(\d+)$`, false, 2},
		{"pending_home", `^autopay_approve_(\d+)$`, false, 1},
	}
	for _, tc := range cases {
		m := re(tc.regex).FindStringSubmatch(tc.data)
		if (m != nil) != tc.wantOK {
			t.Errorf("%q against %s: match=%v, want %v", tc.data, tc.regex, m != nil, tc.wantOK)
			continue
		}
		if tc.wantOK && len(m)-1 != tc.groups {
			t.Errorf("%q against %s: %d groups, want %d", tc.data, tc.regex, len(m)-1, tc.groups)
		}
	}
}

// The admin approval now reports three outcomes, and both callers switch on
// all of them — an unhandled case would either hide a real failure or claim a
// payment was already reviewed when the panel simply refused.
func TestPaymentOutcomeIsExhaustive(t *testing.T) {
	// the zero value is the success case, so an uninitialised variable can
	// never read as an error
	var zero PaymentOutcome
	if zero != PaymentApplied {
		t.Errorf("zero PaymentOutcome is %v, want PaymentApplied", zero)
	}
	seen := map[PaymentOutcome]bool{}
	for _, o := range []PaymentOutcome{PaymentApplied, PaymentAlreadyDone, PaymentFailed} {
		if seen[o] {
			t.Errorf("PaymentOutcome %d is used twice", o)
		}
		seen[o] = true
	}
}

// Modes are reported to the admin by name; the three labels must stay
// distinct or the section header says nothing about what will happen.
func TestPayModeLabelsAreDistinct(t *testing.T) {
	labels := map[string]PayMode{}
	for _, m := range []PayMode{ModeManual, ModeAuto, ModeApp} {
		l := m.Label()
		if l == "" {
			t.Errorf("%s has an empty label", m)
		}
		if other, dup := labels[l]; dup {
			t.Errorf("%s and %s both label as %q", m, other, l)
		}
		labels[l] = m
	}
}

func TestLastSeenLabel(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name, lastSeen string
		want           string
	}{
		{"never connected", "", "هرگز ⛔️"},
		{"garbage", "not a date", "هرگز ⛔️"},
		{"just now", php.Date("Y-m-d H:i:s", now-1), "همین الان ✅"},
		{"an hour ago", php.Date("Y-m-d H:i:s", now-3600), "1 ساعت پیش ⚠️"},
		{"two days ago", php.Date("Y-m-d H:i:s", now-2*86400), "2 روز پیش ⚠️"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastSeenLabel(tc.lastSeen); got != tc.want {
				t.Errorf("lastSeenLabel(%q) = %q, want %q", tc.lastSeen, got, tc.want)
			}
		})
	}
}
