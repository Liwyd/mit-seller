// Package migrate moves an existing PHP bot install onto the Go bot: it reads
// the old config.php and crontab, and can load a mysqldump into the database.
package migrate

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/Liwyd/mit-seller/internal/config"
	"github.com/Liwyd/mit-seller/internal/db"
)

var (
	reVar    = regexp.MustCompile(`\$(\w+)\s*=\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')\s*;`)
	reDefine = regexp.MustCompile(`define\(\s*['"](\w+)['"]\s*,\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')\s*\)`)
)

func unescapePHP(s string, double bool) string {
	if double {
		r := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\$`, `$`, `\n`, "\n", `\t`, "\t")
		return r.Replace(s)
	}
	return strings.NewReplacer(`\\`, `\`, `\'`, `'`).Replace(s)
}

// ReadPHPConfig parses the PHP bot's config.php without executing it.
func ReadPHPConfig(path string) (*config.Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	vars := map[string]string{}
	for _, m := range reVar.FindAllStringSubmatch(string(raw), -1) {
		if m[2] != "" || strings.Contains(m[0], `"`) && !strings.Contains(m[0], `'`) {
			vars[m[1]] = unescapePHP(m[2], true)
		} else {
			vars[m[1]] = unescapePHP(m[3], false)
		}
	}
	defs := map[string]string{}
	for _, m := range reDefine.FindAllStringSubmatch(string(raw), -1) {
		if m[2] != "" {
			defs[m[1]] = unescapePHP(m[2], true)
		} else {
			defs[m[1]] = unescapePHP(m[3], false)
		}
	}
	c := &config.Config{
		BotToken:    vars["APIKEY"],
		AdminID:     vars["adminnumber"],
		Domain:      stripScheme(vars["domainhosts"]),
		BotUsername: strings.TrimPrefix(vars["usernamebot"], "@"),
		MitSecret:   firstDef(defs, "MIT_SECRET_CODE", "NEXRA_SECRET_CODE"),
		DBHost:      "localhost",
		DBPort:      "3306",
		DBName:      vars["dbname"],
		DBUser:      vars["usernamedb"],
		DBPass:      vars["passworddb"],
	}
	for k, v := range map[string]string{"APIKEY": c.BotToken, "adminnumber": c.AdminID, "domainhosts": c.Domain, "dbname": c.DBName, "usernamedb": c.DBUser} {
		if v == "" || strings.HasPrefix(v, "{") {
			return nil, fmt.Errorf("config.php: $%s is not set", k)
		}
	}
	if strings.HasPrefix(c.MitSecret, "{") {
		c.MitSecret = ""
	}
	return c, nil
}

// firstDef returns the value of the first of the given config.php definitions
// that is present, so a config written before the rebrand (NEXRA_SECRET_CODE)
// is read exactly like a current one (MIT_SECRET_CODE).
func firstDef(defs map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := defs[k]; ok {
			return v
		}
	}
	return ""
}

// stripScheme drops a scheme and a trailing slash from a host, because the Go
// config stores a bare host and every crontab/nginx probe below matches on
// "://<domain>/". A PHP config that wrote "https://bot.example.com/" would
// otherwise match nothing, and the migration would leave the PHP crons in
// place to fight the Go bot's own scheduler.
func stripScheme(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(strings.ToLower(s), p) {
			s = s[len(p):]
			break
		}
	}
	return strings.TrimRight(s, "/")
}

// cronDirs are the two script directories the PHP bots put their crontab
// lines under. mirzapanel and nexra used /cron/; mirza_pro used /cronbot/.
var cronDirs = []string{"/cron/", "/cronbot/"}

// goCronScripts are the PHP cron scripts the Go bot reimplements itself. A
// crontab line calling one of these is dropped on migration, because the Go
// bot runs that job from its own scheduler. Any other script is left in the
// crontab and reported: the Go bot has no equivalent for it, and silently
// stopping somebody's backup or uptime job would be worse than leaving it.
var goCronScripts = map[string]bool{
	"configtest.php":         true, // → CronOn("test")
	"cronvolume.php":         true, // → CronOn("volume")
	"cronday.php":            true, // → CronOn("time")
	"removeexpire.php":       true, // → CronOn("remove")
	"croncard.php":           true, // → CronOn("card")
	"sendmessage.php":        true, // always on in Go
	"NoticationsService.php": true, // mirza_pro: volume + day + remove in one
}

