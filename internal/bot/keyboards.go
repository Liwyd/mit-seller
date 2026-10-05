package bot

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"github.com/MHBehzadian/nexra-mirzabot/internal/tg"
)

// Keyboards from keyboard.php.

type B = tg.Button

func txt(key string) B { return tg.Txt(T(key)) }

func rk(rows ...[]B) *tg.ReplyKeyboard { return tg.Reply(rows...) }

func ik(rows ...[]B) *tg.InlineKeyboard { return tg.Inline(rows...) }

func row(b ...B) []B { return b }

func cb(t, d string) B { return tg.CB(t, d) }

const autopayAdminButton = "💳 تأیید خودکار پرداخت"

// mainLabel is the label of a main-menu key.
func (c *Ctx) mainLabel(key string) string {
	switch key {
	case "affiliates":
		return T("users.affiliates.btn")
	case "admin":
		return T("Admin.commendadmin")
	}
	return c.texts[key]
}

// kbMainFor is $keyboard for a given recipient (the PHP code built it for
// $from_id even when sending to someone else; here the admin row only goes to
// admins). In inline mode the same rows become buttons on the message, whose
// taps menuTap turns back into the label text.
func (c *Ctx) kbMainFor(id string) tg.Markup {
	bc := LoadButtons(c.db())
	inline := bc.Mode == MenuInline
	button := func(key, label string) B {
		if inline {
			return bc.styled(key, cb(label, menuTapPrefix+key))
		}
		return bc.styled(key, tg.Txt(label))
	}
	var rows [][]B
	for _, keys := range bc.Layout {
		var r []B
		for _, k := range keys {
			if bc.Buttons[k].Hidden {
				continue
			}
			r = append(r, button(k, c.mainLabel(k)))
		}
		if len(r) > 0 {
			rows = append(rows, r)
		}
	}
	if c.isAdminID(id) {
		rows = append(rows, row(button("admin", T("Admin.commendadmin"))))
	}
	if inline {
		return ik(rows...)
	}
	return rk(rows...)
}

func (c *Ctx) kbMain() tg.Markup { return c.kbMainFor(c.fromID) }

// menuTap turns a tap on an inline main-menu button into the label text, so
// it takes exactly the path of the same button on the keyboard.
func (c *Ctx) menuTap() {
	if !strings.HasPrefix(c.datain, menuTapPrefix) {
		return
	}
	key := strings.TrimPrefix(c.datain, menuTapPrefix)
	known := key == "admin" && c.isAdmin
	for _, k := range MainButtonKeys {
		known = known || k == key
	}
	if !known {
		return
	}
	label := c.mainLabel(key)
	if label == "" {
		return
	}
	c.b.TG.AnswerCallback(c.callbackQueryID, "", false)
	c.text, c.textIn, c.datain, c.callbackQueryID = label, label, "", ""
}

// keyTap maps a keyboard tap whose text is not exactly a button label onto
// that label. With a premium icon on a button, an emoji taken out of a label,
// or an invisible character in it, the text Telegram sends can differ from
// the stored label by emoji, joiners and spaces only, and the tap would
// otherwise do nothing. Exact matches are left alone.
func (c *Ctx) keyTap() {
	if c.text == "" || c.datain != "" || len(c.text) > 200 {
		return
	}
	labels := map[string]string{}
	for _, k := range MainButtonKeys {
		labels[k] = c.mainLabel(k)
	}
	labels["back_home"] = T("users.backhome")
	if c.isAdmin {
		labels["admin"] = T("Admin.commendadmin")
	}
	for _, l := range labels {
		if l == c.text {
			return
		}
	}
	want := menuKey(withoutCustomEmoji(c.text, c.f.Entities))
	if want == "" {
		return
	}
	match := ""
	for _, l := range labels {
		if l != "" && menuKey(l) == want {
			if match != "" && match != l {
				return // ambiguous
			}
			match = l
		}
	}
	if match != "" {
		c.text, c.textIn = match, match
	}
}

