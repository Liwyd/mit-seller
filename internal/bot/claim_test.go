package bot

import (
	"os"
	"strings"
	"testing"

	"github.com/Liwyd/mit-seller/internal/panels"
)

// The whole of the claim feature rests on being able to tell that a config
// belongs to the person asking for it. The panel keeps no Telegram identity
// for a config, so the proof is that the bot wrote the buyer's own id into the
// name when it sold it — <id>_<random>, see generateUsername. Everything here
// is about not getting that comparison wrong.
func TestCanAutoClaim(t *testing.T) {
	idMethod := T("users.customidAndRandom")
	if idMethod == "" {
		t.Fatal("users.customidAndRandom is missing from text.json; the username method labels drive generateUsername")
	}

	cases := []struct {
		name, username, fromID, method string
		want                           bool
	}{
		// the normal case: the default method wrote this user's own id
		{"own id", "712345678_a3f9", "712345678", idMethod, true},
		{"empty suffix after separator", "712345678_", "712345678", idMethod, false},
		{"own id with digits only after", "712345678_42", "712345678", idMethod, true},

		// the attack the separator exists to stop: a shorter id must not be
		// read as a prefix of a longer one
		{"short id cannot claim longer", "1234_x", "123", idMethod, false},
		{"long id cannot claim shorter", "123_x", "1234", idMethod, false},

		// without the separator, any id that starts this one could claim it
		{"missing separator", "7123456784", "712345678", idMethod, false},
		{"id embedded not prefixed", "xx_712345678_a3f9", "712345678", idMethod, false},
		{"another id entirely", "999999999_a3f9", "712345678", idMethod, false},

		// only the method that writes the id may be trusted to contain it
		{"wrong method", "712345678_a3f9", "712345678", T("users.customusername"), false},
		{"handle method", "ali_3", "712345678", T("users.customusernameorder"), false},
		{"empty method", "712345678_a3f9", "712345678", "", false},

		// degenerate inputs
		{"empty username", "", "712345678", idMethod, false},
		{"empty id", "712345678_a3f9", "", idMethod, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canAutoClaim(tc.username, tc.fromID, tc.method); got != tc.want {
				t.Errorf("canAutoClaim(%q, %q, %q) = %v, want %v",
					tc.username, tc.fromID, tc.method, got, tc.want)
			}
		})
	}
}

// A prefix check that accepts a bare id would let one user take another's
// service, so this is asserted on its own rather than only through the table.
func TestAutoClaimRequiresTheSeparator(t *testing.T) {
	method := T("users.customidAndRandom")
	if canAutoClaim("12345_x", "1234", method) {
		t.Error("a shorter id claimed a longer id's config")
	}
	if !canAutoClaim("12345_x", "12345", method) {
		t.Error("the owning id was refused")
	}
}

