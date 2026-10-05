package bot

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/tg"
)

// adminFlow is admin.php; every section returns true where PHP returned.
func (c *Ctx) adminFlow() {
	for _, s := range []func() bool{
		c.admEntry, c.admAdmins, c.admStats, c.admPanelAdd, c.admBroadcast, c.admTexts,
		c.admMessages, c.admPanelStatus, c.admShop, c.admAutopay, c.admPayments, c.admProducts,
		c.admUserBalance, c.admDiscountsAndPanelFlags, c.admPaySettings, c.admManagePanel,
		c.admExtraBalance, c.admAffiliates, c.admCancelRequests, c.admCron, c.admUserSearch, c.admTail,
	} {
		if s() {
			return
		}
	}
}

// savedata is savedata($type, $name, $value) on Processing_value (JSON).
func (c *Ctx) savedata(clear bool, name, value string) {
	var data php.Object
	if !clear {
		cur := c.db().Select("user", "Processing_value", "id", c.fromID).S("Processing_value")
		data = php.DecodeObject(cur)
	}
	c.setUser("Processing_value", php.JSONEncode(data.Set(name, value)))
}

func (c *Ctx) userdata() map[string]string {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(c.user.S("Processing_value")), &m)
	out := map[string]string{}
	for k, v := range m {
		switch x := v.(type) {
		case string:
			out[k] = x
		case float64:
			out[k] = php.NumStr(x)
		}
	}
	return out
}

func (c *Ctx) admEntry() bool {
	if c.text == "panel" || c.text == "/panel" || c.text == T("Admin.commendadminmanagment") || c.text == T("Admin.commendadmin") || c.datain == "PANEL" {
		c.sendHTML(c.fromID, sprintf("Admin.login-admin", Version), kbAdmin())
	}
	switch {
	case c.text == T("Admin.Back-Adminment") || c.datain == "back_admin":
		if c.datain == "back_admin" {
			c.del()
		}
		c.sendHTML(c.fromID, T("Admin.Back-Admin"), kbAdmin())
		c.step("home")
		return true
	case c.text == T("Admin.channel.changechannelbtn"):
		c.sendHTML(c.fromID, T("Admin.channel.changechannel")+c.channels.S("link"), kbBackAdmin())
		c.step("addchannel")
	case c.stepIs("addchannel"):
		c.sendHTML(c.fromID, T("Admin.channel.setchannel"), kbChannel())
		c.step("home")
		if c.db().SelectCount("channels", "", nil) == 0 {
			c.db().Exec("INSERT INTO channels (link) VALUES (?)", c.text)
		} else {
			c.upd("channels", "link", c.text, "", nil)
		}
	}
	return false
}

func (c *Ctx) admAdmins() bool {
	d := c.db()
	if c.text == T("Admin.Addedadmin") {
		c.sendHTML(c.fromID, T("Admin.manageadmin.getid"), kbBackAdmin())
		c.step("addadmin")
	}
	if c.stepIs("addadmin") {
		c.sendHTML(c.fromID, T("Admin.manageadmin.addadminset"), kbAdmin())
		c.step("home")
		d.Exec("INSERT INTO admin (id_admin) VALUES (?)", c.text)
	}
	switch {
	case c.text == T("Admin.Removeedadmin"):
		c.sendHTML(c.fromID, T("Admin.manageadmin.getid"), kbBackAdmin())
		c.step("deleteadmin")
	case c.stepIs("deleteadmin"):
		if php.Intval(c.text) == php.Intval(c.b.Cfg.AdminID) {
			c.sendHTML(c.fromID, T("Admin.manageadmin.InfoAdd"), nil)
			return true
		}
		if !php.IsNumeric(c.text) || !c.isAdminID(c.text) {
			return true
		}
		c.sendHTML(c.fromID, T("Admin.manageadmin.removedadmin"), kbAdmin())
		d.Exec("DELETE FROM admin WHERE id_admin = ?", c.text)
		c.step("home")
	case c.m(`limitusertest_(.*)`):
		c.sendHTML(c.fromID, T("Admin.getlimitusertest.getid"), kbBackAdmin())
		c.setUser("Processing_value", c.g(1))
		c.step("get_number_limit")
	case c.stepIs("get_number_limit"):
		c.sendHTML(c.fromID, T("Admin.getlimitusertest.setlimit"), kbAdmin())
		c.step("home")
		c.upd("user", "limit_usertest", c.text, "id", c.user.S("Processing_value"))
	}
	switch {
	case c.text == T("Admin.getlimitusertest.setlimitallbtn"):
		c.sendHTML(c.fromID, T("Admin.getlimitusertest.limitall"), kbBackAdmin())
		c.step("limit_usertest_allusers")
	case c.stepIs("limit_usertest_allusers"):
		c.sendHTML(c.fromID, T("Admin.getlimitusertest.setlimitall"), kbUsertest())
		c.step("home")
		c.upd("setting", "limit_usertest_all", c.text, "", nil)
		c.upd("user", "limit_usertest", c.text, "", nil)
	}
	if c.text == T("Admin.channel.setting") {
		c.sendHTML(c.fromID, T("users.selectoption"), kbChannel())
	}
	return false
}

