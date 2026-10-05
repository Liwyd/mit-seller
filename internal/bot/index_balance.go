package bot

import (
	"strings"

	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
	"github.com/MHBehzadian/nexra-mirzabot/internal/tg"
)

func (c *Ctx) invoiceTag() string {
	if c.user.S("Processing_value_tow") == "getconfigafterpay" {
		return c.user.S("Processing_value_tow") + "|" + c.user.S("Processing_value_one")
	}
	return "0|0"
}

func (c *Ctx) insertPayment(order, method, status string) {
	c.db().Exec("INSERT INTO Payment_report (id_user, id_order, time, price, payment_Status, Payment_Method,invoice) VALUES (?, ?, ?, ?, ?, ?,?)",
		c.fromID, order, php.DateNow("Y/m/d H:i:s"), c.user.S("Processing_value"), status, method, c.invoiceTag())
}

func (c *Ctx) secBalance() bool {
	switch {
	case c.text == c.texts["text_Add_Balance"] || c.text == "/wallet":
		c.setUser("Processing_value", "0")
		c.setUser("Processing_value_one", "0")
		c.setUser("Processing_value_tow", "0")
		if c.numberGate(false) {
			return true
		}
		c.sendHTML(c.fromID, T("users.Balance.priceinput"), c.kbBackUser())
		c.step("getprice")
	case c.stepIs("getprice"):
		if !php.IsNumeric(c.text) {
			c.sendHTML(c.fromID, T("users.Balance.errorprice"), nil)
			return true
		}
		if v := php.Floatval(c.text); v > 10000000 || v < 20000 {
			c.sendHTML(c.fromID, T("users.Balance.errorpricelimit"), nil)
			return true
		}
		c.setUser("Processing_value", c.text)
		c.sendHTML(c.fromID, T("users.Balance.selectPatment"), c.kbStepPayment())
		c.step("get_step_payment")
	case c.stepIs("get_step_payment"):
		if c.datain == "cart_to_offline" {
			c.cardToCard()
		}
		if c.datain == "aqayepardakht" {
			if c.user.F("Processing_value") < 5000 {
				c.sendHTML(c.fromID, T("users.Balance.zarinpal"), nil)
				return true
			}
			c.sendHTML(c.fromID, T("users.Balance.linkpayments"), c.kbMain())
			order := randHex(5)
			c.insertPayment(order, "aqayepardakht", "Unpaid")
			k := ik(row(tg.URLBtn(T("users.Balance.payments"), "https://"+c.b.Cfg.Domain+"/payment/aqayepardakht/aqayepardakht.php?price="+c.user.S("Processing_value")+"&order_id="+order)))
			c.sendHTML(c.fromID, sprintf("users.moeny.aqayepardakht", order, nfs(c.user.S("Processing_value"))), k)
		}
		if c.datain == "nowpayments" {
			c.del()
			c.sendHTML(c.fromID, T("users.Balance.linkpayments"), c.kbMain())
			usd := tronRate().USD
			usdPrice := 0.0
			if usd != 0 {
				usdPrice = php.Round(c.user.F("Processing_value")/float64(usd), 2)
			}
			order := randHex(5)
			pay := c.b.nowPayments("invoice", usdPrice, order, "order")
			if !pay.isset("id") {
				c.sendHTML(c.fromID, T("users.Balance.errorLinkPayment"), c.kbMain())
				c.step("home")
				c.report(sprintf("users.moeny.nowpayments_create_link_error", pay.json(), c.fromID, c.username))
				return true
			}
			c.insertPayment(order, "Nowpayments", "Unpaid")
			k := ik(row(tg.URLBtn(T("users.Balance.payments"), pay.s("invoice_url"))))
			c.sendHTML(c.fromID, sprintf("users.moeny.nowpayment", order, nfs(c.user.S("Processing_value")), nf(float64(usd)), php.FloatToString(usdPrice)), k)
		}
		if c.datain == "iranpay" {
			rate := tronRate()
			var trxPrice, usdPrice float64
			if rate.TRX != 0 {
				trxPrice = php.Round(c.user.F("Processing_value")/float64(rate.TRX), 2)
			}
			if rate.USD != 0 {
				usdPrice = php.Round(c.user.F("Processing_value")/float64(rate.USD), 2)
			}
			if trxPrice <= 1 {
				c.sendHTML(c.fromID, T("users.Balance.changeto"), nil)
				return true
			}
			c.sendHTML(c.fromID, T("users.Balance.linkpayments"), c.kbMain())
			order := randHex(5)
			c.insertPayment(order, "Currency Rial gateway", "Unpaid")
			pay := c.b.nowPayments("payment", usdPrice, order, "SwapinoBot_"+order+"_"+php.FloatToString(trxPrice))
			// index.php read this reply as an object ($pay->pay_address) although
			// it was decoded as an array, so this gateway always failed there.
			if !pay.isset("pay_address") {
				c.sendHTML(c.fromID, T("users.Balance.errorLinkPayment"), c.kbMain())
				c.step("home")
				for _, a := range c.adminIDs {
					c.sendHTML(a, sprintf("users.moeny.eror", pay.s("message"), c.fromID, c.username), c.kbMainFor(a))
				}
				return true
			}
			amount := strings.ReplaceAll(pay.s("pay_amount"), ".", "_")
			addr := pay.s("pay_address")
			k := ik(row(tg.URLBtn(T("users.Balance.payments"), "https://t.me/SwapinoBot?start=trx-"+addr+"-"+amount+"-Tron")),
				row(cb(T("users.Balance.Confirmpaying"), "Confirmpay_user_"+pay.s("payment_id")+"_"+order)))
			toman := nfs(c.user.S("Processing_value"))
			c.sendHTML(c.fromID, sprintf("users.moeny.iranpay", order, addr, amount, toman, itoa(rate.TRX), toman), k)
		}
	}
	return false
}

