package db

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
	"github.com/MHBehzadian/nexra-mirzabot/internal/text"
)

// This file is the Go version of the PHP bot's table.php. Every step is
// idempotent and only ever adds: it creates what is missing, adds missing
// columns with the same defaults table.php used, and never drops or rewrites
// existing data. Running it against a live PHP-era database is the migration.

func (d *DB) tableExists(name string) bool {
	return d.Count("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", name) > 0
}

func (d *DB) columnExists(table, col string) bool {
	return d.Count("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, col) > 0
}

// addField is addFieldToTable(): add the column and, when a default is
// given, set every existing row to it.
func (d *DB) addField(table, col, def, typ string, report func(string)) {
	if !d.tableExists(table) || d.columnExists(table, col) {
		return
	}
	if _, err := d.Exec("ALTER TABLE " + ident(table) + " ADD " + ident(col) + " " + typ); err != nil {
		return
	}
	if def != "" {
		d.Exec("UPDATE "+ident(table)+" SET "+ident(col)+" = ?", def)
	}
	report("added column " + table + "." + col)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// EnsureSchema brings the database up to the layout the bot expects.
// adminID is the main admin ($adminnumber), always kept in the admin table.
func (d *DB) EnsureSchema(adminID string, report func(string)) error {
	if report == nil {
		report = func(string) {}
	}
	const bin = " CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"
	const engine = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE utf8mb4_bin"

	// ---- user
	if !d.tableExists("user") {
		_, err := d.Exec(`CREATE TABLE user (
        id varchar(500)  PRIMARY KEY,
        ref_code CHAR(32) NOT NULL UNIQUE,
        limit_usertest int(100) NOT NULL,
        roll_Status bool NOT NULL,
        Processing_value  TEXT` + bin + ` NOT NULL,
        Processing_value_one varchar(1000)` + bin + ` NOT NULL,
        Processing_value_tow varchar(1000)` + bin + ` NOT NULL,
        Processing_value_four varchar(1000)` + bin + ` NOT NULL,
        step varchar(1000) NOT NULL,
        description_blocking TEXT` + bin + ` NULL,
        number varchar(2000) NOT null ,
        Balance int(255) NOT null ,
        User_Status varchar(500) NOT NULL,
        pagenumber int(10) NOT NULL,
        message_count varchar(100) NOT NULL,
        last_message_time varchar(100) NOT NULL,
        affiliatescount varchar(100) NOT NULL,
        affiliates varchar(100) NOT NULL,
        verify varchar(50) NOT NULL,
        username varchar(1000) NOT NULL)` + engine)
		if err != nil {
			return err
		}
		report("created table user")
	} else {
		d.addField("user", "affiliatescount", "0", "VARCHAR(100)", report)
		d.addField("user", "verify", "0", "VARCHAR(50)", report)
		d.addField("user", "affiliates", "0", "VARCHAR(100)", report)
		d.addField("user", "message_count", "0", "VARCHAR(100)", report)
		d.addField("user", "last_message_time", "0", "VARCHAR(100)", report)
		d.addField("user", "Processing_value_four", "0", "VARCHAR(100)", report)
		d.addField("user", "username", "none", "VARCHAR(1000)", report)
		d.addField("user", "Processing_value", "0", "TEXT", report)
		d.addField("user", "Processing_value_tow", "0", "VARCHAR(1000)", report)
		d.addField("user", "Processing_value_one", "0", "VARCHAR(1000)", report)
		d.addField("user", "roll_Status", "0", "bool", report)
		d.addField("user", "ref_code", "", "CHAR(32)", report)
		for _, r := range d.MustQuery("SELECT id FROM user WHERE ref_code IS NULL OR ref_code = ''") {
			for {
				code := randomHex(16)
				if !d.Exists("user", "ref_code", code) {
					d.Exec("UPDATE user SET ref_code = ? WHERE id = ?", code, r.S("id"))
					break
				}
			}
		}
	}

	// ---- help
	if !d.tableExists("help") {
		d.Exec(`CREATE TABLE help (
        id INT(6) UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        name_os varchar(500)` + bin + ` NOT NULL,
        Media_os varchar(5000) NOT NULL,
        type_Media_os varchar(500) NOT NULL,
        Description_os TEXT` + bin + ` NOT NULL)` + engine)
		report("created table help")
	}

	// ---- setting
	if !d.tableExists("setting") {
		d.Exec(`CREATE TABLE setting (
        Bot_Status varchar(200)` + bin + ` NULL,
        help_Status varchar(200)` + bin + ` NULL,
        roll_Status varchar(200)` + bin + ` NULL,
        get_number varchar(200)` + bin + ` NULL,
        iran_number varchar(200)` + bin + ` NULL,
        NotUser varchar(200)` + bin + ` NULL,
        Channel_Report varchar(600)  NULL,
        limit_usertest_all varchar(600)  NULL,
        time_usertest varchar(600)  NULL,
        val_usertest varchar(600)  NULL,
        Extra_volume varchar(600)  NULL,
        namecustome varchar(100)  NULL,
        status_verify varchar(50)  NULL,
        removedayc varchar(100)  NULL,
        copy_cart varchar(20)  NULL,
        statuscategory varchar(100)  NULL)` + engine)
		d.Exec(`INSERT INTO setting (Bot_Status,roll_Status,get_number,limit_usertest_all,time_usertest,val_usertest,help_Status,iran_number,NotUser,namecustome,removedayc,status_verify,statuscategory,copy_cart) VALUES ('1','0','0','1','1','100','0','0','0','0','1','0','1','0')`)
		report("created table setting")
	} else {
		d.addField("setting", "copy_cart", "0", "VARCHAR(20)", report)
		d.addField("setting", "status_verify", "0", "VARCHAR(50)", report)
		d.addField("setting", "statuscategory", "1", "VARCHAR(50)", report)
		d.addField("setting", "namecustome", "0", "VARCHAR(200)", report)
		d.addField("setting", "removedayc", "1", "VARCHAR(100)", report)
		d.addField("setting", "Extra_volume", "0", "VARCHAR(200)", report)
		s := d.Setting()
		if s.IsNull("iran_number") {
			d.Exec("UPDATE setting SET iran_number = ?", "0")
		}
		if s.IsNull("NotUser") {
			d.Exec("UPDATE setting SET NotUser = ?", "offnotuser")
		}
		if s == nil {
			d.Exec(`INSERT INTO setting (Bot_Status,roll_Status,get_number,limit_usertest_all,time_usertest,val_usertest,help_Status,iran_number,NotUser,namecustome,removedayc,status_verify,statuscategory,copy_cart) VALUES ('1','0','0','1','1','100','0','0','0','0','1','0','1','0')`)
		}
	}

	// ---- admin
	if !d.tableExists("admin") {
		d.Exec("CREATE TABLE admin (id_admin varchar(200) PRIMARY KEY NOT NULL)")
		report("created table admin")
	}
	if adminID != "" && !d.Exists("admin", "id_admin", adminID) {
		d.Exec("INSERT INTO admin (id_admin) VALUES (?)", adminID)
	}

	// ---- channels
	if !d.tableExists("channels") {
		d.Exec("CREATE TABLE channels (link varchar(200) NOT NULL )")
	}

	// ---- marzban_panel
	if !d.tableExists("marzban_panel") {
		d.Exec(`CREATE TABLE marzban_panel (
        id INT(6) UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        name_panel varchar(2000)` + bin + ` NULL,
        url_panel varchar(2000) NULL,
        username_panel varchar(200) NULL,
        password_panel varchar(200) NULL,
        status varchar(100) NULL,
        statusTest varchar(100) NULL,
        type varchar(200) NULL,
        linksubx varchar(500) NULL,
        inboundid varchar(200) NULL,
        MethodUsername varchar(900)  NULL,
        sublink varchar(200)` + bin + `  NULL,
        configManual varchar(200)` + bin + `  NULL,
        onholdstatus varchar(200) NULL,
        datelogin TEXT NULL,
        inbounds TEXT NULL,
        marzban_url_direct varchar(500) NULL,
        marzban_username_direct varchar(200) NULL,
        marzban_password_direct varchar(200) NULL,
        proxies TEXT NULL)` + engine)
		report("created table marzban_panel")
	} else {
		type col struct{ name, typ, fill string }
		for _, c := range []col{
			{"datelogin", "TEXT", ""},
			{"inbounds", "TEXT", ""},
			{"proxies", "TEXT", ""},
			{"marzban_url_direct", "VARCHAR(500)", ""},
			{"marzban_username_direct", "VARCHAR(200)", ""},
			{"marzban_password_direct", "VARCHAR(200)", ""},
			{"statusTest", "VARCHAR(100)", "ontestshowpanel"},
			{"status", "VARCHAR(100)", "activepanel"},
			{"onholdstatus", "VARCHAR(100)", "offonhold"},
			{"sublink", "VARCHAR(200)", "onsublink"},
			{"configManual", "VARCHAR(200)", "offconfig"},
			{"MethodUsername", "VARCHAR(900)", text.T("users.customidAndRandom")},
			{"inboundid", "VARCHAR(200)", ""},
			{"linksubx", "VARCHAR(500)", ""},
			{"type", "VARCHAR(200)", "marzban"},
		} {
			d.addField("marzban_panel", c.name, c.fill, c.typ, report)
		}
	}

	// ---- product
	if !d.tableExists("product") {
		d.Exec(`CREATE TABLE product (
        id INT(6) UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        code_product varchar(200)  NULL,
        name_product varchar(2000)` + bin + ` NULL,
        price_product varchar(2000) NULL,
        Volume_constraint varchar(2000) NULL,
        Location varchar(1000) NULL,
        Service_time varchar(200) NULL,
        Category varchar(600) NULL)` + engine)
		report("created table product")
	} else {
		d.addField("product", "Location", "", "VARCHAR(1000)", report)
		d.addField("product", "Category", "", "VARCHAR(600)", report)
		d.addField("product", "code_product", "", "VARCHAR(200)", report)
	}

	// ---- invoice
	if !d.tableExists("invoice") {
		d.Exec(`CREATE TABLE invoice (
        id_invoice varchar(200) PRIMARY KEY,
        id_user varchar(200) NULL,
        username varchar(200) NULL,
        Service_location varchar(200) NULL,
        time_sell varchar(200) NULL,
        name_product varchar(200)` + bin + ` NULL,
        price_product varchar(200) NULL,
        Volume varchar(200) NULL,
        Service_time varchar(200) NULL,
        user_info TEXT NULL,
        Status varchar(200) NULL)` + engine)
		report("created table invoice")
	} else {
		d.addField("invoice", "time_sell", "", "VARCHAR(2000)", report)
		d.addField("invoice", "user_info", "", "TEXT", report)
		d.addField("invoice", "Status", "", "VARCHAR(2000)", report)
	}

	// ---- Payment_report
	if !d.tableExists("Payment_report") {
		d.Exec(`CREATE TABLE Payment_report (
        id INT(6) UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        id_user varchar(200),
        id_order varchar(500),
        time varchar(200)  NULL,
        price varchar(400) NULL,
        dec_not_confirmed varchar(2000)` + bin + ` NULL,
        Payment_Method varchar(400)` + bin + ` NULL,
        payment_Status varchar(2000) NULL,
        invoice varchar(300) NULL)` + engine)
		report("created table Payment_report")
	} else {
		d.addField("Payment_report", "invoice", "", "VARCHAR(300)", report)
		d.addField("Payment_report", "Payment_Method", "", "VARCHAR(1000)", report)
	}

	// ---- Discount / Giftcodeconsumed
	if !d.tableExists("Discount") {
		d.Exec("CREATE TABLE Discount (id INT(6) UNSIGNED AUTO_INCREMENT PRIMARY KEY, code varchar(2000) NULL, price varchar(200) NULL)")
	}
	if !d.tableExists("Giftcodeconsumed") {
		d.Exec("CREATE TABLE Giftcodeconsumed (id INT(6) UNSIGNED AUTO_INCREMENT PRIMARY KEY, code varchar(2000) NULL, id_user varchar(200) NULL)")
	}

	// ---- textbot
	if !d.tableExists("textbot") {
		d.Exec(`CREATE TABLE textbot (
        id_text varchar(600) PRIMARY KEY NOT NULL,
        text TEXT` + bin + ` NOT NULL)` + engine)
		report("created table textbot")
	}
	for _, t := range [][2]string{
		{"text_start", text.T("users.start")},
		{"text_usertest", text.T("users.usertest.usertestbtn")},
		{"text_Purchased_services", text.T("Admin.Status.title")},
		{"text_support", text.T("users.support.title")},
		{"text_help", text.T("users.help.title")},
		{"text_bot_off", text.T("users.botoff")},
		{"text_roll", text.T("users.RulesDescription")},
		{"text_fq", text.T("users.fqbtn")},
		{"text_dec_fq", text.T("users.fqDescription")},
		{"text_account", text.T("users.accountbtn")},
		{"text_sell", text.T("users.buybtn")},
		{"text_Add_Balance", text.T("users.add_balance")},
		{"text_channel", text.T("users.channeldosntjoin")},
		{"text_Discount", text.T("users.Discount.titlebtn")},
		{"text_Tariff_list", text.T("users.pricelist")},
		{"text_dec_Tariff_list", "not set"},
	} {
		d.Exec("INSERT IGNORE INTO textbot (id_text,text) VALUES (?, ?)", t[0], t[1])
	}

	// ---- PaySetting
	if !d.tableExists("PaySetting") {
		d.Exec(`CREATE TABLE PaySetting (
        NamePay varchar(500) PRIMARY KEY NOT NULL,
        ValuePay TEXT` + bin + ` NOT NULL)` + engine)
		report("created table PaySetting")
	}
	for _, p := range [][2]string{
		{"CartDescription", "603700000000"},
		{"Cartstatus", "oncard"},
		{"apinowpayment", "0"},
		{"nowpaymentstatus", "offnowpayment"},
		{"digistatus", "offdigi"},
		{"statusaqayepardakht", "offaqayepardakht"},
		{"merchant_id_aqayepardakht", "0"},
	} {
		d.Exec("INSERT IGNORE INTO PaySetting (NamePay,ValuePay) VALUES (?, ?)", p[0], p[1])
	}

	// ---- DiscountSell
	if !d.tableExists("DiscountSell") {
		d.Exec(`CREATE TABLE DiscountSell (
        id INT(6) UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        codeDiscount varchar(1000)  NOT NULL,
        price varchar(200)  NOT NULL,
        limitDiscount varchar(500)  NOT NULL,
        usedDiscount varchar(500) NOT NULL,
        usefirst varchar(500) NOT NULL)`)
	} else {
		d.addField("DiscountSell", "usefirst", "", "VARCHAR(500)", report)
	}

	// ---- affiliates
	if !d.tableExists("affiliates") {
		d.Exec(`CREATE TABLE affiliates (
        description TEXT` + bin + `  NULL,
        status_commission varchar(200)` + bin + `  NULL,
        Discount varchar(200)` + bin + `  NULL,
        price_Discount varchar(200)` + bin + `  NULL,
        id_media varchar(300)` + bin + `  NULL,
        affiliatesstatus varchar(600)  NULL,
        affiliatespercentage varchar(600)  NULL)` + engine)
		d.Exec("INSERT INTO affiliates (description,id_media,status_commission,Discount,affiliatesstatus,affiliatespercentage) VALUES ('none','none','oncommission','onDiscountaffiliates','offaffiliates','0')")
		report("created table affiliates")
	} else if d.Count("SELECT COUNT(*) FROM affiliates") == 0 {
		d.Exec("INSERT INTO affiliates (description,id_media,status_commission,Discount,affiliatesstatus,affiliatespercentage) VALUES ('none','none','oncommission','onDiscountaffiliates','offaffiliates','0')")
	}

	// ---- cancel_service
	if !d.tableExists("cancel_service") {
		d.Exec(`CREATE TABLE cancel_service (
        id INT(6) UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        id_user varchar(500)  NOT NULL,
        username varchar(1000)  NOT NULL,
        description TEXT` + bin + `  NOT NULL,
        status varchar(1000)  NOT NULL)` + engine)
	}

	// ---- category
	if !d.tableExists("category") {
		d.Exec(`CREATE TABLE category (
        id INT(6) UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        remark varchar(500)` + bin + `  NOT NULL)` + engine)
	}

	// table.php re-applied this on every visit; only do it when it differs.
	if coll := d.Scalar("SELECT COLLATION_NAME FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'user' AND column_name = 'Processing_value'"); coll != "" && coll != "utf8mb4_unicode_ci" {
		d.Exec("ALTER TABLE `user` CHANGE `Processing_value` `Processing_value` TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL")
	}

	// ---- autopay (automatic card-to-card confirmation)
	d.Exec(`CREATE TABLE IF NOT EXISTS autopay (
        id INT(6) UNSIGNED PRIMARY KEY,
        status varchar(10) NULL,
        device_key varchar(100) NULL,
        device_info varchar(200) NULL,
        last_seen varchar(40) NULL,
        created_at varchar(40) NULL)` + engine)
	d.Exec(`CREATE TABLE IF NOT EXISTS autopay_order (
        id INT(11) UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        id_user varchar(200) NULL,
        base_price BIGINT NULL,
        amount BIGINT NULL,
        status varchar(20) NULL,
        id_order varchar(200) NULL,
        sms_id INT(11) NULL,
        created_at varchar(40) NULL,
        closed_at varchar(40) NULL,
        INDEX autopay_amount_idx (amount),
        INDEX autopay_status_idx (status))` + engine)
	d.Exec(`CREATE TABLE IF NOT EXISTS autopay_sms (
        id INT(11) UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        hash varchar(70) NULL,
        sender varchar(100) NULL,
        body TEXT NULL,
        amount BIGINT NULL,
        direction varchar(10) NULL,
        card varchar(10) NULL,
        sent_at varchar(40) NULL,
        received_at varchar(40) NULL,
        status varchar(20) NULL,
        id_order varchar(200) NULL,
        UNIQUE KEY autopay_sms_hash (hash))` + engine)

	// ---- tables only the Go bot uses (the PHP bot never touches these)
	d.Exec(`CREATE TABLE IF NOT EXISTS nexra_kv (
        k varchar(191) PRIMARY KEY,
        v MEDIUMTEXT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE utf8mb4_bin`)
	d.Exec(`CREATE TABLE IF NOT EXISTS nexra_broadcast (
        id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
        user_id varchar(200) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE utf8mb4_bin`)
	if _, ok := d.KVOk("schema_version"); !ok {
		d.SetKV("installed_at", php.Date("Y-m-d H:i:s", time.Now().Unix()))
	}
	d.SetKV("schema_version", "1")
	return nil
}

// Tables lists the tables the bot owns (used by the export/import tools).
var Tables = []string{
	"user", "help", "setting", "admin", "channels", "marzban_panel", "product",
	"invoice", "Payment_report", "Discount", "Giftcodeconsumed", "textbot",
	"PaySetting", "DiscountSell", "affiliates", "cancel_service", "category",
	"autopay", "autopay_order", "autopay_sms", "nexra_kv", "nexra_broadcast",
}

// IsBotTable reports whether name is one of Tables (case-sensitive, as MySQL
// on Linux is).
func IsBotTable(name string) bool {
	for _, t := range Tables {
		if t == name {
			return true
		}
	}
	return strings.HasPrefix(name, "nexra_")
}
