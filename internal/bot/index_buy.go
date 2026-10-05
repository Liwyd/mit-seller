package bot

import (
	"html"
	"math"
	"strings"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/tg"
)

func (c *Ctx) secHelpSupport() bool {
	d := c.db()
	if c.text == c.texts["text_help"] || c.datain == "helpbtn" || c.text == "/help" {
		if php.LooseEq(c.setting.S("help_Status"), "0") {
			c.sendHTML(c.fromID, T("users.help.disablehelp"), nil)
			return true
		}
		c.sendHTML(c.fromID, T("users.selectoption"), c.kbHelpList())
		c.step("sendhelp")
	} else if c.stepIs("sendhelp") {
		h := d.Select("help", "*", "name_os", c.text)
		if h.S("Media_os") != "" {
			switch h.S("type_Media_os") {
			case "video":
				c.b.TG.SendVideo(c.fromID, h.S("Media_os"), h.S("Description_os"))
			case "photo":
				c.b.TG.SendPhotoID(c.fromID, h.S("Media_os"), h.S("Description_os"), nil, "HTML")
			}
		} else {
			c.sendHTML(c.fromID, h.S("Description_os"), c.kbHelpList())
		}
	}

	if c.text == c.texts["text_support"] || c.text == "/support" {
		c.sendHTML(c.fromID, T("users.support.btnsupport"), c.kbSupport())
	} else if c.datain == "support" {
		c.sendHTML(c.fromID, T("users.support.sendmessageuser"), c.kbBackUser())
		c.step("gettextpm")
	} else if c.stepIs("gettextpm") {
		c.sendHTML(c.fromID, T("users.support.sendmessageadmin"), c.kbMain())
		resp := ik(row(cb(T("users.support.answermessage"), "Response_"+c.fromID)))
		for _, a := range c.adminIDs {
			if c.text != "" && c.text != "0" {
				c.sendHTML(a, sprintf("users.support.GetMessageOfUser", c.fromID, c.username, c.text), resp)
			}
			if c.photo {
				c.b.TG.SendPhotoID(a, c.photoID, sprintf("users.support.GetMessageOfUser", c.fromID, c.username, c.caption), resp, "HTML")
			}
		}
		c.step("home")
	}
	if c.datain == "fqQuestions" {
		c.sendHTML(c.fromID, c.texts["text_dec_fq"], nil)
	}
	if c.text == c.texts["text_account"] {
		count := d.SelectCount("invoice", "id_user", c.fromID)
		msg := sprintf("users.account", html.EscapeString(c.firstName), c.fromID, nf(c.user.F("Balance")), count, c.user.S("affiliatescount"), php.JdateNow("Y/m/d"), php.JdateNow("H:i:s"))
		c.sendHTML(c.fromID, msg, c.kbPanel())
	}
	return false
}

func (c *Ctx) productFor(code, location string) db.Row {
	return c.db().One("SELECT * FROM product WHERE code_product = ? AND (location = ? OR location = '/all') LIMIT 1", code, location)
}

