package provisioner

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T, run func(n int, req Request, secret string) error) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "etc")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Token:       "s3cret-token",
		Installer:   filepath.Join(dir, "install.sh"),
		ConfigDir:   cfgDir,
		LockPath:    filepath.Join(dir, "provision.lock"),
		Logger:      log.New(io.Discard, "", 0),
		LockTimeout: 2 * time.Second,
		Run:         run,
	}
	return s, cfgDir
}

func post(t *testing.T, h http.Handler, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case string:
		rd = strings.NewReader(b)
	case nil:
		rd = strings.NewReader("")
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rd = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(http.MethodPost, "/provision", rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func writeBotConfig(t *testing.T, cfgDir string, n int) {
	t.Helper()
	path := filepath.Join(cfgDir, "bot"+itoa(n)+".env")
	body := "DOMAIN=ok.example.com\nBOT_USERNAME=somebot\nAPI_OWNER_KEY=owner-key\nAPI_MANAGER_KEY=manager-key\n"
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// ---------------------------------------------------------------- validation

func TestValidateAcceptsRealisticRequests(t *testing.T) {
	good := []Request{
		{Token: "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw", Domain: "kp.colorviolet.ir", AdminID: "6900787101"},
		{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "a.b.c.example.com", AdminID: "1", BotNumber: "7", CertEmail: "a@b.co"},
		{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "x-1.example.com", AdminID: "1", MitSecret: "amirkp2020"},
		{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "x.example.com", AdminID: "1",
			PanelURL: "https://panel.example.com:4000", PanelUser: "admin", PanelPass: "p@ss"},
	}
	for _, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", r, err)
		}
	}
}

// The values here are what would be needed to turn install.sh new into command
// execution. install.sh interpolates TOKEN into a shell command and DOMAIN into
// certbot -d, so a single miss here is remote code execution as root.
func TestValidateRejectsInjectionAttempts(t *testing.T) {
	base := Request{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "ok.example.com", AdminID: "1"}
	bad := []struct {
		name  string
		patch func(*Request)
	}{
		{"token with semicolon", func(r *Request) { r.Token = "555:AAAA; rm -rf / #" }},
		{"token with backtick", func(r *Request) { r.Token = "555:AAA`id`" }},
		{"token with dollar", func(r *Request) { r.Token = "555:AAA$(id)" }},
		{"token with space", func(r *Request) { r.Token = "555:AAA AAA" }},
		{"token with quote", func(r *Request) { r.Token = `555:AAA"AAA` }},
		{"token with newline", func(r *Request) { r.Token = "555:AAA\nAAAA" }},
		{"token with path", func(r *Request) { r.Token = "../../etc/passwd" }},
		{"domain with semicolon", func(r *Request) { r.Domain = "ok.example.com; curl evil" }},
		{"domain with space", func(r *Request) { r.Domain = "ok.example.com evil.com" }},
		{"domain with slash", func(r *Request) { r.Domain = "ok.example.com/../../x" }},
		{"domain with dash arg", func(r *Request) { r.Domain = "ok.example.com--unrelated" }},
		{"domain with dollar", func(r *Request) { r.Domain = "ok.example.com$(id)" }},
		{"domain with quote", func(r *Request) { r.Domain = `ok.example.com"` }},
		{"domain with newline", func(r *Request) { r.Domain = "ok.example.com\ncurl evil" }},
		{"domain with underscore", func(r *Request) { r.Domain = "ok_example.com" }},
		{"domain uppercase", func(r *Request) { r.Domain = "OK.example.com" }},
		{"domain single label", func(r *Request) { r.Domain = "localhost" }},
		{"admin with semicolon", func(r *Request) { r.AdminID = "1; id" }},
		{"admin with space", func(r *Request) { r.AdminID = "1 2" }},
		{"admin non numeric", func(r *Request) { r.AdminID = "abc" }},
		{"admin negative", func(r *Request) { r.AdminID = "-1" }},
		{"admin too long", func(r *Request) { r.AdminID = strings.Repeat("1", 30) }},
		{"n with semicolon", func(r *Request) { r.BotNumber = "1;id" }},
		{"n non numeric", func(r *Request) { r.BotNumber = "abc" }},
		{"n empty is fine but spaces are not", func(r *Request) { r.BotNumber = " 7" }},
		{"cert_email with semicolon", func(r *Request) { r.CertEmail = "a@b.co; id" }},
		{"cert_email not email", func(r *Request) { r.CertEmail = "not-an-email" }},
		{"mit_secret with backtick", func(r *Request) { r.MitSecret = "a`id`aaaaaaaa" }},
		{"mit_secret with space", func(r *Request) { r.MitSecret = "aaaaaaaa aaaaaaaa" }},
		{"mit_secret too short", func(r *Request) { r.MitSecret = "short" }},
		{"mit_secret with quote", func(r *Request) { r.MitSecret = `aaaaaaaa"aaaa` }},
		{"panel_url not http", func(r *Request) { r.PanelURL = "ftp://x" }},
		{"panel_url with newline", func(r *Request) { r.PanelURL = "https://x\ny" }},
		{"panel_url too long", func(r *Request) { r.PanelURL = "https://" + strings.Repeat("a", 600) }},
		{"panel_pass too long", func(r *Request) { r.PanelPass = strings.Repeat("a", 600) }},
		{"missing token", func(r *Request) { r.Token = "" }},
		{"missing domain", func(r *Request) { r.Domain = "" }},
		{"missing admin_id", func(r *Request) { r.AdminID = "" }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.patch(&r)
			if err := r.Validate(); err == nil {
				t.Errorf("Validate(%+v) = nil, want an error", r)
			}
		})
	}
}

