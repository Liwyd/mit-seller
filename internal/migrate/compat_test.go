package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportSeverityAndText(t *testing.T) {
	r := &Report{}
	if r.HasFatal() {
		t.Fatal("an empty report is not fatal")
	}
	r.infof("carried %d row(s)", 3)
	r.warnf("payment: %s has no equivalent", "zarinpal")
	if r.HasFatal() {
		t.Fatal("a warning is not fatal")
	}
	r.fatalf("stopped at %s", "here")
	if !r.HasFatal() {
		t.Fatal("a fatal finding makes the report fatal")
	}
	s := r.String()
	for _, want := range []string{
		"carried 3 row(s)",
		"payment: zarinpal has no equivalent",
		"stopped at here",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("report does not mention %q:\n%s", want, s)
		}
	}
	if (&Report{}).String() != "nothing to adjust\n" {
		t.Error("an empty report should say so")
	}
}

func TestDetectFlavor(t *testing.T) {
	dir := t.TempDir()
	if got := DetectFlavor(dir); got != FlavorLegacy {
		t.Errorf("a plain bot directory is %q, want the nexra/mirzapanel flavour", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "cronbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectFlavor(dir); got != FlavorMirza {
		t.Errorf("cronbot/ makes it %q, want mirza_pro", got)
	}
	// a plain file called cronbot is not the dispatcher directory
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "cronbot"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectFlavor(other); got != FlavorLegacy {
		t.Errorf("a plain file named cronbot is %q, want the nexra/mirzapanel flavour", got)
	}
}

func TestDetectBotRoots(t *testing.T) {
	root := t.TempDir()
	mkdir := func(name string) string {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "config.php"), []byte("<?php"), 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	// mirzapanel's layout: the main bot plus two additional bots named by
	// the operator, and mirza_pro next to them
	mkdir("botmirzapanel1")
	extraA := mkdir("myshop")
	mkdir("a-second-bot")
	mirza := mkdir("mirza_pro")
	if err := os.Mkdir(filepath.Join(mirza, "cronbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	// a directory without config.php is not a bot
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := DetectBotRoots(nil, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	var dirs, flavors []string
	for _, b := range got {
		dirs = append(dirs, b.Dir)
		flavors = append(flavors, string(b.Flavor))
	}
	// the scan is sorted, so "a-second-bot" comes before "botmirzapanel1"
	want := []string{"a-second-bot", "botmirzapanel1", "mirza_pro", "myshop"}
	if len(dirs) != len(want) {
		t.Fatalf("found %v, want one of each of %v", dirs, want)
	}
	for i, name := range want {
		if dirs[i] != filepath.Join(root, name) {
			t.Errorf("roots[%d] = %s, want %s", i, dirs[i], filepath.Join(root, name))
		}
	}
	wantFlavor := map[string]string{
		"a-second-bot":   string(FlavorLegacy),
		"botmirzapanel1": string(FlavorLegacy),
		"mirza_pro":      string(FlavorMirza),
		"myshop":         string(FlavorLegacy),
	}
	for i, name := range want {
		if flavors[i] != wantFlavor[name] {
			t.Errorf("%s has flavour %q, want %q", name, flavors[i], wantFlavor[name])
		}
	}

	// an explicit directory comes first and is not repeated by the scan
	got, err = DetectBotRoots([]string{extraA}, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Dir != extraA {
		t.Errorf("an explicit directory should come first, got %v", got[0].Dir)
	}
	if len(got) != len(want) {
		t.Errorf("the explicit directory was listed twice: %v", got)
	}

	// an explicit directory that is not a bot is refused, not skipped
	if _, err := DetectBotRoots([]string{filepath.Join(root, "assets")}, nil); err == nil {
		t.Error("an explicit directory without config.php should be refused")
	}
}

func TestSplitDirs(t *testing.T) {
	list := strings.Join([]string{"/a", " /b ", "", "/c"}, string(os.PathListSeparator))
	got := SplitDirs(list)
	want := []string{"/a", "/b", "/c"}
	if len(got) != len(want) {
		t.Fatalf("SplitDirs(%q) = %v", list, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SplitDirs(%q)[%d] = %q, want %q", list, i, got[i], want[i])
		}
	}
	if SplitDirs("") != nil {
		t.Error("an empty list has no directories")
	}
}

func TestIdentRejectsBadIdentifiers(t *testing.T) {
	for _, good := range []string{"setting", "marzban_panel", "Payment_report"} {
		if ident(good) != "`"+good+"`" {
			t.Errorf("ident(%q) = %s", good, ident(good))
		}
	}
	for _, bad := range []string{"", "a b", "a;DROP", "`x", "1a"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("ident(%q) should panic", bad)
				}
			}()
			ident(bad)
		}()
	}
}

// withCrontab points PATH at a stub crontab serving table, and returns
// whatever crontab is installed afterwards. No real crontab is read or
// written, so this runs without root.
func withCrontab(t *testing.T, table string) func() string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "tab")
	if err := os.WriteFile(file, []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(dir, "crontab")
	script := "#!/bin/sh\ncase \"$1\" in\n  -l) cat '" + file + "' ;;\n  -)  cat > '" + file + "' ;;\nesac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		b, _ := os.ReadFile(file)
		return string(b)
	}
}

