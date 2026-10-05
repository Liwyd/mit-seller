package bot

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"github.com/MHBehzadian/nexra-mirzabot/internal/panels"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
	"github.com/MHBehzadian/nexra-mirzabot/internal/tg"
)

// Panel management stays behind the secret code. index/admin.php only
// checked the code when entering the menu; here the unlock also expires, and
// every action that changes a panel's connection needs it.
const panelUnlockSeconds = 30 * 60

func (c *Ctx) unlockPanels() { c.db().SetKV("panel_unlock_"+c.fromID, itoa(nowUnix())) }

func (c *Ctx) panelsUnlocked() bool {
	return nowUnix()-php.Intval(c.db().KV("panel_unlock_"+c.fromID)) <= panelUnlockSeconds
}

// requireUnlock asks for the secret again when the unlock has expired.
func (c *Ctx) requireUnlock() bool {
	if c.panelsUnlocked() {
		return true
	}
	c.sendHTML(c.fromID, secretPrompt, kbBackAdmin())
	c.step("nexra_panel_access_gate_manage")
	return false
}

func phpDirname(s string) string {
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i > 0 {
		return s[:i]
	}
	return s
}

func (c *Ctx) admManagePanel() bool {
	d := c.db()
	pm := c.b.PM
	pv := c.user.S("Processing_value")
	mpk := func(k string) string { return T("Admin.managepanel.keyboardpanel." + k) }
	switch {
	case c.text == T("Admin.keyboardadmin.manage_panel"):
		c.sendHTML(c.fromID, secretPrompt, kbBackAdmin())
		c.step("nexra_panel_access_gate_manage")
	case c.stepIs("nexra_panel_access_gate_manage"):
		if !c.secretOK() {
			c.sendHTML(c.fromID, "❌ کد اشتباه است.", kbAdmin())
			c.step("home")
			return true
		}
		c.unlockPanels()
		c.sendHTML(c.fromID, T("Admin.managepanel.getloc"), c.kbPanelList())
		c.step("GetLocationEdit")
	case c.stepIs("GetLocationEdit"):
		p := d.Select("marzban_panel", "*", "name_panel", c.text)
		c.setUser("Processing_value", c.text)
		c.outTypePanel(p.S("type"), T("users.selectoption"))
		c.step("home")
	case c.text == mpk("namepanel"):
		if !c.requireUnlock() {
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.GetNameNew"), kbBackAdmin())
		c.step("GetNameNew")
	case c.stepIs("GetNameNew"):
		p := c.panelRow()
		c.outTypePanel(p.S("type"), T("Admin.managepanel.ChangedNmaePanel"))
		c.upd("marzban_panel", "name_panel", c.text, "name_panel", pv)
		c.upd("invoice", "Service_location", c.text, "Service_location", pv)
		c.upd("product", "Location", c.text, "Location", pv)
		c.setUser("Processing_value", c.text)
		c.step("home")
	case c.text == mpk("editurl"):
		if !c.requireUnlock() {
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.geturlnew"), kbBackAdmin())
		c.step("GeturlNew")
	case c.stepIs("GeturlNew"):
		if !filterValidateURL(c.text) {
			c.sendHTML(c.fromID, T("Admin.managepanel.Invalid-domain"), kbBackAdmin())
			return true
		}
		c.outTypePanel(c.panelRow().S("type"), T("Admin.managepanel.ChangedurlPanel"))
		c.upd("marzban_panel", "url_panel", c.text, "name_panel", pv)
		c.step("home")
	case c.text == mpk("editusername"):
		if !c.requireUnlock() {
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.getusernamenew"), kbBackAdmin())
		c.step("GetusernameNew")
	case c.stepIs("GetusernameNew"):
		c.outTypePanel(c.panelRow().S("type"), T("Admin.managepanel.ChangedusernamePanel"))
		c.upd("marzban_panel", "username_panel", c.text, "name_panel", pv)
		c.step("home")
	case c.text == mpk("editpassword"):
		if !c.requireUnlock() {
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.getpasswordnew"), kbBackAdmin())
		c.step("GetpaawordNew")
	case c.stepIs("GetpaawordNew"):
		c.outTypePanel(c.panelRow().S("type"), T("Admin.managepanel.ChangedpasswordPanel"))
		c.upd("marzban_panel", "password_panel", c.text, "name_panel", pv)
		c.step("home")
	case c.text == mpk("editnexracreds"):
		if !c.requireUnlock() {
			return true
		}
		k := ik(
			row(cb("🔗 آدرس Nexra", "editnexracred_url_panel")),
			row(cb("👤 یوزرنیم Nexra", "editnexracred_username_panel")),
			row(cb("🔐 پسورد Nexra", "editnexracred_password_panel")),
			row(cb("🔗 آدرس واقعیِ مرزبان", "editnexracred_marzban_url_direct")),
			row(cb("👤 یوزرنیمِ واقعیِ مرزبان", "editnexracred_marzban_username_direct")),
			row(cb("🔐 پسوردِ واقعیِ مرزبان", "editnexracred_marzban_password_direct")),
		)
		c.sendHTML(c.fromID, "کدام مورد را می‌خواهید ویرایش کنید؟", k)
	case c.m(`editnexracred_(.*)`):
		field := c.g(1)
		if !nexraField(field) || !c.requireUnlock() {
			return true
		}
		c.setUser("Processing_value_one", field)
		c.del()
		c.sendHTML(c.fromID, "مقدار جدید را ارسال کنید:", kbBackAdmin())
		c.step("nexra_editfield_value")
	case c.stepIs("nexra_editfield_value"):
		field := c.user.S("Processing_value_one")
		if !nexraField(field) {
			c.step("home")
			return true
		}
		if (field == "url_panel" || field == "marzban_url_direct") && !filterValidateURL(c.text) {
			c.sendHTML(c.fromID, T("Admin.managepanel.Invalid-domain"), kbBackAdmin())
			return true
		}
		c.upd("marzban_panel", field, c.text, "name_panel", pv)
		// cached tokens belong to the old credentials
		c.upd("marzban_panel", "datelogin", nil, "name_panel", pv)
		c.outTypePanel(c.panelRow().S("type"), "✅ اطلاعات با موفقیت بروزرسانی شد.")
		c.step("home")
	case c.text == mpk("editinound") || c.text == T("Admin.managepanel.setgroup"):
		if !c.requireUnlock() {
			return true
		}
		if c.text == T("Admin.managepanel.setgroup") {
			c.sendHTML(c.fromID, mpk("getgroup"), kbBackAdmin())
		} else {
			c.sendHTML(c.fromID, mpk("getidinbound"), kbBackAdmin())
		}
		c.step("getinboundiid")
	case c.stepIs("getinboundiid"):
		c.outTypePanel(c.panelRow().S("type"), mpk("setinbound"))
		c.upd("marzban_panel", "inboundid", c.text, "name_panel", pv)
		c.step("home")
	case c.text == mpk("linksub"):
		if !c.requireUnlock() {
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.geturlnew"), kbBackAdmin())
		c.step("GeturlNewx")
	case c.stepIs("GeturlNewx"):
		if !filterValidateURL(c.text) {
			c.sendHTML(c.fromID, T("Admin.managepanel.Invalid-domain"), kbBackAdmin())
			return true
		}
		p := c.panelRow()
		value := c.text
		if p.S("type") == "x-ui_single" {
			body, code, err := fetch(c.text)
			if err != nil || code != 200 {
				c.sendHTML(c.fromID, T("Admin.managepanel.subinvalidDomain"), nil)
				return true
			}
			body = panels.DecodeIfBase64(body)
			proto := strings.SplitN(body, "://", 2)[0]
			if proto != "vmess" && proto != "vless" && proto != "trojan" && proto != "ss" {
				c.sendHTML(c.fromID, T("Admin.managepanel.subinvalid"), nil)
				return true
			}
			value = phpDirname(c.text)
		}
		c.outTypePanel(p.S("type"), T("Admin.managepanel.ChangedurlPanel"))
		c.upd("marzban_panel", "linksubx", value, "name_panel", pv)
		c.step("home")
	}
	if c.text == mpk("removepanel") {
		if !c.requireUnlock() {
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.RemovedPanel"), kbAdmin())
		d.Exec("DELETE FROM marzban_panel WHERE name_panel = ?", pv)
	}
	_ = pm
	return false
}

func nexraField(f string) bool {
	switch f {
	case "url_panel", "username_panel", "password_panel", "marzban_url_direct", "marzban_username_direct", "marzban_password_direct":
		return true
	}
	return false
}

func (c *Ctx) admExtraBalance() bool {
	d := c.db()
	switch {
	case c.text == T("Admin.managepanel.keyboardpanel.setvolume"):
		c.sendHTML(c.fromID, T("users.Extra_volume.SetPrice")+c.setting.S("Extra_volume"), kbBackAdmin())
		c.step("GetPriceExtra")
	case c.stepIs("GetPriceExtra"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Balance.Invalidprice"), kbBackAdmin())
			return true
		}
		c.upd("setting", "Extra_volume", c.text, "", nil)
		c.sendHTML(c.fromID, T("users.Extra_volume.ChangedPrice"), kbShop())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Balance.SendBalanceAll"):
		c.sendHTML(c.fromID, T("Admin.Balance.addallbalance"), kbBackAdmin())
		c.step("add_Balance_all")
	case c.stepIs("add_Balance_all"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Balance.Invalidprice"), kbBackAdmin())
			return true
		}
		c.sendHTML(c.fromID, T("Admin.Balance.AddBalanceUsers"), kbUserServices())
		d.Exec("UPDATE user SET Balance = Balance + ?", php.Intval(c.text))
		c.step("home")
	}
	pv := c.user.S("Processing_value")
	switch {
	case c.text == T("Admin.Discountsell.create"):
		c.sendHTML(c.fromID, T("Admin.Discountsell.GetCode"), kbBackAdmin())
		c.step("get_codesell")
	case c.stepIs("get_codesell"):
		if c.inColumn("DiscountSell", "codeDiscount", c.text) {
			c.sendHTML(c.fromID, T("Admin.Discount.Discountused"), kbBackAdmin())
			return true
		}
		if !re(`^[A-Za-z\d]+$`).MatchString(c.text) {
			c.sendHTML(c.fromID, T("Admin.Discount.ErrorCode"), nil)
			return true
		}
		d.Exec("INSERT INTO DiscountSell (codeDiscount, usedDiscount, price, limitDiscount, usefirst) VALUES (?, ?, ?, ?,?)", c.text, "0", "0", "0", "0")
		c.sendHTML(c.fromID, T("Admin.Discount.PriceCodesell"), nil)
		c.step("get_price_codesell")
		c.setUser("Processing_value", c.text)
	case c.stepIs("get_price_codesell"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Balance.Invalidprice"), kbBackAdmin())
			return true
		}
		c.upd("DiscountSell", "price", c.text, "codeDiscount", pv)
		c.sendHTML(c.fromID, T("Admin.Discountsell.getlimit"), kbBackAdmin())
		c.step("getlimitcode")
	case c.stepIs("getlimitcode"):
		c.upd("DiscountSell", "limitDiscount", c.text, "codeDiscount", pv)
		c.sendHTML(c.fromID, T("Admin.Discount.typediscount"), kbBackAdmin())
		c.step("getusefirst")
	case c.stepIs("getusefirst"):
		c.upd("DiscountSell", "usefirst", c.text, "codeDiscount", pv)
		c.sendHTML(c.fromID, T("Admin.Discount.SaveCode"), kbAdmin())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Discountsell.remove"):
		c.sendHTML(c.fromID, T("Admin.Discount.RemoveCode"), c.kbDiscountSellList())
		c.step("remove-Discountsell")
	case c.stepIs("remove-Discountsell"):
		if !c.inColumn("DiscountSell", "codeDiscount", c.text) {
			c.sendHTML(c.fromID, T("Admin.Discount.NotCode"), nil)
			return true
		}
		d.Exec("DELETE FROM DiscountSell WHERE codeDiscount = ?", c.text)
		c.sendHTML(c.fromID, T("Admin.Discount.RemovedCode"), kbShop())
		c.step("home")
	}
	return false
}

func (c *Ctx) affRow() map[string]string {
	r := c.db().Select("affiliates", "*", "", nil)
	out := map[string]string{}
	for k, v := range r {
		out[k] = v.S
	}
	return out
}

func (c *Ctx) admAffiliates() bool {
	switch {
	case c.text == T("Admin.keyboardadmin.affiliate_settings"):
		c.sendHTML(c.fromID, T("users.selectoption"), kbAffiliates())
	case c.text == T("Admin.affiliate.status"):
		c.sendHTML(c.fromID, T("Admin.Status.affiliates"), statusButton(c.affRow()["affiliatesstatus"]))
	case c.datain == "onaffiliates":
		c.upd("affiliates", "affiliatesstatus", "offaffiliates", "", nil)
		c.edit(T("Admin.Status.affiliatesStatusOff"), statusButton(c.affRow()["affiliatesstatus"]))
	case c.datain == "offaffiliates":
		c.upd("affiliates", "affiliatesstatus", "onaffiliates", "", nil)
		c.edit(T("Admin.Status.affiliatesStatuson"), statusButton(c.affRow()["affiliatesstatus"]))
	}
	switch {
	case c.text == T("Admin.affiliate.Percentageset"):
		c.sendHTML(c.fromID, T("users.affiliates.setpercentage"), kbBackAdmin())
		c.step("setpercentage")
	case c.stepIs("setpercentage"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.invalidvalue"), nil)
			return true
		}
		c.sendHTML(c.fromID, T("users.affiliates.changedpercentage"), kbAffiliates())
		c.upd("affiliates", "affiliatespercentage", c.text, "", nil)
		c.step("home")
	case c.text == T("Admin.affiliate.setbaner"):
		c.sendHTML(c.fromID, T("users.affiliates.banner"), kbBackAdmin())
		c.step("setbanner")
	case c.stepIs("setbanner"):
		if !c.photo {
			c.sendHTML(c.fromID, T("users.affiliates.invalidbanner"), kbBackAdmin())
			return true
		}
		c.upd("affiliates", "description", withCustomEmoji(c.caption, c.f.CaptionEntities), "", nil)
		c.upd("affiliates", "id_media", c.photoID, "", nil)
		c.sendHTML(c.fromID, T("users.affiliates.insertbanner"), kbAffiliates())
		c.step("home")
	case c.text == T("Admin.affiliate.porsantafterbuy"):
		c.sendHTML(c.fromID, T("Admin.Status.commission"), statusButton(c.affRow()["status_commission"]))
	case c.datain == "oncommission":
		c.upd("affiliates", "status_commission", "offcommission", "", nil)
		c.edit(T("Admin.Status.commissionStatusOff"), statusButton(c.affRow()["status_commission"]))
	case c.datain == "offcommission":
		c.upd("affiliates", "status_commission", "oncommission", "", nil)
		c.edit(T("Admin.Status.commissionStatuson"), statusButton(c.affRow()["status_commission"]))
	case c.text == T("Admin.affiliate.gift"):
		c.sendHTML(c.fromID, T("Admin.Status.Discountaffiliates"), statusButton(c.affRow()["Discount"]))
	case c.datain == "onDiscountaffiliates":
		c.upd("affiliates", "Discount", "offDiscountaffiliates", "", nil)
		c.edit(T("Admin.Status.DiscountaffiliatesStatusOff"), statusButton(c.affRow()["Discount"]))
	case c.datain == "offDiscountaffiliates":
		c.upd("affiliates", "Discount", "onDiscountaffiliates", "", nil)
		c.edit(T("Admin.Status.DiscountaffiliatesStatuson"), statusButton(c.affRow()["Discount"]))
	}
	return false
}

func (c *Ctx) admCancelRequests() bool {
	d := c.db()
	pm := c.b.PM
	pv := c.user.S("Processing_value")
	switch {
	case c.text == T("Admin.affiliate.giftstart"):
		c.sendHTML(c.fromID, T("users.affiliates.priceDiscount"), kbBackAdmin())
		c.step("getdiscont")
	case c.stepIs("getdiscont"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.invalidvalue"), nil)
			return true
		}
		c.sendHTML(c.fromID, T("users.affiliates.changedpriceDiscount"), kbAffiliates())
		c.upd("affiliates", "price_Discount", c.text, "", nil)
		c.step("home")
	case c.m(`rejectremoceserviceadmin-(\w+)`):
		svc := c.g(1)
		st := d.Select("cancel_service", "*", "username", svc).S("status")
		if st == "accept" || st == "reject" {
			c.alert(T("users.status.residaccepted"))
			return true
		}
		c.step("descriptionsrequsts")
		c.setUser("Processing_value", svc)
		c.sendHTML(c.fromID, T("users.status.rejectrequest"), c.kbBackUser())
	case c.stepIs("descriptionsrequsts"):
		c.sendHTML(c.fromID, T("users.status.acceptrequestnote"), kbAdmin())
		inv := d.Select("invoice", "*", "username", pv)
		c.upd("cancel_service", "status", "reject", "username", pv)
		c.upd("cancel_service", "description", c.text, "username", pv)
		c.step("home")
		c.sendHTML(inv.S("id_user"), sprintf("users.status.rejectsendtouser", pv, c.text), nil)
	case c.m(`remoceserviceadmin-(\w+)`):
		svc := c.g(1)
		st := d.Select("cancel_service", "*", "username", svc).S("status")
		if st == "accept" || st == "reject" {
			c.alert(T("users.status.residaccepted"))
			return true
		}
		c.step("getpricerequests")
		c.setUser("Processing_value", svc)
		c.sendHTML(c.fromID, T("users.status.getpriceforadd"), c.kbBackUser())
	case c.stepIs("getpricerequests"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.invalidvalue"), nil)
		}
		inv := d.Select("invoice", "*", "username", pv)
		if inv.F("price_product") < php.Floatval(c.text) {
			c.sendHTML(c.fromID, T("Admin.maxvalue"), c.kbBackUser())
			return true
		}
		c.sendHTML(c.fromID, T("users.status.acceptrequestnote"), kbAdmin())
		c.step("home")
		panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
		if pm.DataUser(panel.S("name_panel"), pv).Isset("status") {
			pm.RemoveUser(panel.S("name_panel"), pv)
		}
		c.upd("cancel_service", "status", "accept", "username", pv)
		c.upd("invoice", "status", "removedbyadmin", "username", pv)
		c.sendHTML(inv.S("id_user"), sprintf("users.status.acceptrequest", pv), nil)
		refund := php.Intval(c.text)
		if refund != 0 {
			u := d.Select("user", "*", "id", inv.S("id_user"))
			d.Update("user", "Balance", php.Intval(u.S("Balance"))+refund, "id", inv.S("id_user"))
			c.sendHTML(inv.S("id_user"), sprintf("users.status.addedbalanceremove", nf(float64(refund))), nil)
		}
		c.report(sprintf("Admin.Report.reportremove", c.fromID, nf(float64(refund)), c.username, inv.S("id_user")))
	}
	return false
}

// cron switches (the PHP bot added/removed crontab lines)
var cronKeys = map[string]string{"test": "cron_test", "volume": "cron_volume", "time": "cron_time", "remove": "cron_remove", "card": "cron_card"}

func (b *Bot) CronOn(name string) bool { return b.DB.KV(cronKeys[name]) == "1" }

func (b *Bot) SetCron(name string, on bool) {
	v := "0"
	if on {
		v = "1"
	}
	b.DB.SetKV(cronKeys[name], v)
}

func (c *Ctx) admCron() bool {
	pv := c.user.S("Processing_value")
	if c.text == T("Admin.managepanel.keyboardpanel.on_hold_status") {
		p := c.panelRow()
		if p.IsNull("onholdstatus") {
			c.upd("marzban_panel", "onholdstatus", "offonhold", "name_panel", pv)
		}
		c.sendHTML(c.fromID, T("Admin.Status.onhold"), statusButton(c.panelRow().S("onholdstatus")))
	}
	if c.datain == "ononhold" {
		c.upd("marzban_panel", "onholdstatus", "offonhold", "name_panel", pv)
		c.edit(T("Admin.Status.offstatus"), statusButton(c.panelRow().S("onholdstatus")))
	} else if c.datain == "offonhold" {
		c.upd("marzban_panel", "onholdstatus", "ononhold", "name_panel", pv)
		c.edit(T("Admin.Status.onstatus"), statusButton(c.panelRow().S("onholdstatus")))
	}
	if c.text == T("Admin.keyboardadmin.settingscron") {
		c.sendHTML(c.fromID, T("users.selectoption"), kbCron())
	}
	type sw struct {
		key, name string
		on        bool
		msg       string
	}
	for _, s := range []sw{
		{"Admin.cron.test.active", "test", true, "Admin.cron.test.dec"},
		{"Admin.cron.test.disable", "test", false, "Admin.cron.test.disabled"},
		{"Admin.cron.volume.active", "volume", true, "Admin.cron.volume.dec"},
		{"Admin.cron.volume.disable", "volume", false, "Admin.cron.test.disabled"},
		{"Admin.cron.time.active", "time", true, "Admin.cron.time.dec"},
		{"Admin.cron.time.disable", "time", false, "Admin.cron.test.disabled"},
		{"Admin.cron.remove.active", "remove", true, "Admin.cron.remove.dec"},
		{"Admin.cron.remove.disable", "remove", false, "Admin.cron.test.disabled"},
	} {
		if c.text == T(s.key) {
			c.sendHTML(c.fromID, T(s.msg), nil)
			c.b.SetCron(s.name, s.on)
		}
	}
	return false
}

func (c *Ctx) admUserSearch() bool {
	d := c.db()
	switch {
	case c.text == T("Admin.keyboardadmin.user_search"):
		c.sendHTML(c.fromID, T("Admin.ManageUser.BlockUserId"), kbBackAdmin())
		c.step("show_infos")
	case c.stepIs("show_infos"):
		if !d.Exists("user", "id", c.text) {
			c.sendHTML(c.fromID, T("Admin.not-user"), kbBackAdmin())
			return true
		}
		id := c.text
		const active = "(status = 'active' OR status = 'end_of_time'  OR status = 'end_of_volume' OR status = 'sendedwarn')"
		services := d.Count("SELECT COUNT(*) FROM invoice WHERE "+active+" AND id_user = ?", id)
		paid := d.Scalar("SELECT SUM(price) FROM Payment_report WHERE payment_Status = 'paid' AND id_user = ?", id)
		bought := d.Scalar("SELECT SUM(price_product) FROM invoice WHERE "+active+" AND id_user = ?", id)
		if bought == "" {
			bought = "0"
		}
		u := d.Select("user", "*", "id", id)
		roll := ""
		switch u.S("roll_Status") {
		case "1":
			roll = T("Admin.ManageUser.Acceptedphone")
		case "0":
			roll = T("Admin.ManageUser.Failedphone")
		}
		k := ik(
			row(cb(T("Admin.ManageUser.addbalanceuser"), "addbalanceuser_"+id), cb(T("Admin.ManageUser.lowbalanceuser"), "lowbalanceuser_"+id)),
			row(cb(T("Admin.ManageUser.banuserlist"), "banuserlist_"+id), cb(T("Admin.ManageUser.unbanuserlist"), "unbanuserr_"+id)),
			row(cb(T("Admin.ManageUser.confirmnumber"), "confirmnumber_"+id)),
			row(cb(T("Admin.getlimitusertest.setlimitbtn"), "limitusertest_"+id)),
			row(cb(T("Admin.ManageUser.verify"), "verify_"+id), cb(T("Admin.ManageUser.removeverify"), "verifyun_"+id)),
			row(cb(T("Admin.ManageUser.vieworderuser"), "vieworderall_"+id), cb(T("Admin.ManageUser.addorder"), "addordermanualـ"+id)),
		)
		msg := sprintf("Admin.ManageUser.infouser", u.S("User_Status"), u.S("username"), id, id, php.Jdate("Y/m/d H:i:s", u.I("last_message_time")),
			u.S("limit_usertest"), roll, u.S("number"), nf(u.F("Balance")), services, paid, bought, u.S("affiliatescount"), u.S("affiliates"), u.S("verify"))
		c.sendHTML(c.fromID, msg, k)
		c.sendHTML(c.fromID, T("users.selectoption"), kbAdmin())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.cron.remove.timeset"):
		c.sendHTML(c.fromID, T("Admin.cron.remove.dectime"), kbBackAdmin())
		c.step("gettimeremove")
	case c.stepIs("gettimeremove"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.cron.remove.invalidtime"), kbBackAdmin())
			return true
		}
		c.sendHTML(c.fromID, T("Admin.cron.remove.timeseted"), kbCron())
		c.step("home")
		c.upd("setting", "removedayc", c.text, "", nil)
	}
	return false
}

func onOff(v string) string {
	switch v {
	case "1":
		return T("Admin.Status.statuson")
	case "0":
		return T("Admin.Status.statusoff")
	}
	return ""
}

func (c *Ctx) settingsKeyboard() *tg.InlineKeyboard {
	s := c.db().Setting()
	card := "0"
	if c.b.CronOn("card") {
		card = "1"
	}
	r := func(label, cbLabel, key, val, side string) []B {
		return row(cb(onOff(val), "editstsuts-"+key+"-"+val), cb(label, side))
	}
	return ik(
		row(cb(T("Admin.Status.statussubject"), "subjectde"), cb(T("Admin.Status.subject"), "subject")),
		r(T("Admin.Status.stautsbot"), "", "statusbot", s.S("Bot_Status"), "statusbot"),
		r(T("users.Rulesbtn"), "", "roll_Status", s.S("roll_Status"), "roll_Status"),
		r(T("users.maangeuser"), "", "NotUser", s.S("NotUser"), "NotUser"),
		r(T("Admin.Help.statushelp"), "", "help_Status", s.S("help_Status"), "help_Status"),
		r(T("Admin.ManageUser.verifynumber"), "", "get_number", s.S("get_number"), "get_number"),
		r(T("Admin.ManageUser.verifynumberirani"), "", "iran_number", s.S("iran_number"), "iran_number"),
		r(T("Admin.ManageUser.verify"), "", "verify", s.S("status_verify"), "status_verify"),
		r(T("Admin.category.status"), "", "category", s.S("statuscategory"), "statuscategory"),
		r(T("Admin.Automatic_confirmation.title"), "", "Automatic_confirmation", card, "Automatic_confirmation"),
		r(T("users.moeny.copy_cart_status"), "", "copycart", s.S("copy_cart"), "copycart"),
	)
}

// ToggleSetting flips one of the on/off settings (also used by the API).
func (b *Bot) ToggleSetting(typ, value string) {
	nv := "1"
	if value == "1" {
		nv = "0"
	}
	col := map[string]string{
		"statusbot": "Bot_Status", "roll_Status": "roll_Status", "NotUser": "NotUser", "help_Status": "help_Status",
		"get_number": "get_number", "iran_number": "iran_number", "verify": "status_verify",
		"category": "statuscategory", "copycart": "copy_cart",
	}
	if typ == "Automatic_confirmation" {
		b.SetCron("card", value != "1")
		return
	}
	if c, ok := col[typ]; ok {
		b.DB.Update("setting", c, nv, "", nil)
	}
}

// MigrateLegacySettings turns the old text-valued switches into 1/0.
func (b *Bot) MigrateLegacySettings() {
	d := b.DB
	s := d.Setting()
	pairs := []struct{ col, on, off string }{
		{"Bot_Status", "✅  ربات روشن است", "❌ ربات خاموش است"},
		{"roll_Status", "✅ تایید قانون روشن است", "❌ تایید قوانین خاموش است"},
		{"NotUser", "onnotuser", "offnotuser"},
		{"help_Status", "✅ آموزش فعال است", "❌ آموزش غیرفعال است"},
		{"get_number", "✅ تایید شماره موبایل روشن است", "❌ احرازهویت شماره تماس غیرفعال است"},
		{"iran_number", "✅ احرازشماره ایرانی روشن است", "❌ بررسی شماره ایرانی غیرفعال است"},
	}
	for _, p := range pairs {
		switch s.S(p.col) {
		case p.on:
			d.Update("setting", p.col, "1", "", nil)
		case p.off:
			d.Update("setting", p.col, "0", "", nil)
		}
	}
}

func (c *Ctx) admTail() bool {
	d := c.db()
	pm := c.b.PM
	pv := c.user.S("Processing_value")
	switch {
	case c.text == T("users.status.manageService"):
		c.sendHTML(c.fromID, T("users.status.manageServicedec"), kbBackAdmin())
		c.step("getservceid")
	case c.stepIs("getservceid"):
		u := pm.MarzneshinGetUser(c.text, c.panelRow())
		if jmap(u).s("detail") == "User not found" {
			c.sendHTML(c.fromID, T("Admin.managepanel.keyboardpanel.usernotfount"), nil)
			return true
		}
		b, _ := json.Marshal(u["service_ids"])
		c.upd("marzban_panel", "proxies", string(b), "name_panel", pv)
		c.step("home")
		c.sendHTML(c.fromID, T("Admin.managepanel.setsetting"), optMarzneshin())
	case c.text == T("Admin.Help.edithelp"):
		c.sendHTML(c.fromID, T("Admin.Help.selecthelpforedit"), c.kbHelpList())
		c.step("getnameforedite")
	case c.stepIs("getnameforedite"):
		c.sendHTML(c.fromID, T("users.selectoption"), kbHelpEdit())
		c.setUser("Processing_value", c.text)
		c.step("home")
	case c.text == T("Admin.Help.change.name"):
		c.sendHTML(c.fromID, T("Admin.Help.change.sendnewname"), kbBackAdmin())
		c.step("changenamehelp")
	case c.stepIs("changenamehelp"):
		if len(c.text) >= 150 {
			c.sendHTML(c.fromID, T("Admin.Help.change.namemax"), nil)
			return true
		}
		c.upd("help", "name_os", c.text, "name_os", pv)
		c.sendHTML(c.fromID, T("Admin.Help.change.updated"), kbHelpEdit())
		c.step("home")
	case c.text == T("Admin.Help.change.dec"):
		c.sendHTML(c.fromID, T("Admin.Help.change.newdec"), kbBackAdmin())
		c.step("changedeshelp")
	case c.stepIs("changedeshelp"):
		c.upd("help", "Description_os", withCustomEmoji(c.text, c.f.Entities), "name_os", pv)
		c.sendHTML(c.fromID, T("Admin.Help.change.updated"), kbHelpEdit())
		c.step("home")
	case c.text == T("Admin.Help.change.editmedia"):
		c.sendHTML(c.fromID, T("Admin.Help.change.editmedianew"), kbBackAdmin())
		c.step("changemedia")
	case c.stepIs("changemedia"):
		if c.photo {
			c.upd("help", "Media_os", c.photoID, "name_os", pv)
			c.upd("help", "type_Media_os", "photo", "name_os", pv)
		} else if c.video {
			c.upd("help", "Media_os", c.videoID, "name_os", pv)
			c.upd("help", "type_Media_os", "video", "name_os", pv)
		}
		c.sendHTML(c.fromID, T("Admin.Help.change.updated"), kbHelpEdit())
		c.step("home")
	case c.text == T("Admin.managepanel.setinbound"):
		if !c.requireUnlock() {
			return true
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.setinbounddec"), kbBackAdmin())
		c.step("setinboundandprotocol")
	case c.stepIs("setinboundandprotocol"):
		return c.setInbounds()
	case c.text == T("Admin.keyboardadmin.seetingstatus"):
		c.b.MigrateLegacySettings()
		c.sendHTML(c.fromID, T("Admin.Status.BotTitle"), c.settingsKeyboard())
	case c.m(`^editstsuts-(.*)-(.*)`):
		c.b.ToggleSetting(c.g(1), c.g(2))
		c.edit(T("Admin.Status.BotTitle"), c.settingsKeyboard())
	case c.m(`verify_(\w+)`):
		id := c.g(1)
		if d.Select("user", "*", "id", id).S("verify") == "1" {
			c.sendHTML(c.fromID, T("Admin.ManageUser.verifyeduser"), kbBackAdmin())
			return true
		}
		c.upd("user", "verify", "1", "id", id)
		c.sendHTML(c.fromID, T("Admin.ManageUser.verifyeduser"), kbAdmin())
		c.step("home")
	case c.m(`verifyun_(\w+)`):
		id := c.g(1)
		if d.Select("user", "*", "id", id).S("verify") == "0" {
			c.sendHTML(c.fromID, T("Admin.ManageUser.verifyed"), kbBackAdmin())
			return true
		}
		c.upd("user", "verify", "0", "id", id)
		c.sendHTML(c.fromID, T("Admin.ManageUser.unverifyed"), kbAdmin())
		c.step("home")
	case c.text == T("Admin.category.add"):
		c.sendHTML(c.fromID, T("Admin.category.getname"), kbBackAdmin())
		c.step("getremarkcategory")
	case c.stepIs("getremarkcategory"):
		c.sendHTML(c.fromID, T("Admin.category.addedcategry"), kbShop())
		c.step("home")
		d.Exec("INSERT INTO category (remark) VALUES (?)", c.text)
	case c.text == T("Admin.category.remove"):
		c.sendHTML(c.fromID, T("Admin.category.getcatgory"), c.kbCategory())
		c.step("removecategory")
	case c.stepIs("removecategory"):
		c.sendHTML(c.fromID, T("Admin.category.removedcategory"), kbShop())
		c.step("home")
		d.Exec("DELETE FROM category WHERE remark = ? ", c.text)
	case c.text == T("Admin.ManageUser.searchorder"):
		c.sendHTML(c.fromID, T("Admin.ManageUser.ViewOrder"), kbBackAdmin())
		c.step("getidfororder")
	case c.stepIs("getidfororder"):
		o := d.Select("invoice", "*", "username", c.text)
		if o == nil {
			c.sendHTML(c.fromID, T("Admin.ManageUser.OrderNotFound"), nil)
			return true
		}
		c.sendHTML(c.fromID, orderDetails(o), kbUserServices())
		c.step("home")
	case c.m(`addordermanualـ(\w+)`):
		c.savedata(true, "userid", c.g(1))
		c.sendHTML(c.fromID, T("Admin.addorder.onestep"), kbBackAdmin())
		c.step("getusernameconfig")
	case c.stepIs("getusernameconfig"):
		name := strings.ToLower(c.text)
		if !wordName(name) {
			c.send(c.fromID, T("users.status.Invalidusername"), c.kbBackUser(), "html")
			return true
		}
		if d.Exists("invoice", "username", name) {
			c.sendHTML(c.fromID, T("Admin.addorder.user_exits"), nil)
			return true
		}
		c.savedata(false, "username", name)
		c.sendHTML(c.fromID, T("Admin.addorder.getname_panel"), c.kbPanelList())
		c.step("getnamepanelconfig")
	case c.stepIs("getnamepanelconfig"):
		c.savedata(false, "name_panel", c.text)
		c.sendHTML(c.fromID, T("Admin.addorder.get_product"), c.kbProductsAdmin())
		c.step("stependforaddorder")
	case c.stepIs("stependforaddorder"):
		u := c.userdata()
		p := d.One("SELECT * FROM product  WHERE name_product = ? AND (Location = ? OR Location = '/all') LIMIT 1", c.text, u["name_panel"])
		d.Exec("INSERT IGNORE INTO invoice (id_user, id_invoice, username, time_sell, Service_location, name_product, price_product, Volume, Service_time, Status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			u["userid"], randHex(2), u["username"], itoa(nowUnix()), u["name_panel"], nullable(p, "name_product"), nullable(p, "price_product"), nullable(p, "Volume_constraint"), nullable(p, "Service_time"), "active")
		c.sendHTML(c.fromID, T("Admin.addorder.added_order"), kbAdmin())
		c.step("home")
	}
	return false
}

func nullable(r db.Row, k string) any {
	if r == nil {
		return nil
	}
	v, ok := r[k]
	if !ok || v.Null {
		return nil
	}
	return v.S
}

func (c *Ctx) setInbounds() bool {
	pm := c.b.PM
	pv := c.user.S("Processing_value")
	p := c.panelRow()
	if p.S("type") == "nexra" {
		c.sendHTML(c.fromID, "این تنظیم برای پنل Nexra لازم نیست - اینباند و پروکسی هر ادمین از داشبورد خودِ Nexra Panel مدیریت می‌شود.", kbBackAdmin())
		c.step("home")
		return true
	}
	if p.S("type") == "marzban" {
		u := pm.MarzbanGetUser(c.text, p)
		proxies, ok := u["proxies"].(map[string]any)
		if jmap(u).s("msg") == "User not found" || !ok {
			c.send(c.fromID, T("users.status.usernotfound"), nil, "html")
			return true
		}
		for k, v := range proxies {
			m, _ := v.(map[string]any)
			if m == nil {
				m = map[string]any{}
			}
			if k == "shadowsocks" || k == "trojan" {
				delete(m, "password")
			} else {
				delete(m, "id")
			}
			proxies[k] = m
		}
		in, _ := json.Marshal(u["inbounds"])
		px, _ := json.Marshal(proxies)
		c.upd("marzban_panel", "inbounds", string(in), "name_panel", pv)
		c.upd("marzban_panel", "proxies", string(px), "name_panel", pv)
	} else {
		cl := pm.SUIGetClient(c.text, p)
		if len(cl) == 0 {
			c.sendHTML(c.fromID, T("Admin.managepanel.keyboardpanel.usernotfount"), optSUI())
			return true
		}
		var services []any
		switch x := cl["inbounds"].(type) {
		case []any:
			services = x
		case map[string]any:
			for _, v := range x {
				services = append(services, v)
			}
		}
		if services == nil {
			services = []any{}
		}
		b, _ := json.Marshal(services)
		c.upd("marzban_panel", "proxies", string(b), "name_panel", pv)
	}
	c.sendHTML(c.fromID, T("Admin.managepanel.setedinbound"), optMarzban())
	c.step("home")
	return false
}

func fetch(u string) (string, int, error) {
	cl := &http.Client{Timeout: 20 * time.Second}
	res, err := cl.Get(u)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	b := make([]byte, 0, 4096)
	buf := make([]byte, 32*1024)
	for {
		n, err := res.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil || len(b) > 8<<20 {
			break
		}
	}
	return string(b), res.StatusCode, nil
}