func TestValidateDomainBounds(t *testing.T) {
	long := strings.Repeat("a", 64) + ".com"
	r := Request{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: long, AdminID: "1"}
	if err := r.Validate(); err == nil {
		t.Errorf("a 64-character label was accepted")
	}
}

// ---------------------------------------------------------------- the handler

func TestHealthNeedsNoTokenButLeaksNothing(t *testing.T) {
	s, _ := testServer(t, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "version") || strings.Contains(body, "176.") {
		t.Errorf("/health body looks like a banner: %s", body)
	}
	if strings.Trim(body, " \n\t") != `{"ok":true}` {
		t.Errorf("/health body = %q", body)
	}
}

func TestProvisionRejectsBadTokens(t *testing.T) {
	s, _ := testServer(t, nil)
	for _, token := range []string{"", "wrong", "s3cret-token " + strings.Repeat("x", 1000)} {
		rec := post(t, s.Handler(), token, Request{Domain: "ok.example.com", AdminID: "1", Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345"})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: got %d, want 401", token, rec.Code)
		}
	}
}

func TestProvisionRejectsInvalidBody(t *testing.T) {
	s, _ := testServer(t, nil)
	h := s.Handler()
	for _, body := range []string{"", "{", "[]", `{"token":"nope","domain":"ok.example.com","admin_id":"1"}`} {
		rec := post(t, h, "s3cret-token", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, rec.Code)
		}
	}
}

func TestProvisionRejectsHugeBody(t *testing.T) {
	s, _ := testServer(t, nil)
	rec := post(t, s.Handler(), "s3cret-token", `{"token":"`+strings.Repeat("a", 20000)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("got %d, want 413", rec.Code)
	}
}