// menuKey is a label with emoji, joiners, marks and extra spaces removed.
func menuKey(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		case unicode.In(r, unicode.So, unicode.Sk, unicode.Cf, unicode.Mn, unicode.Me, unicode.Co, unicode.Cs) ||
			r == 0xFE0F || r == 0x200D || r == 0x200C:
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// withoutCustomEmoji drops the custom emoji ranges from text.
func withoutCustomEmoji(text string, ents []tg.Entity) string {
	ce := customEmojis(ents)
	if len(ce) == 0 {
		return text
	}
	u := utf16.Encode([]rune(text))
	var out []uint16
	pos := 0
	for _, e := range ce {
		if e.Offset < pos || e.Offset+e.Length > len(u) {
			continue
		}
		out = append(out, u[pos:e.Offset]...)
		pos = e.Offset + e.Length
	}
	out = append(out, u[pos:]...)
	return string(utf16.Decode(out))
}

// dropReplyKeyboard clears the keyboard under the text field (left from the
// keyboard menu) when the menu is inline: a throwaway message removes it.
func (c *Ctx) dropReplyKeyboard() {
	if LoadButtons(c.db()).Mode != MenuInline {
		return
	}
	r := c.b.TG.SendMessage(c.fromID, "…", map[string]any{"remove_keyboard": true}, "")
	var m struct {
		MessageID int64 `json:"message_id"`
	}
	if r.OK && json.Unmarshal(r.Result, &m) == nil && m.MessageID != 0 {
		c.b.TG.DeleteMessage(c.fromID, m.MessageID)
	}
}

func (c *Ctx) kbPanel() *tg.InlineKeyboard {
	bc := LoadButtons(c.db())
	k := ik(row(bc.styled("text_Discount", cb(c.texts["text_Discount"], "Discount"))))
	k.ResizeKeyboard = true
	return k
}

func kbAdmin() *tg.ReplyKeyboard {
	return rk(
		row(txt("Admin.keyboardadmin.bot_statistics")),
		row(txt("Admin.keyboardadmin.manage_panel"), txt("Admin.keyboardadmin.add_panel")),
		row(txt("Admin.keyboardadmin.shop_section"), txt("Admin.keyboardadmin.finance")),
		row(txt("Admin.keyboardadmin.admin_section"), txt("Admin.keyboardadmin.bot_text_settings")),
		row(txt("Admin.keyboardadmin.user_services"), txt("Admin.keyboardadmin.user_search"), txt("Admin.keyboardadmin.send_message")),
		row(txt("Admin.keyboardadmin.tutorial_section"), txt("Admin.keyboardadmin.settings")),
		row(tg.Txt(autopayAdminButton)),
		row(tg.Txt(emojiIDButton)),
		row(txt("users.backhome")),
	)
}

func backAdminRow() []B { return row(txt("Admin.Back-Adminment")) }

func kbCartManage() *tg.ReplyKeyboard {
	return rk(row(txt("users.moeny.card_number_settings")), backAdminRow())
}
func kbAqayepardakht() *tg.ReplyKeyboard {
	return rk(row(txt("users.moeny.mr_payment_merchant_settings")), backAdminRow())
}
func kbNowPayments() *tg.ReplyKeyboard {
	return rk(row(txt("users.moeny.nowpayment_api")), backAdminRow())
}
func kbAdminSection() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.Addedadmin"), txt("Admin.Removeedadmin")), row(txt("Admin.manageadmin.showlistbtn")), backAdminRow())
}
func kbUsertest() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.getlimitusertest.setlimitallbtn")),
		row(txt("Admin.Usertest.settimeusertest"), txt("Admin.Usertest.setvolumeusertest")), backAdminRow())
}
func kbSettingPanel() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.keyboardadmin.seetingstatus")),
		row(txt("Admin.keyboardadmin.settingscron"), txt("Admin.keyboardadmin.test_account_settings")),
		row(txt("Admin.channel.channelreport"), txt("Admin.channel.setting")),
		row(txt("Admin.keyboardadmin.affiliate_settings")), backAdminRow())
}