func (c *Ctx) secBuy() bool {
	d := c.db()
	switch {
	case c.text == c.texts["text_sell"] || c.datain == "buy" || c.text == "/buy":
		n := d.SelectCount("marzban_panel", "status", "activepanel")
		if n == 0 {
			c.sendHTML(c.fromID, T("Admin.managepanel.nullpanel"), nil)
			return true
		}
		if c.numberGate(false) {
			return true
		}
		if n == 1 {
			panel := d.Select("marzban_panel", "*", "status", "activepanel")
			c.setUser("Processing_value", panel.S("name_panel"))
			if php.LooseEq(c.setting.S("statuscategory"), "0") {
				if d.SelectCount("product", "", nil) == 0 {
					c.sendHTML(c.fromID, T("Admin.Product.nullpProduct"), nil)
					return true
				}
				c.sendHTML(c.fromID, sprintf("users.buy.selectService", panel.S("name_panel")), c.kbProduct(panel.S("name_panel"), "backuser", panel.S("MethodUsername"), nil))
			} else {
				if d.SelectCount("category", "", nil) == 0 {
					c.sendHTML(c.fromID, T("users.category.NotFound"), nil)
					return true
				}
				if c.datain == "buy" {
					c.edit(T("users.category.selectCategory"), c.kbCategoryBuy("backuser", panel.S("name_panel")))
				} else {
					c.sendHTML(c.fromID, T("users.category.selectCategory"), c.kbCategoryBuy("backuser", panel.S("name_panel")))
				}
			}
		} else {
			if c.datain == "buy" {
				c.edit(T("users.Service.Location"), c.kbPanelsUser())
			} else {
				c.sendHTML(c.fromID, T("users.Service.Location"), c.kbPanelsUser())
			}
		}
	case c.m(`^categorylist_(.*)`):
		cat := c.g(1)
		if d.SelectCount("product", "", nil) == 0 {
			c.sendHTML(c.fromID, T("Admin.Product.nullpProduct"), nil)
			return true
		}
		loc := d.Select("marzban_panel", "*", "name_panel", c.user.S("Processing_value"))
		if loc == nil {
			c.sendHTML(c.fromID, T("users.category.error"), nil)
			return true
		}
		c.edit(sprintf("users.buy.selectService", loc.S("name_panel")), c.kbProduct(loc.S("name_panel"), "buy", loc.S("MethodUsername"), &cat))
		c.setUser("Processing_value", loc.S("name_panel"))
	case c.m(`^location_(.*)`):
		panel := d.Select("marzban_panel", "*", "id", c.g(1))
		c.setUser("Processing_value", panel.S("name_panel"))
		if php.LooseEq(c.setting.S("statuscategory"), "0") {
			if d.SelectCount("product", "", nil) == 0 {
				c.sendHTML(c.fromID, T("Admin.Product.nullpProduct"), nil)
				return true
			}
			c.edit(sprintf("users.buy.selectService", panel.S("name_panel")), c.kbProduct(panel.S("name_panel"), "buy", panel.S("MethodUsername"), nil))
		} else {
			if d.SelectCount("category", "", nil) == 0 {
				c.sendHTML(c.fromID, T("users.category.NotFound"), nil)
				return true
			}
			c.edit(T("users.category.selectCategory"), c.kbCategoryBuy("buy", panel.S("name_panel")))
		}
	case c.m(`^prodcutservices_(.*)`):
		c.setUser("Processing_value_one", c.g(1))
		c.send(c.fromID, T("users.selectusername"), c.kbBackUser(), "html")
		c.step("endstepuser")
	case c.stepIs("waitusernamechoice_buy"):
		// paid purchases offering "custom or random" (customuserchoice)
		return c.buyUsernameChoice()
	case c.stepIs("endstepuser") || c.m(`prodcutservice_(.*)`):
		return c.buildInvoice()
	case (c.stepIs("payment") && c.datain == "confirmandgetservice") || c.datain == "confirmandgetserviceDiscount":
		return c.payFromBalance()
	case c.datain == "aptdc":
		c.sendHTML(c.fromID, T("users.Discount.getcodesell"), c.kbBackUser())
		c.step("getcodesellDiscount")
		c.del()
	case c.stepIs("getcodesellDiscount"):
		return c.applySellDiscount()
	}
	return false
}

// buyUsernameChoice handles the two buttons shown to a buyer when the panel
// lets the customer pick between a custom and a random username. The PHP bot
// only did this for trial accounts; paid purchases fell through to an empty
// username.
func (c *Ctx) buyUsernameChoice() bool {
	switch c.datain {
	case "usernamechoice_custom_buy":
		c.del()
		c.send(c.fromID, T("users.selectusername"), c.kbBackUser(), "html")
		c.step("endstepuser")
		return true
	case "usernamechoice_random_buy":
		c.del()
		c.user.Set("step", "endstepuser_random") // in memory only
		return c.buildInvoice()
	}
	k := ik(row(cb("✍️ "+T("users.customusername"), "usernamechoice_custom_buy"), cb("🎲 "+T("users.customidAndRandom"), "usernamechoice_random_buy")))
	c.sendHTML(c.fromID, T("users.selectusernamechoice"), k)
	return true
}

