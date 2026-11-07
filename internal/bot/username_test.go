package bot

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/tg"
)

func TestUsernameBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"ab", false}, {"abc", true}, {strings.Repeat("a", 32), true},
		{strings.Repeat("a", 33), false}, {"a_b", true}, {"abc_", false},
		{"_abc", false}, {"1abc", false}, {"AbC123", true},
		{"a b", false}, {"ab@c", false}, {"ali/name", false}, {"علی", false},
	} {
		if got := validServiceUsername(tc.name); got != tc.valid {
			t.Errorf("validServiceUsername(%q) = %v", tc.name, got)
		}
	}
	if got := fitUsername(strings.Repeat("A", 31) + "_suffix"); got != strings.Repeat("a", 31) {
		t.Errorf("fitUsername = %q", got)
	}
	for _, base := range []string{"ali", strings.Repeat("a", 26), strings.Repeat("a", 32), strings.Repeat("A", 33), strings.Repeat("a", 25) + "_abcdef"} {
		name, err := suffixUsername(base)
		if err != nil {
			t.Fatal(err)
		}
		prefix := strings.ToLower(base)
		if len(prefix) > 26 {
			prefix = prefix[:26]
		}
		if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+6 || !validServiceUsername(name) {
			t.Errorf("suffixUsername(%q) = %q", base, name)
		}
		if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(name[len(name)-6:]) {
			t.Errorf("not exactly six decimal digits: %q", name)
		}
	}
}