// kbStepPayment is $step_payment: one row per enabled gateway.
func (c *Ctx) kbStepPayment() *tg.InlineKeyboard {
	d := c.db()
	var rows [][]B
	if d.PaySetting("Cartstatus") == "oncard" {
		rows = append(rows, row(cb(T("users.moeny.cart_to_Cart_btn"), "cart_to_offline")))
	}
	if d.PaySetting("nowpaymentstatus") == "onnowpayment" {
		rows = append(rows, row(cb(T("users.moeny.nowpaymentbtn"), "nowpayments")))
	}
	if d.PaySetting("digistatus") == "ondigi" {
		rows = append(rows, row(cb(T("users.moeny.currency_rial_gateway"), "iranpay")))
	}
	if d.PaySetting("statusaqayepardakht") == "onaqayepardakht" {
		rows = append(rows, row(cb(T("users.moeny.mr_payment_gateway"), "aqayepardakht")))
	}
	rows = append(rows, row(cb(T("users.closelist"), "closelist")))
	return ik(rows...)
}

func kbUserServices() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.ManageUser.searchorder")),
		row(txt("Admin.ManageUser.removeorderbtn"), txt("Admin.Balance.SendBalanceAll")), backAdminRow())
}
func kbHelpAdmin() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.Help.addhelp"), txt("Admin.Help.removehelpbtn")), row(txt("Admin.Help.edithelp")), backAdminRow())
}
func kbShop() *tg.ReplyKeyboard {
	return rk(
		row(txt("Admin.Product.addproduct"), txt("Admin.Product.titlebtnremove")),
		row(txt("Admin.category.add"), txt("Admin.category.remove")),
		row(txt("Admin.Product.titlebtnedit")),
		row(txt("Admin.managepanel.keyboardpanel.setvolume")),
		row(txt("Admin.Discount.titlebtn"), txt("Admin.Discount.titlebtnremove")),
		row(txt("Admin.Discountsell.create"), txt("Admin.Discountsell.remove")),
		backAdminRow())
}
func (c *Ctx) kbConfirmRules() *tg.ReplyKeyboard {
	bc := LoadButtons(c.db())
	return rk(row(bc.styled("rules_accept", txt("users.rulesaccept"))))
}
func kbRequestContact() *tg.ReplyKeyboard {
	b := txt("users.sendnumber")
	b.RequestContact = true
	return rk(row(b), row(txt("users.backhome")))
}
func kbSendMessage() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.systemsms.sendbulkbtn"), txt("Admin.systemsms.forwardbulkbtn")),
		row(txt("Admin.systemsms.sendmessageauser")), backAdminRow())
}
func kbChannel() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.channel.changechannelbtn")), backAdminRow())
}
func (c *Ctx) kbBackUser() *tg.ReplyKeyboard {
	bc := LoadButtons(c.db())
	return rk(row(bc.styled("back_home", txt("users.backhome"))))
}
func kbBackAdmin() *tg.ReplyKeyboard { return rk(backAdminRow()) }

// kbPanelList is $json_list_marzban_panel (every panel by name).
func (c *Ctx) kbPanelList() *tg.ReplyKeyboard {
	var rows [][]B
	for _, r := range c.db().MustQuery("SELECT name_panel FROM marzban_panel") {
		rows = append(rows, row(tg.Txt(r.S("name_panel"))))
	}
	rows = append(rows, backAdminRow())
	return rk(rows...)
}

// kbHelpList is $json_list_help.
func (c *Ctx) kbHelpList() *tg.ReplyKeyboard {
	var rows [][]B
	for _, r := range c.db().MustQuery("SELECT name_os FROM help") {
		rows = append(rows, row(tg.Txt(r.S("name_os"))))
	}
	rows = append(rows, row(txt("users.backhome")))
	return rk(rows...)
}

