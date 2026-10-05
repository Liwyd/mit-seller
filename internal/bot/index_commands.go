package bot

import (
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/tg"
)

const activeInvoiceWhere = "(status = 'active' OR status = 'end_of_time'  OR status = 'end_of_volume' OR status = 'sendedwarn')"

// numberGate is the repeated "ask for the phone number first" check. It
// returns true when the caller has to stop.
func (c *Ctx) numberGate(returnAfterAsk bool) bool {
	if php.LooseEq(c.setting.S("get_number"), "1") && c.user.S("step") != "get_number" && c.user.S("number") == "none" {
		c.sendHTML(c.fromID, T("users.number.Confirming"), kbRequestContact())
		c.step("get_number")
		if returnAfterAsk {
			return true
		}
	}
	return c.user.S("number") == "none" && php.LooseEq(c.setting.S("get_number"), "1")
}

// showBuyStart is the shared start of /new and the buy button.
func (c *Ctx) secCommands() bool {
	d := c.db()
	if c.text == "/start" {
		c.setUser("Processing_value", "0")
		c.setUser("Processing_value_one", "0")
		c.setUser("Processing_value_tow", "0")
		c.dropReplyKeyboard()
		c.send(c.fromID, c.texts["text_start"], c.kbMain(), "html")
		c.step("home")
		return true
	}
	if c.text == "/new" {
		n := d.SelectCount("marzban_panel", "status", "activepanel")
		if n == 0 {
			c.sendHTML(c.fromID, T("Admin.managepanel.nullpanel"), nil)
			return true
		}
		if c.numberGate(true) {
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
				c.sendHTML(c.fromID, sprintf("users.buy.selectService", panel.S("name_panel")),
					c.kbProduct(panel.S("name_panel"), "backuser", panel.S("MethodUsername"), nil))
			} else {
				if d.SelectCount("category", "", nil) == 0 {
					c.sendHTML(c.fromID, T("users.category.NotFound"), nil)
					return true
				}
				c.sendHTML(c.fromID, T("users.category.selectCategory"), c.kbCategoryBuy("backuser", panel.S("name_panel")))
			}
		} else {
			c.sendHTML(c.fromID, T("users.Service.Location"), c.kbPanelsUser())
		}
		return true
	}
	if c.text == "/status" {
		n := d.Count("SELECT COUNT(*) FROM invoice WHERE id_user = ? AND "+activeInvoiceWhere, c.fromID)
		if n == 0 && c.setting.S("NotUser") == "offnotuser" {
			c.send(c.fromID, T("users.sell.service_not_available"), nil, "html")
			return true
		}
		c.setUser("pagenumber", "1")
		page, per := int64(1), int64(10)
		var rows [][]B
		for _, r := range d.MustQuery("SELECT username FROM invoice WHERE id_user = ? AND "+activeInvoiceWhere+" ORDER BY time_sell DESC LIMIT 0, 10", c.fromID) {
			rows = append(rows, row(cb("🌟"+r.S("username")+"🌟", "product_"+r.S("username"))))
		}
		if c.setting.S("NotUser") == "onnotuser" {
			rows = append(rows, row(cb(T("Admin.Status.notusenameinbot"), "notusernameget")))
		}
		total := d.SelectCount("invoice", "id_user", c.fromID)
		pages := (total + per - 1) / per
		if page > 1 {
			rows = append(rows, row(cb(T("users.page.previous"), "prevpage_"+itoa(page-1))))
		}
		if page < pages {
			rows = append(rows, row(cb(T("users.page.next"), "nextpage_"+itoa(page+1))))
		}
		rows = append(rows, row(cb(T("users.backhome"), "backuser")))
		c.sendHTML(c.fromID, T("users.sell.service_sell"), ik(rows...))
		c.step("userservices")
		return true
	}
	if c.text == "/renew" {
		rs := d.MustQuery("SELECT username FROM invoice WHERE id_user = ? AND "+activeInvoiceWhere, c.fromID)
		if len(rs) == 0 {
			c.send(c.fromID, T("users.sell.service_not_available"), nil, "html")
			return true
		}
		var rows [][]B
		for _, r := range rs {
			rows = append(rows, row(cb("💊 "+r.S("username"), "extend_"+r.S("username"))))
		}
		rows = append(rows, row(cb(T("users.backhome"), "backuser")))
		c.sendHTML(c.fromID, T("users.extend.selectservice"), ik(rows...))
		return true
	}
	if c.text == T("users.backhome") || c.datain == "backuser" {
		c.setUser("Processing_value", "0")
		c.setUser("Processing_value_one", "0")
		c.setUser("Processing_value_tow", "0")
		if c.datain == "backuser" {
			c.del()
		}
		c.dropReplyKeyboard()
		c.send(c.fromID, T("users.back"), c.kbMain(), "html")
		c.step("home")
		return true
	}
	if c.stepIs("get_number") {
		if c.userPhone == "" || c.userPhone == "0" {
			c.send(c.fromID, T("users.number.false"), kbRequestContact(), "html")
			return true
		}
		if !php.LooseEq(c.contactID, c.fromID) {
			c.send(c.fromID, T("users.number.Warning"), kbRequestContact(), "html")
			return true
		}
		if php.LooseEq(c.setting.S("iran_number"), "1") && !re(`989[0-9]{9}$`).MatchString(c.userPhone) {
			c.send(c.fromID, T("users.number.erroriran"), kbRequestContact(), "html")
			return true
		}
		c.send(c.fromID, T("users.number.active"), c.kbMain(), "html")
		c.setUser("number", c.userPhone)
		c.step("home")
	}
	return false
}

// serviceListKeyboard is the purchased-services list with next/prev buttons.
func (c *Ctx) serviceListKeyboard(page int64, star string) *tg.InlineKeyboard {
	start := (page - 1) * 10
	if start < 0 {
		start = 0
	}
	var rows [][]B
	for _, r := range c.db().MustQuery("SELECT username FROM invoice WHERE id_user = ? AND "+activeInvoiceWhere+" ORDER BY time_sell DESC LIMIT "+itoa(start)+", 10", c.fromID) {
		rows = append(rows, row(cb(star+r.S("username")+star, "product_"+r.S("username"))))
	}
	if c.setting.S("NotUser") == "1" {
		rows = append(rows, row(cb(T("Admin.Status.notusenameinbot"), "usernotlist")))
	}
	rows = append(rows, row(cb(T("users.page.next"), "next_page"), cb(T("users.page.previous"), "previous_page")))
	return ik(rows...)
}