// Exercise the actual paid username-entry handler, including persisted state
// and the username delivered in the invoice (Telegram and SQL are isolated).
func TestBuildInvoiceUsernameEntry(t *testing.T) {
	for _, mode := range []string{"typed", "random", "choice random", "taken", "long taken"} {
		t.Run(mode, func(t *testing.T) {
			sqldb, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqldb.Close()
			var messages []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var p struct {
					Text string `json:"text"`
				}
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Error(err)
				}
				messages = append(messages, p.Text)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"ok":true,"result":{}}`))
			}))
			defer server.Close()
			d := &db.DB{DB: sqldb}
			c := &Ctx{b: &Bot{DB: d, PM: panels.New(d), TG: tg.New("test", server.URL, "")}, fromID: "42", text: "Ali", user: db.Row{
				"step": {S: "endstepuser"}, "Processing_value": {S: "panel"}, "Processing_value_one": {S: "product"}, "Balance": {S: "50000"},
			}}
			method := T("users.customusername")
			if mode == "random" || mode == "choice random" {
				c.text = T("users.randomusername")
			}
			if mode == "choice random" {
				method = T("users.customuserchoice")
			}
			if mode == "long taken" {
				c.text = strings.Repeat("a", 32)
			}
			panelRows := func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{"name_panel", "MethodUsername", "type"}).AddRow("panel", method, "test")
			}
			mock.ExpectQuery("SELECT.*marzban_panel").WithArgs("panel").WillReturnRows(panelRows())
			mock.ExpectExec("UPDATE.*user.*Processing_value_one").WithArgs("product", "42").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT.*product").WithArgs("product", "panel").WillReturnRows(sqlmock.NewRows([]string{"name_product", "Service_time", "price_product", "Volume_constraint"}).AddRow("plan", "30", "50000", "10"))
			mock.ExpectQuery("SELECT.*marzban_panel").WithArgs("panel").WillReturnRows(panelRows())
			taken := strings.Contains(mode, "taken")
			rows := sqlmock.NewRows([]string{"1"})
			if taken {
				rows.AddRow(1)
			}
			mock.ExpectQuery("SELECT 1 FROM.*invoice").WillReturnRows(rows)
			if taken {
				mock.ExpectQuery("SELECT.*marzban_panel").WithArgs("panel").WillReturnRows(panelRows())
				mock.ExpectQuery("SELECT 1 FROM.*invoice").WillReturnRows(sqlmock.NewRows([]string{"1"}))
			}
			mock.ExpectExec("UPDATE.*user.*Processing_value_tow").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE user SET step").WithArgs("payment", "42").WillReturnResult(sqlmock.NewResult(0, 1))
			c.buildInvoice()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
			wantMessages := 1
			if taken {
				wantMessages = 2
			}
			if len(messages) != wantMessages {
				t.Fatalf("messages=%v", messages)
			}
			invoice := messages[len(messages)-1]
			if mode == "typed" && !strings.Contains(invoice, "<code>ali</code>") {
				t.Fatal(invoice)
			}
			if mode == "random" || mode == "choice random" {
				if !regexp.MustCompile(`<code>user[0-9]{6}</code>`).MatchString(invoice) {
					t.Fatal(invoice)
				}
			}
			if taken {
				prefix := "ali"
				if mode == "long taken" {
					prefix = strings.Repeat("a", 26)
				}
				pattern := regexp.MustCompile(`<code>` + prefix + `[0-9]{6}</code>`)
				if !pattern.MatchString(invoice) || !pattern.MatchString(messages[0]) {
					t.Fatal(messages)
				}
			}
		})
	}
}

func TestRandomUsername(t *testing.T) {
	for i := 0; i < 100; i++ {
		name, err := randomUsername()
		if err != nil || !validServiceUsername(name) || !regexp.MustCompile(`^user[0-9]{6}$`).MatchString(name) {
			t.Fatalf("randomUsername = %q, %v", name, err)
		}
	}
}

func TestChooseAvailableUsername(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		baseTaken, suffixTaken, randomFails bool
		calls                               int
		wantErr                             bool
	}{
		{"free", false, false, false, 1, false},
		{"taken", true, false, false, 2, false},
		{"replacement taken", true, true, false, 2, true},
		{"random failure", true, false, true, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			name, err := chooseAvailableUsername("ali", func(n string) bool {
				calls++
				if n == "ali" {
					return tc.baseTaken
				}
				return tc.suffixTaken
			}, func(string) (string, error) {
				if tc.randomFails {
					return "", errors.New("entropy unavailable")
				}
				return "ali012345", nil
			})
			if calls != tc.calls || (err != nil) != tc.wantErr {
				t.Fatalf("calls=%d name=%q err=%v", calls, name, err)
			}
			if err == nil && name != "ali" && name != "ali012345" {
				t.Fatal(name)
			}
		})
	}
}

func TestRetryUsernameCreate(t *testing.T) {
	for _, tc := range []struct {
		name, msg                                       string
		success, saveFail, replacementFail, secondFails bool
		wantCalls                                       int
	}{
		{"success", "", true, false, false, false, 1},
		{"duplicate", "User already exists", false, false, false, false, 2},
		{"Mit duplicate", "This username already exists on the panel", false, false, false, false, 2},
		{"persist failure", "User already exists", false, true, false, false, 1},
		{"replacement failure", "User already exists", false, false, true, false, 1},
		{"second duplicate stops", "User already exists", false, false, false, true, 2},
		{"timeout", "context deadline exceeded", false, false, false, false, 1},
		{"quota", "Insufficient traffic", false, false, false, false, 1},
		{"auth", "Incorrect username or password", false, false, false, false, 1},
		{"unrelated duplicate", "duplicate UUID", false, false, false, false, 1},
		{"generic failure", "Failed to create the user on the panel. Check the username and panel connection.", false, false, false, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []string
			calls := 0
			name, out := retryUsernameCreate("ali", func(n string) panels.Out {
				calls++
				events = append(events, "create:"+n)
				if tc.success || (calls == 2 && !tc.secondFails) {
					return panels.Out{"username": n}
				}
				return panels.Out{"msg": tc.msg}
			}, func() (string, error) {
				if tc.replacementFail {
					return "", errors.New("no free name")
				}
				return "ali012345", nil
			}, func(n string) error {
				events = append(events, "save:"+n)
				if tc.saveFail {
					return errors.New("database down")
				}
				return nil
			})
			if calls != tc.wantCalls {
				t.Fatalf("calls=%d, events=%v", calls, events)
			}
			if calls == 2 {
				if name != "ali012345" || !reflect.DeepEqual(events, []string{"create:ali", "save:ali012345", "create:ali012345"}) {
					t.Fatalf("name=%q events=%v", name, events)
				}
				if out.Isset("username") == tc.secondFails {
					t.Fatal(out)
				}
			}
		})
	}
}

func TestPendingUsernameRenameTransaction(t *testing.T) {
	for _, failAt := range []string{"", "invoice", "reports", "user", "commit", "missing invoice"} {
		t.Run("fail="+failAt, func(t *testing.T) {
			sqldb, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqldb.Close()
			b := &Bot{DB: &db.DB{DB: sqldb}}
			mock.ExpectBegin()
			queries := []struct {
				label, sql string
				args       []any
			}{
				{"invoice", "UPDATE invoice SET username = ? WHERE id_invoice = ? AND id_user = ? AND username = ? AND Status = 'unpaid'", []any{"ali012345", "inv1", "42", "ali"}},
				{"reports", "UPDATE Payment_report SET invoice = ? WHERE id_user = ? AND invoice = ?", []any{"getconfigafterpay|ali012345", "42", "getconfigafterpay|ali"}},
				{"user", "UPDATE user SET Processing_value_one = ? WHERE id = ? AND Processing_value_tow = 'getconfigafterpay' AND Processing_value_one = ?", []any{"ali012345", "42", "ali"}},
			}
			for _, q := range queries {
				e := mock.ExpectExec(regexp.QuoteMeta(q.sql))
				// Explicit arguments ensure renaming cannot cross a buyer boundary.
				switch q.label {
				case "invoice":
					e.WithArgs(q.args[0], q.args[1], q.args[2], q.args[3])
				case "reports", "user":
					e.WithArgs(q.args[0], q.args[1], q.args[2])
				}
				if failAt == q.label {
					e.WillReturnError(errors.New("database down"))
					break
				}
				if failAt == "missing invoice" {
					e.WillReturnResult(sqlmock.NewResult(0, 0))
					break
				}
				e.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if failAt == "commit" {
				mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
			} else if failAt == "" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = b.renamePendingUsername("inv1", "42", "ali", "ali012345")
			if (err != nil) != (failAt != "") {
				t.Fatalf("err=%v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUsernameEntryKeyboard(t *testing.T) {
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	// Button loading also reads the emoji allow-list; no settings changes.
	mock.ExpectQuery("SELECT.*mit_kv").WillReturnRows(sqlmock.NewRows([]string{"v"}))
	mock.ExpectQuery("SELECT.*mit_kv").WillReturnRows(sqlmock.NewRows([]string{"v"}))
	c := &Ctx{b: &Bot{DB: &db.DB{DB: sqldb}}}
	k := c.kbUsernameEntry()
	if len(k.Keyboard) != 2 || k.Keyboard[0][0].Text != T("users.randomusername") || k.Keyboard[1][0].Text != T("users.backhome") {
		t.Fatal(k)
	}
	for _, key := range []string{"users.randomusername", "users.buy.usernametaken", "users.selectusername"} {
		if T(key) == "" {
			t.Fatalf("missing text %s", key)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTrialUsernameEntry(t *testing.T) {
	for _, mode := range []string{"typed", "random", "choice random", "taken"} {
		t.Run(mode, func(t *testing.T) {
			sqldb, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqldb.Close()
			var created string
			var messages []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasPrefix(r.URL.Path, "/bottest/"):
					var p struct {
						Text string `json:"text"`
					}
					json.NewDecoder(r.Body).Decode(&p)
					messages = append(messages, p.Text)
					w.Write([]byte(`{"ok":true,"result":{}}`))
				case r.Method == "POST" && r.URL.Path == "/admin/user":
					var p struct {
						Email string `json:"email"`
					}
					json.NewDecoder(r.Body).Decode(&p)
					created = p.Email
					w.Write([]byte(`{"success":true}`))
				case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/user/"):
					name := strings.TrimPrefix(r.URL.Path, "/api/user/")
					if name == created || (mode == "taken" && name == "ali") {
						json.NewEncoder(w).Encode(map[string]any{"username": name, "status": "active", "subscription_url": "/sub/" + name})
					} else {
						w.WriteHeader(404)
						w.Write([]byte(`{"detail":"User not found"}`))
					}
				default:
					t.Errorf("unexpected panel call %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			d := &db.DB{DB: sqldb}
			c := &Ctx{b: &Bot{DB: d, PM: panels.New(d), TG: tg.New("test", server.URL, "")}, fromID: "42", text: "Ali", setting: db.Row{"time_usertest": {S: "2"}, "val_usertest": {S: "200"}}, user: db.Row{
				"step": {S: "createusertest"}, "Processing_value_one": {S: "panel"}, "limit_usertest": {S: "1"},
			}}
			method := T("users.customusername")
			if mode == "random" || mode == "choice random" {
				c.text = T("users.randomusername")
			}
			if mode == "choice random" {
				method = T("users.customuserchoice")
			}
			stamp := php.DateNow("Y/m/d H:i:s")
			cache, _ := json.Marshal(map[string]any{"mit": map[string]string{"time": stamp, "access_token": "test"}, "marzban": map[string]string{"time": stamp, "access_token": "test"}})
			panelRows := func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{"id", "name_panel", "MethodUsername", "type", "datelogin", "url_panel", "marzban_url_direct"}).AddRow("panel", "panel", method, "mit", string(cache), server.URL, server.URL)
			}
			// The repeated reads include both panel lookup and token-cache reads.
			mock.ExpectQuery("SELECT.*marzban_panel").WithArgs("panel").WillReturnRows(panelRows())
			for i := 0; i < 2; i++ {
				mock.ExpectQuery("SELECT.*marzban_panel").WithArgs("panel").WillReturnRows(panelRows())
			}
			if mode == "taken" {
				for i := 0; i < 2; i++ {
					mock.ExpectQuery("SELECT.*marzban_panel").WithArgs("panel").WillReturnRows(panelRows())
				}
			}
			mock.ExpectQuery("SELECT 1 FROM.*invoice").WillReturnRows(sqlmock.NewRows([]string{"1"}))
			for i := 0; i < 3; i++ {
				mock.ExpectQuery("SELECT.*marzban_panel").WithArgs("panel").WillReturnRows(panelRows())
			}
			mock.ExpectExec("INSERT IGNORE INTO invoice").WillReturnResult(sqlmock.NewResult(0, 1))
			for i := 0; i < 2; i++ {
				mock.ExpectQuery("SELECT.*mit_kv").WillReturnRows(sqlmock.NewRows([]string{"v"}))
			}
			mock.ExpectExec("UPDATE user SET step").WithArgs("home", "42").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE.*user.*limit_usertest").WithArgs("0", "42").WillReturnResult(sqlmock.NewResult(0, 1))
			c.secUserTest()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
			if !validServiceUsername(created) {
				t.Fatalf("created=%q messages=%v", created, messages)
			}
			if mode == "typed" && created != "ali" {
				t.Fatal(created)
			}
			if (mode == "random" || mode == "choice random") && !regexp.MustCompile(`^user[0-9]{6}$`).MatchString(created) {
				t.Fatal(created)
			}
			if mode == "taken" && (!regexp.MustCompile(`^ali[0-9]{6}$`).MatchString(created) || len(messages) != 3) {
				t.Fatalf("created=%q messages=%v", created, messages)
			}
			if !strings.Contains(messages[len(messages)-2], "<code>"+created+"</code>") {
				t.Fatal(messages)
			}
		})
	}
}

func TestWalletCreationBilling(t *testing.T) {
	for _, mode := range []string{"normal", "duplicate", "rejected", "insert failed"} {
		t.Run(mode, func(t *testing.T) {
			sqldb, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqldb.Close()
			mock.MatchExpectationsInOrder(false)
			var attempts []string
			var delivered string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasPrefix(r.URL.Path, "/bottest/"):
					var p struct {
						Text string `json:"text"`
					}
					json.NewDecoder(r.Body).Decode(&p)
					if strings.Contains(p.Text, "✅ سرویس با موفقیت") {
						delivered = p.Text
					}
					w.Write([]byte(`{"ok":true,"result":{}}`))
				case r.Method == "POST" && r.URL.Path == "/admin/user":
					var p struct {
						Email string `json:"email"`
					}
					json.NewDecoder(r.Body).Decode(&p)
					attempts = append(attempts, p.Email)
					if mode == "duplicate" && len(attempts) == 1 {
						w.WriteHeader(409)
						w.Write([]byte(`{"success":false,"message":"This username already exists on the panel"}`))
					} else if mode == "rejected" {
						w.WriteHeader(403)
						w.Write([]byte(`{"success":false,"message":"Insufficient traffic"}`))
					} else {
						w.Write([]byte(`{"success":true}`))
					}
				case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/user/"):
					name := strings.TrimPrefix(r.URL.Path, "/api/user/")
					if len(attempts) > 0 && name == attempts[len(attempts)-1] {
						json.NewEncoder(w).Encode(map[string]any{"username": name, "subscription_url": "/sub/" + name})
					} else {
						w.WriteHeader(404)
						w.Write([]byte(`{"detail":"User not found"}`))
					}
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			d := &db.DB{DB: sqldb}
			c := &Ctx{b: &Bot{DB: d, PM: panels.New(d), TG: tg.New("test", server.URL, "")}, fromID: "42", datain: "confirmandgetservice", setting: db.Row{}, user: db.Row{
				"Processing_value": {S: "panel"}, "Processing_value_one": {S: "product"}, "Processing_value_tow": {S: "ali"}, "Balance": {S: "100000"},
			}}
			stamp := php.DateNow("Y/m/d H:i:s")
			cache, _ := json.Marshal(map[string]any{"mit": map[string]string{"time": stamp, "access_token": "test"}, "marzban": map[string]string{"time": stamp, "access_token": "test"}})
			panelRows := func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{"id", "name_panel", "type", "datelogin", "url_panel", "marzban_url_direct"}).AddRow("panel", "panel", "mit", string(cache), server.URL, server.URL)
			}
			panelReads := 4
			if mode == "duplicate" {
				panelReads = 8
			}
			if mode == "rejected" {
				panelReads = 3
			}
			if mode == "insert failed" {
				panelReads = 1
			}
			for i := 0; i < panelReads; i++ {
				mock.ExpectQuery("SELECT.*marzban_panel").WithArgs("panel").WillReturnRows(panelRows())
			}
			mock.ExpectQuery("SELECT.*product").WithArgs("product", "panel").WillReturnRows(sqlmock.NewRows([]string{"name_product", "Service_time", "price_product", "Volume_constraint"}).AddRow("plan", "30", "50000", "10"))
			mock.ExpectQuery("SELECT 1 FROM.*invoice").WillReturnRows(sqlmock.NewRows([]string{"1"}))
			insert := mock.ExpectExec("INSERT IGNORE INTO invoice").WithArgs("42", sqlmock.AnyArg(), "ali", sqlmock.AnyArg(), "panel", "plan", "50000", "10", "30", "pending_creation")
			if mode == "insert failed" {
				insert.WillReturnError(errors.New("database unavailable"))
			} else {
				insert.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if mode == "duplicate" {
				mock.ExpectQuery("SELECT 1 FROM.*invoice").WillReturnRows(sqlmock.NewRows([]string{"1"}))
				mock.ExpectExec("UPDATE invoice SET username").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE.*user.*Processing_value_tow").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			success := mode == "normal" || mode == "duplicate"
			if success {
				mock.ExpectExec("UPDATE.*invoice.*Status").WithArgs("active", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT.*affiliates").WillReturnRows(sqlmock.NewRows([]string{"status_commission"}).AddRow("offcommission"))
				// Exactly one debit, only after confirmed creation (including retries).
				mock.ExpectExec("UPDATE.*user.*Balance").WithArgs("50000", "42").WillReturnResult(sqlmock.NewResult(0, 1))
			} else if mode == "rejected" {
				mock.ExpectExec("UPDATE.*invoice.*Status").WithArgs("creation_failed", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			for i := 0; i < 2; i++ {
				mock.ExpectQuery("SELECT.*mit_kv").WillReturnRows(sqlmock.NewRows([]string{"v"}))
			}
			mock.ExpectExec("UPDATE user SET step").WithArgs("home", "42").WillReturnResult(sqlmock.NewResult(0, 1))
			c.payFromBalance()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
			wantAttempts := 1
			if mode == "duplicate" {
				wantAttempts = 2
			}
			if mode == "insert failed" {
				wantAttempts = 0
			}
			if len(attempts) != wantAttempts {
				t.Fatalf("attempts=%v", attempts)
			}
			if success && !strings.Contains(delivered, "<code>"+attempts[len(attempts)-1]+"</code>") {
				t.Fatalf("wrong delivery: %q", delivered)
			}
			if !success && delivered != "" {
				t.Fatalf("delivered despite rejection: %q", delivered)
			}
		})
	}
}