// kbPanelsUser is $list_marzban_panel_user; the callback prefix depended on
// the user's step when the update arrived.
func (c *Ctx) kbPanelsUser() *tg.InlineKeyboard {
	var rows [][]B
	prefix := "location_"
	if c.usersStep == "getusernameinfo" {
		prefix = "locationnotuser_"
	}
	for _, r := range c.db().MustQuery("SELECT id, name_panel FROM marzban_panel WHERE status = 'activepanel'") {
		rows = append(rows, row(cb(r.S("name_panel"), prefix+r.S("id"))))
	}
	rows = append(rows, row(cb(T("users.backhome"), "backuser")))
	return ik(rows...)
}

// kbPanelsTest is $list_marzban_usertest.
func (c *Ctx) kbPanelsTest() *tg.InlineKeyboard {
	var rows [][]B
	for _, r := range c.db().MustQuery("SELECT id, name_panel FROM marzban_panel WHERE statusTest = 'ontestshowpanel'") {
		rows = append(rows, row(cb(r.S("name_panel"), "locationtests_"+r.S("id"))))
	}
	rows = append(rows, row(cb(T("users.backhome"), "backuser")))
	return ik(rows...)
}

func kbTextbot() *tg.ReplyKeyboard {
	return rk(
		row(txt("users.changetext.set_start_text"), txt("users.changetext.purchased_service_button")),
		row(txt("users.changetext.test_account_button"), txt("users.changetext.faq_button")),
		row(txt("users.changetext.tutorial_button"), txt("users.changetext.support_button")),
		row(txt("users.changetext.increase_balance_button"), txt("users.changetext.law_text")),
		row(txt("users.changetext.buy_subscription_button"), txt("users.changetext.tariff_list_button")),
		row(txt("users.changetext.tariff_list_description")),
		row(txt("users.changetext.user_account_button")),
		row(txt("users.changetext.mandatory_membership_description")),
		row(txt("users.changetext.faq_description")),
		backAdminRow())
}

// kbProductsAdmin is $json_list_product_list_admin: products of the location
// named by the text that arrived with this update (or '/all').
func (c *Ctx) kbProductsAdmin() *tg.ReplyKeyboard {
	rows := [][]B{backAdminRow()}
	for _, r := range c.db().MustQuery("SELECT name_product FROM product WHERE Location = ? OR Location = '/all'", c.textIn) {
		rows = append(rows, row(tg.Txt(r.S("name_product"))))
	}
	return rk(rows...)
}

func (c *Ctx) kbDiscountList() *tg.ReplyKeyboard {
	rows := [][]B{backAdminRow()}
	for _, r := range c.db().MustQuery("SELECT code FROM Discount") {
		rows = append(rows, row(tg.Txt(r.S("code"))))
	}
	return rk(rows...)
}

func (c *Ctx) kbDiscountSellList() *tg.ReplyKeyboard {
	rows := [][]B{backAdminRow()}
	for _, r := range c.db().MustQuery("SELECT codeDiscount FROM DiscountSell") {
		rows = append(rows, row(tg.Txt(r.S("codeDiscount"))))
	}
	return rk(rows...)
}

func kbPayment() *tg.InlineKeyboard {
	return ik(row(cb(T("users.buy.payandGet"), "confirmandgetservice")),
		row(cb(T("users.buy.discount"), "aptdc")),
		row(cb(T("users.backhome"), "backuser")))
}

func kbChangeProduct() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.Product.editprice"), txt("Admin.Product.editvolume"), txt("Admin.Product.edittime")),
		row(txt("Admin.Product.editname"), txt("Admin.Product.editcategory")), backAdminRow())
}

func kbMethodUsername() *tg.ReplyKeyboard {
	return rk(row(txt("users.customusernameorder")), row(txt("users.customidAndRandom")), row(txt("users.customusername")),
		row(txt("users.customtextandrandom")), row(txt("users.customuserchoice")), backAdminRow())
}

func mp(k string) B { return txt("Admin.managepanel." + k) }

