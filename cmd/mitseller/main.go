// Command mitseller is the Go version of the Mit Mirza bot.
//
//	mitseller serve        -c /etc/mitseller/bot7.env
//	mitseller migrate-php  --php-dir /var/www/html/botmirzapanel7 --out /etc/mitseller/bot7.env --listen 127.0.0.1:8107
//	mitseller import-sql   -c /etc/mitseller/bot7.env --file backup.sql [--force]
//	mitseller schema       -c /etc/mitseller/bot7.env
//	mitseller set-webhook  -c /etc/mitseller/bot7.env
//	mitseller keys         -c /etc/mitseller/bot7.env
//	mitseller check        -c /etc/mitseller/bot7.env
//	mitseller db-dump      -c /etc/mitseller/bot7.env --out /root/bot7.sql.gz
//	mitseller autopay-test -c ... parse | status | send <amount> | send-raw <text>
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Liwyd/mit-seller/internal/api"
	"github.com/Liwyd/mit-seller/internal/bot"
	"github.com/Liwyd/mit-seller/internal/config"
	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/migrate"
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/tg"
	"github.com/Liwyd/mit-seller/internal/web"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: mitseller <command> [flags]

commands:
  serve         run the bot (webhook, crons, management API)
  migrate-php   create a config from an existing PHP bot install
  import-sql    load a mysqldump backup into the configured database
  schema        create/upgrade the database tables (safe to repeat)
  set-webhook   point the Telegram webhook at this bot
  keys          print the management API keys for Mit Panel
  check         test the database, the schema and the bot token
  init-config   write a config file for a new bot (used by install.sh)
  panel-register connect this bot to Mit Panel and give it to its owner
  db-dump       back up the bot's database (mysqldump, gzipped)
  autopay-test  check the bank SMS parser / simulate a deposit
  admin-ids     print the bot admins' Telegram ids (main admin first)
  version       print the version`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "serve":
		cmdServe(args)
	case "migrate-php":
		cmdMigrate(args)
	case "import-sql":
		cmdImport(args)
	case "schema":
		cmdSchema(args)
	case "set-webhook":
		cmdWebhook(args)
	case "keys":
		cmdKeys(args)
	case "check":
		cmdCheck(args)
	case "init-config":
		cmdInitConfig(args)
	case "panel-register":
		cmdPanelRegister(args)
	case "db-dump":
		cmdDump(args)
	case "autopay-test":
		cmdAutopayTest(args)
	case "admin-ids":
		cmdAdminIDs(args)
	case "version", "--version", "-v":
		fmt.Println(bot.Version)
	default:
		usage()
	}
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

// envValue reads the first of the given environment variables that is set,
// so a current name and its pre-rebrand spelling both work.
func envValue(names ...string) string {
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok {
			return v
		}
	}
	return ""
}

func loadConfig(fs *flag.FlagSet, args []string) *config.Config {
	path := fs.String("c", envValue("MITSELLER_CONFIG", "NEXRABOT_CONFIG"), "config file")
	_ = fs.Parse(args)
	cfg, err := config.Load(*path)
	if err != nil {
		die("%v", err)
	}
	if err := cfg.Validate(); err != nil {
		die("%v", err)
	}
	return cfg
}

func openDB(cfg *config.Config) *db.DB {
	var d *db.DB
	var err error
	for i := 0; i < 10; i++ {
		d, err = db.Open(cfg.DSN())
		if err == nil {
			return d
		}
		time.Sleep(2 * time.Second)
	}
	die("database: %v", err)
	return nil
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	logger := log.New(os.Stdout, "", log.LstdFlags)
	db.Logger = logger.Printf
	d := openDB(cfg)
	if err := d.EnsureSchema(cfg.AdminID, func(s string) { logger.Println("schema:", s) }); err != nil {
		die("schema: %v", err)
	}
	if cfg.APIOwnerKey == "" || cfg.APIManagerKey == "" {
		logger.Println("warning: API_OWNER_KEY / API_MANAGER_KEY not set; the management API is disabled")
	}
	t := tg.New(cfg.BotToken, cfg.TelegramAPI, cfg.TelegramProxy)
	t.Log = logger
	b := bot.New(cfg, d, t, logger)
	b.Start()

	srv := &web.Server{B: b}
	if cfg.APIOwnerKey != "" || cfg.APIManagerKey != "" {
		srv.API = api.New(b)
	}
	stop := make(chan struct{})
	if !cfg.DisableCrons {
		b.RunCrons(stop)
	}
	hs := srv.Run(cfg.Listen)
	go func() {
		logger.Printf("mitseller %s listening on %s for @%s", bot.Version, cfg.Listen, cfg.BotUsername)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			die("http: %v", err)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	logger.Println("shutting down")
	close(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = hs.Shutdown(ctx)
	b.Stop()
}

func cmdMigrate(args []string) {
	fs := flag.NewFlagSet("migrate-php", flag.ExitOnError)
	dir := fs.String("php-dir", "", "directory of the PHP bot (holding config.php)")
	out := fs.String("out", "", "config file to write (e.g. /etc/mitseller/bot7.env)")
	listen := fs.String("listen", "127.0.0.1:8080", "address the Go bot will listen on")
	dataDir := fs.String("data-dir", "/var/lib/mitseller", "data directory")
	cleanCron := fs.Bool("clean-crontab", true, "remove this bot's /cron/*.php crontab lines (the Go bot runs them itself)")
	skipDB := fs.Bool("skip-db", false, "only write the config; do not touch the database or the crontab")
	_ = fs.Parse(args)
	if *dir == "" || *out == "" {
		die("--php-dir and --out are required")
	}
	cfg, err := migrate.ReadPHPConfig(strings.TrimRight(*dir, "/") + "/config.php")
	if err != nil {
		die("%v", err)
	}
	cfg.Listen = *listen
	cfg.DataDir = *dataDir
	cfg.LegacyPHPDir = *dir
	if old, err := config.Load(*out); err == nil && old.APIOwnerKey != "" {
		// re-running keeps the keys Mit Panel already uses
		cfg.APIOwnerKey, cfg.APIManagerKey, cfg.WebhookSecret = old.APIOwnerKey, old.APIManagerKey, old.WebhookSecret
	}
	if cfg.APIOwnerKey == "" {
		cfg.APIOwnerKey = config.RandomKey(24)
		cfg.APIManagerKey = config.RandomKey(24)
		cfg.WebhookSecret = config.RandomKey(16)
	}
	full, _ := config.Load("")
	cfg.DBSocket = full.DBSocket
	if err := cfg.Write(*out); err != nil {
		die("writing %s: %v", *out, err)
	}
	fmt.Println("config written:", *out)
	if *skipDB {
		return
	}

	d, err := db.Open(cfg.DSN())
	if err != nil {
		die("database (check the credentials in %s): %v", *out, err)
	}
	if err := d.EnsureSchema(cfg.AdminID, func(s string) { fmt.Println("  schema:", s) }); err != nil {
		die("schema: %v", err)
	}
	crons, _ := migrate.CronState(cfg.Domain)
	for name, on := range crons {
		v := "0"
		if on {
			v = "1"
		}
		if _, exists := d.KVOk("cron_" + name); !exists {
			d.SetKV("cron_"+name, v)
		}
	}
	fmt.Printf("cron switches carried over: %v\n", crons)
	if *cleanCron {
		if old, err := migrate.RemoveCronLines(cfg.Domain); err != nil {
			fmt.Println("warning:", err)
		} else if old != "" {
			_ = os.WriteFile(strings.TrimSuffix(*out, ".env")+".crontab.bak", []byte(old), 0600)
			fmt.Println("old /cron/*.php crontab lines removed (backup next to the config)")
		}
	}
	fmt.Println("done. Users, services, products, payments and settings stay exactly where they were.")
}

func cmdImport(args []string) {
	fs := flag.NewFlagSet("import-sql", flag.ExitOnError)
	file := fs.String("file", "", "mysqldump file (.sql or .sql.gz)")
	force := fs.Bool("force", false, "overwrite a database that already has users")
	cfg := loadConfig(fs, args)
	if *file == "" {
		die("--file is required")
	}
	d := openDB(cfg)
	err := migrate.ImportSQL(d, *file, *force, func(n int) { fmt.Printf("\r  %d statements", n) })
	fmt.Println()
	if err != nil {
		die("import failed: %v", err)
	}
	if err := d.EnsureSchema(cfg.AdminID, func(s string) { fmt.Println("  schema:", s) }); err != nil {
		die("schema: %v", err)
	}
	fmt.Println("imported and upgraded.")
}

func cmdSchema(args []string) {
	fs := flag.NewFlagSet("schema", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	d := openDB(cfg)
	if err := d.EnsureSchema(cfg.AdminID, func(s string) { fmt.Println(s) }); err != nil {
		die("%v", err)
	}
	fmt.Println("schema is up to date")
}

func cmdWebhook(args []string) {
	fs := flag.NewFlagSet("set-webhook", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	t := tg.New(cfg.BotToken, cfg.TelegramAPI, cfg.TelegramProxy)
	r := t.SetWebhook("https://"+cfg.Domain+"/index.php", cfg.WebhookSecret)
	if !r.OK {
		die("setWebhook failed: %s", r.Description)
	}
	fmt.Println("webhook set to https://" + cfg.Domain + "/index.php")
}

func cmdKeys(args []string) {
	fs := flag.NewFlagSet("keys", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	fmt.Println("bot:          @" + cfg.BotUsername)
	fmt.Println("api url:      https://" + cfg.Domain + "/api/v1")
	fmt.Println("owner key:    " + cfg.APIOwnerKey)
	fmt.Println("manager key:  " + cfg.APIManagerKey)
}

func cmdAutopayTest(args []string) {
	fs := flag.NewFlagSet("autopay-test", flag.ExitOnError)
	path := fs.String("c", envValue("MITSELLER_CONFIG", "NEXRABOT_CONFIG"), "config file")
	_ = fs.Parse(args)
	rest := fs.Args()
	sub := "status"
	if len(rest) > 0 {
		sub = rest[0]
	}
	samples := []string{
		"بانک ملت\nواریز:۱۲۵,۳۰۰ ریال\n۱۴۰۵/۰۷/۱۱-۱۲:۳۳\nمانده:۵,۴۳۲,۱۰۰ ریال",
		"ملی\n*1234\nمبلغ واریز 1253000 ریال\nمانده 9870000",
		"بانک سامان\nبرداشت 500,000 ریال\nمانده 1,000,000 ریال",
		"انتقال از 6219-****-1234\nبستانکار 1,253,000 ریال\nساعت 14:02",
	}
	if sub == "parse" {
		for i, s := range samples {
			p := bot.ParseSMS(s)
			fmt.Printf("--- sample %d\n   direction : %s\n   amount    : %s toman\n   candidates: %v\n   card      : %s\n", i+1, p.Direction, php.NumberFormat(float64(p.Amount), 0), p.Candidates, p.Card)
		}
		return
	}
	cfg, err := config.Load(*path)
	if err != nil {
		die("%v", err)
	}
	if err := cfg.Validate(); err != nil {
		die("%v", err)
	}
	d := openDB(cfg)
	t := tg.New(cfg.BotToken, cfg.TelegramAPI, cfg.TelegramProxy)
	b := bot.New(cfg, d, t, log.New(os.Stdout, "", 0))
	switch sub {
	case "status":
		s := b.AutopaySettings()
		fmt.Println("status    :", s.S("status"))
		fmt.Println("last seen :", s.S("last_seen"))
		fmt.Println("device    :", s.S("device_info"))
		fmt.Println(b.AutopayOpenOrders())
		fmt.Println(b.AutopayRecentSMS())
	case "send":
		if len(rest) < 2 || php.Intval(rest[1]) <= 0 {
			die("usage: mitseller autopay-test -c FILE send <amount in toman>")
		}
		amount := php.Intval(rest[1])
		body := "بانک تست\nواریز " + php.NumberFormat(float64(amount*10), 0) + " ریال\nمانده 1,000,000 ریال"
		fmt.Println(b.AutopayHandleSMS(body, "TEST", php.DateNow("Y-m-d H:i:s")))
	case "send-raw":
		if len(rest) < 2 {
			die("usage: mitseller autopay-test -c FILE send-raw \"<sms text>\"")
		}
		fmt.Println(b.AutopayHandleSMS(rest[1], "TEST", php.DateNow("Y-m-d H:i:s")))
	default:
		die("commands: parse | status | send <amount> | send-raw <text>")
	}
}

func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	failed := false
	d, err := db.Open(cfg.DSN())
	if err != nil {
		fmt.Println("database:  FAIL", err)
		failed = true
	} else {
		fmt.Printf("database:  ok (%d users, %d services, %d panels)\n",
			d.Count("SELECT COUNT(*) FROM user"), d.Count("SELECT COUNT(*) FROM invoice"), d.Count("SELECT COUNT(*) FROM marzban_panel"))
	}
	t := tg.New(cfg.BotToken, cfg.TelegramAPI, cfg.TelegramProxy)
	me, err := t.GetMe()
	if err != nil {
		fmt.Println("telegram:  FAIL", err)
		failed = true
	} else {
		fmt.Println("telegram:  ok @" + me.Username)
		if cfg.BotUsername != "" && !strings.EqualFold(me.Username, cfg.BotUsername) {
			fmt.Println("           warning: BOT_USERNAME in the config is @" + cfg.BotUsername)
		}
	}
	if failed {
		os.Exit(1)
	}
}

// cmdDump runs mysqldump with the config's credentials (passed through a
// private defaults file, never on the command line) and gzips the result.
func cmdDump(args []string) {
	fs := flag.NewFlagSet("db-dump", flag.ExitOnError)
	out := fs.String("out", "", "file to write (.sql.gz)")
	cfg := loadConfig(fs, args)
	if *out == "" {
		die("--out is required")
	}
	defs, err := os.CreateTemp("", "mitseller-my-*.cnf")
	if err != nil {
		die("%v", err)
	}
	defer os.Remove(defs.Name())
	fmt.Fprintf(defs, "[client]\nuser=%s\npassword=\"%s\"\n", cfg.DBUser, strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(cfg.DBPass))
	if cfg.DBSocket != "" {
		fmt.Fprintf(defs, "socket=%s\n", cfg.DBSocket)
	} else {
		fmt.Fprintf(defs, "host=%s\nport=%s\n", cfg.DBHost, cfg.DBPort)
	}
	defs.Close()
	f, err := os.OpenFile(*out+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		die("%v", err)
	}
	gz := gzip.NewWriter(f)
	dump := "mysqldump"
	if _, err := exec.LookPath(dump); err != nil {
		dump = "mariadb-dump"
	}
	cmd := exec.Command(dump, "--defaults-extra-file="+defs.Name(), "--single-transaction", "--quick",
		"--default-character-set=utf8mb4", "--no-tablespaces", cfg.DBName)
	cmd.Stdout = gz
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	if err := gz.Close(); err != nil && runErr == nil {
		runErr = err
	}
	if err := f.Close(); err != nil && runErr == nil {
		runErr = err
	}
	if runErr != nil {
		os.Remove(*out + ".tmp")
		die("dump failed: %v", runErr)
	}
	if err := os.Rename(*out+".tmp", *out); err != nil {
		die("%v", err)
	}
	st, _ := os.Stat(*out)
	fmt.Printf("database %s saved to %s (%d bytes)\n", cfg.DBName, *out, st.Size())
}

func cmdInitConfig(args []string) {
	fs := flag.NewFlagSet("init-config", flag.ExitOnError)
	out := fs.String("out", "", "config file to write")
	c := &config.Config{DBHost: "localhost", DBPort: "3306", DataDir: "/var/lib/mitseller"}
	fs.StringVar(&c.BotToken, "token", "", "bot token")
	fs.StringVar(&c.AdminID, "admin", "", "numeric Telegram id of the main admin")
	fs.StringVar(&c.Domain, "domain", "", "domain the webhook is served on")
	fs.StringVar(&c.BotUsername, "bot-username", "", "bot username")
	fs.StringVar(&c.MitSecret, "secret", "", "secret code for panel management")
	fs.StringVar(&c.DBName, "db-name", "", "database name")
	fs.StringVar(&c.DBUser, "db-user", "", "database user")
	fs.StringVar(&c.DBPass, "db-pass", envValue("MITSELLER_INIT_DB_PASS", "NEXRABOT_INIT_DB_PASS"), "database password (or MITSELLER_INIT_DB_PASS)")
	fs.StringVar(&c.Listen, "listen", "127.0.0.1:8080", "address to listen on")
	_ = fs.Parse(args)
	if *out == "" || c.BotToken == "" || c.AdminID == "" || c.Domain == "" || c.DBName == "" || c.DBUser == "" {
		die("--out, --token, --admin, --domain, --db-name and --db-user are required")
	}
	if _, err := os.Stat(*out); err == nil {
		die("%s already exists", *out)
	}
	c.APIOwnerKey, c.APIManagerKey, c.WebhookSecret = config.RandomKey(24), config.RandomKey(24), config.RandomKey(16)
	if err := c.Write(*out); err != nil {
		die("%v", err)
	}
	fmt.Println("config written:", *out)
}

// cmdAdminIDs prints the bot's admins' Telegram ids (the main admin first),
// one per line; install.sh uses it to find the id that is admin everywhere.
func cmdAdminIDs(args []string) {
	fs := flag.NewFlagSet("admin-ids", flag.ExitOnError)
	cfg := loadConfig(fs, args)
	d := openDB(cfg)
	seen := map[string]bool{}
	for _, id := range append([]string{cfg.AdminID}, d.AdminIDs()...) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			fmt.Println(id)
		}
	}
}

// cmdPanelRegister logs into Mit Panel as its superadmin and connects this
// bot there (or refreshes the connection); the panel gives it to the admin it
// belongs to. The password is read from MIT_PANEL_PASS (NEXRA_PANEL_PASS before the rebrand).
func cmdPanelRegister(args []string) {
	fs := flag.NewFlagSet("panel-register", flag.ExitOnError)
	panelURL := fs.String("panel", envValue("MIT_PANEL_URL", "NEXRA_PANEL_URL"), "Mit Panel address with its path, e.g. https://panel.example.com/dashboard")
	user := fs.String("user", envValue("MIT_PANEL_USER", "NEXRA_PANEL_USER"), "Mit Panel superadmin username")
	fetchApp := fs.Bool("fetch-app", true, "also have the panel fetch the auto-confirm app if it has none")
	ignore := fs.String("ignore-ids", envValue("MIT_PANEL_IGNORE_IDS", "NEXRA_PANEL_IGNORE_IDS"),
		"comma-separated Telegram ids that are admin on every bot (the panel owner's); not used to pick the bot's owner")
	cfg := loadConfig(fs, args)
	pass := envValue("MIT_PANEL_PASS", "NEXRA_PANEL_PASS")
	if *panelURL == "" || *user == "" || pass == "" {
		die("--panel, --user and MIT_PANEL_PASS are required")
	}
	base := strings.TrimRight(*panelURL, "/")
	hc := &http.Client{Timeout: 60 * time.Second}
	// a panel on this same server with its own (domain) certificate is
	// reached by 127.0.0.1, where that certificate can't match
	if u, err := url.Parse(base); err == nil && envValue("MIT_PANEL_INSECURE", "NEXRA_PANEL_INSECURE") == "1" &&
		(u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost") {
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}

	form := url.Values{"username": {*user}, "password": {pass}}
	res, err := hc.PostForm(base+"/login", form)
	if err != nil {
		die("panel unreachable: %v", err)
	}
	var login struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&login)
	res.Body.Close()
	if !login.Success || login.Data.AccessToken == "" {
		die("panel login failed: %s", login.Message)
	}
	call := func(method, path string, body any) (map[string]any, error) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, rd)
		req.Header.Set("Authorization", "Bearer "+login.Data.AccessToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		r, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		if r.StatusCode >= 300 || out["success"] != true {
			return out, fmt.Errorf("%v (HTTP %d)", out["message"], r.StatusCode)
		}
		return out, nil
	}
	ignoreIDs := []string{}
	for _, id := range strings.FieldsFunc(*ignore, func(r rune) bool { return r == ',' || r == ' ' }) {
		ignoreIDs = append(ignoreIDs, id)
	}
	out, err := call("POST", "/sales-bots/register", map[string]any{
		"url": cfg.PublicURL, "owner_key": cfg.APIOwnerKey, "manager_key": cfg.APIManagerKey,
		"ignore_ids": ignoreIDs,
	})
	if err != nil {
		die("panel refused the bot: %v", err)
	}
	data, _ := out["data"].(map[string]any)
	if who, _ := data["assigned_to"].(string); who != "" {
		fmt.Printf("connected to Mit Panel and given to %s (%v)\n", who, data["reason"])
	} else {
		fmt.Printf("connected to Mit Panel; NOT given to any admin: %v. Assign it in Bot → اتصال ربات‌ها.\n", data["reason"])
	}
	if *fetchApp {
		if info, err := call("GET", "/sales-bots/autopay-app/info", nil); err == nil {
			if d, _ := info["data"].(map[string]any); d["available"] != true {
				if _, err := call("POST", "/sales-bots/autopay-app/fetch", nil); err != nil {
					fmt.Println("note: the panel could not fetch the auto-confirm app:", err)
				} else {
					fmt.Println("auto-confirm app fetched into the panel")
				}
			}
		}
	}
}