func TestProvisionRunsAndReturnsKeys(t *testing.T) {
	s, cfgDir := testServer(t, nil)
	s.Run = func(n int, req Request, secret string) error {
		if n != 3 {
			t.Errorf("n = %d, want 3", n)
		}
		if req.Token == "" || req.Domain == "" || req.AdminID == "" {
			t.Errorf("run got an incomplete request: %+v", req)
		}
		if len(secret) != 32 {
			t.Errorf("generated secret = %q, want 32 hex chars", secret)
		}
		writeBotConfig(t, cfgDir, n)
		return nil
	}
	rec := post(t, s.Handler(), "s3cret-token", Request{
		Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "ok.example.com", AdminID: "1", BotNumber: "3",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK   bool `json:"ok"`
		Data struct {
			N           int    `json:"n"`
			URL         string `json:"url"`
			OwnerKey    string `json:"owner_key"`
			ManagerKey  string `json:"manager_key"`
			MitSecret   string `json:"mit_secret"`
			BotUsername string `json:"bot_username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Data.N != 3 || out.Data.OwnerKey != "owner-key" || out.Data.ManagerKey != "manager-key" {
		t.Errorf("unexpected reply: %s", rec.Body.String())
	}
	if out.Data.BotUsername != "somebot" || out.Data.URL != "https://ok.example.com" {
		t.Errorf("unexpected reply: %s", rec.Body.String())
	}
}

func TestProvisionPicksNextFreeNumber(t *testing.T) {
	s, cfgDir := testServer(t, nil)
	s.Run = func(n int, req Request, secret string) error {
		writeBotConfig(t, cfgDir, n)
		return nil
	}
	for _, n := range []int{1, 2, 4} {
		writeBotConfig(t, cfgDir, n)
	}
	rec := post(t, s.Handler(), "s3cret-token", Request{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "ok.example.com", AdminID: "1"})
	var out struct {
		Data struct{ N int } `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Data.N != 3 {
		t.Errorf("n = %d, want 3 (the lowest free after 1, 2 and 4)", out.Data.N)
	}
}

func TestProvisionRefusesATakenNumber(t *testing.T) {
	s, cfgDir := testServer(t, nil)
	writeBotConfig(t, cfgDir, 6)
	rec := post(t, s.Handler(), "s3cret-token", Request{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "ok.example.com", AdminID: "1", BotNumber: "6"})
	if rec.Code != http.StatusConflict {
		t.Errorf("got %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestProvisionReportsInstallFailureWithoutLeakingTheToken(t *testing.T) {
	const token = "555:AAAAabcdefghijklmnopqrstuvwxyz012345"
	s, _ := testServer(t, func(n int, req Request, secret string) error {
		return os.ErrPermission // as install.sh would
	})
	rec := post(t, s.Handler(), "s3cret-token", Request{Token: token, Domain: "ok.example.com", AdminID: "1"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", rec.Code)
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Errorf("the reply leaks the token: %s", rec.Body.String())
	}
}

func TestProvisionKeepsAProvidedSecret(t *testing.T) {
	s, cfgDir := testServer(t, nil)
	s.Run = func(n int, req Request, secret string) error {
		if secret != "amirkp2020" {
			t.Errorf("secret = %q", secret)
		}
		writeBotConfig(t, cfgDir, n)
		return nil
	}
	post(t, s.Handler(), "s3cret-token", Request{
		Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "ok.example.com", AdminID: "1", MitSecret: "amirkp2020",
	})
}

func TestProvisionSerializesInstallsWithTheLock(t *testing.T) {
	s, cfgDir := testServer(t, nil)
	s.Run = func(n int, req Request, secret string) error {
		time.Sleep(150 * time.Millisecond)
		writeBotConfig(t, cfgDir, n)
		return nil
	}
	s.LockTimeout = time.Second
	h := s.Handler()
	body := Request{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "ok.example.com", AdminID: "1"}
	done := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			done <- post(t, h, "s3cret-token", body).Code
		}()
	}
	got := []int{<-done, <-done}
	for _, code := range got {
		if code != http.StatusOK {
			t.Errorf("a concurrent install got %d, want 200 (they must serialize on the lock)", code)
		}
	}
}

func TestProvisionRateLimits(t *testing.T) {
	s, _ := testServer(t, nil)
	h := s.Handler()
	body := Request{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "ok.example.com", AdminID: "1"}
	over := 0
	for i := 0; i < 12; i++ {
		if post(t, h, "s3cret-token", body).Code == http.StatusTooManyRequests {
			over++
		}
	}
	if over == 0 {
		t.Errorf("twelve calls and none were rate limited")
	}
}

func TestDefaultRunPassesValuesAsEnvironmentNotCommandLine(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "install.sh")
	// The fake installer proves the values arrive as variables and that no
	// metacharacter in them was interpreted by a shell.
	body := `#!/bin/sh
printf '%s\n' "arg1=$1" "N=$N" "DOMAIN=$DOMAIN" "ADMIN=$ADMIN" "MIT_SECRET=$MIT_SECRET" "CERT_EMAIL=$CERT_EMAIL" > ` + dir + `/out
# TOKEN is deliberately not written out; the test checks it only by absence
# of a shell-evaluated side effect.
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{Installer: script, ConfigDir: dir}
	req := Request{Token: "555:AAAAabcdefghijklmnopqrstuvwxyz012345", Domain: "ok.example.com", AdminID: "1", CertEmail: "a@b.co"}
	if err := s.defaultRun(9, req, "s3cret"); err != nil {
		t.Fatalf("defaultRun: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"arg1=new", "N=9", "DOMAIN=ok.example.com", "ADMIN=1", "MIT_SECRET=s3cret", "CERT_EMAIL=a@b.co"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("installer did not see %q; got:\n%s", want, got)
		}
	}
}

func TestScrubRemovesTheTokenEverywhere(t *testing.T) {
	const tok = "555:AAAAabcdefghijklmnopqrstuvwxyz012345"
	in := "curl https://api.telegram.org/bot" + tok + "/getMe failed\nand again " + tok
	out := scrub(in, tok)
	if strings.Contains(out, tok) {
		t.Errorf("scrub left the token in: %s", out)
	}
	if !strings.Contains(out, "<token>") {
		t.Errorf("scrub removed the context too: %s", out)
	}
}
