package panels

import (
	"testing"

	"github.com/Liwyd/mit-seller/internal/db"
)

// The reseller's dashboard in Mit Panel lives under a trailing slash: the
// panel serves its web page at "/dashboard" and the JSON the bot reads at
// "/dashboard/". Asking for the one without the slash used to answer with a
// page of HTML, so every "mit" panel failed its own connection test.
func TestMitDashboardAsksForTheJSON(t *testing.T) {
	row := db.Row{"url_panel": db.Val{S: "https://panel.test/panpan/"}}
	want := "https://panel.test/panpan/dashboard/"
	if got := mitBase(row) + mitDashboardPath; got != want {
		t.Fatalf("dashboard url = %q, want %q", got, want)
	}
}

// A panel row whose address already has a trailing slash must not end up
// with two of them.
func TestMitBaseTrimsTheSlash(t *testing.T) {
	row := db.Row{"url_panel": db.Val{S: "https://panel.test/panpan/"}}
	if got := mitBase(row); got != "https://panel.test/panpan" {
		t.Fatalf("base = %q, want %q", got, "https://panel.test/panpan")
	}
}