// The invoice column is MB, the same unit the buy path stores, so a panel
// reporting bytes has to be divided and a panel reporting null or unlimited
// has to come across as empty rather than "0 MB".
func TestClaimVolume(t *testing.T) {
	cases := []struct {
		name string
		out  panels.Out
		want string
	}{
		{"one gig", panels.Out{"data_limit": float64(1024 * 1024 * 1024)}, "1024"},
		{"bytes to MB", panels.Out{"data_limit": float64(5 * 1024 * 1024 * 1024)}, "5120"},
		{"unlimited is null", panels.Out{"data_limit": nil}, ""},
		{"unlimited is zero", panels.Out{"data_limit": float64(0)}, ""},
		{"absent", panels.Out{}, ""},
		{"string form", panels.Out{"data_limit": "1073741824"}, "1024"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimVolume(tc.out); got != tc.want {
				t.Errorf("claimVolume(%v) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

// Every string the claim flow sends has to be in text.json, and every admin
// callback the buttons produce has to be matched by the handler — otherwise
// the message arrives empty or the button does nothing.
func TestClaimTextsAndCallbacks(t *testing.T) {
	for _, k := range []string{
		"users.claim.productname",
		"users.claim.attached",
		"users.claim.pending",
		"users.claim.approved",
		"users.claim.rejected",
		"users.claim.alreadyowned",
		"users.claim.ownlist",
		"users.claim.throttled",
		"Admin.claim.request",
		"Admin.claim.approve",
		"Admin.claim.reject",
		"Admin.claim.askreason",
		"Admin.claim.approved_msg",
		"Admin.claim.rejected_msg",
		"Admin.claim.missing",
		"Admin.claim.taken",
		"Admin.claim.notfound",
		// the button that starts the whole flow
		"Admin.Status.notusenameinbot",
	} {
		if T(k) == "" {
			t.Errorf("%s is missing from text.json", k)
		}
	}

	// the button labels are what the admin actually presses
	if !strings.Contains(T("Admin.claim.approve"), "تأیید") {
		t.Error("the approve button should read as approving")
	}

	// the callback a button sends must be caught by the handler's pattern
	id := "abc123"
	for _, cbData := range []string{"claimok_" + id, "claimno_" + id} {
		matched := false
		for _, pat := range []string{`claimok_(\w+)`, `claimno_(\w+)`} {
			if re(pat).MatchString(cbData) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("callback %q is not matched by any claim handler", cbData)
		}
	}
}

// DataUser puts the account's own status in the "status" slot on success and
// Unsuccessful on failure — there is no "successful" value anywhere in that
// path, so testing for one would reject every config on every panel.
func TestClaimFound(t *testing.T) {
	cases := []struct {
		name string
		out  panels.Out
		want bool
	}{
		// the statuses marzban, mit, sui, alireza, xui and marzneshin return
		{"active", panels.Out{"status": "active", "username": "7_x"}, true},
		{"expired", panels.Out{"status": "expired", "username": "7_x"}, true},
		{"limited", panels.Out{"status": "limited", "username": "7_x"}, true},
		{"disabled", panels.Out{"status": "disabled", "username": "7_x"}, true},
		{"on_hold", panels.Out{"status": "on_hold", "username": "7_x"}, true},
		// a config that is expired or disabled is still the claimant's
		{"panel says successful", panels.Out{"status": "successful", "username": "7_x"}, true},

		// what every *Data function returns when the user is missing, the
		// credentials are wrong, or the panel cannot be reached
		{"Unsuccessful", panels.Out{"status": "Unsuccessful", "msg": "User not found"}, false},
		{"unsuccessful lowercase", panels.Out{"status": "unsuccessful", "msg": "x"}, false},
		{"network failure", panels.Out{"status": "Unsuccessful", "msg": "connection refused"}, false},
		{"no status at all", panels.Out{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimFound(tc.out); got != tc.want {
				t.Errorf("claimFound(%v) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

// php.Sprintf is positional-free, so a message with the wrong number of verbs
// for its call site renders as literal text in front of a user or an admin.
// The counts are pinned to what each sprintf call site passes.
func TestClaimFormatArity(t *testing.T) {
	cases := []struct {
		key  string
		want int
	}{
		{"users.claim.attached", 1},
		{"users.claim.pending", 1},
		{"users.claim.approved", 1},
		{"users.claim.ownlist", 1},
		{"users.claim.rejected", 2},
		{"Admin.claim.request", 5},
		{"Admin.claim.approved_msg", 1},
		{"Admin.claim.rejected_msg", 1},
		{"Admin.claim.notfound", 1},
	}
	for _, tc := range cases {
		if got := strings.Count(T(tc.key), "%s"); got != tc.want {
			t.Errorf("%s has %d %%s verbs, call site passes %d", tc.key, got, tc.want)
		}
	}
}

// The switch this feature hangs off is written by Mit Panel as "1"/"0"
// (ToggleSetting), so the gate that shows the button has to read those values.
// A regression to the legacy onnotuser/offnotuser wording would make the
// button invisible everywhere without failing anything else.
func TestNotUserGateReadsThePanelValue(t *testing.T) {
	for _, f := range []string{"index_commands.go", "index_services.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		src := string(b)
		if strings.Contains(src, `"offnotuser"`) || strings.Contains(src, `"onnotuser"`) {
			t.Errorf("%s still tests the legacy NotUser wording; Mit Panel writes \"1\"/\"0\"", f)
		}
	}
}
