package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/bot"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

func (a *API) routes() {
	// overview
	a.handle("GET /api/v1/info", roleManager, a.info)
	a.handle("GET /api/v1/stats", roleManager, a.stats)

	// settings, texts, buttons
	a.handle("GET /api/v1/settings", roleManager, a.getSettings)
	a.handle("PUT /api/v1/settings", roleManager, a.putSettings)
	a.handle("GET /api/v1/texts", roleManager, a.getTexts)
	a.handle("PUT /api/v1/texts", roleManager, a.putTexts)
	a.handle("GET /api/v1/buttons", roleManager, a.getButtons)
	a.handle("PUT /api/v1/buttons", roleManager, a.putButtons)
	a.handle("GET /api/v1/emoji/{id}", roleManager, a.emojiPreview)
	a.handle("GET /api/v1/emoji-pack/{name}", roleManager, a.emojiPack)
	a.handle("GET /api/v1/emoji-sets", roleManager, a.emojiSets)
	a.handle("GET /api/v1/button-styles", roleManager, a.getLabelStyles)
	a.handle("PUT /api/v1/button-styles", roleManager, a.putLabelStyles)
	a.handle("GET /api/v1/emoji-allow", roleManager, a.getEmojiAllow)
	a.handle("PUT /api/v1/emoji-allow", roleOwner, a.putEmojiAllow)

	// catalogue
	a.handle("GET /api/v1/products", roleManager, a.listProducts)
	a.handle("POST /api/v1/products", roleManager, a.createProduct)
	a.handle("PUT /api/v1/products/{id}", roleManager, a.updateProduct)
	a.handle("DELETE /api/v1/products/{id}", roleManager, a.deleteProduct)
	a.handle("GET /api/v1/categories", roleManager, a.listCategories)
	a.handle("POST /api/v1/categories", roleManager, a.createCategory)
	a.handle("PUT /api/v1/categories/{id}", roleManager, a.updateCategory)
	a.handle("DELETE /api/v1/categories/{id}", roleManager, a.deleteCategory)
	a.handle("GET /api/v1/giftcodes", roleManager, a.listGiftCodes)
	a.handle("POST /api/v1/giftcodes", roleManager, a.createGiftCode)
	a.handle("DELETE /api/v1/giftcodes/{id}", roleManager, a.deleteGiftCode)
	a.handle("GET /api/v1/discounts", roleManager, a.listDiscounts)
	a.handle("POST /api/v1/discounts", roleManager, a.createDiscount)
	a.handle("DELETE /api/v1/discounts/{id}", roleManager, a.deleteDiscount)
	a.handle("GET /api/v1/help", roleManager, a.listHelp)
	a.handle("POST /api/v1/help", roleManager, a.createHelp)
	a.handle("PUT /api/v1/help/{id}", roleManager, a.updateHelp)
	a.handle("DELETE /api/v1/help/{id}", roleManager, a.deleteHelp)

	// customers
	a.handle("GET /api/v1/users", roleManager, a.listUsers)
	a.handle("GET /api/v1/users/{id}", roleManager, a.getUser)
	a.handle("POST /api/v1/users/{id}/balance", roleManager, a.userBalance)
	a.handle("POST /api/v1/users/{id}/block", roleManager, a.userBlock)
	a.handle("POST /api/v1/users/{id}/unblock", roleManager, a.userUnblock)
	a.handle("POST /api/v1/users/{id}/verify", roleManager, a.userVerify)
	a.handle("POST /api/v1/users/{id}/test-limit", roleManager, a.userTestLimit)
	a.handle("POST /api/v1/users/{id}/message", roleManager, a.userMessage)
	a.handle("GET /api/v1/services", roleManager, a.listServices)
	a.handle("GET /api/v1/services/{username}", roleManager, a.getService)
	a.handle("DELETE /api/v1/services/{username}", roleManager, a.deleteService)

	// money
	a.handle("GET /api/v1/payments", roleManager, a.listPayments)
	a.handle("POST /api/v1/payments/{order}/approve", roleManager, a.approvePayment)
	a.handle("POST /api/v1/payments/{order}/reject", roleManager, a.rejectPayment)
	a.handle("GET /api/v1/payments/{order}/receipt", roleManager, a.receipt)
	a.handle("GET /api/v1/payment-settings", roleManager, a.getPaySettings)
	a.handle("PUT /api/v1/payment-settings", roleManager, a.putPaySettings)
	a.handle("GET /api/v1/cancel-requests", roleManager, a.listCancelRequests)
	a.handle("GET /api/v1/autopay", roleManager, a.getAutopay)
	a.handle("PUT /api/v1/autopay", roleManager, a.putAutopay)
	a.handle("POST /api/v1/autopay/new-key", roleManager, a.autopayNewKey)
	a.handle("GET /api/v1/affiliates", roleManager, a.getAffiliates)
	a.handle("PUT /api/v1/affiliates", roleManager, a.putAffiliates)

	// messaging and staff
	a.handle("GET /api/v1/broadcast", roleManager, a.broadcastStatus)
	a.handle("POST /api/v1/broadcast", roleManager, a.broadcast)
	a.handle("DELETE /api/v1/broadcast", roleManager, a.cancelBroadcast)
	a.handle("GET /api/v1/admins", roleManager, a.listAdmins)
	a.handle("POST /api/v1/admins", roleManager, a.addAdmin)
	a.handle("DELETE /api/v1/admins/{id}", roleManager, a.removeAdmin)

	// panels: names are visible to managers (products point at them), the
	// connections themselves are owner-only
	a.handle("GET /api/v1/panels", roleManager, a.listPanels)
	a.handle("POST /api/v1/panels", roleOwner, a.createPanel)
	a.handle("PUT /api/v1/panels/{id}", roleOwner, a.updatePanel)
	a.handle("DELETE /api/v1/panels/{id}", roleOwner, a.deletePanel)
	a.handle("POST /api/v1/panels/{id}/test", roleOwner, a.testPanel)
}

