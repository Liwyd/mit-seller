package bot

import (
	"encoding/json"
	"math"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/tg"
)

// secServices covers "Purchased services" through the cancel-service request.
func (c *Ctx) secServices() bool {
	d := c.db()
	if c.text == c.texts["text_Purchased_services"] || c.datain == "backorder" || c.text == "/services" {
		n := d.Count("SELECT COUNT(*) FROM invoice WHERE id_user = ? AND "+activeInvoiceWhere, c.fromID)
		if n == 0 && c.setting.S("NotUser") == "offnotuser" {
			c.send(c.fromID, T("users.sell.service_not_available"), nil, "html")
			return true
		}
		c.setUser("pagenumber", "1")
		k := c.serviceListKeyboard(1, "🌟")
		if c.datain == "backorder" {
			c.edit(T("users.sell.service_sell"), k)
		} else {
			c.send(c.fromID, T("users.sell.service_sell"), k, "html")
		}
	}
	if c.datain == "next_page" {
		total := d.SelectCount("invoice", "id_user", c.fromID)
		page := c.user.I("pagenumber")
		next := page + 1
		if page*10 > total {
			next = 1
		}
		k := c.serviceListKeyboard(next, "🌟️")
		c.setUser("pagenumber", next)
		c.edit(c.textCallback, k)
	} else if c.datain == "previous_page" {
		page := c.user.I("pagenumber")
		next := page - 1
		if page <= 1 {
			next = 1
		}
		k := c.serviceListKeyboard(next, "🌟️")
		c.setUser("pagenumber", next)
		c.edit(c.textCallback, k)
	}
	if c.datain == "usernotlist" {
		c.send(c.fromID, T("users.status.SendUsername"), c.kbBackUser(), "html")
		c.step("getusernameinfo")
	}
	if c.stepIs("getusernameinfo") {
		if !wordName(c.text) {
			c.send(c.fromID, T("users.status.Invalidusername"), c.kbBackUser(), "html")
			return true
		}
		c.setUser("Processing_value", c.text)
		c.send(c.fromID, T("users.Service.Location"), c.kbPanelsUser(), "html")
		c.step("getdata")
	} else if c.m(`locationnotuser_(.*)`) {
		panel := d.Select("marzban_panel", "name_panel", "id", c.g(1))
		out := c.b.PM.DataUser(panel.S("name_panel"), c.user.S("Processing_value"))
		if out.S("status") == "Unsuccessful" && out.S("msg") == "User not found" {
			c.send(c.fromID, T("users.status.notUsernameget"), c.kbMain(), "html")
			c.step("home")
			return true
		}
		s := describe(out, true)
		info := ik(
			row(cb(out.S("username"), "username"), cb(T("users.status.username"), "username")),
			row(cb(s.statusVar, "status_var"), cb(T("users.status.status"), "status_var")),
			row(cb(s.expiration, "expirationDate"), cb(T("users.status.expirationDate"), "expirationDate")),
			[]B{},
			row(cb(s.day, "day"), cb(T("users.status.daysleft"), "day")),
			row(cb(s.lastTraffic, "LastTraffic"), cb(T("users.status.LastTraffic"), "LastTraffic")),
			row(cb(s.used, "expirationDate"), cb(T("users.status.usedTrafficGb"), "expirationDate")),
			row(cb(s.remaining, "RemainingVolume"), cb(T("users.status.RemainingVolume"), "RemainingVolume")),
		)
		c.send(c.fromID, T("users.status.info"), info, "html")
		c.send(c.fromID, T("users.selectoption"), c.kbMain(), "html")
		c.step("home")
	}
	if c.m(`product_(\w+)`) {
		if c.serviceCard(c.g(1)) {
			return true
		}
	}
	return c.serviceActions()
}

// lastOnline formats online_at the way the PHP card did.
func lastOnline(v string, isNull bool) string {
	switch v {
	case "online":
		return T("users.online")
	case "offline":
		return T("users.offline")
	}
	if isNull || v == "" {
		return T("users.status.notconnected")
	}
	ts, ok := php.Strtotime(v)
	if !ok {
		ts = 0
	}
	return php.Jdate("Y/m/d h:i:s", ts)
}