func TestCronStateReadsBothLayouts(t *testing.T) {
	const domain = "bot.example.com"

	// legacy: the crontab is the record of what is switched on.
	cases := []struct {
		name   string
		flavor Flavor
		tab    string
		want   map[string]bool
	}{
		{"nexra/mirzapanel", FlavorLegacy, strings.Join([]string{
			"*/15 * * * * curl https://" + domain + "/cron/configtest.php",
			"*/1 * * * * curl https://" + domain + "/cron/cronvolume.php",
			"*/1 * * * * curl https://" + domain + "/cron/cronday.php",
			"*/1 * * * * curl https://" + domain + "/cron/removeexpire.php",
			"*/1 * * * * curl https://" + domain + "/cron/croncard.php",
			"0 * * * * curl https://other.example.com/cron/configtest.php",
		}, "\n"), map[string]bool{"test": true, "volume": true, "time": true, "remove": true, "card": true}},
		{"mirza_pro ignores the crontab except for card", FlavorMirza, strings.Join([]string{
			// statusday.php is a sales report, not the expiry job, and
			// disableconfig.php only deactivates already-flagged configs.
			// Reading these as "the day and remove jobs are on" would switch
			// on Go's destructive cronDay/cronRemoveExpire.
			"*/15 * * * * curl https://" + domain + "/cronbot/statusday.php",
			"*/1 * * * * curl https://" + domain + "/cronbot/disableconfig.php",
			"*/1 * * * * curl https://" + domain + "/cronbot/configtest.php",
			"*/1 * * * * curl https://" + domain + "/cronbot/NoticationsService.php",
			"*/1 * * * * curl https://" + domain + "/cronbot/croncard.php",
		}, "\n"), map[string]bool{"test": false, "volume": false, "time": false, "remove": false, "card": true}},
		{"no cron lines at all", FlavorLegacy, "# nothing here", map[string]bool{"test": false, "volume": false, "time": false, "remove": false, "card": false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withCrontab(t, c.tab)
			got, _, err := CronState(domain, c.flavor, nil)
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range c.want {
				if got[name] != want {
					t.Errorf("cron %s = %v, want %v (whole state: %v)", name, got[name], want, got)
				}
			}
		})
	}
}