// ---------------------------------------------------------------- overview

func (a *API) info(w http.ResponseWriter, r *http.Request) {
	role := "manager"
	if a.role(r) == roleOwner {
		role = "owner"
	}
	ok(w, map[string]any{
		"bot_username": a.B.Cfg.BotUsername,
		"domain":       a.B.Cfg.Domain,
		"version":      bot.Version,
		"role":         role,
		"admin_id":     a.B.Cfg.AdminID,
	})
}

func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	const active = "(Status = 'active' OR Status = 'end_of_time' OR Status = 'end_of_volume' OR Status = 'sendedwarn') AND name_product != 'usertest'"
	now := time.Now().Unix()
	ok(w, map[string]any{
		"users":            d.SelectCount("user", "", nil),
		"blocked_users":    d.SelectCount("user", "User_Status", "block"),
		"wallet_total":     php.Floatval(d.Scalar("SELECT SUM(Balance) FROM user")),
		"active_services":  d.Count("SELECT COUNT(*) FROM invoice WHERE " + active),
		"sales_total":      php.Floatval(d.Scalar("SELECT SUM(price_product) FROM invoice WHERE " + active)),
		"sales_24h":        d.Count("SELECT COUNT(*) FROM invoice WHERE time_sell > ? AND "+active, now-86400),
		"sales_24h_amount": php.Floatval(d.Scalar("SELECT SUM(price_product) FROM invoice WHERE time_sell > ? AND "+active, now-86400)),
		"test_accounts":    d.SelectCount("invoice", "name_product", "usertest"),
		"panels":           d.SelectCount("marzban_panel", "", nil),
		"pending_payments": d.Count("SELECT COUNT(*) FROM Payment_report WHERE payment_Status = 'waiting'"),
		"paid_total":       php.Floatval(d.Scalar("SELECT SUM(price) FROM Payment_report WHERE payment_Status = 'paid'")),
		"cancel_requests":  d.Count("SELECT COUNT(*) FROM cancel_service WHERE status = 'waiting'"),
	})
}

// ---------------------------------------------------------------- settings

var settingFlags = []string{"Bot_Status", "roll_Status", "NotUser", "help_Status", "get_number", "iran_number", "status_verify", "statuscategory", "copy_cart"}
var settingValues = []string{"time_usertest", "val_usertest", "limit_usertest_all", "Extra_volume", "removedayc", "namecustome", "Channel_Report"}
var cronNames = []string{"test", "volume", "time", "remove", "card"}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	// old installs keep some switches as text; convert them like the bot's
	// status page does so they read correctly here
	a.B.MigrateLegacySettings()
	s := d.Setting()
	out := map[string]any{}
	for _, k := range settingFlags {
		out[k] = php.LooseEq(s.S(k), "1")
	}
	for _, k := range settingValues {
		out[k] = s.S(k)
	}
	crons := map[string]bool{}
	for _, c := range cronNames {
		crons[c] = a.B.CronOn(c)
	}
	out["crons"] = crons
	out["channel"] = d.Select("channels", "link", "", nil).S("link")
	ok(w, out)
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	d := a.B.DB
	for _, k := range settingFlags {
		if f.has(k) {
			v := "0"
			if f.boolean(k) {
				v = "1"
			}
			d.Update("setting", k, v, "", nil)
		}
	}
	for _, k := range settingValues {
		if !f.has(k) {
			continue
		}
		v := f.str(k)
		switch k {
		case "time_usertest", "limit_usertest_all", "Extra_volume", "removedayc":
			if !isDigits(v) {
				fail(w, 400, k+" must be a whole number")
				return
			}
		case "val_usertest":
			if !isDigits(v) || php.Intval(v) < 100 {
				fail(w, 400, "val_usertest must be at least 100 (MB)")
				return
			}
		}
		d.Update("setting", k, v, "", nil)
		if k == "limit_usertest_all" && f.boolean("apply_limit_to_all") {
			d.Exec("UPDATE user SET limit_usertest = ?", v)
		}
	}
	if f.has("channel") {
		link := strings.TrimPrefix(strings.TrimPrefix(f.str("channel"), "https://t.me/"), "@")
		if d.SelectCount("channels", "", nil) == 0 {
			d.Exec("INSERT INTO channels (link) VALUES (?)", link)
		} else {
			d.Update("channels", "link", link, "", nil)
		}
	}
	if c, okc := f["crons"].(map[string]any); okc {
		for _, n := range cronNames {
			if _, has := c[n]; has {
				a.B.SetCron(n, fields(c).boolean(n))
			}
		}
	}
	a.getSettings(w, r)
}