func optMarzban() *tg.ReplyKeyboard {
	return rk(row(mp("btnshowconnect"), mp("showpanelbtn")), row(mp("showpaneltestbtn"), mp("setinbound")),
		row(mp("keyboardpanel.namepanel"), mp("keyboardpanel.removepanel")),
		row(mp("keyboardpanel.editurl"), mp("keyboardpanel.editusername")),
		row(mp("keyboardpanel.editpassword")), row(mp("methodusername")),
		row(mp("sublinkstatus"), mp("configstatus")), row(mp("keyboardpanel.on_hold_status")), backAdminRow())
}
func optNexra() *tg.ReplyKeyboard {
	return rk(row(mp("btnshowconnect"), mp("showpanelbtn")), row(mp("showpaneltestbtn")),
		row(mp("keyboardpanel.namepanel"), mp("keyboardpanel.removepanel")),
		row(mp("keyboardpanel.editnexracreds")), row(mp("methodusername")), backAdminRow())
}
func optMikrotik() *tg.ReplyKeyboard {
	return rk(row(mp("btnshowconnect"), mp("showpanelbtn")), row(mp("showpaneltestbtn"), mp("setgroup")),
		row(mp("keyboardpanel.namepanel"), mp("keyboardpanel.removepanel")),
		row(mp("keyboardpanel.editurl"), mp("keyboardpanel.editusername")),
		row(mp("keyboardpanel.editpassword"), mp("methodusername")), backAdminRow())
}
func optWG() *tg.ReplyKeyboard {
	return rk(row(mp("showpanelbtn")), row(mp("showpaneltestbtn")),
		row(mp("keyboardpanel.namepanel"), mp("keyboardpanel.removepanel")),
		row(mp("keyboardpanel.editpassword"), mp("keyboardpanel.editusername")),
		row(mp("keyboardpanel.editurl"), mp("keyboardpanel.editinound")), backAdminRow())
}
func optSUI() *tg.ReplyKeyboard {
	return rk(row(mp("showpanelbtn")), row(mp("showpaneltestbtn"), mp("setinbound")),
		row(mp("keyboardpanel.namepanel"), mp("keyboardpanel.removepanel")),
		row(mp("keyboardpanel.editurl"), mp("keyboardpanel.editusername")),
		row(mp("keyboardpanel.editpassword")), row(mp("methodusername")),
		row(mp("sublinkstatus"), mp("configstatus")), backAdminRow())
}
func optMarzneshin() *tg.ReplyKeyboard {
	return rk(row(mp("btnshowconnect"), mp("showpanelbtn")), row(mp("showpaneltestbtn")),
		row(mp("keyboardpanel.namepanel"), mp("keyboardpanel.removepanel")),
		row(mp("keyboardpanel.editurl"), mp("keyboardpanel.editusername")),
		row(mp("keyboardpanel.editpassword"), txt("users.status.manageService")),
		row(mp("methodusername"), mp("keyboardpanel.on_hold_status")),
		row(mp("sublinkstatus"), mp("configstatus")), backAdminRow())
}
func optXUI() *tg.ReplyKeyboard {
	return rk(row(mp("btnshowconnect"), mp("showpanelbtn")), row(mp("showpaneltestbtn")),
		row(mp("keyboardpanel.namepanel"), mp("keyboardpanel.removepanel")), row(mp("methodusername")),
		row(mp("keyboardpanel.editpassword"), mp("keyboardpanel.editusername")),
		row(mp("keyboardpanel.editurl"), mp("keyboardpanel.editinound")),
		row(mp("sublinkstatus"), mp("configstatus")), row(mp("keyboardpanel.linksub")), backAdminRow())
}

func (c *Ctx) kbSupport() *tg.InlineKeyboard {
	bc := LoadButtons(c.db())
	return ik(row(bc.styled("text_fq", cb(c.texts["text_fq"], "fqQuestions"))),
		row(bc.styled("support_message", cb(T("users.sendmessagesupport"), "support"))))
}

func kbAffiliates() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.affiliate.status")), row(txt("Admin.affiliate.Percentageset")), row(txt("Admin.affiliate.setbaner")),
		row(txt("Admin.affiliate.porsantafterbuy"), txt("Admin.affiliate.gift")), row(txt("Admin.affiliate.giftstart")), backAdminRow())
}