// cronURL reports whether a crontab line calls one of this domain's cron
// scripts. The domain is anchored behind "://" so that example.com never
// matches a line belonging to evil-example.com or sub.example.com — a bare
// strings.Contains would both switch on a stranger's job and, worse, delete
// that line during the cutover.
func cronURL(line, domain string) (string, bool) {
	for _, dir := range cronDirs {
		if i := strings.Index(line, "://"+domain+dir); i >= 0 {
			rest := line[i+len("://"+domain+dir):]
			if j := strings.IndexAny(rest, " ?\"'"); j >= 0 {
				rest = rest[:j]
			}
			return rest, true
		}
	}
	return "", false
}

// readCrontab returns the calling user's crontab. An empty table (the usual
// case on a box that never had a PHP bot) is not an error; a failure to run
// crontab at all is, so the caller can tell "no crontab" from "could not
// read the crontab" and refuse rather than migrate blind.
func readCrontab() (string, error) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) || errors.Is(err, exec.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("reading the crontab: %w", err)
	}
	return string(out), nil
}

// mirzaCronFlags reads mirza_pro's setting.cron_status, the JSON object its
// admin menu writes and every /cronbot/ script checks before doing anything.
// nexra and mirzapanel kept these switches in the crontab itself, so this is
// only meaningful for the mirza_pro flavour.
func mirzaCronFlags(d *db.DB) map[string]bool {
	if d == nil {
		return map[string]bool{}
	}
	return parseMirzaCronFlags(d.Setting().S("cron_status"))
}

// parseMirzaCronFlags decodes the cron_status blob. mirza_pro wrote its
// booleans as JSON true/false, but an operator who edited the row by hand
// could have left "1"/"0" or "true"/"false" behind, so those are accepted too.
func parseMirzaCronFlags(raw string) map[string]bool {
	out := map[string]bool{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return out
	}
	for k, v := range m {
		switch t := v.(type) {
		case bool:
			out[k] = t
		case string:
			out[k] = t == "1" || strings.EqualFold(t, "true")
		case float64:
			out[k] = t != 0
		}
	}
	return out
}

// CronState reads which of the PHP bot's cron jobs were switched on.
//
// nexra and mirzapanel turned a job on by adding a curl line to the crontab,
// so the table itself is the record. mirza_pro moved the switches into
// setting.cron_status and gates every /cronbot/ script on that JSON, so the
// crontab only tells you whether croncard.php — the one script with no gate —
// was scheduled at all. Reading mirza_pro's state from the crontab would
// enable the wrong jobs: statusday.php is a sales report, not the expiry job,
// and disableconfig.php only deactivates already-flagged configs.
func CronState(domain string, flavor Flavor, d *db.DB) (map[string]bool, string, error) {
	tab, err := readCrontab()
	if err != nil {
		return nil, "", err
	}
	inTable := func(file string) bool {
		for _, line := range strings.Split(tab, "\n") {
			l := strings.TrimSpace(line)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			if script, ok := cronURL(l, domain); ok && script == file {
				return true
			}
		}
		return false
	}
	state := map[string]bool{}
	if flavor == FlavorMirza {
		f := mirzaCronFlags(d)
		state["test"] = f["test"]
		state["volume"] = f["volume"]
		state["time"] = f["day"]
		state["remove"] = f["remove"]
		// cron_status has no "card" key and croncard.php has no gate, so the
		// crontab is still the only record of that one.
		state["card"] = inTable("croncard.php")
		return state, tab, nil
	}
	for _, f := range []struct{ name, file string }{
		{"test", "configtest.php"},
		{"volume", "cronvolume.php"},
		{"time", "cronday.php"},
		{"remove", "removeexpire.php"},
		{"card", "croncard.php"},
	} {
		state[f.name] = inTable(f.file)
	}
	return state, tab, nil
}

// cronScriptName pulls the script basename out of a crontab line, or "".
func cronScriptName(line string) string {
	for _, dir := range cronDirs {
		if i := strings.Index(line, dir); i >= 0 {
			rest := line[i+len(dir):]
			if j := strings.IndexAny(rest, " ?\"'"); j >= 0 {
				rest = rest[:j]
			}
			return rest
		}
	}
	return ""
}

// RemoveCronLines drops the cron lines this domain ran for jobs the Go bot
// now runs itself, and leaves everything else in place. The old crontab is
// returned so it can be restored on rollback; the scripts that were left
// behind are returned too, so the operator is told which PHP jobs keep
// running alongside the Go bot.
func RemoveCronLines(domain string) (tab string, kept []string, err error) {
	tab, err = readCrontab()
	if err != nil {
		return "", nil, err
	}
	if tab == "" {
		return "", nil, nil
	}
	var keep, drop []string
	for _, line := range strings.Split(tab, "\n") {
		script, isOurs := cronURL(line, domain)
		if isOurs && goCronScripts[script] {
			drop = append(drop, script)
			continue
		}
		if isOurs {
			kept = append(kept, script)
		}
		keep = append(keep, line)
	}
	if len(drop) == 0 {
		return tab, kept, nil
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(keep, "\n"))
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		return tab, kept, fmt.Errorf("crontab: %v %s", cerr, out)
	}
	return tab, kept, nil
}

