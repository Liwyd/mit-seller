package bot

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

func randomInt(min, max int64) int64 {
	n, _ := rand.Int(rand.Reader, big.NewInt(max-min+1))
	return min + n.Int64()
}

// usernameTaken reports isset($DataUserOut['username']) || in_array($u, $usernameinvoice).
func (c *Ctx) usernameTaken(panelName, u string) bool {
	if c.b.PM.DataUser(panelName, u).Isset("username") {
		return true
	}
	return c.db().Exists("invoice", "username", u)
}

func (c *Ctx) secUserTest() bool {
	d := c.db()
	if c.text == c.texts["text_usertest"] {
		if d.SelectCount("marzban_panel", "", nil) == 0 {
			c.sendHTML(c.fromID, T("Admin.managepanel.nullpanel"), nil)
			return true
		}
		if c.numberGate(false) {
			return true
		}
		if c.user.F("limit_usertest") <= 0 {
			c.send(c.fromID, T("users.usertest.limitwarning"), c.kbMain(), "html")
			return true
		}
		c.send(c.fromID, T("users.Service.Location"), c.kbPanelsTest(), "html")
	}
	isLoc := c.m(`locationtests_(.*)`)
	if !(c.stepIs("createusertest") || c.stepIs("waitusernamechoice_test") || isLoc) {
		return false
	}
	if c.user.F("limit_usertest") <= 0 {
		c.send(c.fromID, T("users.usertest.limitwarning"), c.kbMain(), "html")
		return true
	}
	var namePanel string
	if c.stepIs("createusertest") || c.stepIs("waitusernamechoice_test") {
		namePanel = c.user.S("Processing_value_one")
		if c.stepIs("createusertest") && !validServiceUsername(c.text) {
			c.sendHTML(c.fromID, T("users.invalidusername"), c.kbBackUser())
			return true
		}
	} else {
		c.del()
		namePanel = d.Select("marzban_panel", "*", "id", c.g(1)).S("name_panel")
	}
	random := randHex(2)
	panel := d.Select("marzban_panel", "*", "name_panel", namePanel)
	method := panel.S("MethodUsername")
	if method == T("users.customuserchoice") {
		switch {
		case c.datain == "usernamechoice_random_test":
			method = T("users.customidAndRandom")
		case c.datain == "usernamechoice_custom_test" || c.stepIs("createusertest"):
			method = T("users.customusername")
		default:
			c.setUser("Processing_value_one", namePanel)
			c.step("waitusernamechoice_test")
			k := ik(row(cb("✍️ "+T("users.customusername"), "usernamechoice_custom_test"), cb("🎲 "+T("users.customidAndRandom"), "usernamechoice_random_test")))
			c.sendHTML(c.fromID, T("users.selectusernamechoice"), k)
			return true
		}
	}
	if method == T("users.customusername") && !c.stepIs("createusertest") {
		c.step("createusertest")
		c.setUser("Processing_value_one", namePanel)
		c.send(c.fromID, T("users.selectusername"), c.kbBackUser(), "html")
		return true
	}
	userAc := strings.ToLower(c.generateUsername(method, c.user.S("username"), random, c.text))
	if c.usernameTaken(panel.S("name_panel"), userAc) {
		userAc += itoa(randomInt(1000000, 9999999))
	}
	expire := php.PlusHoursUnix(c.setting.S("time_usertest"))
	dataLimit := c.setting.F("val_usertest") * 1048576
	out := c.b.PM.CreateUser(namePanel, userAc, expire, dataLimit, true)
	if !out.Isset("username") {
		c.send(c.fromID, T("users.usertest.errorcreat"), c.kbMain(), "html")
		msg := sprintf("users.buy.errorInCreate", out.MsgJSON(), c.fromID, c.username)
		for _, a := range c.adminIDs {
			c.send(a, msg, nil, "html")
		}
		c.step("home")
		return true
	}
	idInvoice := randHex(4)
	d.Exec("INSERT IGNORE INTO invoice (id_user, id_invoice, username, time_sell, Service_location, name_product, price_product, Volume, Service_time, Status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		c.fromID, idInvoice, userAc, nowUnix(), namePanel, "usertest", "0", c.setting.S("val_usertest"), c.setting.S("time_usertest"), "active")
	var link, cfgText string
	if panel.S("sublink") == "onsublink" {
		link = out.S("subscription_url")
	}
	if panel.S("configManual") == "onconfig" {
		for _, x := range out.List("configs") {
			cfgText += "\n" + x
		}
	}
	shop := ik(row(cb(T("users.help.btninlinebuy"), "helpbtn")))
	var msg string
	switch panel.S("type") {
	case "wgdashboard":
		msg = sprintf("users.buy.createservicewg", userAc, panel.S("name_panel"), c.setting.S("time_usertest"), c.setting.S("val_usertest"))
	case "mikrotik":
		msg = sprintf("users.buy.createservice_mikrotik_test", userAc, out.S("subscription_url"), panel.S("name_panel"), c.setting.S("time_usertest"), c.setting.S("val_usertest"))
	default:
		msg = sprintf("users.buy.createservicetest", userAc, panel.S("name_panel"), c.setting.S("time_usertest"), c.setting.S("val_usertest"), link, cfgText)
	}
	if panel.S("sublink") == "onsublink" && panel.S("type") != "mikrotik" {
		c.sendQR(c.fromID, link, msg, shop)
		if panel.S("type") == "wgdashboard" {
			c.b.TG.SendDocument(c.fromID, panel.S("inboundid")+"_"+idInvoice+".conf", []byte(link), T("users.buy.configwg"))
		}
		c.sendHTML(c.fromID, T("users.selectoption"), c.kbMain())
	} else {
		c.sendHTML(c.fromID, msg, shop)
		c.sendHTML(c.fromID, T("users.selectoption"), c.kbMain())
	}
	c.step("home")
	c.setUser("limit_usertest", php.NumStr(c.user.F("limit_usertest")-1))
	c.report(sprintf("Admin.Report.ReportTestCreate", c.fromID, c.username, userAc, c.firstName, panel.S("name_panel"), c.user.S("number")))
	return false
}