func (c *Ctx) serviceCard(username string) bool {
	d := c.db()
	c.username = username
	inv := d.Select("invoice", "*", "username", username)
	panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
	out := c.b.PM.DataUser(inv.S("Service_location"), username)
	if out.Isset("msg") && out.S("msg") == "User not found" {
		c.send(c.fromID, T("users.status.usernotfound"), c.kbMain(), "html")
		d.Update("invoice", "Status", "disabledn", "id_invoice", inv.S("id_invoice"))
		return true
	}
	if out.S("status") == "Unsuccessful" {
		c.send(c.fromID, T("users.status.error"), c.kbMain(), "html")
		return true
	}
	online := lastOnline(out.S("online_at"), !out.Isset("online_at"))
	s := describe(out, true)
	var k *tg.InlineKeyboard
	var info string
	if s.status != "active" && s.status != "on_hold" {
		k = ik(
			row(cb(T("users.extend.title"), "extend_"+username)),
			row(cb(T("users.status.RemoveSerivecbtn"), "removebyuser-"+username), cb(T("users.Extra_volume.sellextra"), "Extra_volume_"+username)),
			row(cb(T("users.status.backlist"), "backorder")),
		)
		info = sprintf("users.status.InfoSerivceDisable", s.statusVar, out.S("username"), inv.S("Service_location"), inv.S("id_invoice"), s.lastTraffic, s.used, s.expiration, s.day)
	} else {
		type item struct{ key, text, data string }
		items := []item{
			{"linksub", T("users.status.linksub"), "subscriptionurl_"},
			{"config", T("users.status.config"), "config_"},
			{"extend", T("users.extend.title"), "extend_"},
			{"changelink", T("users.changelink.btntitle"), "changelink_"},
			{"removeservice", T("users.removeconfig.btnremoveuser"), "removeserviceuserco-"},
			{"Extra_volume", T("users.Extra_volume.sellextra"), "Extra_volume_"},
		}
		drop := map[string]bool{}
		switch panel.S("type") {
		case "wgdashboard":
			drop["config"], drop["changelink"] = true, true
		case "mikrotik":
			for _, x := range []string{"Extra_volume", "linksub", "config", "extend", "changelink"} {
				drop[x] = true
			}
		}
		if inv.S("name_product") == "usertest" {
			drop["removeservice"] = true
		}
		var rows [][]B
		var tmp []B
		for _, it := range items {
			if drop[it.key] {
				continue
			}
			tmp = append(tmp, cb(it.text, it.data+username))
			if len(tmp) == 2 {
				rows = append(rows, tmp)
				tmp = nil
			}
		}
		if len(tmp) > 0 {
			rows = append(rows, tmp)
		}
		rows = append(rows, row(cb(T("users.status.backlist"), "backorder")))
		k = ik(rows...)
		if panel.S("type") == "mikrotik" {
			info = sprintf("users.status.InfoSerivceActive_mikrotik", s.statusVar, out.S("username"), out.S("subscription_url"), inv.S("Service_location"), inv.S("id_invoice"), s.lastTraffic, s.used, s.expiration, s.day)
		} else {
			info = sprintf("users.status.InfoSerivceActive", s.statusVar, out.S("username"), inv.S("Service_location"), inv.S("id_invoice"), online, s.lastTraffic, s.used, s.expiration, s.day)
		}
	}
	c.edit(info, k)
	return false
}

// sendQR sends a QR code photo (falls back to a text message if the code
// cannot be drawn, e.g. empty content).
func (c *Ctx) sendQR(chat any, content, caption string, markup tg.Markup) {
	png, err := qrPNG(content)
	if err != nil {
		c.sendHTML(chat, caption, markup)
		return
	}
	c.b.TG.SendPhotoFile(chat, png, caption, markup)
}

