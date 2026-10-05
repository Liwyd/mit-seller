package bot

import (
	"strconv"
	"strings"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/tg"
)

// run is index.php followed by admin.php (a `return` in index.php skipped
// admin.php too, which is what a true from indexFlow means).
func (c *Ctx) run() {
	if c.chatType != "private" {
		return
	}
	if c.prelude() {
		return
	}
	for _, section := range []func() bool{
		c.secCommands,
		c.secServices,
		c.secUserTest,
		c.secHelpSupport,
		c.secBuy,
		c.secBalance,
		c.secPayConfirm,
		c.secDiscount,
		c.secMisc,
	} {
		if section() {
			return
		}
	}
	if !c.isAdmin {
		return
	}
	c.adminFlow()
}

func sanitizeUserName(s string) string {
	for _, f := range []string{"'", "\"", "<", ">", "--", "#", ";", "\\", "%", "(", ")"} {
		s = strings.ReplaceAll(s, f, "")
	}
	return s
}

func (c *Ctx) loadTexts() {
	c.texts = map[string]string{}
	for _, k := range []string{"text_usertest", "text_Purchased_services", "text_support", "text_help", "text_start",
		"text_bot_off", "text_roll", "text_fq", "text_dec_fq", "text_account", "text_sell", "text_Add_Balance",
		"text_channel", "text_Discount", "text_Tariff_list", "text_dec_Tariff_list"} {
		c.texts[k] = ""
	}
	for _, r := range c.db().MustQuery("SELECT id_text, text FROM textbot") {
		if _, ok := c.texts[r.S("id_text")]; ok {
			c.texts[r.S("id_text")] = r.S("text")
		}
	}
}

// phpNotZero is PHP 8 `$v != 0` for a string value.
func phpNotZero(v string) bool {
	if php.IsNumeric(v) {
		return php.Floatval(v) != 0
	}
	return v != "0"
}

