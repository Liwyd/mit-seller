package migrate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Liwyd/mit-seller/internal/db"
)

// Report collects what a migration changed, what it could not carry over and
// what the operator has to finish by hand. It is printed after the migration
// and written next to the new config, so nothing is lost in a scrollback.
type Report struct {
	Info  []string
	Warn  []string
	Fatal []string
}

func (r *Report) infof(format string, a ...any) { r.Info = append(r.Info, fmt.Sprintf(format, a...)) }
func (r *Report) warnf(format string, a ...any) { r.Warn = append(r.Warn, fmt.Sprintf(format, a...)) }
func (r *Report) fatalf(format string, a ...any) {
	r.Fatal = append(r.Fatal, fmt.Sprintf(format, a...))
}

// HasFatal reports whether the migration must be refused.
func (r *Report) HasFatal() bool { return len(r.Fatal) > 0 }

// String renders the report for the terminal and for the report file.
func (r *Report) String() string {
	var b strings.Builder
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s\n", title)
		for _, l := range lines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}
	section("what the migration changed", r.Info)
	section("what does not come across", r.Warn)
	section("migration stopped here", r.Fatal)
	if b.Len() == 0 {
		return "nothing to adjust\n"
	}
	return b.String()
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ident guards the table and column names interpolated into SQL below. Every
// caller passes one of the constants in this file; this is defence in depth,
// matching db.ident.
func ident(s string) string {
	if !identRe.MatchString(s) {
		panic("migrate: bad identifier " + s)
	}
	return "`" + s + "`"
}

