// Package config reads one bot instance's settings from an env-style file
// (KEY=value per line) with environment variables taking precedence.
package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

type Config struct {
	Path string

	BotToken    string // $APIKEY
	AdminID     string // $adminnumber
	Domain      string // $domainhosts (host and optional path, no scheme, no trailing /)
	BotUsername string // $usernamebot
	NexraSecret string // NEXRA_SECRET_CODE

	DBHost   string
	DBPort   string
	DBSocket string
	DBName   string
	DBUser   string
	DBPass   string

	Listen        string // address the HTTP server binds to
	WebhookSecret string // Telegram secret_token, checked on every update
	CheckTGIP     bool   // also accept updates only from Telegram's IP ranges
	TelegramAPI   string // base URL, overridable for tests
	APIOwnerKey   string // management API: everything
	APIManagerKey string // management API: everything except panel/admin management
	DataDir       string
	PublicURL     string // https://Domain unless overridden
	LegacyPHPDir  string // where the old PHP bot lived (for reference/rollback)
	Workers       int
	DisableCrons  bool
	SyncUpdates   bool // process an update before answering the webhook (tests)
	TelegramProxy string
	LogLevel      string
}

func parseFile(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			if v[0] == '"' {
				if uq, err := strconv.Unquote(v); err == nil {
					v = uq
				} else {
					v = v[1 : len(v)-1]
				}
			} else {
				v = v[1 : len(v)-1]
			}
		}
		out[k] = v
	}
	return out, sc.Err()
}

// Load reads path (may be empty to use only the environment).
func Load(path string) (*Config, error) {
	vals := map[string]string{}
	if path != "" {
		v, err := parseFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		vals = v
	}
	get := func(k, def string) string {
		if e, ok := os.LookupEnv("NEXRABOT_" + k); ok {
			return e
		}
		if v, ok := vals[k]; ok && v != "" {
			return v
		}
		return def
	}
	c := &Config{
		Path:          path,
		BotToken:      get("BOT_TOKEN", ""),
		AdminID:       get("ADMIN_ID", ""),
		Domain:        strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(get("DOMAIN", ""), "https://"), "http://"), "/"),
		BotUsername:   strings.TrimPrefix(get("BOT_USERNAME", ""), "@"),
		NexraSecret:   get("NEXRA_SECRET", ""),
		DBHost:        get("DB_HOST", "localhost"),
		DBPort:        get("DB_PORT", "3306"),
		DBSocket:      get("DB_SOCKET", ""),
		DBName:        get("DB_NAME", ""),
		DBUser:        get("DB_USER", ""),
		DBPass:        get("DB_PASS", ""),
		Listen:        get("LISTEN", "127.0.0.1:8080"),
		WebhookSecret: get("WEBHOOK_SECRET", ""),
		CheckTGIP:     get("CHECK_TELEGRAM_IP", "1") != "0",
		TelegramAPI:   strings.TrimRight(get("TELEGRAM_API", "https://api.telegram.org"), "/"),
		APIOwnerKey:   get("API_OWNER_KEY", ""),
		APIManagerKey: get("API_MANAGER_KEY", ""),
		DataDir:       get("DATA_DIR", "/var/lib/nexrabot"),
		PublicURL:     strings.TrimRight(get("PUBLIC_URL", ""), "/"),
		LegacyPHPDir:  get("LEGACY_PHP_DIR", ""),
		DisableCrons:  get("DISABLE_CRONS", "0") == "1",
		SyncUpdates:   get("SYNC_UPDATES", "0") == "1",
		TelegramProxy: get("TELEGRAM_PROXY", ""),
		LogLevel:      get("LOG_LEVEL", "info"),
	}
	c.Workers, _ = strconv.Atoi(get("WORKERS", "16"))
	if c.Workers <= 0 {
		c.Workers = 16
	}
	if c.PublicURL == "" && c.Domain != "" {
		c.PublicURL = "https://" + c.Domain
	}
	if c.DBSocket == "" && (c.DBHost == "localhost" || c.DBHost == "") {
		for _, s := range []string{"/var/run/mysqld/mysqld.sock", "/run/mysqld/mysqld.sock", "/tmp/mysql.sock"} {
			if _, err := os.Stat(s); err == nil {
				c.DBSocket = s
				break
			}
		}
	}
	return c, nil
}

// Validate reports the settings a running bot cannot do without.
func (c *Config) Validate() error {
	var missing []string
	for k, v := range map[string]string{"BOT_TOKEN": c.BotToken, "ADMIN_ID": c.AdminID, "DOMAIN": c.Domain, "DB_NAME": c.DBName, "DB_USER": c.DBUser} {
		if v == "" {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("missing settings: %s", strings.Join(missing, ", "))
	}
	return nil
}

// DSN is the go-sql-driver/mysql connection string.
func (c *Config) DSN() string {
	addr := "tcp(" + c.DBHost + ":" + c.DBPort + ")"
	if c.DBHost == "localhost" {
		addr = "tcp(127.0.0.1:" + c.DBPort + ")"
	}
	if c.DBSocket != "" {
		addr = "unix(" + c.DBSocket + ")"
	}
	return fmt.Sprintf("%s:%s@%s/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=false&loc=Local&timeout=10s&readTimeout=60s&writeTimeout=60s&maxAllowedPacket=0",
		c.DBUser, c.DBPass, addr, c.DBName)
}

// RandomKey returns n random bytes as hex.
func RandomKey(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Write stores the config in env format at path (0600).
func (c *Config) Write(path string) error {
	lines := []string{
		"# nexrabot instance settings",
		"BOT_TOKEN=" + c.BotToken,
		"ADMIN_ID=" + c.AdminID,
		"DOMAIN=" + c.Domain,
		"BOT_USERNAME=" + c.BotUsername,
		"NEXRA_SECRET=" + quote(c.NexraSecret),
		"",
		"DB_HOST=" + c.DBHost,
		"DB_PORT=" + c.DBPort,
		"DB_SOCKET=" + c.DBSocket,
		"DB_NAME=" + c.DBName,
		"DB_USER=" + c.DBUser,
		"DB_PASS=" + quote(c.DBPass),
		"",
		"LISTEN=" + c.Listen,
		"WEBHOOK_SECRET=" + c.WebhookSecret,
		"API_OWNER_KEY=" + c.APIOwnerKey,
		"API_MANAGER_KEY=" + c.APIManagerKey,
		"DATA_DIR=" + c.DataDir,
	}
	if c.LegacyPHPDir != "" {
		lines = append(lines, "LEGACY_PHP_DIR="+c.LegacyPHPDir)
	}
	if c.TelegramAPI != "" && c.TelegramAPI != "https://api.telegram.org" {
		lines = append(lines, "TELEGRAM_API="+c.TelegramAPI)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		return err
	}
	// rewriting an existing config keeps its mode and group, so a service
	// running as its own user (root:nexrabot 0640) can still read it
	if st, err := os.Stat(path); err == nil {
		_ = os.Chmod(tmp, st.Mode().Perm())
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			_ = os.Chown(tmp, int(sys.Uid), int(sys.Gid))
		}
	}
	return os.Rename(tmp, path)
}

func quote(s string) string {
	if s == "" || strings.ContainsAny(s, " #'\"\\$`") {
		return strconv.Quote(s)
	}
	return s
}