// prelude is index.php from the top down to "#-----------/start".
func (c *Ctx) prelude() bool {
	d := c.db()
	c.firstName = sanitizeUserName(c.firstName)
	c.setting = d.Setting()
	c.adminIDs = d.AdminIDs()
	c.isAdmin = c.isAdminID(c.fromID)
	fromInt := php.Intval(c.fromID)

	// keyboard.php read the user's step before anything changed it
	c.usersStep = normStep(d.Select("user", "step", "id", c.fromID).S("step"))

	if fromInt != 0 && !d.Exists("user", "id", c.fromID) {
		resp := ik(row(cb(T("Admin.ManageUser.sendmessageUser"), "Response_"+c.fromID)))
		msg := sprintf("Admin.ManageUser.NewUserMessage", c.firstName, c.username, c.fromID, c.fromID)
		for _, a := range c.adminIDs {
			c.send(a, msg, resp, "html")
		}
	}
	if fromInt != 0 {
		verify := "1"
		if php.Intval(c.setting.S("status_verify")) == 1 {
			verify = "0"
		}
		var ref string
		for {
			ref = randHex(16)
			if !d.Exists("user", "ref_code", ref) {
				break
			}
		}
		d.Exec(`INSERT IGNORE INTO user
            (id, ref_code, step, limit_usertest, User_Status, number, Balance,
            pagenumber, username, message_count, last_message_time,
            affiliatescount, affiliates, verify)
        VALUES
            (?, ?, 'none', ?, 'Active', 'none', '0',
            '1', ?, '0', '0', '0', '0', ?)`,
			c.fromID, ref, defaultStr(c.setting.S("limit_usertest_all"), "0"), c.username, verify)
	}
	c.user = d.Select("user", "*", "id", c.fromID)
	if c.user == nil {
		c.user = db.Row{}
	}
	if _, ok := c.user["step"]; ok {
		c.user.Set("step", normStep(c.user.S("step")))
	}
	if (php.LooseEq(c.setting.S("status_verify"), "1") && php.Intval(c.user.S("verify")) == 0) && !c.isAdmin {
		c.send(c.fromID, T("users.VerifyUser"), nil, "html")
		return true
	}
	c.loadTexts()
	c.menuTap()
	c.keyTap()
	c.channels = d.Select("channels", "*", "", nil)

	if c.user.S("username") == "none" || c.user.IsNull("username") {
		c.setUser("username", c.username)
	}
	if c.user.S("User_Status") == "block" {
		c.send(c.fromID, sprintf("Admin.ManageUser.BlockedUser", c.user.S("description_blocking")), nil, "html")
		return true
	}

	if strings.Contains(c.text, "/start ") {
		if c.referral() {
			return true
		}
	}

	now := nowUnix()
	since := now - php.Intval(c.user.S("last_message_time"))
	if since >= 60 {
		c.setUser("last_message_time", now)
		c.setUser("message_count", "1")
	} else {
		if !c.isAdmin {
			c.setUser("message_count", php.Intval(c.user.S("message_count"))+1)
			if php.Floatval(c.user.S("message_count")) >= 35 {
				c.setUser("User_Status", "block")
				c.setUser("description_blocking", T("users.spamtext"))
				c.send(c.fromID, T("users.spamtext"), nil, "html")
				return true
			}
		}
		if c.setting.S("Bot_Status") == "✅  ربات روشن است" && !c.isAdmin {
			c.send(c.fromID, T("users.updatingbot"), nil, "html")
			for _, a := range c.adminIDs {
				c.send(a, "❌ ادمین عزیز ربات فعال نیست جهت فعالسازی به منوی تنظیمات عمومی > وضعیت قابلیت ها بروید تا رباتتان فعال شود.", nil, "html")
			}
			return true
		}
	}

	notJoined := c.channelMissing()
	if c.datain == "confirmchannel" {
		if notJoined != "" && !c.isAdmin {
			c.alert(T("users.channel.notconfirmed"))
		} else {
			c.del()
			c.send(c.fromID, T("users.channel.confirmed"), c.kbMain(), "html")
		}
		return true
	}
	if notJoined != "" && !c.isAdmin {
		k := ik(row(tg.URLBtn(T("users.channel.text_join"), "https://t.me/"+notJoined)),
			row(cb(T("users.channel.confirmjoin"), "confirmchannel")))
		c.send(c.fromID, c.texts["text_channel"], k, "html")
		return true
	}

	if php.LooseEq(c.setting.S("roll_Status"), "1") && php.LooseEq(c.user.S("roll_Status"), "0") && c.text != T("users.rulesaccept") && !c.isAdmin {
		c.send(c.fromID, c.texts["text_roll"], c.kbConfirmRules(), "html")
		return true
	}
	if c.text == T("users.rulesaccept") {
		c.send(c.fromID, T("users.Rules"), c.kbMain(), "html")
		c.setUser("roll_Status", "1")
	}
	if php.LooseEq(c.setting.S("Bot_Status"), "0") && !c.isAdmin {
		c.send(c.fromID, c.texts["text_bot_off"], nil, "html")
		return true
	}

	// unpaid invoices older than a day are dropped
	for _, inv := range d.MustQuery("SELECT id_invoice, time_sell FROM invoice WHERE id_user = ? AND status = 'unpaid'", c.fromID) {
		if php.CtypeDigit(inv.S("time_sell")) && now-php.Intval(inv.S("time_sell")) > 86400 {
			d.Exec("DELETE FROM invoice WHERE id_invoice = ?", inv.S("id_invoice"))
		}
	}
	return false
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// channelMissing is channel(): the channel the user still has to join, or "".
func (c *Ctx) channelMissing() string {
	link := c.channels.S("link")
	if link == "" {
		return ""
	}
	status, ok := c.b.TG.ChatMemberStatus("@"+link, c.fromID)
	if !ok {
		return ""
	}
	switch status {
	case "member", "creator", "administrator":
		return ""
	}
	return link
}

// referral handles "/start <code>". Returns true when the script stopped.
func (c *Ctx) referral() bool {
	d := c.db()
	if phpNotZero(c.user.S("affiliates")) {
		c.send(c.fromID, T("users.affiliates.affiliateseduser"), nil, "html")
		return true
	}
	aff := d.Select("affiliates", "*", "", nil)
	if aff.S("affiliatesstatus") == "offaffiliates" {
		c.send(c.fromID, T("users.affiliates.offaffiliates"), c.kbMain(), "HTML")
		return true
	}
	token := strings.ReplaceAll(c.text, "/start ", "")
	var affID string
	digits := false
	if r := d.Select("user", "id", "ref_code", token); r != nil {
		affID = r.S("id")
		digits = php.CtypeDigit(affID)
	} else if php.CtypeDigit(token) {
		n := php.Intval(token)
		affID = strconv.FormatInt(n, 10)
		// ctype_digit() on an int: values below 256 are read as a character
		digits = n >= 256 || (n >= 48 && n <= 57)
	}
	if !digits {
		return false
	}
	if !d.Exists("user", "id", affID) {
		c.send(c.fromID, T("users.affiliates.affiliatesyou"), nil, "html")
		return true
	}
	if php.LooseEq(affID, c.fromID) {
		c.send(c.fromID, T("users.affiliates.invalidaffiliates"), nil, "html")
		return true
	}
	if inv := d.Select("user", "affiliates", "id", affID); inv != nil && php.Intval(inv.S("affiliates")) == php.Intval(c.fromID) {
		c.send(c.fromID, T("users.affiliates.invalidMutual"), nil, "html")
		return true
	}
	if aff.S("Discount") == "onDiscountaffiliates" {
		inviter := d.Select("user", "*", "id", affID)
		bal := inviter.F("Balance") + aff.F("price_Discount")
		d.Update("user", "Balance", php.NumStr(bal), "id", affID)
		c.send(affID, sprintf("users.affiliates.giftuser", nf(aff.F("price_Discount")), c.fromID), nil, "html")
	}
	c.dropReplyKeyboard()
	c.send(c.fromID, c.texts["text_start"], c.kbMain(), "html")
	inviter := d.Select("user", "*", "id", affID)
	d.Update("user", "affiliates", affID, "id", c.fromID)
	d.Update("user", "affiliatescount", php.Intval(inviter.S("affiliatescount"))+1, "id", affID)
	return false
}