func loadAvg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "0.00"
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return "0.00"
	}
	return php.NumberFormat(php.Floatval(f[0]), 2)
}

func (c *Ctx) admStats() bool {
	d := c.db()
	if c.text == T("Admin.keyboardadmin.bot_statistics") {
		const active = "(Status = 'active' OR Status = 'end_of_time'  OR Status = 'end_of_volume' OR status = 'sendedwarn') AND name_product != 'usertest'"
		users := d.SelectCount("user", "", nil)
		balanceAll := d.Scalar("SELECT SUM(Balance) FROM user")
		panelsN := d.SelectCount("marzban_panel", "", nil)
		invoices := d.Count("SELECT COUNT(*) FROM invoice WHERE " + active)
		sum := d.Scalar("SELECT SUM(price_product) FROM invoice WHERE " + active)
		day := d.Count("SELECT COUNT(*) FROM invoice WHERE time_sell > ? AND "+active, nowUnix()-86400)
		tests := d.SelectCount("invoice", "name_product", "usertest")
		msg := sprintf("Admin.Statistics.info", users, balanceAll, loadAvg(), tests, invoices, sum, day, panelsN)
		c.sendHTML(c.fromID, msg, nil)
	}
	if c.text == T("Admin.managepanel.btnshowconnect") {
		c.showConnect()
	}
	if c.text == T("Admin.manageadmin.showlistbtn") {
		var list string
		for _, a := range c.adminIDs {
			if a != "" && a != "0" {
				list += a + "\n"
			}
		}
		c.sendHTML(c.fromID, sprintf("Admin.manageadmin.showlist", list), kbAdminSection())
	}
	return false
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (c *Ctx) showConnect() {
	pm := c.b.PM
	pv := c.user.S("Processing_value")
	panel := c.db().Select("marzban_panel", "*", "name_panel", pv)
	switch panel.S("type") {
	case "marzban":
		tok := pm.MarzbanToken(panel)
		if _, ok := tok["access_token"]; ok {
			st := pm.MarzbanSystemStats(panel)
			o := jmap(st)
			msg := sprintf("Admin.managepanel.infomarzban", o.s("total_user"), o.s("users_active"), o.s("version"),
				formatBytes(php.Floatval(o.s("mem_total"))), formatBytes(php.Floatval(o.s("mem_used"))),
				formatBytes(php.Floatval(o.s("outgoing_bandwidth"))+php.Floatval(o.s("incoming_bandwidth"))))
			c.sendHTML(c.fromID, msg, nil)
		} else if jmap(tok).s("detail") == "Incorrect username or password" {
			c.sendHTML(c.fromID, T("Admin.managepanel.Incorrectinfo"), nil)
		} else {
			c.sendHTML(c.fromID, T("Admin.managepanel.errorstatuspanel")+jsonStr(tok), nil)
		}
	case "mit", "nexra":
		dash := pm.MitDashboard(panel)
		if d, ok := dash["detail"]; ok {
			c.sendHTML(c.fromID, T("Admin.managepanel.errorstatuspanel")+jmap{"d": d}.s("d"), nil)
		} else {
			gb := php.Round(php.Floatval(jmap(dash).s("remaining_traffic"))/(1024*1024*1024), 2)
			c.sendHTML(c.fromID, "✅ اتصال به Mit Panel برقرار است\nحجم باقیمانده‌ی این ادمین: "+php.FloatToString(gb)+" GB", nil)
		}
	case "marzneshin":
		tok := pm.MarzneshinToken(panel)
		if _, ok := tok["access_token"]; ok {
			st := jmap(pm.MarzneshinStats(panel))
			c.sendHTML(c.fromID, sprintf("Admin.managepanel.infomarzneshin", st.s("total"), st.s("active")), nil)
		} else if jmap(tok).s("detail") == "Incorrect username or password" {
			c.sendHTML(c.fromID, T("Admin.managepanel.Incorrectinfo"), nil)
		} else {
			c.sendHTML(c.fromID, T("Admin.managepanel.errorstatuspanel")+jsonStr(tok), nil)
		}
	case "x-ui_single", "alireza":
		var res map[string]any
		if panel.S("type") == "x-ui_single" {
			_, res = pm.XUILogin(panel, false)
		} else {
			_, res = pm.AlirezaLogin(panel)
		}
		r := jmap(res)
		if v, _ := res["success"].(bool); v {
			c.sendHTML(c.fromID, T("Admin.managepanel.connectx-ui"), nil)
		} else if r.s("msg") == "Invalid username or password." {
			c.sendHTML(c.fromID, T("Admin.managepanel.Incorrectinfo"), nil)
		} else {
			c.sendHTML(c.fromID, T("Admin.managepanel.errorstatuspanel"), nil)
		}
	case "wgdashboard":
		c.sendHTML(c.fromID, T("users.selectoption"), optWG())
	case "mikrotik":
		if _, bad := pm.MikrotikLogin(panel)["error"]; bad {
			c.sendHTML(c.fromID, T("Admin.managepanel.notconnect"), optMikrotik())
		} else {
			c.sendHTML(c.fromID, T("Admin.managepanel.connectx-ui"), optMikrotik())
		}
	}
	c.step("home")
}

const secretPrompt = "🔒 برای مدیریت پنل‌ها، کد مخفی را وارد کنید:"

func (c *Ctx) secretOK() bool {
	return c.b.Cfg.MitSecret != "" && c.text == c.b.Cfg.MitSecret
}

func (c *Ctx) admPanelAdd() bool {
	d := c.db()
	backAdm := kbBackAdmin()
	switch {
	case c.text == T("Admin.keyboardadmin.add_panel"):
		c.sendHTML(c.fromID, secretPrompt, backAdm)
		c.step("mit_panel_access_gate_add")
	case c.stepIs("mit_panel_access_gate_add"):
		if !c.secretOK() {
			c.sendHTML(c.fromID, "❌ کد اشتباه است.", kbAdmin())
			c.step("home")
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.selecttypepanel"), kbTypePanel())
		// The type buttons only work right after the code was accepted.
		c.step("mit_panel_pick_type")
	case c.m(`typepanel%(.*)`):
		if !c.stepIs("mit_panel_pick_type") {
			return true
		}
		c.savedata(true, "type", c.g(1))
		c.del()
		c.sendHTML(c.fromID, T("Admin.managepanel.addpanelname"), backAdm)
		c.step("add_name_panel")
	case c.stepIs("add_name_panel"):
		if c.inColumn("marzban_panel", "name_panel", c.text) {
			c.sendHTML(c.fromID, T("Admin.managepanel.Repeatpanel"), backAdm)
			return true
		}
		c.savedata(false, "name", c.text)
		if panels.IsMitType(c.userdata()["type"]) {
			c.sendHTML(c.fromID, "🔗 آدرس Mit Panel را همراه با مسیرش وارد کنید (مثال: https://panel.example.com/dashboard):", backAdm)
			c.step("mit_get_mit_url")
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.addpanelurl"), backAdm)
		c.step("add_link_panel")
	case c.stepIs("mit_get_mit_url"):
		if !filterValidateURL(c.text) {
			c.sendHTML(c.fromID, T("Admin.managepanel.Invalid-domain"), backAdm)
			return true
		}
		c.savedata(false, "url_panel", c.text)
		c.sendHTML(c.fromID, "👤 یوزرنیم ادمین در Mit Panel را وارد کنید:", backAdm)
		c.step("mit_get_mit_username")
	case c.stepIs("mit_get_mit_username"):
		c.savedata(false, "username_panel", c.text)
		c.sendHTML(c.fromID, "🔐 پسورد ادمین در Mit Panel را وارد کنید:", backAdm)
		c.step("mit_get_mit_password")
	case c.stepIs("mit_get_mit_password"):
		c.savedata(false, "password_panel", c.text)
		c.sendHTML(c.fromID, "🔗 آدرس واقعیِ مرزبان (بدون واسطه) را وارد کنید:", backAdm)
		c.step("mit_get_marzban_url")
	case c.stepIs("mit_get_marzban_url"):
		if !filterValidateURL(c.text) {
			c.sendHTML(c.fromID, T("Admin.managepanel.Invalid-domain"), backAdm)
			return true
		}
		u := c.userdata()
		_, err := d.Exec("INSERT INTO marzban_panel (name_panel,url_panel,username_panel,password_panel,type,inboundid,sublink,configManual,MethodUsername,statusTest,status,onholdstatus,marzban_url_direct,marzban_username_direct,marzban_password_direct) VALUES (?, ?, ?, ?, ?,?,?,?,?,?,?,?,?,?,?)",
			u["name"], u["url_panel"], u["username_panel"], u["password_panel"], u["type"], "0", "onsublink", "offconfig", T("users.customidAndRandom"), "ontestshowpanel", "activepanel", "offonhold", c.text, u["username_panel"], u["password_panel"])
		if err != nil {
			c.step("home")
			c.sendHTML(c.fromID, "❌ ذخیره‌ی پنل انجام نشد:\n<code>"+escapeHTML(err.Error())+"</code>", kbAdmin())
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.addedpanel"), backAdm)
		c.sendHTML(c.fromID, "🥳", kbAdmin())
		c.sendHTML(c.fromID, T("Admin.managepanel.panelnotes"), nil)
		c.step("home")
	case c.stepIs("add_link_panel"):
		if !filterValidateURL(c.text) {
			c.sendHTML(c.fromID, T("Admin.managepanel.Invalid-domain"), backAdm)
			return true
		}
		c.savedata(false, "url_panel", c.text)
		t := c.userdata()["type"]
		if t == "s_ui" || t == "wgdashboard" {
			c.sendHTML(c.fromID, T("Admin.managepanel.settoken"), backAdm)
			c.step("add_password_panel")
			c.savedata(false, "username_panel", "none")
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.usernameset"), backAdm)
		c.step("add_username_panel")
	case c.stepIs("add_username_panel"):
		c.sendHTML(c.fromID, T("Admin.managepanel.getpassword"), backAdm)
		c.step("add_password_panel")
		c.savedata(false, "username_panel", c.text)
	case c.stepIs("add_password_panel"):
		u := c.userdata()
		d.Exec("INSERT INTO marzban_panel (name_panel,url_panel,username_panel,password_panel,type,inboundid,sublink,configManual,MethodUsername,statusTest,status,onholdstatus) VALUES (?, ?, ?, ?, ?,?,?,?,?,?,?,?)",
			u["name"], u["url_panel"], u["username_panel"], c.text, u["type"], "0", "onsublink", "offconfig", T("users.customidAndRandom"), "ontestshowpanel", "activepanel", "offonhold")
		c.sendHTML(c.fromID, T("Admin.managepanel.addedpanel"), backAdm)
		c.sendHTML(c.fromID, "🥳", kbAdmin())
		switch u["type"] {
		case "x-ui_single", "alireza":
			c.sendHTML(c.fromID, T("Admin.managepanel.notex-ui"), nil)
		case "marzban", "s_ui", "marzneshin":
			c.sendHTML(c.fromID, T("Admin.managepanel.notemarzban"), nil)
		case "mit", "nexra":
			c.sendHTML(c.fromID, T("Admin.managepanel.panelnotes"), nil)
		case "wgdashboard":
			c.sendHTML(c.fromID, T("Admin.managepanel.wgdashboard"), nil)
		case "mikrotik":
			c.sendHTML(c.fromID, T("Admin.managepanel.mikrotik"), nil)
		}
		c.step("home")
	}
	return false
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#039;")
	return r.Replace(s)
}

func (c *Ctx) admBroadcast() bool {
	d := c.db()
	switch {
	case c.text == T("Admin.keyboardadmin.send_message"):
		c.sendHTML(c.fromID, T("users.selectoption"), kbSendMessage())
	case c.text == T("Admin.systemsms.sendbulkbtn"):
		c.sendHTML(c.fromID, T("Admin.ManageUser.GetText"), kbBackAdmin())
		c.step("getconfirmsendall")
	case c.stepIs("getconfirmsendall"):
		if c.text == "" || c.text == "0" {
			c.sendHTML(c.fromID, T("Admin.systemsms.allowsendtext"), kbBackAdmin())
			return true
		}
		c.savedata(true, "text", withCustomEmoji(c.text, c.f.Entities))
		c.savedata(false, "id_admin", c.fromID)
		c.sendHTML(c.fromID, T("Admin.systemsms.acceptsend"), kbBackAdmin())
		c.step("gettextforsendall")
	case c.stepIs("gettextforsendall"):
		if c.text == T("Admin.accept") {
			c.step("home")
			c.b.StartBroadcast(c.user.S("Processing_value"))
			k := ik(row(cb(T("Admin.systemsms.cancelsend"), "cancel_sendmessage")))
			c.sendHTML(c.fromID, T("Admin.systemsms.sendingmessage"), k)
		}
	case c.datain == "cancel_sendmessage":
		c.b.CancelBroadcast()
		c.del()
		c.sendHTML(c.fromID, T("Admin.systemsms.canceledmessage"), nil)
	case c.text == T("Admin.systemsms.forwardbulkbtn"):
		c.sendHTML(c.fromID, T("Admin.ManageUser.ForwardGetext"), kbBackAdmin())
		c.step("gettextforwardMessage")
	case c.stepIs("gettextforwardMessage"):
		c.sendHTML(c.fromID, T("Admin.systemsms.sendingforward"), kbAdmin())
		c.step("home")
		ids := d.Column("user", "id")
		from, msgID := c.fromID, c.messageID
		go func() {
			for _, id := range ids {
				c.b.TG.ForwardMessage(from, msgID, strings.TrimSpace(id))
				sleepMs(2000)
			}
			c.b.TG.SendMessage(from, T("Admin.systemsms.sendforwardtousers"), kbAdmin(), "HTML")
		}()
	}
	return false
}

func (c *Ctx) admTexts() bool {
	type entry struct{ button, step, id string }
	entries := []entry{
		{T("Admin.changetext.textstart"), "changetextstart", "text_start"},
		{T("Admin.changetext.text_Purchased_services"), "changetextinfo", "text_Purchased_services"},
		{T("Admin.changetext.text_usertest"), "changetextusertest", "text_usertest"},
		{T("Admin.changetext.text_help"), "text_help", "text_help"},
		{T("Admin.changetext.text_support"), "text_support", "text_support"},
		{T("Admin.changetext.text_fq"), "text_fq", "text_fq"},
		{T("Admin.changetext.text_dec_fq"), "text_dec_fq", "text_dec_fq"},
		{T("Admin.changetext.text_channel"), "text_channel", "text_channel"},
		{T("Admin.changetext.text_account"), "text_account", "text_account"},
		{T("Admin.changetext.text_Add_Balance"), "text_Add_Balance", "text_Add_Balance"},
		{T("users.changetext.buy_subscription_button"), "text_sell", "text_sell"},
		{T("Admin.changetext.text_Tariff_list"), "text_Tariff_list", "text_Tariff_list"},
		{T("Admin.changetext.text_dec_Tariff_list"), "text_dec_Tariff_list", "text_dec_Tariff_list"},
	}
	if c.text == T("Admin.keyboardadmin.bot_text_settings") {
		c.sendHTML(c.fromID, T("users.selectoption"), kbTextbot())
		return false
	}
	for _, e := range entries {
		if c.text == e.button {
			c.sendHTML(c.fromID, T("Admin.ManageUser.ChangeTextGet")+c.texts[e.id], kbBackAdmin())
			c.step(e.step)
			return false
		}
		if c.stepIs(e.step) {
			if c.text == "" || c.text == "0" {
				c.sendHTML(c.fromID, T("Admin.ManageUser.ErrorText"), kbTextbot())
				return true
			}
			c.sendHTML(c.fromID, T("Admin.ManageUser.SaveText"), kbTextbot())
			c.saveBotText(e.id)
			c.step("home")
			return false
		}
	}
	return false
}

func (c *Ctx) admMessages() bool {
	d := c.db()
	switch {
	case c.text == T("Admin.systemsms.sendmessageauser"):
		c.sendHTML(c.fromID, T("Admin.ManageUser.GetText"), kbBackAdmin())
		c.step("sendmessagetext")
	case c.stepIs("sendmessagetext"):
		c.setUser("Processing_value", withCustomEmoji(c.text, c.f.Entities))
		c.sendHTML(c.fromID, T("Admin.ManageUser.GetIDMessage"), kbBackAdmin())
		c.step("sendmessagetid")
	case c.stepIs("sendmessagetid"):
		if !d.Exists("user", "id", c.text) {
			c.sendHTML(c.fromID, T("Admin.not-user"), kbBackAdmin())
			return true
		}
		c.sendHTML(c.text, sprintf("Admin.systemsms.sendedmessagetouser", c.user.S("Processing_value")), nil)
		c.sendHTML(c.fromID, T("Admin.ManageUser.MessageSent"), kbAdmin())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Help.titlebtn"):
		c.sendHTML(c.fromID, T("users.selectoption"), kbHelpAdmin())
	case c.text == T("Admin.Help.addhelp"):
		c.sendHTML(c.fromID, T("Admin.Help.GetAddNameHelp"), kbBackAdmin())
		c.step("add_name_help")
	case c.stepIs("add_name_help"):
		d.Exec("INSERT IGNORE INTO help (name_os, Media_os, type_Media_os, Description_os) VALUES (?, '', '', '')", c.text)
		c.sendHTML(c.fromID, T("Admin.Help.GetAddDecHelp"), kbBackAdmin())
		c.step("add_dec")
		c.setUser("Processing_value", c.text)
	case c.stepIs("add_dec"):
		pv := c.user.S("Processing_value")
		if c.photo {
			c.upd("help", "Media_os", c.photoID, "name_os", pv)
			c.upd("help", "Description_os", withCustomEmoji(c.caption, c.f.CaptionEntities), "name_os", pv)
			c.upd("help", "type_Media_os", "photo", "name_os", pv)
		} else if c.text != "" && c.text != "0" {
			c.upd("help", "Description_os", withCustomEmoji(c.text, c.f.Entities), "name_os", pv)
		} else if c.video {
			c.upd("help", "Media_os", c.videoID, "name_os", pv)
			c.upd("help", "Description_os", c.caption, "name_os", pv)
			c.upd("help", "type_Media_os", "video", "name_os", pv)
		}
		c.sendHTML(c.fromID, T("Admin.Help.SaveHelp"), kbAdmin())
		c.step("home")
	case c.text == T("Admin.Help.removehelpbtn"):
		c.sendHTML(c.fromID, T("Admin.Help.SelectName"), c.kbHelpList())
		c.step("remove_help")
	case c.stepIs("remove_help"):
		d.Exec("DELETE FROM help WHERE name_os = ?", c.text)
		c.sendHTML(c.fromID, T("Admin.Help.RemoveHelp"), kbHelpAdmin())
		c.step("home")
	}
	switch {
	case c.m(`Response_(\w+)`):
		c.setUser("Processing_value", c.g(1))
		c.step("getmessageAsAdmin")
		c.sendHTML(c.fromID, T("Admin.ManageUser.GetTextResponse"), kbBackAdmin())
	case c.stepIs("getmessageAsAdmin"):
		c.sendHTML(c.fromID, T("Admin.ManageUser.SendMessageuser"), nil)
		to := c.user.S("Processing_value")
		if c.text != "" && c.text != "0" {
			c.sendHTML(to, sprintf("Admin.systemsms.sendedmessagetouser", withCustomEmoji(c.text, c.f.Entities)), nil)
		}
		if c.photo {
			c.b.TG.SendPhotoID(to, c.photoID, sprintf("Admin.systemsms.sendedmessagetouser", c.caption), nil, "HTML")
		}
		c.step("home")
	}
	return false
}

func statusButton(v string) *tg.InlineKeyboard { return ik(row(cb(v, v))) }

func (c *Ctx) panelRow() db.Row {
	return c.db().Select("marzban_panel", "*", "name_panel", c.user.S("Processing_value"))
}

func (c *Ctx) admPanelStatus() bool {
	pv := c.user.S("Processing_value")
	if c.text == T("Admin.managepanel.showpanelbtn") {
		c.sendHTML(c.fromID, T("Admin.managepanel.showpaneldec"), statusButton(c.panelRow().S("status")))
	}
	if c.datain == "activepanel" {
		c.upd("marzban_panel", "status", "disablepanel", "name_panel", pv)
		c.edit(T("Admin.managepanel.offpanel"), statusButton(c.panelRow().S("status")))
	} else if c.datain == "disablepanel" {
		c.upd("marzban_panel", "status", "activepanel", "name_panel", pv)
		c.edit(T("Admin.managepanel.onpanel"), statusButton(c.panelRow().S("status")))
	}
	if c.text == T("Admin.managepanel.showpaneltestbtn") {
		c.sendHTML(c.fromID, T("Admin.managepanel.showpaneldec"), statusButton(c.panelRow().S("statusTest")))
	}
	d := c.db()
	switch {
	case c.datain == "ontestshowpanel":
		c.upd("marzban_panel", "statusTest", "offtestshowpanel", "name_panel", pv)
		c.edit(T("Admin.managepanel.offpanel"), statusButton(c.panelRow().S("statusTest")))
	case c.datain == "offtestshowpanel":
		c.upd("marzban_panel", "statusTest", "ontestshowpanel", "name_panel", pv)
		c.edit(T("Admin.managepanel.onpanel"), statusButton(c.panelRow().S("statusTest")))
	case c.m(`banuserlist_(\w+)`):
		id := c.g(1)
		if d.Select("user", "*", "id", id).S("User_Status") == "block" {
			c.sendHTML(c.fromID, T("Admin.ManageUser.BlockedUser"), kbBackAdmin())
			return true
		}
		c.setUser("Processing_value", id)
		c.upd("user", "User_Status", "block", "id", id)
		c.sendHTML(c.fromID, T("Admin.ManageUser.BlockUser"), kbBackAdmin())
		c.step("adddecriptionblock")
	case c.stepIs("adddecriptionblock"):
		c.upd("user", "description_blocking", c.text, "id", pv)
		c.sendHTML(c.fromID, T("Admin.ManageUser.DescriptionBlock"), kbAdmin())
		c.step("home")
	case c.m(`unbanuserr_(\w+)`):
		id := c.g(1)
		if d.Select("user", "*", "id", id).S("User_Status") == "Active" {
			c.sendHTML(c.fromID, T("Admin.ManageUser.UserNotBlock"), kbBackAdmin())
			return true
		}
		c.upd("user", "User_Status", "Active", "id", id)
		c.upd("user", "description_blocking", "", "id", id)
		c.sendHTML(c.fromID, T("Admin.ManageUser.UserUnblocked"), kbAdmin())
		c.step("home")
	case c.text == T("Admin.changetext.ruletext"):
		c.sendHTML(c.fromID, T("Admin.ManageUser.ChangeTextGet")+c.texts["text_roll"], kbBackAdmin())
		c.step("text_roll")
	case c.stepIs("text_roll"):
		c.sendHTML(c.fromID, T("Admin.ManageUser.SaveText"), kbTextbot())
		c.saveBotText("text_roll")
		c.step("home")
	}
	switch {
	case c.text == T("Admin.keyboardadmin.user_services"):
		c.sendHTML(c.fromID, T("users.selectoption"), kbUserServices())
	case c.m(`confirmnumber_(\w+)`):
		id := c.g(1)
		c.upd("user", "number", "confrim number by admin", "id", id)
		c.stepOf(id, "home")
		c.sendHTML(c.fromID, T("Admin.phone.active"), kbUserServices())
	}
	switch {
	case c.text == T("Admin.channel.channelreport"):
		c.sendHTML(c.fromID, T("Admin.Channel.ReportChannel")+c.setting.S("Channel_Report"), kbBackAdmin())
		c.step("addchannelid")
	case c.stepIs("addchannelid"):
		c.sendHTML(c.fromID, T("Admin.Channel.SetChannelReport"), kbAdmin())
		c.upd("setting", "Channel_Report", c.text, "", nil)
		c.step("home")
		// (the PHP bot sent this test to the previous channel)
		c.sendHTML(c.text, T("Admin.Channel.TestChannel"), nil)
	}
	return false
}

func (c *Ctx) admShop() bool {
	d := c.db()
	pv := c.user.S("Processing_value")
	switch {
	case c.text == T("Admin.keyboardadmin.shop_section"):
		c.sendHTML(c.fromID, T("users.selectoption"), kbShop())
	case c.text == T("Admin.Product.addproduct"):
		if d.SelectCount("marzban_panel", "", nil) == 0 {
			c.sendHTML(c.fromID, T("Admin.managepanel.nullpaneladmin"), nil)
			return true
		}
		c.sendHTML(c.fromID, T("Admin.Product.AddProductStepOne"), kbBackAdmin())
		c.step("get_limit")
	case c.stepIs("get_limit"):
		code := randHex(2)
		d.Exec("INSERT IGNORE INTO product (name_product, code_product) VALUES (?, ?)", c.text, code)
		c.setUser("Processing_value", code)
		c.sendHTML(c.fromID, T("Admin.Product.Service_location"), c.kbPanelList())
		c.step("get_location")
	case c.stepIs("get_location"):
		c.upd("product", "Location", c.text, "code_product", pv)
		c.sendHTML(c.fromID, T("Admin.Product.Getcategory"), c.kbCategory())
		c.step("get_category")
	case c.stepIs("get_category"):
		cat := d.Select("category", "*", "remark", c.text)
		if cat == nil {
			c.sendHTML(c.fromID, T("Admin.Product.invalidcategory"), kbBackAdmin())
			return true
		}
		c.upd("product", "category", cat.S("id"), "code_product", pv)
		c.sendHTML(c.fromID, T("Admin.Product.GetLimit"), kbBackAdmin())
		c.step("get_time")
	case c.stepIs("get_time"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Product.Invalidvolume"), kbBackAdmin())
			return true
		}
		c.upd("product", "Volume_constraint", c.text, "code_product", pv)
		c.sendHTML(c.fromID, T("Admin.Product.GettIime"), kbBackAdmin())
		c.step("get_price")
	case c.stepIs("get_price"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Product.InvalidTime"), kbBackAdmin())
			return true
		}
		c.upd("product", "Service_time", c.text, "code_product", pv)
		c.sendHTML(c.fromID, T("Admin.Product.GetPrice"), kbBackAdmin())
		c.step("endstep")
	case c.stepIs("endstep"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Product.InvalidPrice"), kbBackAdmin())
			return true
		}
		c.upd("product", "price_product", c.text, "code_product", pv)
		c.sendHTML(c.fromID, T("Admin.Product.SaveProduct"), kbShop())
		c.step("home")
	}
	return false
}