// productModifyConfig is the per-panel "set expiry and volume" payload used
// when a service is renewed. ok=false for panels handled separately.
func renewConfig(panelType, inboundID string, product db.Row) (map[string]any, bool) {
	days := product.S("Service_time")
	var newDate int64
	if php.Intval(days) != 0 {
		newDate = php.PlusDaysUnix(days)
	}
	dataLimit := float64(php.Intval(product.S("Volume_constraint"))) * math.Pow(1024, 3)
	switch panelType {
	case "marzban", "mit", "nexra":
		return map[string]any{"expire": newDate, "data_limit": int64(dataLimit)}, true
	case "marzneshin":
		return map[string]any{"expire_date": newDate, "data_limit": int64(dataLimit)}, true
	case "x-ui_single", "alireza":
		ms := php.PlusDaysUnix(days) * 1000
		settings, _ := json.Marshal(map[string]any{"clients": []any{map[string]any{"totalGB": int64(dataLimit), "expiryTime": ms, "enable": true}}})
		cfg := map[string]any{"settings": string(settings)}
		if panelType == "alireza" {
			cfg["id"] = php.Intval(inboundID)
		}
		return cfg, true
	case "s_ui":
		return map[string]any{"volume": int64(dataLimit), "expiry": php.PlusDaysUnix(days), "enable": true}, true
	}
	return nil, false
}