func (c *Ctx) buildInvoice() bool {
	d := c.db()
	var prodcut string
	if !c.stepIs("endstepuser") && !c.stepIs("endstepuser_random") {
		prodcut = c.g(1)
	}
	panel := d.Select("marzban_panel", "*", "name_panel", c.user.S("Processing_value"))
	if panel == nil {
		c.send(c.fromID, T("users.category.error"), c.kbMain(), "html")
		c.step("home")
		return true
	}
	method := panel.S("MethodUsername")
	var loc string
	switch {
	case method == T("users.customusername") || (method == T("users.customuserchoice") && c.stepIs("endstepuser")):
		if !validServiceUsername(c.text) {
			c.sendHTML(c.fromID, T("users.invalidusername"), c.kbBackUser())
			return true
		}
		loc = c.user.S("Processing_value_one")
		method = T("users.customusername")
	case method == T("users.customuserchoice") && c.stepIs("endstepuser_random"):
		loc = c.user.S("Processing_value_one")
		method = T("users.customidAndRandom")
	case method == T("users.customuserchoice"):
		c.del()
		if prodcut == "" {
			c.send(c.fromID, T("users.category.error"), c.kbMain(), "html")
			c.step("home")
			return true
		}
		c.setUser("Processing_value_one", prodcut)
		c.step("waitusernamechoice_buy")
		k := ik(row(cb("✍️ "+T("users.customusername"), "usernamechoice_custom_buy"), cb("🎲 "+T("users.customidAndRandom"), "usernamechoice_random_buy")))
		c.sendHTML(c.fromID, T("users.selectusernamechoice"), k)
		return true
	default:
		c.del()
		loc = prodcut
	}
	if loc == "" {
		c.send(c.fromID, T("users.category.error"), c.kbMain(), "html")
		c.step("home")
		return true
	}
	c.setUser("Processing_value_one", loc)
	p := c.productFor(loc, c.user.S("Processing_value"))
	if p == nil {
		c.sendHTML(c.fromID, T("users.status.error2"), c.kbMain())
		c.step("home")
		return true
	}
	random := randHex(2)
	userAc := strings.ToLower(c.generateUsername(method, c.username, random, c.text))
	if c.usernameTaken(panel.S("name_panel"), userAc) {
		userAc = itoa(randomInt(1000000, 9999999)) + userAc
	}
	c.setUser("Processing_value_tow", userAc)
	volume := p.S("Volume_constraint")
	if php.LooseEq(volume, "0") || volume == "" {
		volume = T("users.status.Unlimited")
	}
	msg := sprintf("users.buy.invoicebuy", userAc, p.S("name_product"), p.S("Service_time"), nfs(p.S("price_product")), volume, nf(c.user.F("Balance")))
	c.sendHTML(c.fromID, msg, kbPayment())
	c.step("payment")
	return false
}

