package panels

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
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

// ---------------------------------------------------------------- creation
//
// mitCreate runs after a payment has arrived, so a stall between the bot and
// the panel must not swallow the delivery. These tests exercise the calls by
// hand: the DB is sqlmock for the panel re-reads every login performs, and
// the fake servers only see the calls beyond auth because the datelogin row
// below carries just-now tokens.

// freshLogins is a datelogin value whose Mit and direct-Marzban tokens are a
// moment old, so neither server is asked for a token during the test.
func freshLogins() string {
	cache := map[string]any{
		"mit":     map[string]any{"time": nowStamp(), "access_token": "mit-tok"},
		"marzban": map[string]any{"time": nowStamp(), "access_token": "marz-tok"},
	}
	return string(marshal(cache))
}

// expectPanelRead registers one marzban_panel re-read, which every MitLogin
// and direct-token call performs first.
func expectPanelRead(mock sqlmock.Sqlmock, mitURL, directURL string) {
	mock.ExpectQuery("SELECT \\* FROM `marzban_panel`").WithArgs("1").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name_panel", "url_panel", "username_panel", "password_panel",
			"type", "datelogin", "marzban_url_direct", "marzban_username_direct", "marzban_password_direct"}).
			AddRow("1", "mitp", mitURL, "panu", "panp", "mit", freshLogins(), directURL, "mu", "mp"))
}

// The report of 2026-11-09, bot @Ir90bot: money had arrived, then the bot
// gave up on "POST .../login: context deadline exceeded" — the mit panel was
// stuck on its own upstream — and the payment sat waiting. When the panel's
// answer to a create is a refusal, the real Marzban may still hold the user
// the create produced, so mitCreate must hand over whatever the direct server
// has instead of reporting a failure.
func TestMitCreateConvergesOnTheUserThePanelHas(t *testing.T) {
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	var creates atomic.Int32
	mitSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panpan/admin/user" {
			t.Errorf("unexpected mit call %s", r.URL.Path)
			return
		}
		if creates.Add(1) != 1 {
			t.Errorf("mit saw %d create posts", creates.Load())
			return
		}
		// the panel refuses the create, like an "already exists" answer
		w.Write([]byte(`{"success":false,"detail":"duplicate"}`))
	}))
	defer mitSrv.Close()

	marzSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/u1" {
			t.Errorf("unexpected direct call %s", r.URL.Path)
			return
		}
		w.Write([]byte(`{"username":"u1","status":"active","subscription_url":"sub/u1","links":["vless://client"]}`))
	}))
	defer marzSrv.Close()

	expectPanelRead(mock, mitSrv.URL+"/panpan", marzSrv.URL) // MitLogin for the create
	expectPanelRead(mock, mitSrv.URL+"/panpan", marzSrv.URL) // direct token for the lookup

	m := New(&db.DB{DB: sqldb})
	// the row the bot holds, complete as production passes it
	panel := db.Row{
		"id": db.Val{S: "1"}, "url_panel": db.Val{S: mitSrv.URL + "/panpan"},
		"marzban_url_direct": db.Val{S: marzSrv.URL},
	}
	out := m.mitCreate(panel, "u1", 1000, 1024)
	if out.S("status") != "successful" || out.S("username") != "u1" {
		t.Fatalf("out = %v", out)
	}
	// a relative sub path from Marzban belongs on the direct server
	if w := marzSrv.URL + "/sub/u1"; out.S("subscription_url") != w {
		t.Fatalf("subscription_url = %q, want %q", out.S("subscription_url"), w)
	}
	if got := out.List("configs"); !reflect.DeepEqual(got, []string{"vless://client"}) {
		t.Fatalf("configs = %v", got)
	}
	// the refusal must have reached the panel stub, not failed client-side
	if n := creates.Load(); n != 1 {
		t.Fatalf("create posts = %d, want exactly one", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// When the panel answers nothing at all and the direct server holds nothing,
// the create is re-run once: this is the case where nothing was created, and
// the second try carries the delivery.
func TestMitCreateRetriesACreateThePanelNeverAnswered(t *testing.T) {
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	var creates atomic.Int32
	mitSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panpan/admin/user" {
			t.Errorf("unexpected mit call %s", r.URL.Path)
			return
		}
		if creates.Add(1) == 1 {
			panic(http.ErrAbortHandler) // the connection dies with no answer
		}
		w.Write([]byte(`{"success":true}`))
	}))
	defer mitSrv.Close()

	var looks atomic.Int32
	marzSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/u1" {
			t.Errorf("unexpected direct call %s", r.URL.Path)
			return
		}
		if looks.Add(1) == 1 {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"detail":"Not Found"}`))
			return
		}
		w.Write([]byte(`{"username":"u1","status":"active","subscription_url":"sub/u1","links":["vless://client"]}`))
	}))
	defer marzSrv.Close()

	expectPanelRead(mock, mitSrv.URL+"/panpan", marzSrv.URL) // MitLogin for the create
	expectPanelRead(mock, mitSrv.URL+"/panpan", marzSrv.URL) // direct token: nothing there yet
	expectPanelRead(mock, mitSrv.URL+"/panpan", marzSrv.URL) // MitLogin for the retry
	expectPanelRead(mock, mitSrv.URL+"/panpan", marzSrv.URL) // direct token after the retry

	m := New(&db.DB{DB: sqldb})
	// the row the bot holds, complete as production passes it
	panel := db.Row{
		"id": db.Val{S: "1"}, "url_panel": db.Val{S: mitSrv.URL + "/panpan"},
		"marzban_url_direct": db.Val{S: marzSrv.URL},
	}
	out := m.mitCreate(panel, "u1", 1000, 1024)
	if out.S("status") != "successful" || out.S("username") != "u1" {
		t.Fatalf("out = %v", out)
	}
	if holds := marzSrv.URL + "/sub/u1"; out.S("subscription_url") != holds {
		t.Fatalf("subscription_url = %q", out.S("subscription_url"))
	}
	if got := out.List("configs"); !reflect.DeepEqual(got, []string{"vless://client"}) {
		t.Fatalf("configs = %v", got)
	}
	if creates.Load() != 2 {
		t.Fatalf("create posts = %d, want 2", creates.Load())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A login has no side effects, so when the panel never answers the first
// time, the second must still get the bot its token instead of failing the
// delivery that waits behind it.
func TestMitLoginRetriesWhenThePanelNeverAnswers(t *testing.T) {
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	var logins atomic.Int32
	mitSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panpan/login" {
			t.Errorf("unexpected mit call %s", r.URL.Path)
			return
		}
		if logins.Add(1) == 1 {
			panic(http.ErrAbortHandler)
		}
		w.Write([]byte(`{"data":{"access_token":"tok-two"}}`))
	}))
	defer mitSrv.Close()

	// no login has ever happened on this panel: its logins column is empty
	mock.ExpectQuery("SELECT \\* FROM `marzban_panel`").WithArgs("1").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name_panel", "url_panel", "username_panel", "password_panel", "type", "datelogin"}).
			AddRow("1", "mitp", mitSrv.URL+"/panpan", "panu", "panp", "mit", ""))
	mock.ExpectExec("UPDATE.*marzban_panel").WillReturnResult(sqlmock.NewResult(0, 1))

	m := New(&db.DB{DB: sqldb})
	auth := m.MitLogin(db.Row{"id": db.Val{S: "1"}})
	if tok, ok := auth["access_token"].(string); !ok || tok != "tok-two" {
		t.Fatalf("auth = %v", auth)
	}
	if logins.Load() != 2 {
		t.Fatalf("login posts = %d, want 2", logins.Load())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