func (c *Ctx) serviceActions() bool {
	d := c.db()
	pm := c.b.PM
	switch {
	case c.m(`subscriptionurl_(\w+)`):
		username := c.g(1)
		c.username = username
		inv := d.Select("invoice", "*", "username", username)
		panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
		out := pm.DataUser(inv.S("Service_location"), username)
		sub := out.S("subscription_url")
		caption := "<code>" + sub + "</code>"
		if panel.S("type") == "wgdashboard" {
			caption = ""
		}
		c.sendQR(c.fromID, sub, caption, nil)
		if panel.S("type") == "wgdashboard" {
			c.b.TG.SendDocument(c.fromID, panel.S("inboundid")+"_"+inv.S("id_invoice")+".conf", []byte(sub), T("users.buy.configwg"))
		}
	case c.m(`config_(\w+)`):
		username := c.g(1)
		c.username = username
		inv := d.Select("invoice", "*", "username", username)
		out := pm.DataUser(inv.S("Service_location"), username)
		for _, cfg := range out.List("links") {
			c.sendQR(c.fromID, cfg, "<code>"+cfg+"</code>", nil)
		}
	case c.m(`extend_(\w+)`):
		username := c.g(1)
		c.username = username
		inv := d.Select("invoice", "*", "username", username)
		out := pm.DataUser(inv.S("Service_location"), username)
		if out.S("status") == "Unsuccessful" {
			c.send(c.fromID, T("users.status.error"), nil, "html")
			return true
		}
		if out.S("status") == "on_hold" {
			c.send(c.fromID, T("users.status.error_onhold"), nil, "html")
			return true
		}
		c.setUser("Processing_value", username)
		var rows [][]B
		for _, p := range d.MustQuery("SELECT name_product, code_product FROM product WHERE (Location = ? OR location = '/all')", inv.S("Service_location")) {
			rows = append(rows, row(cb(p.S("name_product"), "serviceextendselect_"+p.S("code_product"))))
		}
		rows = append(rows, row(cb(T("users.backorder"), "product_"+username)))
		c.edit(T("users.extend.selectservice"), ik(rows...))
	case c.m(`serviceextendselect_(\w+)`):
		code := c.g(1)
		pv := c.user.S("Processing_value")
		inv := d.Select("invoice", "*", "username", pv)
		if inv == nil {
			c.sendHTML(c.fromID, T("users.extend.error2"), nil)
			return true
		}
		p := d.One("SELECT * FROM product WHERE (Location = ? OR location = '/all') AND code_product = ? LIMIT 1", inv.S("Service_location"), code)
		if p == nil {
			c.sendHTML(c.fromID, T("users.extend.error2"), nil)
			return true
		}
		d.Update("invoice", "name_product", p.S("name_product"), "username", pv)
		d.Update("invoice", "Service_time", p.S("Service_time"), "username", pv)
		d.Update("invoice", "Volume", p.S("Volume_constraint"), "username", pv)
		d.Update("invoice", "price_product", p.S("price_product"), "username", pv)
		c.setUser("Processing_value_one", code)
		k := ik(row(cb(T("users.extend.confirm"), "confirmserivce-"+code)), row(cb(T("users.backhome"), "backuser")))
		c.edit(sprintf("users.extend.invoicExtend", inv.S("username"), p.S("name_product"), p.S("price_product"), p.S("Service_time"), p.S("Service_time"), p.S("Volume_constraint")), k)
	case c.m(`confirmserivce-(.*)`):
		return c.confirmRenew(c.g(1))
	case c.m(`changelink_(\w+)`):
		username := c.g(1)
		c.username = username
		k := ik(row(cb(T("users.changelink.confirm"), "confirmchange_"+username)), row(cb(T("users.status.backservice"), "product_"+username)))
		c.edit(T("users.changelink.warnchange"), k)
	case c.m(`confirmchange_(\w+)`):
		username := c.g(1)
		inv := d.Select("invoice", "*", "username", username)
		panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
		pm.RevokeSub(panel.S("name_panel"), username)
		c.edit(T("users.changelink.confirmed"), ik(row(cb(T("users.status.backservice"), "product_"+username))))
	case c.m(`Extra_volume_(\w+)`):
		username := c.g(1)
		c.username = username
		c.setUser("Processing_value", username)
		c.sendHTML(c.fromID, sprintf("users.Extra_volume.VolumeValue", c.setting.S("Extra_volume")), c.kbBackUser())
		c.step("getvolumeextra")
	case c.stepIs("getvolumeextra"):
		if !php.CtypeDigit(c.text) {
			c.sendHTML(c.fromID, T("Admin.Product.Invalidvolume"), c.kbBackUser())
			return true
		}
		if php.Floatval(c.text) < 1 {
			c.sendHTML(c.fromID, T("users.Extra_volume.invalidprice"), c.kbBackUser())
			return true
		}
		k := ik(row(cb(T("users.Extra_volume.extracheck"), "confirmaextra_"+c.text)))
		price := nf(php.Floatval(c.text) * c.setting.F("Extra_volume"))
		c.sendHTML(c.fromID, sprintf("users.Extra_volume.invoiceExtraVolume", nf(c.setting.F("Extra_volume")), price, c.text), k)
		c.step("home")
	case c.m(`confirmaextra_(\w+)`):
		return c.confirmExtraVolume(c.g(1))
	case c.m(`removeserviceuserco-(\w+)`):
		username := c.g(1)
		c.username = username
		inv := d.Select("invoice", "*", "username", username)
		panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
		out := pm.DataUser(panel.S("name_panel"), username)
		if out.Isset("status") {
			switch out.S("status") {
			case "expired", "limited", "disabled":
				c.send(c.fromID, T("users.status.notusername"), nil, "html")
				return true
			}
		}
		if d.SelectCount("cancel_service", "username", username) != 0 {
			c.send(c.fromID, T("users.status.errorexits"), nil, "html")
			return true
		}
		c.edit(T("users.status.descriptions_removeservice"), ik(row(cb(T("users.status.RequestRemove"), "confirmremoveservices-"+username))))
	case c.m(`removebyuser-(\w+)`):
		username := c.g(1)
		c.username = username
		inv := d.Select("invoice", "*", "username", username)
		pm.RemoveUser(inv.S("Service_location"), inv.S("username"))
		d.Update("invoice", "status", "removebyuser", "id_invoice", inv.S("id_invoice"))
		c.report(sprintf("Admin.Report.NotifRemoveByUser", inv.S("username")))
		c.del()
		c.send(c.fromID, T("users.status.RemovedService"), nil, "html")
	case c.m(`confirmremoveservices-(\w+)`):
		if d.Count("SELECT COUNT(*) FROM cancel_service WHERE id_user = ? AND status = 'waiting'", c.fromID) != 0 {
			c.sendHTML(c.fromID, T("users.status.exitsrequsts"), nil)
			return true
		}
		svc := c.g(1)
		inv := d.Select("invoice", "*", "username", svc)
		panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
		d.Exec("INSERT IGNORE INTO cancel_service (id_user, username,description,status) VALUES (?, ?, ?, ?)", c.fromID, svc, "0", "waiting")
		out := pm.DataUser(panel.S("name_panel"), svc)
		s := describe(out, false)
		msg := sprintf("users.status.RequestInfoRemove", c.fromID, c.username, inv.S("username"), s.statusVar, inv.S("Service_location"), inv.S("id_invoice"), s.used, s.lastTraffic, s.remaining, s.expiration, s.day)
		k := ik(row(cb(T("users.removeconfig.btnremoveuser"), "remoceserviceadmin-"+svc), cb(T("users.removeconfig.rejectremove"), "rejectremoceserviceadmin-"+svc)))
		for _, a := range c.adminIDs {
			c.send(a, msg, k, "html")
			c.stepOf(a, "home")
		}
		c.del()
		c.send(c.fromID, T("users.removeconfig.accepetrequest"), c.kbMain(), "html")
	}
	return false
}