// mirza_pro keeps its cron switches in setting.cron_status, not in the
// crontab. The Go bot's five switches have to line up with that JSON.
func TestParseMirzaCronFlags(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]bool
	}{
		{"the default mirza_pro row", `{"day":true,"volume":true,"remove":false,"remove_volume":false,"test":false,"on_hold":false,"uptime_node":false,"uptime_panel":false}`,
			map[string]bool{"day": true, "volume": true, "remove": false, "test": false}},
		{"hand-edited strings", `{"day":"1","volume":"0","test":"true","remove":"false"}`,
			map[string]bool{"day": true, "volume": false, "test": true, "remove": false}},
		{"numeric", `{"day":1,"volume":0}`, map[string]bool{"day": true, "volume": false}},
		{"empty", "", map[string]bool{}},
		{"not json", "day=true", map[string]bool{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseMirzaCronFlags(c.raw)
			for name, want := range c.want {
				if got[name] != want {
					t.Errorf("flag %s = %v, want %v (whole: %v)", name, got[name], want, got)
				}
			}
		})
	}
}

// A domain must never match a crontab line belonging to a different host.
// example.com is a suffix of evil-example.com and a prefix of
// sub.example.com; a bare strings.Contains would read a stranger's job as
// this bot's, and RemoveCronLines would then delete that stranger's line.
func TestCronURLAnchorsTheHost(t *testing.T) {
	const domain = "example.com"
	ours := "*/1 * * * * curl https://example.com/cron/cronday.php"
	if script, ok := cronURL(ours, domain); !ok || script != "cronday.php" {
		t.Errorf("cronURL(our line) = %q, %v; want cronday.php, true", script, ok)
	}
	for _, line := range []string{
		"*/1 * * * * curl https://evil-example.com/cron/cronday.php",
		"*/1 * * * * curl https://sub.example.com/cron/cronday.php",
		"*/1 * * * * curl https://example.com.evil.net/cron/cronday.php",
	} {
		if script, ok := cronURL(line, domain); ok {
			t.Errorf("cronURL(%q) matched as %q — that is another host", line, script)
		}
	}
	// a scheme-less match must not fire either
	if _, ok := cronURL("*/1 * * * * curl example.com/cron/cronday.php", domain); ok {
		t.Error("cronURL matched a line with no scheme; the host was not anchored")
	}
}

func TestRemoveCronLinesOnlyDropsWhatGoImplements(t *testing.T) {
	const domain = "bot.example.com"
	tab := strings.Join([]string{
		"0 * * * * bash /root/backup.sh",
		"*/1 * * * * curl https://" + domain + "/cron/cronday.php",
		"*/1 * * * * curl https://" + domain + "/cronbot/NoticationsService.php",
		"*/5 * * * * curl https://" + domain + "/cronbot/uptime_panel.php",
		"*/1 * * * * curl https://" + domain + "/cronbot/lottery.php",
		"*/1 * * * * curl https://other.example.com/cron/cronday.php",
	}, "\n")
	installed := withCrontab(t, tab)

	_, kept, err := RemoveCronLines(domain)
	if err != nil {
		t.Fatal(err)
	}

	left := installed()
	// the jobs Go reimplements are gone
	for _, gone := range []string{domain + "/cron/cronday.php", domain + "/cronbot/NoticationsService.php"} {
		if strings.Contains(left, gone) {
			t.Errorf("the Go bot runs %s itself, but the line is still installed:\n%s", gone, left)
		}
	}
	// the jobs Go has no equivalent for are left running and reported
	for _, stay := range []string{domain + "/cronbot/uptime_panel.php", domain + "/cronbot/lottery.php"} {
		if !strings.Contains(left, stay) {
			t.Errorf("the Go bot has no job for %s; dropping it would silently stop it:\n%s", stay, left)
		}
	}
	if len(kept) != 2 {
		t.Errorf("kept = %v, want uptime_panel.php and lottery.php", kept)
	}
	if !strings.Contains(left, "bash /root/backup.sh") {
		t.Errorf("a crontab line that is not a cron curl was removed:\n%s", left)
	}
	if !strings.Contains(left, "other.example.com/cron/") {
		t.Errorf("another bot's cron lines were removed too:\n%s", left)
	}
}