// textLabels describes the editable textbot entries for the panel UI.
var textLabels = []struct {
	ID, Label string
	Button    bool
}{
	{"text_start", "پیام خوش‌آمد (/start)", false},
	{"text_sell", "دکمه خرید سرویس", true},
	{"text_usertest", "دکمه اکانت تست", true},
	{"text_Purchased_services", "دکمه سرویس‌های من", true},
	{"text_Tariff_list", "دکمه تعرفه‌ها", true},
	{"text_dec_Tariff_list", "متن تعرفه‌ها", false},
	{"text_account", "دکمه حساب کاربری", true},
	{"text_Add_Balance", "دکمه افزایش موجودی", true},
	{"text_support", "دکمه پشتیبانی", true},
	{"text_help", "دکمه آموزش", true},
	{"text_fq", "دکمه سوالات متداول", true},
	{"text_dec_fq", "متن سوالات متداول", false},
	{"text_Discount", "دکمه کد هدیه", true},
	{"text_roll", "متن قوانین", false},
	{"text_channel", "متن عضویت اجباری کانال", false},
	{"text_bot_off", "پیام خاموش بودن ربات", false},
}

func (a *API) getTexts(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	cur := map[string]string{}
	for _, row := range d.MustQuery("SELECT id_text, text FROM textbot") {
		cur[row.S("id_text")] = row.S("text")
	}
	out := []map[string]any{}
	for _, t := range textLabels {
		out = append(out, map[string]any{"id": t.ID, "label": t.Label, "button": t.Button, "text": cur[t.ID]})
	}
	ok(w, out)
}

func (a *API) putTexts(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := decode(r, &in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	known := map[string]bool{}
	for _, t := range textLabels {
		known[t.ID] = true
	}
	for id, txt := range in {
		if !known[id] {
			fail(w, 400, "unknown text id "+id)
			return
		}
		if strings.TrimSpace(txt) == "" {
			fail(w, 400, id+" cannot be empty")
			return
		}
		if bad := bot.DisallowedEmojiInText(a.B.DB, txt); bad != "" {
			fail(w, 400, id+": premium emoji "+bad+" is not in the allowed packs")
			return
		}
	}
	for id, txt := range in {
		a.B.DB.Exec("INSERT INTO textbot (id_text, text) VALUES (?, ?) ON DUPLICATE KEY UPDATE text = VALUES(text)", id, txt)
	}
	a.getTexts(w, r)
}

func (a *API) getButtons(w http.ResponseWriter, r *http.Request) {
	bc := bot.LoadButtons(a.B.DB)
	labels := map[string]string{}
	for _, row := range a.B.DB.MustQuery("SELECT id_text, text FROM textbot") {
		labels[row.S("id_text")] = row.S("text")
	}
	labels["affiliates"] = bot.T("users.affiliates.btn")
	labels["admin"] = bot.T("Admin.commendadmin")
	labels["support_message"] = bot.T("users.sendmessagesupport")
	labels["rules_accept"] = bot.T("users.rulesaccept")
	labels["back_home"] = bot.T("users.backhome")
	ok(w, map[string]any{
		"layout":     bc.Layout,
		"buttons":    bc.Buttons,
		"mode":       menuMode(bc.Mode),
		"labels":     labels,
		"main_keys":  bot.MainButtonKeys,
		"extra_keys": bot.ExtraStyleKeys,
		"styles":     []string{"", "primary", "success", "danger"},
	})
}

func (a *API) putButtons(w http.ResponseWriter, r *http.Request) {
	var in bot.ButtonConfig
	if err := decode(r, &in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := bot.ValidateButtons(in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	for k, s := range in.Buttons {
		if s.Emoji != "" && !bot.EmojiAllowed(a.B.DB, s.Emoji) {
			fail(w, 400, "button "+k+": this premium emoji is not in the allowed packs")
			return
		}
	}
	// a field left out keeps what is stored
	cur := bot.LoadButtons(a.B.DB)
	if in.Layout == nil {
		in.Layout = cur.Layout
	}
	if in.Buttons == nil {
		in.Buttons = cur.Buttons
	}
	if in.Mode == "" {
		in.Mode = cur.Mode
	} else if in.Mode == "reply" {
		in.Mode = ""
	}
	bot.StoreButtons(a.B.DB, in)
	a.getButtons(w, r)
}

func menuMode(m string) string {
	if m == bot.MenuInline {
		return bot.MenuInline
	}
	return "reply"
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