func kbTypePanel() *tg.InlineKeyboard {
	t := func(k string) string { return T("Admin.managepanel.type." + k) }
	return ik(
		row(cb(t("marzban"), "typepanel%marzban"), cb(t("3x-ui"), "typepanel%x-ui_single")),
		row(cb(t("marzneshin"), "typepanel%marzneshin"), cb(t("alireza"), "typepanel%alireza")),
		row(cb(t("s-ui"), "typepanel%s_ui"), cb(t("wgdashboard"), "typepanel%wgdashboard")),
		row(cb(t("mikrotik"), "typepanel%mikrotik")),
		row(cb(t("nexra"), "typepanel%nexra")),
		row(cb(T("Admin.Back-Adminment"), "back_admin")))
}

func kbCron() *tg.ReplyKeyboard {
	c := func(k string) B { return txt("Admin.cron." + k) }
	return rk(row(c("test.active"), c("test.disable")), row(c("volume.active"), c("volume.disable")),
		row(c("time.active"), c("time.disable")), row(c("remove.active"), c("remove.disable")),
		row(c("remove.timeset")), backAdminRow())
}

func kbHelpEdit() *tg.ReplyKeyboard {
	return rk(row(txt("Admin.Help.change.name"), txt("Admin.Help.change.dec")), row(txt("Admin.Help.change.editmedia")), backAdminRow())
}

// kbCategory is KeyboardCategory().
func (c *Ctx) kbCategory() *tg.ReplyKeyboard {
	var rows [][]B
	for _, r := range c.db().MustQuery("SELECT remark FROM category") {
		rows = append(rows, row(tg.Txt(r.S("remark"))))
	}
	rows = append(rows, backAdminRow())
	return rk(rows...)
}

// kbCategoryBuy is KeyboardCategorybuy(): categories that have products here.
func (c *Ctx) kbCategoryBuy(back, location string) *tg.InlineKeyboard {
	var rows [][]B
	for _, r := range c.db().MustQuery("SELECT id, remark FROM category") {
		n := c.db().Count("SELECT COUNT(*) FROM product WHERE (Location = ? OR Location = '/all') AND category = ?", location, r.S("id"))
		if n == 0 {
			continue
		}
		rows = append(rows, row(cb(r.S("remark"), "categorylist_"+r.S("id"))))
	}
	rows = append(rows, row(cb(T("users.backmenu"), back)))
	return ik(rows...)
}

// kbProduct is KeyboardProduct().
func (c *Ctx) kbProduct(location, back, method string, category *string) *tg.InlineKeyboard {
	var rs []db.Row
	if category != nil {
		rs = c.db().MustQuery("SELECT name_product, code_product FROM product WHERE (Location = ? OR Location = '/all') AND category = ?", location, *category)
	} else {
		rs = c.db().MustQuery("SELECT name_product, code_product FROM product WHERE (Location = ? OR Location = '/all')", location)
	}
	var rows [][]B
	for _, r := range rs {
		if method == T("users.customusername") {
			rows = append(rows, row(cb(r.S("name_product"), "prodcutservices_"+r.S("code_product"))))
		} else {
			rows = append(rows, row(cb(r.S("name_product"), "prodcutservice_"+r.S("code_product"))))
		}
	}
	rows = append(rows, row(cb(T("users.backmenu"), back)))
	return ik(rows...)
}

// outTypePanel is outtypepanel(): reply with the option keyboard of a panel type.
func (c *Ctx) outTypePanel(typ, msg string) {
	var k *tg.ReplyKeyboard
	switch typ {
	case "marzban":
		k = optMarzban()
	case "x-ui_single", "alireza":
		k = optXUI()
	case "marzneshin":
		k = optMarzneshin()
	case "wgdashboard":
		k = optWG()
	case "s_ui":
		k = optSUI()
	case "mikrotik":
		k = optMikrotik()
	case "nexra":
		k = optNexra()
	default:
		return
	}
	c.sendHTML(c.fromID, msg, k)
}
