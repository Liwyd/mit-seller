// Package migrate moves an existing PHP bot install onto the Go bot: it reads
// the old config.php and crontab, and can load a mysqldump into the database.
package migrate

import (
	"bufio"
	"bytes"
	"compress/gzip"
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
		Domain:      strings.TrimRight(vars["domainhosts"], "/"),
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

// CronState reads which of the PHP bot's cron jobs were switched on for
// domain (the admin menu added/removed crontab lines).
func CronState(domain string) (map[string]bool, string) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return map[string]bool{}, ""
	}
	tab := string(out)
	has := func(file string) bool {
		for _, line := range strings.Split(tab, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "#") {
				continue
			}
			if strings.Contains(l, domain+"/cron/"+file) {
				return true
			}
		}
		return false
	}
	return map[string]bool{
		"test":   has("configtest.php"),
		"volume": has("cronvolume.php"),
		"time":   has("cronday.php"),
		"remove": has("removeexpire.php"),
		"card":   has("croncard.php"),
	}, tab
}

// RemoveCronLines drops this domain's /cron/*.php lines from the crontab
// (the Go bot runs those jobs itself). The old crontab is returned so it can
// be restored on rollback.
func RemoveCronLines(domain string) (string, error) {
	_, tab := CronState(domain)
	if tab == "" {
		return "", nil
	}
	var keep []string
	for _, line := range strings.Split(tab, "\n") {
		if strings.Contains(line, domain+"/cron/") && strings.Contains(line, "curl") {
			continue
		}
		keep = append(keep, line)
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(keep, "\n"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return tab, fmt.Errorf("crontab: %v %s", err, out)
	}
	return tab, nil
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

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// ImportSQL loads a mysqldump (.sql or .sql.gz) into d. Unless force is
// set it refuses when the database already holds users, so a live bot is
// never overwritten by mistake.
func ImportSQL(d *db.DB, path string, force bool, progress func(n int)) error {
	if !force {
		var n int64
		if err := d.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'user'").Scan(&n); err == nil && n > 0 {
			var users int64
			_ = d.QueryRow("SELECT COUNT(*) FROM user").Scan(&users)
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