func (c *Ctx) cardToCard() {
	d := c.db()
	card := d.PaySetting("CartDescription")
	note := ""
	amount := php.Intval(c.user.S("Processing_value"))
	if c.b.AutopayEnabled() {
		if res, ok := c.b.AutopayReserve(c.fromID, c.user.S("Processing_value")); ok {
			amount = res
			c.setUser("Processing_value", amount)
			c.user.Set("Processing_value", itoa(amount))
			note = AutopayPriceNote(amount)
		}
	}
	waiting := note != ""
	msg := sprintf("users.moeny.carttext", nf(float64(amount)), card) + note
	digits := re(`\d+`).FindAllString(card, -1)
	if len(digits) > 0 && php.Intval(c.setting.S("copy_cart")) == 1 {
		k := ik(
			row(B{Text: T("users.moeny.copy_card_number"), CopyText: &tg.CopyText{Text: strings.Join(digits, "")}},
				B{Text: T("users.moeny.copy_price"), CopyText: &tg.CopyText{Text: c.user.S("Processing_value")}}),
			row(cb(T("users.backhome"), "backuser")),
		)
		c.edit(msg, k)
	} else {
		c.del()
		c.sendHTML(c.fromID, msg, c.kbBackUser())
	}
	if waiting {
		c.step("home")
	} else {
		c.step("cart_to_cart_user")
	}
}

func (c *Ctx) secPayConfirm() bool {
	d := c.db()
	if c.m(`Confirmpay_user_(\w+)_(\w+)`) {
		paymentID, order := c.g(1), c.g(2)
		rep := d.Select("Payment_report", "*", "id_order", order)
		if rep.S("payment_Status") == "paid" {
			c.alert(T("users.Balance.Confirmpayadmin"))
			return true
		}
		st := c.b.paymentStatus(paymentID).s("payment_status")
		switch st {
		case "finished":
			c.alert(T("users.Balance.finished"))
			u := d.Select("user", "*", "id", rep.S("id_user"))
			d.Update("user", "Balance", php.Intval(u.S("Balance"))+php.Intval(rep.S("price")), "id", rep.S("id_user"))
			d.Update("Payment_report", "payment_Status", "paid", "id_order", rep.S("id_order"))
			c.sendHTML(c.fromID, T("users.Balance.Confirmpay"), nil)
			c.report(sprintf("users.Report.reportpayiranpay", c.fromID, nfs(rep.S("price"))))
		case "expired":
			c.alert(T("users.Balance.expired"))
		case "refunded":
			c.alert(T("users.Balance.refunded"))
		case "waiting":
			c.alert(T("users.Balance.waiting"))
		case "sending":
			c.alert(T("users.Balance.sending"))
		default:
			c.alert(T("users.Balance.Failed"))
		}
	} else if c.stepIs("cart_to_cart_user") {
		if !c.photo {
			c.sendHTML(c.fromID, T("users.Balance.Invalid-receipt"), nil)
			return true
		}
		order := randHex(5)
		c.insertPayment(order, "cart to cart", "waiting")
		// kept so the receipt can be shown in Nexra Panel's payment review
		c.db().SetKV("receipt_"+order, c.photoID)
		if open := c.b.AutopayOpenOrder(c.fromID); open != nil && open.I("amount") == php.Intval(c.user.S("Processing_value")) {
			c.b.AutopayAttach(open.S("id"), order)
		}
		if c.user.S("Processing_value_tow") == "getconfigafterpay" {
			c.sendHTML(c.fromID, T("users.Balance.Send-receip-buy"), c.kbMain())
		} else {
			c.sendHTML(c.fromID, T("users.Balance.Send-receipt"), c.kbMain())
		}
		k := ik(row(cb(T("users.Balance.Confirmpaying"), "Confirm_pay_"+order), cb(T("users.Balance.reject_pay"), "reject_pay_"+order)))
		cap := sprintf("users.moeny.cartresid", c.fromID, order, c.username, nfs(c.user.S("Processing_value")), c.caption)
		for _, a := range c.adminIDs {
			c.b.TG.SendPhotoID(a, c.photoID, cap, k, "HTML")
		}
		c.step("home")
	}
	return false
}