func (c *Ctx) confirmRenew(code string) bool {
	d := c.db()
	pm := c.b.PM
	c.del()
	pv := c.user.S("Processing_value")
	inv := d.Select("invoice", "*", "username", pv)
	if inv == nil {
		c.sendHTML(c.fromID, T("users.extend.error2"), nil)
		return true
	}
	panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
	if panel == nil {
		c.sendHTML(c.fromID, T("users.extend.error2"), nil)
		return true
	}
	p := d.One("SELECT * FROM product WHERE (Location = ? OR location = '/all') AND code_product = ? LIMIT 1", inv.S("Service_location"), code)
	if p == nil {
		c.sendHTML(c.fromID, T("users.extend.error2"), nil)
		return true
	}
	balance := c.user.F("Balance")
	price := p.F("price_product")
	if balance < price {
		c.setUser("Processing_value", php.NumStr(price-balance))
		c.sendHTML(c.fromID, T("users.sell.None-credit"), c.kbStepPayment())
		c.sendHTML(c.fromID, T("users.sell.selectpayment"), c.kbBackUser())
		c.step("get_step_payment")
		return true
	}
	svc := inv.S("username")
	c.setUser("Balance", php.NumStr(balance-price))
	pm.ResetUserDataUsage(inv.S("Service_location"), pv)
	typ := panel.S("type")
	if cfg, ok := renewConfig(typ, panel.S("inboundid"), p); ok {
		res := pm.Modifyuser(pv, inv.S("Service_location"), cfg)
		if panels.IsMitType(typ) && res.Isset("detail") {
			// The Mit panel refused (usually: the reseller is out of traffic). The
			// PHP bot kept the money anyway; give it back and tell the admins.
			c.setUser("Balance", c.user.S("Balance"))
			c.sendHTML(c.fromID, T("users.extend.ErrorExtend"), c.kbMain())
			for _, a := range c.adminIDs {
				c.sendHTML(a, sprintf("Admin.Report.ErrorExtend", res.S("detail"), c.fromID, svc), nil)
			}
			return true
		}
	} else if typ == "wgdashboard" {
		c.renewWG(svc, inv.S("Service_location"), p)
	}
	k := ik(row(cb(T("users.status.backlist"), "backorder")), row(cb(T("users.status.backservice"), "product_"+svc)))
	balanceNow := nfs(d.Select("user", "Balance", "id", c.fromID).S("Balance"))
	d.Update("invoice", "Status", "active", "id_invoice", inv.S("id_invoice"))
	c.sendHTML(c.fromID, T("users.extend.thanks"), k)
	c.report(sprintf("Admin.Report.extend", c.fromID, c.username, p.S("name_product"), nfs(p.S("price_product")), svc, balanceNow, inv.S("Service_location")))
	return false
}

func jobIndex(jobs []any, field string) int {
	for i, j := range jobs {
		if m, ok := j.(map[string]any); ok && m["Field"] == field {
			return i
		}
	}
	return len(jobs)
}

func (c *Ctx) renewWG(svc, location string, p db.Row) {
	pm := c.b.PM
	pm.WGAllowAccess(location, svc)
	u := pm.WGGetUser(svc, location)
	jobs, _ := u["jobs"].([]any)
	if i := jobIndex(jobs, "date"); i < len(jobs) {
		pm.WGDeleteJob(location, map[string]any{"Job": jobs[i]})
	} else {
		pm.WGDeleteJob(location, map[string]any{"Job": nil})
	}
	if i := jobIndex(jobs, "total_data"); i < len(jobs) {
		pm.WGDeleteJob(location, map[string]any{"Job": jobs[i]})
	} else {
		pm.WGDeleteJob(location, map[string]any{"Job": nil})
	}
	id, _ := u["id"].(string)
	if php.Intval(p.S("Service_time")) != 0 {
		pm.WGSetJob(location, "date", php.Date("Y-m-d H:i:s", php.PlusDaysUnix(p.S("Service_time"))), id)
	}
	pm.WGSetJob(location, "total_data", p.S("Volume_constraint"), id)
}