// ---------------------------------------------------------------- SQL import

// splitter yields complete SQL statements from a mysqldump stream.
type splitter struct {
	r     *bufio.Reader
	delim string
}

func (s *splitter) next() (string, error) {
	var buf bytes.Buffer
	var quote byte
	inLineComment, inBlock := false, false
	empty := true  // nothing but whitespace so far in this statement
	lineStart := 0 // index in buf where the current line starts
	for {
		c, err := s.r.ReadByte()
		if err != nil {
			if err == io.EOF && !empty {
				return buf.String(), nil
			}
			return "", err
		}
		if inLineComment {
			if c == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlock {
			buf.WriteByte(c)
			if c == '*' {
				if n, _ := s.r.Peek(1); len(n) == 1 && n[0] == '/' {
					s.r.ReadByte()
					buf.WriteByte('/')
					inBlock = false
				}
			}
			continue
		}
		if quote != 0 {
			buf.WriteByte(c)
			if c == '\\' && quote != '`' {
				if n, err := s.r.ReadByte(); err == nil {
					buf.WriteByte(n)
				}
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
			empty = false
			buf.WriteByte(c)
			continue
		case '-':
			if empty {
				if n, _ := s.r.Peek(2); len(n) == 2 && n[0] == '-' && (n[1] == ' ' || n[1] == '\t' || n[1] == '\n' || n[1] == '\r') {
					inLineComment = true
					continue
				}
			}
		case '#':
			if empty {
				inLineComment = true
				continue
			}
		case '/':
			if n, _ := s.r.Peek(1); len(n) == 1 && n[0] == '*' {
				inBlock = true
				empty = false
				buf.WriteByte(c)
				continue
			}
		}
		buf.WriteByte(c)
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			empty = false
		}
		if c == '\n' {
			line := strings.TrimSpace(string(buf.Bytes()[lineStart:]))
			lineStart = buf.Len()
			if len(line) > 10 && strings.EqualFold(line[:10], "DELIMITER ") {
				s.delim = strings.TrimSpace(line[10:])
				buf.Reset()
				empty, lineStart = true, 0
				continue
			}
		}
		if c == s.delim[len(s.delim)-1] && bytes.HasSuffix(buf.Bytes(), []byte(s.delim)) {
			stmt := string(buf.Bytes()[:buf.Len()-len(s.delim)])
			if empty || strings.TrimSpace(stmt) == "" {
				buf.Reset()
				empty, lineStart = true, 0
				continue
			}
			return stmt, nil
		}
	}
}

// ImportSQL loads a mysqldump (.sql or .sql.gz) into d. Unless force is
// set it refuses when the database already holds users, so a live bot is
// never overwritten by mistake.
func ImportSQL(d *db.DB, path string, force bool, progress func(n int)) error {
	if !force {
		var n int64
		if err := d.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'user'").Scan(&n); err != nil {
			return fmt.Errorf("checking whether the database already has users: %w", err)
		}
		if n > 0 {
			var users int64
			if err := d.QueryRow("SELECT COUNT(*) FROM user").Scan(&users); err != nil {
				return fmt.Errorf("counting the users already in the database: %w", err)
			}
			if users > 0 {
				return fmt.Errorf("the database already has %d users; pass --force to overwrite it", users)
			}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	head := make([]byte, 2)
	if _, err := io.ReadFull(f, head); err == nil && head[0] == 0x1f && head[1] == 0x8b {
		f.Seek(0, 0)
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	} else {
		f.Seek(0, 0)
	}
	sp := &splitter{r: bufio.NewReaderSize(r, 1<<20), delim: ";"}
	conn, err := d.Conn(bgctx())
	if err != nil {
		return err
	}
	defer conn.Close()
	count := 0
	for {
		stmt, err := sp.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(bgctx(), stmt); err != nil {
			short := stmt
			if len(short) > 200 {
				short = short[:200] + "..."
			}
			return fmt.Errorf("statement %d failed: %v\n%s", count+1, err, short)
		}
		count++
		if progress != nil && count%200 == 0 {
			progress(count)
		}
	}
	if progress != nil {
		progress(count)
	}
	return nil
}