func (c *Ctx) secDiscount() bool {
	d := c.db()
	if c.datain == "Discount" {
		c.sendHTML(c.fromID, T("users.Discount.getcode"), c.kbBackUser())
		c.step("get_code_user")
	} else if c.stepIs("get_code_user") {
		if !c.inColumn("Discount", "code", c.text) {
			c.sendHTML(c.fromID, T("users.Discount.notcode"), nil)
			return true
		}
		for _, r := range d.MustQuery("SELECT code FROM Giftcodeconsumed WHERE id_user = ?", c.fromID) {
			if php.LooseEq(r.S("code"), c.text) {
				c.sendHTML(c.fromID, T("users.Discount.onecode"), c.kbMain())
				c.step("home")
				return true
			}
		}
		code := d.One("SELECT * FROM Discount WHERE code = ? LIMIT 1", c.text)
		d.Update("user", "Balance", php.NumStr(c.user.F("Balance")+code.F("price")), "id", c.fromID)
		c.step("home")
		c.sendHTML(c.fromID, sprintf("users.Discount.acceptdiscount", code.S("price")), c.kbMain())
		d.Exec("INSERT INTO Giftcodeconsumed (id_user, code) VALUES (?, ?)", c.fromID, c.text)
		c.report(sprintf("users.Report.discountuser", c.text, c.fromID, c.username, code.S("price")))
	}
	return false
}

func (c *Ctx) secMisc() bool {
	d := c.db()
	if c.text == c.texts["text_Tariff_list"] {
		c.sendHTML(c.fromID, c.texts["text_dec_Tariff_list"], nil)
	}
	if c.datain == "closelist" {
		c.del()
		c.sendHTML(c.fromID, T("users.back"), c.kbMain())
	}
	if c.text == T("users.affiliates.btn") {
		aff := d.Select("affiliates", "*", "", nil)
		if aff.S("affiliatesstatus") == "offaffiliates" {
			c.sendHTML(c.fromID, T("users.affiliates.offaffiliates"), c.kbMain())
			return true
		}
		link := "https://t.me/" + c.b.Cfg.BotUsername + "?start=" + c.user.S("ref_code")
		share := ik(row(tg.URLBtn(T("users.affiliates.share"), link)))
		desc := aff.S("description")
		msg := "🔗 " + link
		if !aff.IsNull("description") && desc != "" && desc != "none" {
			msg = desc + "\n\n🔗 " + link
		}
		if media := aff.S("id_media"); media != "" && media != "0" && media != "none" {
			c.b.TG.SendPhotoID(c.fromID, media, msg, share, "HTML")
		} else {
			c.sendHTML(c.fromID, msg, share)
		}
		pct := T("users.status.disabled")
		if aff.S("status_commission") == "oncommission" {
			pct = aff.S("affiliatespercentage") + T("users.Percentage")
		}
		gift := T("users.status.disabled")
		if aff.S("Discount") == "onDiscountaffiliates" {
			gift = aff.S("price_Discount") + T("users.IRT")
		}
		c.sendHTML(c.fromID, sprintf("users.affiliates.infotext", gift, pct), c.kbMain())
	}
	return false
}