func columnExists(d *db.DB, table, col string) (bool, error) {
	var n int64
	err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
        WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`, table, col).Scan(&n)
	if err != nil {
		// Swallowing this would make every copy and every value mapping below
		// a silent no-op while the report still said the migration completed.
		return false, fmt.Errorf("looking up %s.%s: %w", table, col, err)
	}
	return n > 0, nil
}

// copyColumn fills the empty cells of dst from src. blanks lists the values
// that also count as unset: the schema upgrade may have just added dst and
// filled it with the Go bot's own default, which would otherwise hide the old
// data. It only runs when both columns are present, so a database that never
// had the old column is left alone. 'none' is skipped because mirza_pro
// bulk-filled Payment_report.id_invoice with it when it added the column.
func copyColumn(d *db.DB, table, src, dst string, r *Report, blanks ...string) error {
	if strings.EqualFold(src, dst) {
		return nil
	}
	hasSrc, err := columnExists(d, table, src)
	if err != nil {
		return err
	}
	if !hasSrc {
		return nil
	}
	hasDst, err := columnExists(d, table, dst)
	if err != nil {
		return err
	}
	if !hasDst {
		return nil
	}
	empty := "(" + ident(dst) + " IS NULL OR " + ident(dst) + " = ''"
	args := []any{}
	for _, b := range blanks {
		empty += " OR " + ident(dst) + " = ?"
		args = append(args, b)
	}
	empty += ")"
	res, err := d.Exec("UPDATE "+ident(table)+" SET "+ident(dst)+" = "+ident(src)+
		" WHERE "+empty+" AND "+ident(src)+" IS NOT NULL AND "+ident(src)+" <> ''"+
		" AND "+ident(src)+" <> 'none'", args...)
	if err != nil {
		return fmt.Errorf("%s: copying %s into %s: %w", table, src, dst, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.infof("%s: carried %d row(s) from the old %s column into %s", table, n, src, dst)
	}
	return nil
}

// mapValues rewrites the old PHP wording of a switch into the 1/0 (or the
// panel vocabulary) the Go bot reads. It stops at the first value the Go bot
// would already understand, so running it twice changes nothing.
func mapValues(d *db.DB, table, col string, m map[string]string, r *Report) error {
	ok, err := columnExists(d, table, col)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	from := make([]string, 0, len(m))
	for k := range m {
		from = append(from, k)
	}
	sort.Strings(from)
	var total int64
	for _, old := range from {
		res, err := d.Exec("UPDATE "+ident(table)+" SET "+ident(col)+" = ? WHERE "+ident(col)+" = ?",
			m[old], old)
		if err != nil {
			return fmt.Errorf("%s.%s: reading the old value %q: %w", table, col, old, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			total += n
		}
	}
	if total > 0 {
		r.infof("%s: read %d row(s) in the old wording of %s", table, total, col)
	}
	return nil
}

// settingSwitches are the PHP words mirza_pro wrote into the switches the Go
// bot reads as 1/0. The same map is applied to every switch column, because a
// column never holds more than its own vocabulary.
var settingSwitches = map[string]string{
	"botstatuson":            "1",
	"botstatusoff":           "0",
	"rolleon":                "1",
	"rolloff":                "0",
	"onAuthenticationphone":  "1",
	"offAuthenticationphone": "0",
	"onAuthenticationiran":   "1",
	"offAuthenticationiran":  "0",
	"onnotuser":              "1",
	"offnotuser":             "0",
	"oncategory":             "1",
	"offcategory":            "0",
	"onverify":               "1",
	"offverify":              "0",
	"oncopycart":             "1",
	"offcopycart":            "0",
}

// settingSwitchCols are the switch columns the Go bot reads as 1/0.
var settingSwitchCols = []string{
	"Bot_Status", "roll_Status", "NotUser", "help_Status", "get_number",
	"iran_number", "status_verify", "statuscategory", "copy_cart",
}

// panelSwitchCols: same idea for marzban_panel, whose switches keep their
// own wording (activepanel, ononhold, …).
var panelSwitchCols = map[string]map[string]string{
	"status":       {"active": "activepanel", "disable": "disablepanel"},
	"onholdstatus": {"on_extend": "ononhold", "off_extend": "offonhold"},
	"type":         {"alireza_single": "alireza", "WGDashboard": "wgdashboard"},
}

// unsupportedPanels are marzban_panel.type values mirza_pro could drive and
// the Go bot cannot. Their rows are kept exactly as they are and reported.
var unsupportedPanels = []string{"hiddify", "ibsng", "Manualsale", "eylanpanel"}

// unsupportedGateways are the payment providers mirza_pro could switch on and
// the Go bot does not implement. The keys are the real PaySetting names its
// admin menu writes — several of the plausible-sounding ones (statusplisio,
// statusiranpay1, statusperfect) are not keys at all, and a warning built on
// a key that does not exist can never fire. The Go bot implements card
// (Cartstatus), NOWPayments (nowpaymentstatus, which mirza_pro labels
// "plisio"), digi (digistatus) and aqayepardakht (statusaqayepardakht).
var unsupportedGateways = []struct {
	name string
	keys []string
}{
	{"zarinpal", []string{"zarinpalstatus"}},
	{"zarinpey", []string{"zarinpeystatus"}},
	{"tronado", []string{"statustarnado"}},
	{"SwapWallet (nSolutions)", []string{"statusSwapWallet"}},
	{"Telegram Stars", []string{"statusstar"}},
	{"iranpay3", []string{"statusiranpay3"}},
	{"payment without verification (paymentnv/donatekon)", []string{"paymentstatussnotverify", "marchentpaynotverify"}},
	{"floryapay", []string{"marchent_floypay"}},
	{"the second nowpayments toggle", []string{"statusnowpayment"}},
}

// legacyOnlyPaths are the URLs mirza_pro served that the Go bot has no
// handler for. Users' old subscription links and the mini-app address point
// here, so they are listed for every mirza_pro migration.
var legacyOnlyPaths = []string{
	"/sub/", "/app/", "/panel/", "/vpnbot/", "/webhooks.php", "/api/",
}

// migratedCompatKey marks that ApplyCompat has already carried the mirza_pro
// columns across. Without it a second run would copy the old value back over
// an admin's change: the copy only fills cells that still hold the schema
// default, and an admin turning a switch off puts it right back on that
// default.
const migratedCompatKey = "migrate.compat.mirza_pro"

// ApplyCompat puts a mirza_pro database into the shape the Go bot expects.
// mirza_pro renamed columns, changed how it spelled its switches and kept a
// few features the Go bot has none of; this copies what can be copied and
// writes down what cannot. It never deletes a row and never drops a column.
// It is a no-op for every other flavour, whose databases the Go bot's own
// schema upgrade already understands.
//
// The destructive half — the copies and the value rewrites — runs only once,
// recorded in mit_kv. Everything that merely reports runs on every migration,
// so a report is still produced when the command is re-run.
func ApplyCompat(d *db.DB, flavor Flavor, r *Report) error {
	if r == nil {
		r = &Report{}
	}
	if flavor != FlavorMirza {
		return nil
	}
	r.infof("source is mirza_pro: carried over what the Go bot reads, listed what it does not")

	if _, done := d.KVOk(migratedCompatKey); done {
		r.infof("the mirza_pro columns were already carried across by an earlier migration; only the report is refreshed")
	} else {
		if err := copyMirzaColumns(d, r); err != nil {
			return err
		}
		if err := renameTestServices(d, r); err != nil {
			return err
		}
		if err := fixProductCategories(d, r); err != nil {
			return err
		}
		d.SetKV(migratedCompatKey, "1")
	}

	if err := reportUnsupportedPanels(d, r); err != nil {
		return err
	}
	reportUnsupportedGateways(d, r)
	reportDroppedMirzaSettings(d, r)
	r.warnf("the Go bot does not serve %s — old links to them stop working", strings.Join(legacyOnlyPaths, ", "))

	var orphans int64
	if err := d.QueryRow(`SELECT COUNT(*) FROM product p
        WHERE p.category IS NOT NULL AND p.category <> '' AND p.category <> '0'
          AND NOT EXISTS (SELECT 1 FROM category c WHERE c.id = p.category)`).Scan(&orphans); err != nil {
		return fmt.Errorf("product: counting rows whose category no longer resolves: %w", err)
	}
	if orphans > 0 {
		r.warnf("product: %d row(s) carry a category name with no matching category; set their category again in the admin panel", orphans)
	}
	return nil
}

// copyMirzaColumns carries the columns mirza_pro named differently over into
// the names the Go bot reads. The trailing arguments of each copy are the
// defaults the schema upgrade has just filled the new column with; counting
// them as empty is what lets the old data through.
func copyMirzaColumns(d *db.DB, r *Report) error {
	// mirza_pro's statusnamecustom is deliberately not copied: it is an
	// on/off switch there, while the Go bot's namecustome is the free-text
	// prefix it puts in front of generated client ids.
	copies := []struct {
		table, src, dst string
		blanks          []string
	}{
		{"Payment_report", "id_invoice", "invoice", nil},
		{"marzban_panel", "config", "configManual", []string{"offconfig"}},
		{"marzban_panel", "status_extend", "onholdstatus", []string{"offonhold"}},
		{"setting", "verifybucodeuser", "status_verify", []string{"0"}},
		{"setting", "statuscopycart", "copy_cart", []string{"0"}},
	}
	for _, c := range copies {
		if err := copyColumn(d, c.table, c.src, c.dst, r, c.blanks...); err != nil {
			return err
		}
	}
	// ---- switches written in mirza_pro's wording
	for _, col := range settingSwitchCols {
		if err := mapValues(d, "setting", col, settingSwitches, r); err != nil {
			return err
		}
	}
	for col, m := range panelSwitchCols {
		if err := mapValues(d, "marzban_panel", col, m, r); err != nil {
			return err
		}
	}
	return nil
}

// renameTestServices renames mirza_pro's Persian test-service label to the
// "usertest" the Go bot skips from volume, expiry and stats.
func renameTestServices(d *db.DB, r *Report) error {
	res, err := d.Exec(`UPDATE product SET name_product = 'usertest' WHERE name_product = 'سرویس تست'`)
	if err != nil {
		return fmt.Errorf("product: renaming the test service: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.infof("product: %d test service(s) renamed from \"سرویس تست\" to \"usertest\"", n)
		r.warnf("product: test services are now called \"usertest\"; rename them again in the admin panel if you want the old label")
	}
	res, err = d.Exec(`UPDATE invoice SET name_product = 'usertest' WHERE name_product = 'سرویس تست'`)
	if err != nil {
		return fmt.Errorf("invoice: renaming the test service: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.infof("invoice: %d test invoice(s) renamed from \"سرویس تست\" to \"usertest\"", n)
	}
	return nil
}

// fixProductCategories turns mirza_pro's category names into the ids the Go
// bot joins on. mirza_pro stored the category's remark; the Go bot stores its
// id. Only one row per remark is taken, and a category value that is already
// purely digits is left alone, so a second run cannot re-match a remark that
// happens to look like an id.
func fixProductCategories(d *db.DB, r *Report) error {
	res, err := d.Exec(`UPDATE product p
        JOIN (SELECT MIN(id) AS id, remark FROM category
              WHERE remark IS NOT NULL AND remark <> '' GROUP BY remark) c
          ON c.remark = p.category
        SET p.category = c.id
        WHERE p.category IS NOT NULL AND p.category <> ''
          AND p.category NOT REGEXP '^[0-9]+$'
          AND p.category <> c.id`)
	if err != nil {
		return fmt.Errorf("product: turning the category names into category ids: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.infof("product: %d row(s) now point at their category by id", n)
	}
	return nil
}

// reportUnsupportedPanels warns about panel types the Go bot cannot drive.
func reportUnsupportedPanels(d *db.DB, r *Report) error {
	for _, t := range unsupportedPanels {
		var n int64
		if err := d.QueryRow("SELECT COUNT(*) FROM marzban_panel WHERE type = ?", t).Scan(&n); err != nil {
			return fmt.Errorf("marzban_panel: counting panels of type %s: %w", t, err)
		}
		if n > 0 {
			r.warnf("marzban_panel: %d panel(s) of type %s cannot be driven by this bot; their services stay but will not renew", n, t)
		}
	}
	return nil
}

// reportUnsupportedGateways warns about payment providers that were switched
// on in the old settings and have no equivalent here.
func reportUnsupportedGateways(d *db.DB, r *Report) {
	for _, g := range unsupportedGateways {
		if paySettingEnabled(d, g.keys...) {
			r.warnf("payment: %s is switched on in the old settings and has no equivalent here; switch the payments you need back on in the admin panel", g.name)
		}
	}
}

// mirzaSettingsDropped lists the mirza_pro settings the Go bot has no switch
// for. They are named so the operator can see what will not carry over
// rather than discovering it later.
var mirzaSettingsDropped = []struct{ key, note string }{
	{"volumewarn", "the Go bot warns below a fixed 1 GB, not the old configurable threshold"},
	{"cronvolumere", "the volume-regex variant of the volume warning has no switch here"},
	{"on_hold_day", "the separate on-hold day count is not used; the Go bot follows the panel"},
	{"daywarn", "the day-warning text knob is not carried over"},
	{"statuscategorygenral", "the general-category switch has no equivalent"},
	{"timeauto_not_verify", "the automatic no-review delay is fixed at about a minute"},
	{"statusnamecustom", "on/off here, but free text in the Go bot; set the prefix again if you want one"},
}

// reportDroppedMirzaSettings names the settings that will not carry over.
func reportDroppedMirzaSettings(d *db.DB, r *Report) {
	for _, s := range mirzaSettingsDropped {
		var v string
		if err := d.QueryRow("SELECT ValuePay FROM PaySetting WHERE NamePay = ?", s.key).Scan(&v); err == nil {
			continue // a PaySetting key, not a setting column
		}
		if d.Setting().S(s.key) != "" {
			r.warnf("setting.%s: %s", s.key, s.note)
		}
	}
	// cron_status holds flags the Go bot's five switches do not cover.
	for _, k := range []string{"remove_volume", "uptime_node", "uptime_panel", "on_hold"} {
		if mirzaCronFlags(d)[k] {
			r.warnf("setting.cron_status[%s]: switched on in mirza_pro but the Go bot has no job for it", k)
		}
	}
}

// paySettingEnabled reports whether any of the named PaySetting keys shows a
// gateway switched on. mirza_pro spelled the switches three ways — "on…",
// "off…" and "0"/"1" — so all three are read.
func paySettingEnabled(d *db.DB, keys ...string) bool {
	for _, k := range keys {
		var v string
		if err := d.QueryRow("SELECT ValuePay FROM PaySetting WHERE NamePay = ?", k).Scan(&v); err != nil {
			continue
		}
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || v == "0" || v == "off" || v == "false" || v == "no" {
			continue
		}
		if strings.HasPrefix(v, "off") {
			continue
		}
		return true
	}
	return false
}
