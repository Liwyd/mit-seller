package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Liwyd/mit-seller/internal/config"
	"github.com/Liwyd/mit-seller/internal/migrate"
)

// bundleManifest is the header of a bot moved between servers. It carries
// everything the importing server cannot know about the source bot, and
// nothing it should not: the database itself travels as dump.sql.gz next to
// it. The bot token is in here because the moved bot is the *same* Telegram
// bot — a bundle is therefore as sensitive as the config file it becomes, and
// is written 0600 for the same reason.
type bundleManifest struct {
	Version     int             `json:"version"`
	Flavor      string          `json:"flavor"` // "" = nexra/mirzapanel, "mirza_pro"
	Domain      string          `json:"domain"`
	AdminID     string          `json:"admin_id"`
	BotUsername string          `json:"bot_username"`
	BotToken    string          `json:"bot_token"`
	SourceDB    string          `json:"source_db"` // name only, for the report
	MitSecret   string          `json:"mit_secret,omitempty"`
	Cron        map[string]bool `json:"cron,omitempty"`
	Source      string          `json:"source"` // "php" or "go"
	PHPDir      string          `json:"php_dir,omitempty"`
	ExportedAt  string          `json:"exported_at"`
}

const bundleVersion = 1

// dumpSQLGz runs mysqldump with the config's credentials (passed through a
// private defaults file, never on the command line) and writes the gzipped
// result to w.
func dumpSQLGz(cfg *config.Config, w io.Writer) error {
	defs, err := os.CreateTemp("", "mitseller-my-*.cnf")
	if err != nil {
		return err
	}
	defer os.Remove(defs.Name())
	fmt.Fprintf(defs, "[client]\nuser=%s\npassword=\"%s\"\n", cfg.DBUser, strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(cfg.DBPass))
	if cfg.DBSocket != "" {
		fmt.Fprintf(defs, "socket=%s\n", cfg.DBSocket)
	} else {
		fmt.Fprintf(defs, "host=%s\nport=%s\n", cfg.DBHost, cfg.DBPort)
	}
	defs.Close()
	dump := "mysqldump"
	if _, err := exec.LookPath(dump); err != nil {
		dump = "mariadb-dump"
	}
	gz := gzip.NewWriter(w)
	cmd := exec.Command(dump, "--defaults-extra-file="+defs.Name(), "--single-transaction", "--quick",
		"--default-character-set=utf8mb4", "--no-tablespaces", cfg.DBName)
	cmd.Stdout = gz
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	if err := gz.Close(); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

// cmdExportBundle packs a bot up so it can be installed on another server. It
// reads the source without changing it: the PHP bot (or the Go bot) keeps
// running, keeps its crontab and keeps its nginx site. The bundle holds the
// database dump, a copy of config.php when there was one, and a manifest with
// the settings the other server needs.
func cmdExportBundle(args []string) {
	fs := flag.NewFlagSet("export-bundle", flag.ExitOnError)
	phpDir := fs.String("php-dir", "", "directory of the PHP bot to export (holding config.php)")
	out := fs.String("out", "", "bundle file to write (.tar.gz)")
	cfgPath := fs.String("c", envValue("MITSELLER_CONFIG", "NEXRABOT_CONFIG"), "config file of a Go bot to export (instead of --php-dir)")
	_ = fs.Parse(args)
	if *out == "" {
		die("--out is required")
	}
	if *phpDir != "" && *cfgPath != "" {
		die("give either --php-dir or -c, not both")
	}

	var cfg *config.Config
	var flavor migrate.Flavor
	source, phpDirReal := "go", ""
	switch {
	case *phpDir != "":
		roots, err := migrate.DetectBotRoots([]string{*phpDir}, nil)
		if err != nil {
			die("%v", err)
		}
		if len(roots) == 0 {
			die("%s has no config.php", *phpDir)
		}
		phpDirReal = roots[0].Dir
		flavor = roots[0].Flavor
		cfg, err = migrate.ReadPHPConfig(phpDirReal + "/config.php")
		if err != nil {
			die("%v", err)
		}
		source = "php"
	default:
		loaded, err := config.Load(*cfgPath)
		if err != nil {
			die("%v", err)
		}
		if err := loaded.Validate(); err != nil {
			die("%v", err)
		}
		cfg = loaded
		// a Go bot's database is already in the Go shape; there is nothing
		// for the importer's compat layer to rename
		flavor = migrate.FlavorLegacy
	}

	rep := &migrate.Report{}
	d := openDB(cfg)
	crons, _, err := migrate.CronState(cfg.Domain, flavor, d)
	if err != nil {
		// the cron switches are a convenience, not the payload: a bot whose
		// cron state cannot be read still moves, it just moves with no switches
		rep.Warn = append(rep.Warn, "cron switches: "+err.Error())
		crons = nil
	}

	man := bundleManifest{
		Version:     bundleVersion,
		Flavor:      string(flavor),
		Domain:      cfg.Domain,
		AdminID:     cfg.AdminID,
		BotUsername: cfg.BotUsername,
		BotToken:    cfg.BotToken,
		SourceDB:    cfg.DBName,
		MitSecret:   cfg.MitSecret,
		Cron:        crons,
		Source:      source,
		PHPDir:      phpDirReal,
		ExportedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	manJSON, err := json.MarshalIndent(&man, "", "  ")
	if err != nil {
		die("%v", err)
	}

	tmp := *out + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		die("%v", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	write := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0600, Size: int64(len(data)), ModTime: time.Now(),
		}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := write("manifest.json", append(manJSON, '\n')); err != nil {
		die("%v", err)
	}
	if source == "php" {
		raw, err := os.ReadFile(filepath.Join(phpDirReal, "config.php"))
		if err != nil {
			die("%v", err)
		}
		if err := write("config.php", raw); err != nil {
			die("%v", err)
		}
	}
	if err := dumpSQLGz(cfg, tw); err != nil {
		os.Remove(tmp)
		die("dump failed: %v", err)
	}
	if err := tw.Close(); err != nil {
		die("%v", err)
	}
	if err := gz.Close(); err != nil {
		die("%v", err)
	}
	if err := f.Close(); err != nil {
		die("%v", err)
	}
	if err := os.Rename(tmp, *out); err != nil {
		die("%v", err)
	}
	os.Chmod(*out, 0600)

	st, _ := os.Stat(*out)
	fmt.Printf("bot %s (@%s) packed into %s (%d bytes)\n", man.Domain, man.BotUsername, *out, st.Size())
	for _, l := range rep.String() {
		fmt.Print(l)
	}
	fmt.Println()
	fmt.Println("the bundle holds this bot's token and its whole user database — treat it like the")
	fmt.Println("config file, and delete it once the other server has taken over.")
}

// cmdImportBundle reads a bundle made by export-bundle and turns it into a
// config file plus a loaded database on this server. It writes the config and
// fills the database; the caller (install.sh) has already created that
// database and its user, and decides what to do about nginx, the service and
// the webhook.
func cmdImportBundle(args []string) {
	fs := flag.NewFlagSet("import-bundle", flag.ExitOnError)
	file := fs.String("file", "", "bundle made by export-bundle (.tar.gz)")
	out := fs.String("out", "", "config file to write (e.g. /etc/mitseller/bot7.env)")
	listen := fs.String("listen", "127.0.0.1:8080", "address the Go bot will listen on")
	dataDir := fs.String("data-dir", "/var/lib/mitseller", "data directory")
	domain := fs.String("domain", "", "domain this bot will be served on (default: the source bot's domain)")
	dbName := fs.String("db-name", "", "database to load the dump into")
	dbUser := fs.String("db-user", "", "database user")
	dbHost := fs.String("db-host", "localhost", "database host")
	dbPort := fs.String("db-port", "3306", "database port")
	reportPath := fs.String("report", "", "file to write the report to (default: next to --out)")
	_ = fs.Parse(args)
	if *file == "" || *out == "" || *dbName == "" || *dbUser == "" {
		die("--file, --out, --db-name and --db-user are required")
	}
	if _, err := os.Stat(*out); err == nil {
		die("%s already exists; refusing to overwrite an installed bot", *out)
	}

	// the bundle is read into a scratch directory: manifest.json and the dump
	// are pulled out, config.php is kept only as a reference for the operator
	tmp, err := os.MkdirTemp("", "mitseller-bundle-")
	if err != nil {
		die("%v", err)
	}
	defer os.RemoveAll(tmp)
	if err := untarBundle(*file, tmp); err != nil {
		die("%v", err)
	}
	rawMan, err := os.ReadFile(filepath.Join(tmp, "manifest.json"))
	if err != nil {
		die("that file is not a mitseller bundle: %v", err)
	}
	var man bundleManifest
	if err := json.Unmarshal(rawMan, &man); err != nil {
		die("manifest.json: %v", err)
	}
	if man.Version != bundleVersion {
		die("bundle version %d, but this binary reads version %d", man.Version, bundleVersion)
	}
	if man.BotToken == "" || man.AdminID == "" {
		die("the bundle's manifest has no bot token or no admin id")
	}
	dumpPath := filepath.Join(tmp, "dump.sql.gz")
	if _, err := os.Stat(dumpPath); err != nil {
		die("the bundle has no dump.sql.gz")
	}

	flavor := migrate.Flavor(man.Flavor)
	if man.Flavor != "" && flavor != migrate.FlavorMirza {
		die("the bundle says flavour %q, which this binary does not know", man.Flavor)
	}

	cfg := &config.Config{
		BotToken:    man.BotToken,
		AdminID:     man.AdminID,
		Domain:      strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(man.Domain, "https://"), "http://"), "/"),
		BotUsername: man.BotUsername,
		MitSecret:   man.MitSecret,
		DBHost:      *dbHost,
		DBPort:      *dbPort,
		DBName:      *dbName,
		DBUser:      *dbUser,
		DBPass:      os.Getenv("MITSELLER_INIT_DB_PASS"),
		Listen:      *listen,
		DataDir:     *dataDir,
	}
	if *domain != "" {
		cfg.Domain = strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(*domain, "https://"), "http://"), "/")
	}
	if cfg.Domain == "" {
		die("the bundle has no domain and --domain was not given")
	}
	if strings.Contains(cfg.Domain, "/") {
		die("the bot is served under a path (%q). mit-seller needs a site that serves it at its own domain root", cfg.Domain)
	}
	if cfg.DBPass == "" {
		die("MITSELLER_INIT_DB_PASS is not set")
	}
	// a fresh secret and fresh keys: this is a new bot *instance*, even though
	// it is the same Telegram bot. The panel must not be handed keys that
	// another server is already using.
	if cfg.MitSecret == "" {
		cfg.MitSecret = os.Getenv("MIT_SECRET")
	}
	if cfg.MitSecret == "" {
		cfg.MitSecret = config.RandomKey(32)
		fmt.Println()
		fmt.Println("  MIT_SECRET was generated for this bot. Store it now — it gates panel management")
		fmt.Println("  from inside Telegram.")
		fmt.Println()
		fmt.Println("    MIT_SECRET=" + cfg.MitSecret)
		fmt.Println()
	}
	cfg.APIOwnerKey = config.RandomKey(24)
	cfg.APIManagerKey = config.RandomKey(24)
	cfg.WebhookSecret = config.RandomKey(16)
	// held dark until the webhook is cut over: while the old server still
	// answers Telegram, a live copy running its own crons would message the
	// same users a second time from stale data
	cfg.DisableCrons = true

	if err := cfg.Write(*out); err != nil {
		die("writing %s: %v", *out, err)
	}
	fmt.Println("config written:", *out)

	rep := &migrate.Report{}
	rep.Info = append(rep.Info, fmt.Sprintf("moved from %s (a %s bot, exported %s)", man.Source, man.Source, man.ExportedAt))
	if man.PHPDir != "" {
		rep.Info = append(rep.Info, "the source PHP directory was "+man.PHPDir)
	}
	d := openDB(cfg)
	if err := migrate.ImportSQL(d, dumpPath, false, func(n int) { fmt.Printf("\r  %d statements", n) }); err != nil {
		fmt.Println()
		die("loading the database: %v", err)
	}
	fmt.Println()
	if err := d.EnsureSchema(cfg.AdminID, func(s string) { fmt.Println("  schema:", s) }); err != nil {
		die("schema: %v", err)
	}
	if err := migrate.ApplyCompat(d, flavor, rep); err != nil {
		die("adapting the database: %v", err)
	}
	// the cron switches travel in the manifest because on the source they may
	// have lived in a crontab this server cannot see; reading them back off
	// the imported database is only right for mirza_pro
	crons := man.Cron
	if crons == nil {
		if c, _, err := migrate.CronState(cfg.Domain, flavor, d); err == nil {
			crons = c
		}
	}
	for name, on := range crons {
		v := "0"
		if on {
			v = "1"
		}
		if _, exists := d.KVOk("cron_" + name); !exists {
			d.SetKV("cron_"+name, v)
		}
	}
	if len(crons) > 0 {
		rep.Info = append(rep.Info, fmt.Sprintf("cron switches carried over: %v", crons))
	}
	rep.Info = append(rep.Info, "the webhook was NOT moved; the old server still answers Telegram until you cut over")
	rep.Info = append(rep.Info, "cron jobs are held off here so the old server does not get a second voice; they start at cutover")
	rep.Info = append(rep.Info, "MIT_SECRET and the API keys are new to this server")

	path := *reportPath
	if path == "" {
		path = strings.TrimSuffix(*out, ".env") + ".import-report.txt"
	}
	if err := os.WriteFile(path, []byte(rep.String()), 0600); err != nil {
		fmt.Println("warning: could not write the report:", err)
	} else {
		fmt.Println()
		fmt.Print(rep.String())
		fmt.Println("report written:", path)
	}
	if rep.HasFatal() {
		die("the import stopped; read the report above")
	}
	fmt.Println("done. The bot is loaded here but is not yet answering Telegram — cut it over when ready.")
}

// untarBundle unpacks a .tar.gz into dir. Only regular files are accepted; a
// bundle is data, never a program, and nothing in it is executed.
func untarBundle(file, dir string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			continue
		}
		dst := filepath.Join(dir, name)
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
}