func (c *Ctx) confirmExtraVolume(volumeS string) bool {
	d := c.db()
	pm := c.b.PM
	volume := php.Floatval(volumeS)
	priceExtra := c.setting.F("Extra_volume") * volume
	c.edit(c.textCallback, ik())
	pv := c.user.S("Processing_value")
	inv := d.Select("invoice", "*", "username", pv)
	if inv == nil {
		c.send(c.fromID, T("users.status.error"), nil, "html")
		return true
	}
	panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
	if panel == nil {
		c.send(c.fromID, T("users.status.error"), nil, "html")
		return true
	}
	balance := c.user.F("Balance")
	priced := php.Intval(c.setting.S("Extra_volume")) != 0
	if balance < priceExtra && priced {
		c.setUser("Processing_value", php.NumStr(priceExtra-balance))
		c.sendHTML(c.fromID, T("users.sell.None-credit"), c.kbStepPayment())
		c.step("get_step_payment")
		return true
	}
	if priced {
		c.setUser("Balance", php.NumStr(balance-priceExtra))
	}
	out := pm.DataUser(panel.S("name_panel"), pv)
	gib := math.Pow(1024, 3)
	dataLimit := out.F("data_limit") + volume*gib
	var cfg map[string]any
	switch panel.S("type") {
	case "marzban", "mit", "nexra", "marzneshin":
		cfg = map[string]any{"data_limit": jsonNum(dataLimit)}
	case "x-ui_single", "alireza":
		s, _ := json.Marshal(map[string]any{"clients": []any{map[string]any{"totalGB": jsonNum(dataLimit)}}})
		cfg = map[string]any{"settings": string(s)}
		if panel.S("type") == "alireza" {
			cfg["id"] = php.Intval(panel.S("inboundid"))
		}
	case "s_ui":
		cfg = map[string]any{"volume": jsonNum(dataLimit)}
	case "wgdashboard":
		extra := c.setting.F("Extra_volume")
		gb := out.F("data_limit") / gib
		if extra != 0 {
			gb += volume / extra
		} else {
			gb += volume // PHP divided by zero here and crashed
		}
		u := pm.WGGetUser(inv.S("username"), inv.S("Service_location"))
		jobs, _ := u["jobs"].([]any)
		i := jobIndex(jobs, "total_data")
		pm.WGAllowAccess(inv.S("Service_location"), inv.S("username"))
		id, _ := u["id"].(string)
		if i < len(jobs) {
			pm.WGDeleteJob(inv.S("Service_location"), map[string]any{"Job": jobs[i]})
		} else {
			pm.WGResetData(id, inv.S("Service_location"))
		}
		pm.WGSetJob(inv.S("Service_location"), "total_data", php.FloatToString(gb), id)
	}
	res := pm.Modifyuser(inv.S("username"), panel.S("name_panel"), cfg)
	if panels.IsMitType(panel.S("type")) && res.Isset("detail") {
		if priced {
			c.setUser("Balance", c.user.S("Balance"))
		}
		c.sendHTML(c.fromID, T("users.Extra_volume.ErrorExtra"), c.kbMain())
		for _, a := range c.adminIDs {
			c.sendHTML(a, sprintf("Admin.Report.ErrorExtraVolume", res.S("detail"), c.fromID, inv.S("username")), nil)
		}
		return true
	}
	c.sendHTML(c.fromID, T("users.Extra_volume.extraadded"), ik(row(cb(T("users.status.backservice"), "product_"+pv))))
	c.report(sprintf("Admin.Report.Extra_volume", c.fromID, volumeS, nf(priceExtra)))
	return false
}

// jsonNum keeps whole numbers integral in JSON like PHP's json_encode.
func jsonNum(f float64) any {
	if f == math.Trunc(f) && math.Abs(f) < 9e18 {
		return int64(f)
	}
	return f
}
