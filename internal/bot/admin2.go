package bot

import (
	"path"
	"strings"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"github.com/MHBehzadian/nexra-mirzabot/internal/panels"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
	"github.com/MHBehzadian/nexra-mirzabot/internal/tg"
)

func sleepMs(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

func (b *Bot) autopayPanelText() string {
	s := b.AutopaySettings()
	on := s.S("status") == "on"
	seen := "—"
	if s.S("last_seen") != "" {
		if ts, ok := php.Strtotime(s.S("last_seen")); ok {
			ago := nowUnix() - ts
			switch {
			case ago < 120:
				seen = "همین الان ✅"
			case ago < 3600:
				seen = itoa(ago/60) + " دقیقه پیش"
				if ago > 900 {
					seen += " ⚠️"
				} else {
					seen += " ✅"
				}
			default:
				seen = itoa(ago/3600) + " ساعت پیش ⚠️"
			}
		}
	}
	d := b.DB
	open := d.Count("SELECT COUNT(*) FROM autopay_order WHERE status = 'open'")
	paid := d.Count("SELECT COUNT(*) FROM autopay_order WHERE status = 'paid'")
	sms := d.Count("SELECT COUNT(*) FROM autopay_sms")
	state := "خاموش ⛔️"
	if on {
		state = "روشن ✅"
	}
	dev := ""
	if s.S("device_info") != "" {
		dev = "دستگاه: " + s.S("device_info") + "\n"
	}
	return "💳 <b>تأیید خودکار پرداخت</b>\n\n" +
		"وضعیت: " + state + "\n" +
		"آخرین ارتباط گوشی: " + seen + "\n" + dev +
		"\nپرداخت‌های در انتظار واریز: " + itoa(open) + "\n" +
		"پرداخت‌های خودکار تأییدشده: " + itoa(paid) + "\n" +
		"پیامک‌های دریافتی: " + itoa(sms) + "\n\n" +
		"آدرس سرور برای برنامه:\n<code>https://" + b.Cfg.Domain + "/autopay.php</code>"
}

func (b *Bot) autopayPanelKeyboard() *tg.InlineKeyboard {
	toggle := "✅ روشن کردن"
	if b.AutopayEnabled() {
		toggle = "⛔️ خاموش کردن"
	}
	return ik(row(cb(toggle, "autopay_toggle")), row(cb("🔑 نمایش کلید اتصال", "autopay_key")),
		row(cb("♻️ ساخت کلید جدید", "autopay_newkey")),
		row(cb("📨 آخرین پیامک‌ها", "autopay_sms"), cb("🧾 در انتظار واریز", "autopay_orders")),
		row(cb("🔄 بروزرسانی", "autopay_home")))
}

// AutopayRecentSMS is the text of the "last SMS" view.
func (b *Bot) AutopayRecentSMS() string {
	rows := b.DB.MustQuery("SELECT * FROM autopay_sms ORDER BY id DESC LIMIT 8")
	if len(rows) == 0 {
		return "هنوز پیامکی نرسیده."
	}
	t := "📨 <b>آخرین پیامک‌ها</b>\n\n"
	for _, r := range rows {
		t += "• " + r.S("received_at") + " — " + r.S("sender") + "\n" +
			"   مبلغ: " + nf(r.F("amount")) + " | " + r.S("direction") + " | <b>" + r.S("status") + "</b>\n"
	}
	return t
}

// AutopayOpenOrders is the text of the "waiting for deposit" view.
func (b *Bot) AutopayOpenOrders() string {
	rows := b.DB.MustQuery("SELECT * FROM autopay_order WHERE status = 'open' ORDER BY id DESC LIMIT 10")
	if len(rows) == 0 {
		return "هیچ پرداختی در انتظار واریز نیست."
	}
	t := "🧾 <b>در انتظار واریز</b>\n\n"
	for _, r := range rows {
		t += "• کاربر <code>" + r.S("id_user") + "</code> — باید <b>" + nf(r.F("amount")) + "</b> تومان بزند" +
			" (قیمت " + nf(r.F("base_price")) + ")\n   از " + r.S("created_at") + "\n"
	}
	return t
}

func (c *Ctx) admAutopay() bool {
	b := c.b
	if c.text == autopayAdminButton {
		c.sendHTML(c.fromID, b.autopayPanelText(), b.autopayPanelKeyboard())
		c.step("home")
	}
	if c.text == emojiIDButton {
		c.sendHTML(c.fromID, "یک پیام شامل ایموجی‌های پریمیوم بفرستید تا شناسه‌شان را بدهم:", kbBackAdmin())
		c.step("nexra_emoji_id")
		return true
	}
	if c.stepIs("nexra_emoji_id") {
		c.emojiIDReply()
		return true
	}
	switch c.datain {
	case "autopay_home":
		c.edit(b.autopayPanelText(), b.autopayPanelKeyboard())
	case "autopay_toggle":
		if b.AutopayEnabled() {
			b.AutopaySet("status", "off")
		} else {
			b.AutopaySet("status", "on")
		}
		c.edit(b.autopayPanelText(), b.autopayPanelKeyboard())
	case "autopay_key", "autopay_newkey":
		if c.datain == "autopay_newkey" {
			b.AutopaySet("device_key", randHex(24))
			b.AutopaySet("last_seen", nil)
		}
		t := "🔑 <b>کلید اتصال برنامه</b>\n\n" +
			"این رشته را در برنامه‌ی اندروید جای‌گذاری کن:\n\n" +
			"<code>" + b.AutopayPairing() + "</code>\n\n" +
			"⚠️ این کلید مثل رمز است؛ هرکس داشته باشد می‌تواند پرداخت جعلی ثبت کند. " +
			"اگر جایی لو رفت، «ساخت کلید جدید» را بزن."
		c.sendHTML(c.fromID, t, nil)
	case "autopay_sms":
		c.sendHTML(c.fromID, b.AutopayRecentSMS(), nil)
	case "autopay_orders":
		c.sendHTML(c.fromID, b.AutopayOpenOrders(), nil)
	}
	if c.text == T("Admin.keyboardadmin.admin_section") {
		c.sendHTML(c.fromID, T("users.selectoption"), kbAdminSection())
	}
	if c.text == T("Admin.keyboardadmin.settings") {
		c.sendHTML(c.fromID, T("users.selectoption"), kbSettingPanel())
	}
	if c.text == T("Admin.keyboardadmin.test_account_settings") {
		c.sendHTML(c.fromID, T("users.selectoption"), kbUsertest())
	}
	return false
}

// ApprovePayment is the admin "approve receipt" action, shared with the
// management API. It returns false when the payment was already handled.
// afterDirect (optional) runs right after the payment was applied, before the
// bookkeeping, matching where the PHP button handler edited the receipt.
func (b *Bot) ApprovePayment(order string, c *Ctx, adminID string, afterDirect func()) (done bool) {
	b.lockOrder(order, func() { done = b.approvePayment(order, c, adminID, afterDirect) })
	return done
}

func (b *Bot) approvePayment(order string, c *Ctx, adminID string, afterDirect func()) bool {
	d := b.DB
	rep := d.Select("Payment_report", "*", "id_order", order)
	if rep == nil || rep.S("payment_Status") == "paid" || rep.S("payment_Status") == "reject" {
		return false
	}
	b.AutopayCloseByOrder(order, "manual")
	b.DirectPayment(order, c)
	if afterDirect != nil {
		afterDirect()
	}
	uid := rep.S("id_user")
	d.Update("user", "Processing_value", "0", "id", uid)
	d.Update("user", "Processing_value_one", "0", "id", uid)
	d.Update("user", "Processing_value_tow", "0", "id", uid)
	d.Update("Payment_report", "payment_Status", "paid", "id_order", order)
	if ch := d.Setting().S("Channel_Report"); ch != "" {
		b.TG.SendMessage(ch, sprintf("Admin.Report.acceptcartresid", adminID, rep.S("price")), nil, "HTML")
	}
	return true
}

// RejectPayment marks a receipt rejected and tells the customer why.
func (b *Bot) RejectPayment(order, reason string) (done bool) {
	b.lockOrder(order, func() { done = b.rejectPayment(order, reason) })
	return done
}

func (b *Bot) rejectPayment(order, reason string) bool {
	d := b.DB
	rep := d.Select("Payment_report", "*", "id_order", order)
	if rep == nil || rep.S("payment_Status") == "paid" || rep.S("payment_Status") == "reject" {
		return false
	}
	d.Update("Payment_report", "payment_Status", "reject", "id_order", order)
	b.AutopayCloseByOrder(order, "rejected")
	d.Update("Payment_report", "dec_not_confirmed", reason, "id_order", order)
	b.TG.SendMessage(rep.S("id_user"), sprintf("users.moeny.rejectresid", reason, order), nil, "HTML")
	return true
}

func (c *Ctx) admPayments() bool {
	d := c.db()
	if c.m(`Confirm_pay_(\w+)`) {
		order := c.g(1)
		edit := func() {
			k := ik(row(cb(T("users.moeny.paymentaccepted"), "none")))
			c.b.TG.EditMessageCaption(c.fromID, c.messageID, c.caption, k)
		}
		if !c.b.ApprovePayment(order, c, c.fromID, edit) {
			c.alert(T("Admin.Payment.reviewedpayment"))
			return true
		}
	}
	switch {
	case c.m(`reject_pay_(\w+)`):
		order := c.g(1)
		rep := d.Select("Payment_report", "*", "id_order", order)
		c.setUser("Processing_value", rep.S("id_user"))
		c.setUser("Processing_value_one", order)
		if rep.S("payment_Status") == "reject" || rep.S("payment_Status") == "paid" {
			c.alert(T("Admin.Payment.reviewedpayment"))
			return true
		}
		c.upd("Payment_report", "payment_Status", "reject", "id_order", order)
		c.b.AutopayCloseByOrder(order, "rejected")
		c.sendHTML(c.fromID, T("Admin.Payment.Reasonrejecting"), kbBackAdmin())
		c.step("reject-dec")
		c.edit(c.textCallback, nil)
	case c.stepIs("reject-dec"):
		c.upd("Payment_report", "dec_not_confirmed", c.text, "id_order", c.user.S("Processing_value_one"))
		c.sendHTML(c.fromID, T("Admin.Payment.Rejected"), kbAdmin())
		c.sendHTML(c.user.S("Processing_value"), sprintf("users.moeny.rejectresid", c.text, c.user.S("Processing_value_one")), nil)
		c.step("home")
	}
	return false
}

func (c *Ctx) admProducts() bool {
	d := c.db()
	pv, pv1 := c.user.S("Processing_value"), c.user.S("Processing_value_one")
	switch {
	case c.text == T("Admin.Product.titlebtnremove"):
		c.sendHTML(c.fromID, T("Admin.Product.Rmove_location"), c.kbPanelList())
		c.step("selectloc")
	case c.stepIs("selectloc"):
		c.setUser("Processing_value", c.text)
		c.step("remove-product")
		c.sendHTML(c.fromID, T("Admin.Product.selectRemoveProduct"), c.kbProductsAdmin())
	case c.stepIs("remove-product"):
		if !c.inColumn("product", "name_product", c.text) {
			c.sendHTML(c.fromID, T("users.sell.error-product"), nil)
			return true
		}
		d.Exec("DELETE FROM product WHERE name_product = ? AND (Location = ? OR Location = ?)", c.text, pv, "/all")
		c.sendHTML(c.fromID, T("Admin.Product.RemoveedProduct"), kbShop())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Product.titlebtnedit"):
		c.sendHTML(c.fromID, T("Admin.Product.Rmove_location"), c.kbPanelList())
		c.step("selectlocedite")
	case c.stepIs("selectlocedite"):
		c.setUser("Processing_value_one", c.text)
		c.sendHTML(c.fromID, T("Admin.Product.selectEditProduct"), c.kbProductsAdmin())
		c.step("change_filde")
	case c.stepIs("change_filde"):
		if !c.inColumn("product", "name_product", c.text) {
			c.sendHTML(c.fromID, T("users.sell.error-product"), nil)
			return true
		}
		c.setUser("Processing_value", c.text)
		c.sendHTML(c.fromID, T("Admin.Product.selectfieldProduct"), kbChangeProduct())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Product.editprice"):
		c.sendHTML(c.fromID, T("Admin.Product.sendnewprice"), kbBackAdmin())
		c.step("change_price")
	case c.stepIs("change_price"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Product.InvalidPrice"), kbBackAdmin())
			return true
		}
		d.Exec("UPDATE product SET price_product = ? WHERE name_product = ? AND (Location = ? OR Location = ?)", c.text, pv, pv1, "/all")
		d.Exec("UPDATE invoice SET price_product = ? WHERE name_product = ? AND Service_location = ?", c.text, pv, pv1)
		c.sendHTML(c.fromID, T("Admin.Product.updatedprice"), kbShop())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Product.editcategory"):
		c.sendHTML(c.fromID, T("Admin.Product.sendnewcategory"), c.kbCategory())
		c.step("change_category")
	case c.stepIs("change_category"):
		cat := d.Select("category", "*", "remark", c.text)
		if cat == nil {
			c.sendHTML(c.fromID, T("Admin.Product.invalidcategory"), kbBackAdmin())
			return true
		}
		d.Exec("UPDATE product SET category = ? WHERE name_product = ? AND (Location = ? OR Location = ?)", cat.S("id"), pv, pv1, "/all")
		c.sendHTML(c.fromID, T("Admin.Product.updatedcategory"), kbShop())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Product.editname"):
		c.sendHTML(c.fromID, T("Admin.Product.sendnewname"), kbBackAdmin())
		c.step("change_name")
	case c.stepIs("change_name"):
		d.Exec("UPDATE product SET name_product = ? WHERE name_product = ? AND (Location = ? OR Location = ?)", c.text, pv, pv1, "/all")
		d.Exec("UPDATE invoice SET name_product = ? WHERE name_product = ? AND Service_location = ?", c.text, pv, pv1)
		c.sendHTML(c.fromID, T("Admin.Product.updatedname"), kbShop())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Product.editvolume"):
		c.sendHTML(c.fromID, T("Admin.Product.sendnewvolume"), kbBackAdmin())
		c.step("change_val")
	case c.stepIs("change_val"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Product.Invalidvolume"), kbBackAdmin())
			return true
		}
		d.Exec("UPDATE invoice SET Volume = ? WHERE name_product = ? AND Service_location = ?", c.text, pv, pv1)
		d.Exec("UPDATE product SET Volume_constraint = ? WHERE name_product = ? AND Location = ?", c.text, pv, pv1)
		c.sendHTML(c.fromID, T("Admin.Product.updatedvolume"), kbShop())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Product.edittime"):
		c.sendHTML(c.fromID, T("Admin.Product.NewTime"), kbBackAdmin())
		c.step("change_time")
	case c.stepIs("change_time"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Product.InvalidTime"), kbBackAdmin())
			return true
		}
		d.Exec("UPDATE invoice SET Service_time = ? WHERE name_product = ? AND Service_location = ?", c.text, pv, pv1)
		d.Exec("UPDATE product SET Service_time = ? WHERE name_product = ? AND Location = ?", c.text, pv, pv1)
		c.sendHTML(c.fromID, T("Admin.Product.TimeUpdated"), kbShop())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Usertest.settimeusertest"):
		c.sendHTML(c.fromID, sprintf("Admin.Usertest.sendtimeusertest", c.setting.S("time_usertest")), kbBackAdmin())
		c.step("updatetime")
	case c.stepIs("updatetime"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Product.InvalidTime"), kbBackAdmin())
			return true
		}
		c.upd("setting", "time_usertest", c.text, "", nil)
		c.sendHTML(c.fromID, T("Admin.Usertest.TimeUpdated"), kbUsertest())
		c.step("home")
	}
	return false
}

func (c *Ctx) admUserBalance() bool {
	d := c.db()
	switch {
	case c.text == T("Admin.Usertest.setvolumeusertest"):
		c.sendHTML(c.fromID, sprintf("Admin.Usertest.sendvoluemusertest", c.setting.S("val_usertest")), kbBackAdmin())
		c.step("val_usertest")
	case c.stepIs("val_usertest"):
		if !php.CtypeDigit(c.text) || php.Intval(c.text) < 100 {
			c.sendHTML(c.fromID, "❌ حجم نامعتبر است. حداقل حجم مجاز اکانت تست 100 مگابایت است (حجم نامحدود پشتیبانی نمی‌شود).", kbBackAdmin())
			return true
		}
		c.upd("setting", "val_usertest", c.text, "", nil)
		c.sendHTML(c.fromID, T("Admin.Usertest.VolumeUpdated"), kbUsertest())
		c.step("home")
	case c.m(`addbalanceuser_(\w+)`):
		c.setUser("Processing_value", c.g(1))
		c.sendHTML(c.fromID, T("Admin.Balance.PriceBalance"), kbBackAdmin())
		c.step("get_price_add")
	case c.stepIs("get_price_add"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Balance.Invalidprice"), kbBackAdmin())
			return true
		}
		if php.Intval(c.text) > 100000000 {
			c.sendHTML(c.fromID, T("Admin.Balance.maxpricebalance"), kbBackAdmin())
			return true
		}
		c.sendHTML(c.fromID, T("Admin.Balance.AddBalanceUser"), kbUserServices())
		target := c.user.S("Processing_value")
		u := d.Select("user", "*", "id", target)
		d.Update("user", "Balance", php.NumStr(u.F("Balance")+php.Floatval(c.text)), "id", target)
		c.sendHTML(target, sprintf("Admin.Balance.AddedBalance", nfs(c.text)), nil)
		c.text = nfs(c.text)
		c.step("home")
	case c.m(`lowbalanceuser_(\w+)`):
		c.setUser("Processing_value", c.g(1))
		c.sendHTML(c.fromID, T("Admin.Balance.PriceBalancek"), kbBackAdmin())
		c.step("get_price_Negative")
	case c.stepIs("get_price_Negative"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Balance.Invalidprice"), kbBackAdmin())
			return true
		}
		if php.Intval(c.text) > 100000000 {
			c.sendHTML(c.fromID, T("Admin.Balance.maxpricebalance"), kbBackAdmin())
			return true
		}
		c.sendHTML(c.fromID, T("Admin.Balance.NegativeBalanceUser"), kbUserServices())
		target := c.user.S("Processing_value")
		u := d.Select("user", "*", "id", target)
		d.Update("user", "Balance", php.NumStr(u.F("Balance")-php.Floatval(c.text)), "id", target)
		c.sendHTML(target, sprintf("Admin.Balance.ReduceBalance", nfs(c.text)), nil)
		c.text = nfs(c.text)
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Discount.titlebtn"):
		c.sendHTML(c.fromID, T("Admin.Discount.GetCode"), kbBackAdmin())
		c.step("get_code")
	case c.stepIs("get_code"):
		if !re(`^[A-Za-z]+$`).MatchString(c.text) {
			c.sendHTML(c.fromID, T("Admin.Discount.ErrorCode"), nil)
			return true
		}
		d.Exec("INSERT INTO Discount (code) VALUES (?)", c.text)
		c.sendHTML(c.fromID, T("Admin.Discount.PriceCode"), nil)
		c.step("get_price_code")
		c.setUser("Processing_value", c.text)
	case c.stepIs("get_price_code"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Balance.Invalidprice"), kbBackAdmin())
			return true
		}
		c.upd("Discount", "price", c.text, "code", c.user.S("Processing_value"))
		c.sendHTML(c.fromID, T("Admin.Discount.SaveCode"), kbAdmin())
		c.step("home")
	}
	return false
}

func (c *Ctx) admDiscountsAndPanelFlags() bool {
	d := c.db()
	pm := c.b.PM
	pv := c.user.S("Processing_value")
	if c.text == T("Admin.managepanel.sublinkstatus") {
		p := c.panelRow()
		if p.IsNull("sublink") {
			c.upd("marzban_panel", "sublink", "onsublink", "name_panel", pv)
		}
		p = c.panelRow()
		if p.S("configManual") == "onconfig" {
			c.sendHTML(c.fromID, T("Admin.managepanel.checkoffconfig"), nil)
			return true
		}
		c.sendHTML(c.fromID, T("Admin.Status.subTitle"), statusButton(p.S("sublink")))
	}
	if c.datain == "onsublink" {
		c.upd("marzban_panel", "sublink", "offsublink", "name_panel", pv)
		c.edit(T("Admin.Status.subStatusOff"), statusButton(c.panelRow().S("sublink")))
	} else if c.datain == "offsublink" {
		c.upd("marzban_panel", "sublink", "onsublink", "name_panel", pv)
		c.edit(T("Admin.Status.subStatuson"), statusButton(c.panelRow().S("sublink")))
	}
	if c.text == T("Admin.managepanel.configstatus") {
		p := c.panelRow()
		if p.IsNull("configManual") {
			c.upd("marzban_panel", "configManual", "offconfig", "name_panel", pv)
		}
		p = c.panelRow()
		if p.S("sublink") == "onsublink" {
			c.sendHTML(c.fromID, T("Admin.managepanel.notoffsublink"), nil)
			return true
		}
		c.sendHTML(c.fromID, T("Admin.Status.configTitle"), statusButton(p.S("configManual")))
	}
	switch {
	case c.datain == "onconfig":
		c.upd("marzban_panel", "configManual", "offconfig", "name_panel", pv)
		c.edit(T("Admin.Status.configStatusOff"), statusButton(c.panelRow().S("configManual")))
	case c.datain == "offconfig":
		c.upd("marzban_panel", "configManual", "onconfig", "name_panel", pv)
		c.edit(T("Admin.Status.configStatuson"), statusButton(c.panelRow().S("configManual")))
	case c.m(`vieworderall_(\w+)`):
		for _, o := range d.SelectAll("invoice", "*", "id_user", c.g(1)) {
			c.sendHTML(c.fromID, orderDetails(o), nil)
		}
		c.sendHTML(c.fromID, T("Admin.ManageUser.SendOrder"), kbAdmin())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.Discount.titlebtnremove"):
		c.sendHTML(c.fromID, T("Admin.Discount.RemoveCode"), c.kbDiscountList())
		c.step("remove-Discount")
	case c.stepIs("remove-Discount"):
		if !c.inColumn("Discount", "code", c.text) {
			c.sendHTML(c.fromID, T("Admin.Discount.NotCode"), nil)
			return true
		}
		d.Exec("DELETE FROM Discount WHERE code = ?", c.text)
		c.sendHTML(c.fromID, T("Admin.Discount.RemovedCode"), kbShop())
	}
	switch {
	case c.text == T("Admin.ManageUser.removeorderbtn"):
		c.sendHTML(c.fromID, T("Admin.ManageUser.RemoveService"), kbBackAdmin())
		c.step("removeservice")
	case c.stepIs("removeservice"):
		inv := d.Select("invoice", "*", "username", c.text)
		panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
		if pm.DataUser(panel.S("name_panel"), c.text).Isset("status") {
			pm.RemoveUser(panel.S("name_panel"), c.text)
		}
		d.Exec("DELETE FROM invoice WHERE username = ?", c.text)
		c.sendHTML(c.fromID, T("Admin.ManageUser.RemovedService"), kbAdmin())
		c.step("home")
	}
	switch {
	case c.text == T("Admin.managepanel.methodusername"):
		c.sendHTML(c.fromID, T("Admin.managepanel.decmthodusername"), kbMethodUsername())
		c.step("updatemethodusername")
	case c.stepIs("updatemethodusername"):
		c.upd("marzban_panel", "MethodUsername", c.text, "name_panel", pv)
		c.sendHTML(c.fromID, T("Admin.AlgortimeUsername.SaveData"), kbAdmin())
		if c.text == T("users.customtextandrandom") {
			c.step("getnamecustom")
			c.sendHTML(c.fromID, T("Admin.managepanel.customnamesend"), c.kbBackUser())
			return true
		}
		c.step("home")
	case c.stepIs("getnamecustom"):
		if !wordName(c.text) {
			c.send(c.fromID, T("Admin.managepanel.invalidname"), kbBackAdmin(), "html")
			return true
		}
		c.upd("setting", "namecustome", c.text, "", nil)
		c.step("home")
		p := c.panelRow()
		c.setUser("Processing_value", c.text)
		c.outTypePanel(p.S("type"), T("Admin.managepanel.savedname"))
	}
	return false
}

func orderDetails(o db.Row) string {
	return sprintf("Admin.ManageUser.Datails", o.S("id_invoice"), o.S("Status"), o.S("id_user"), o.S("username"), o.S("Service_location"),
		o.S("name_product"), o.S("price_product"), o.S("Volume"), o.S("Service_time"), php.Jdate("Y/m/d H:i:s", o.I("time_sell")))
}

var _ = panels.Out{}

func payStatusLabel(v, on, off string) string {
	switch v {
	case on:
		return T("Admin.turnon")
	case off:
		return T("Admin.turnoff")
	}
	return ""
}

func (c *Ctx) financeKeyboard() *tg.InlineKeyboard {
	d := c.db()
	card, now, iran, aqa := d.PaySetting("Cartstatus"), d.PaySetting("nowpaymentstatus"), d.PaySetting("digistatus"), d.PaySetting("statusaqayepardakht")
	return ik(
		row(cb(T("users.moeny.setting"), "settingcart"), cb(payStatusLabel(card, "oncard", "offcard"), "editpay-cart-"+card), cb(T("users.moeny.cart_to_Cart_btn"), "none")),
		row(cb(T("users.moeny.setting"), "SettingnowPayment"), cb(payStatusLabel(now, "onnowpayment", "offnowpayment"), "editpay-nowpayment-"+now), cb(T("users.moeny.nowpayment_gateway_status"), "none")),
		row(cb(T("users.moeny.setting"), "Settingaqayepardakht"), cb(payStatusLabel(aqa, "onaqayepardakht", "offaqayepardakht"), "editpay-aqayepardakht-"+aqa), cb(T("users.moeny.mr_payment_gateway"), "none")),
		row(cb(payStatusLabel(iran, "ondigi", "offdigi"), "editpay-iranpay-"+iran), cb(T("users.moeny.currency_rial_gateway"), "none")),
	)
}

// TogglePayMethod flips one gateway on/off (also used by the API).
func (b *Bot) TogglePayMethod(method, current string) {
	type m struct{ key, on, off string }
	all := map[string]m{
		"cart":          {"Cartstatus", "oncard", "offcard"},
		"nowpayment":    {"nowpaymentstatus", "onnowpayment", "offnowpayment"},
		"iranpay":       {"digistatus", "ondigi", "offdigi"},
		"aqayepardakht": {"statusaqayepardakht", "onaqayepardakht", "offaqayepardakht"},
	}
	x, ok := all[method]
	if !ok {
		return
	}
	v := x.on
	if current == x.on {
		v = x.off
	}
	b.DB.Update("PaySetting", "ValuePay", v, "NamePay", x.key)
}

func (c *Ctx) admPaySettings() bool {
	switch {
	case c.text == T("Admin.keyboardadmin.finance"):
		c.sendHTML(c.fromID, T("users.moeny.settingpay"), c.financeKeyboard())
	case c.m(`^editpay-(.*)-(.*)`):
		c.b.TogglePayMethod(c.g(1), c.g(2))
		c.edit(T("users.moeny.settingpay"), c.financeKeyboard())
	case c.datain == "settingcart":
		c.sendHTML(c.fromID, T("users.selectoption"), kbCartManage())
	}
	switch {
	case c.text == T("users.moeny.card_number_settings"):
		c.sendHTML(c.fromID, sprintf("users.moeny.sendcart", c.db().PaySetting("CartDescription")), kbBackAdmin())
		c.step("changecard")
	case c.stepIs("changecard"):
		c.sendHTML(c.fromID, T("Admin.SettingPayment.Savacard"), kbCartManage())
		c.upd("PaySetting", "ValuePay", c.text, "NamePay", "CartDescription")
		c.step("home")
	}
	if c.datain == "SettingnowPayment" {
		c.sendHTML(c.fromID, T("users.selectoption"), kbNowPayments())
	}
	switch {
	case c.text == T("users.moeny.nowpayment_api"):
		c.sendHTML(c.fromID, sprintf("users.moeny.getapinowpayment", c.db().PaySetting("apinowpayment")), kbBackAdmin())
		c.step("apinowpayment")
	case c.stepIs("apinowpayment"):
		c.sendHTML(c.fromID, T("Admin.SettingnowPayment.Savaapi"), kbNowPayments())
		c.upd("PaySetting", "ValuePay", c.text, "NamePay", "apinowpayment")
		c.step("home")
	}
	if c.datain == "Settingaqayepardakht" {
		c.sendHTML(c.fromID, T("users.selectoption"), kbAqayepardakht())
	}
	switch {
	case c.text == T("users.moeny.mr_payment_merchant_settings"):
		c.sendHTML(c.fromID, sprintf("users.moeny.getmarchent", c.db().PaySetting("merchant_id_aqayepardakht")), kbBackAdmin())
		c.step("merchant_id_aqayepardakht")
	case c.stepIs("merchant_id_aqayepardakht"):
		c.sendHTML(c.fromID, T("Admin.SettingnowPayment.Savaapi"), kbAqayepardakht())
		c.upd("PaySetting", "ValuePay", c.text, "NamePay", "merchant_id_aqayepardakht")
		c.step("home")
	}
	return false
}

var _ = path.Dir
var _ = strings.TrimSpace