func (c *Ctx) payFromBalance() bool {
	d := c.db()
	pm := c.b.PM
	c.edit(c.textCallback, ik())
	parts := strings.Split(c.user.S("Processing_value_four"), "_")
	p := c.productFor(c.user.S("Processing_value_one"), c.user.S("Processing_value"))
	if p == nil {
		c.sendHTML(c.fromID, T("users.status.error2"), c.kbMain())
		return true
	}
	panel := d.Select("marzban_panel", "*", "name_panel", c.user.S("Processing_value"))
	if panel == nil {
		c.sendHTML(c.fromID, T("users.status.error2"), c.kbMain())
		return true
	}
	if panel.S("linksubx") == "" && (panel.S("type") == "x-ui_single" || panel.S("type") == "alireza") {
		for _, a := range c.adminIDs {
			c.sendHTML(a, sprintf("Admin.managepanel.notsetlinksub", panel.S("name_panel")), nil)
		}
		c.sendHTML(c.fromID, T("Admin.managepanel.paneldeactive"), c.kbMain())
		return true
	}
	userAc := c.user.S("Processing_value_tow")
	idInvoice := randHex(4)
	if !phpTruthy(p.S("price_product")) {
		return true
	}
	price := p.S("price_product")
	if c.datain == "confirmandgetserviceDiscount" {
		price = ""
		if len(parts) > 2 {
			price = parts[2]
		}
	}
	balance := c.user.F("Balance")
	if php.Floatval(price) > balance {
		c.setUser("Processing_value", php.NumStr(php.Floatval(price)-balance))
		c.sendHTML(c.fromID, T("users.sell.None-credit"), c.kbStepPayment())
		c.step("get_step_payment")
		d.Exec("INSERT IGNORE INTO invoice(id_user, id_invoice, username,time_sell, Service_location, name_product, price_product, Volume, Service_time,Status) VALUES (?, ?, ?, ?, ?, ?, ?, ?,?,?)",
			c.fromID, idInvoice, userAc, nowUnix(), panel.S("name_panel"), p.S("name_product"), p.S("price_product"), p.S("Volume_constraint"), p.S("Service_time"), "unpaid")
		c.setUser("Processing_value_one", userAc)
		c.setUser("Processing_value_tow", "getconfigafterpay")
		return true
	}
	if d.Exists("invoice", "id_invoice", idInvoice) {
		idInvoice = itoa(randomInt(1000000, 9999999)) + idInvoice
	}
	d.Exec("INSERT IGNORE INTO invoice (id_user, id_invoice, username, time_sell, Service_location, name_product, price_product, Volume, Service_time, Status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		c.fromID, idInvoice, userAc, nowUnix(), c.user.S("Processing_value"), p.S("name_product"), p.S("price_product"), p.S("Volume_constraint"), p.S("Service_time"), "active")
	var expire int64
	if p.S("Service_time") != "0" {
		expire = php.PlusDaysUnix(p.S("Service_time"))
	}
	out := pm.CreateUser(panel.S("name_panel"), userAc, expire, php.Floatval(p.S("Volume_constraint"))*math.Pow(1024, 3), false)
	if !out.Isset("username") {
		c.sendHTML(c.fromID, T("users.sell.ErrorConfig"), c.kbMain())
		msg := sprintf("users.buy.errorInCreate", out.MsgJSON(), c.fromID, c.username)
		for _, a := range c.adminIDs {
			c.sendHTML(a, msg, nil)
		}
		c.step("home")
		return true
	}
	if c.datain == "confirmandgetserviceDiscount" && len(parts) > 1 {
		// Processing_value_four is "dis_<code>_<price>"; index.php looked
		// the code up as parts[0] ("dis"), so usage was never counted.
		code := parts[1]
		sd := d.Select("DiscountSell", "*", "codeDiscount", code)
		d.Update("DiscountSell", "usedDiscount", php.Intval(sd.S("usedDiscount"))+1, "codeDiscount", code)
		c.report(sprintf("users.Report.discountused", c.username, c.fromID, code))
		c.setUser("Processing_value_four", "0")
	}
	c.payCommission(c.user, php.Floatval(price))
	c.deliverService(c.fromID, panel, out, userAc, p.S("name_product"), p.S("Service_time"), p.S("Volume_constraint"), idInvoice)
	c.setUser("Balance", php.NumStr(balance-php.Floatval(price)))
	c.report(sprintf("users.Report.reportbuy", userAc, p.S("price_product"), p.S("Volume_constraint"), c.fromID, c.user.S("number"), c.user.S("Processing_value"), nf(balance), c.username))
	c.step("home")
	return false
}

func phpTruthy(s string) bool { return s != "" && s != "0" }

// payCommission credits the referrer after a purchase (affiliate percentage).
func (c *Ctx) payCommission(buyer db.Row, price float64) {
	d := c.db()
	aff := d.Select("affiliates", "*", "", nil)
	if aff.S("status_commission") != "oncommission" {
		return
	}
	// PHP: ($user['affiliates'] !== null || $user['affiliates'] != "0") is
	// always true for a varchar; the lookup below is what really decides.
	ref := buyer.S("affiliates")
	result := price * aff.F("affiliatespercentage") / 100
	inviter := d.Select("user", "*", "id", ref)
	if inviter == nil {
		return
	}
	d.Update("user", "Balance", php.NumStr(inviter.F("Balance")+result), "id", ref)
	c.sendHTML(ref, sprintf("users.affiliates.porsantuser", nf(result)), nil)
}

// deliverService sends the freshly created service to its owner, the same
// way the purchase and DirectPayment code paths did.
func (c *Ctx) deliverService(to string, panel db.Row, out panels.Out, userAc, productName, serviceTime, volume, idInvoice string) {
	var link, cfgText, cfgQR string
	if panel.S("sublink") == "onsublink" {
		link = out.S("subscription_url")
	}
	if panel.S("configManual") == "onconfig" {
		for _, x := range out.List("configs") {
			cfgText += "\n" + x
			cfgQR += x
		}
	}
	shop := ik(row(cb(T("users.help.btninlinebuy"), "helpbtn")))
	var msg string
	switch panel.S("type") {
	case "wgdashboard":
		msg = sprintf("users.buy.createservicewgbuy", userAc, productName, panel.S("name_panel"), serviceTime, volume)
	case "mikrotik":
		msg = sprintf("users.buy.createservice_mikrotik_buy", userAc, out.S("subscription_url"), productName, panel.S("name_panel"), serviceTime, volume)
	default:
		msg = sprintf("users.buy.createservice", userAc, productName, panel.S("name_panel"), serviceTime, volume, cfgText, link)
	}
	kb := c.kbMainFor(to)
	if panel.S("type") == "mikrotik" {
		c.sendHTML(to, msg, shop)
		c.sendHTML(to, T("users.selectoption"), kb)
		return
	}
	switch {
	case panel.S("sublink") == "onsublink":
		c.sendQR(to, link, msg, shop)
		if panel.S("type") == "wgdashboard" {
			c.b.TG.SendDocument(to, panel.S("inboundid")+"_"+idInvoice+".conf", []byte(link), T("users.buy.configwg"))
		}
		c.sendHTML(to, T("users.selectoption"), kb)
	case panel.S("configManual") == "onconfig":
		// (index.php tested a non-existent 'config' column here, so this
		// branch never ran there and the message went out as plain text)
		if len(out.List("configs")) == 1 {
			c.sendQR(to, cfgQR, msg, shop)
		} else {
			c.sendHTML(to, msg, shop)
		}
		c.sendHTML(to, T("users.selectoption"), kb)
	default:
		c.sendHTML(to, msg, shop)
		c.sendHTML(to, T("users.selectoption"), kb)
	}
}

// deliverAfterPayment is DirectPayment()'s delivery (configs before the sub
// link, no extra "select an option" message).
func (c *Ctx) deliverAfterPayment(to string, panel db.Row, out panels.Out, userAc, productName, serviceTime, volume, idInvoice string) {
	var link, cfgText, cfgQR string
	if panel.S("sublink") == "onsublink" {
		link = out.S("subscription_url")
	}
	if panel.S("configManual") == "onconfig" {
		for _, x := range out.List("configs") {
			cfgText += "\n" + x
			cfgQR += x
		}
	}
	shop := ik(row(cb(T("users.help.btninlinebuy"), "helpbtn")))
	var msg string
	switch panel.S("type") {
	case "wgdashboard":
		msg = sprintf("users.buy.createservicewgbuy", userAc, productName, panel.S("name_panel"), serviceTime, volume)
	case "mikrotik":
		msg = sprintf("users.buy.createservice_mikrotik_buy", userAc, out.S("subscription_url"), productName, panel.S("name_panel"), serviceTime, volume)
	default:
		msg = sprintf("users.buy.createservice", userAc, productName, panel.S("name_panel"), serviceTime, volume, cfgText, link)
	}
	switch {
	case panel.S("type") == "mikrotik":
		c.sendHTML(to, msg, shop)
		c.sendHTML(to, T("users.selectoption"), c.kbMainFor(to))
	case panel.S("configManual") == "onconfig":
		if len(out.List("configs")) == 1 {
			c.sendQR(to, cfgQR, msg, shop)
		} else {
			c.sendHTML(to, msg, shop)
		}
	case panel.S("sublink") == "onsublink":
		c.sendQR(to, link, msg, shop)
		if panel.S("type") == "wgdashboard" {
			c.b.TG.SendDocument(to, panel.S("inboundid")+"_"+idInvoice+".conf", []byte(link), T("users.buy.configwg"))
		}
	default:
		// DirectPayment sent nothing here; the customer should still get it
		c.sendHTML(to, msg, shop)
	}
}

func (c *Ctx) applySellDiscount() bool {
	d := c.db()
	if !c.inColumn("DiscountSell", "codeDiscount", c.text) {
		c.sendHTML(c.fromID, T("users.Discount.notcode"), c.kbBackUser())
		return true
	}
	sd := d.Select("DiscountSell", "*", "codeDiscount", c.text)
	if sd == nil {
		c.sendHTML(c.fromID, T("Admin.Discount.invalidcodedis"), nil)
		return true
	}
	p := c.productFor(c.user.S("Processing_value_one"), c.user.S("Processing_value"))
	if p == nil {
		c.sendHTML(c.fromID, T("users.status.error2"), c.kbMain())
		c.step("home")
		return true
	}
	if php.LooseEq(sd.S("limitDiscount"), sd.S("usedDiscount")) {
		c.sendHTML(c.fromID, T("users.Discount.erorrlimit"), nil)
		return true
	}
	if php.LooseEq(sd.S("usefirst"), "1") {
		if d.SelectCount("invoice", "id_user", c.fromID) != 0 {
			c.sendHTML(c.fromID, T("users.Discount.firstdiscount"), nil)
			return true
		}
	}
	c.sendHTML(c.fromID, T("users.Discount.correctcode"), c.kbMain())
	c.step("payment")
	result := sd.F("price") / 100 * p.F("price_product")
	price := php.Round(p.F("price_product")-result, 0)
	if price < 0 {
		price = 0
	}
	priceS := php.FloatToString(price)
	msg := sprintf("users.buy.invoicebuy", c.user.S("Processing_value_tow"), p.S("name_product"), p.S("Service_time"), priceS, p.S("Volume_constraint"), c.user.S("Balance"))
	k := ik(row(cb(T("users.buy.payandGet"), "confirmandgetserviceDiscount")), row(cb(T("users.backhome"), "backuser")))
	c.setUser("Processing_value_four", "dis_"+c.text+"_"+priceS)
	c.sendHTML(c.fromID, msg, k)
	return false
}

var _ tg.Markup
