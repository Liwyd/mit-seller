package bot

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/php"
	"github.com/Liwyd/mit-seller/internal/tg"
)

// jmap is a decoded JSON object with PHP-ish accessors.
type jmap map[string]any

func (m jmap) isset(k string) bool { v, ok := m[k]; return ok && v != nil }
func (m jmap) s(k string) string   { return panels.Out(m).S(k) }
func (m jmap) json() string {
	b, _ := json.Marshal(map[string]any(m))
	if m == nil {
		return "null"
	}
	return string(b)
}

var httpc = &http.Client{Timeout: 20 * time.Second}

func getJSON(url string, headers map[string]string) jmap {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := httpc.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var m map[string]any
	if d.Decode(&m) != nil {
		return nil
	}
	return m
}

type rates struct{ USD, TRX int64 }

// tronRate is tronratee(): toman price of USDT (Wallex) and TRX (DIA).
func tronRate() rates {
	var r rates
	tron := getJSON("https://api.diadata.org/v1/assetQuotation/Tron/0x0000000000000000000000000000000000000000", nil)
	usd := getJSON("https://api.wallex.ir/v1/markets", nil)
	if res, ok := usd["result"].(map[string]any); ok {
		if sym, ok := res["symbols"].(map[string]any); ok {
			if u, ok := sym["USDTTMN"].(map[string]any); ok {
				if st, ok := u["stats"].(map[string]any); ok {
					r.USD = php.Intval(panels.Out(st).S("lastPrice"))
				}
			}
		}
	}
	price := php.Floatval(tron.s("Price"))
	r.TRX = php.FloatToInt(price * float64(r.USD))
	return r
}

// nowPayments is functions.php nowPayments().
func (b *Bot) nowPayments(endpoint string, amount float64, orderID, desc string) jmap {
	body, _ := json.Marshal(map[string]any{
		"price_amount": amount, "price_currency": "usd", "pay_currency": "trx", "order_id": orderID,
		"order_description": desc, "ipn_callback_url": "https://" + b.Cfg.Domain + "/payment/nowpayments/back.php",
	})
	req, _ := http.NewRequest("POST", "https://api.nowpayments.io/v1/"+endpoint, bytes.NewReader(body))
	req.Header.Set("x-api-key", b.DB.PaySetting("apinowpayment"))
	req.Header.Set("Content-Type", "application/json")
	cl := &http.Client{Timeout: 8 * time.Second}
	res, err := cl.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&m) != nil {
		return nil
	}
	return m
}

// paymentStatus is StatusPayment().
func (b *Bot) paymentStatus(id string) jmap {
	return getJSON("https://api.nowpayments.io/v1/payment/"+id, map[string]string{"x-api-key": b.DB.PaySetting("apinowpayment")})
}

// DirectPayment is functions.php DirectPayment(): credits a confirmed
// payment, or — when the payment was for a pending purchase — creates and
// delivers that service. ctx may be nil (cron, autopay, payment callbacks).
//
// The bool is whether the money or the service actually moved. It is the
// only thing callers may mark a payment paid on: reporting success when the
// panel refused to create the service is what leaves a customer credited
// nothing while the admin is told the payment went through.
func (b *Bot) DirectPayment(orderID string, ctx *Ctx) (applied bool) {
	c := ctx
	if c == nil {
		c = b.systemCtx()
	}
	d := b.DB
	setting := d.Setting()
	c.setting = setting
	rep := d.Select("Payment_report", "*", "id_order", orderID)
	buyer := d.Select("user", "*", "id", rep.S("id_user"))
	parts := strings.Split(rep.S("invoice"), "|")
	if parts[0] == "getconfigafterpay" && len(parts) > 1 {
		inv := d.One("SELECT * FROM invoice WHERE username = ? AND Status = 'unpaid' LIMIT 1", parts[1])
		userAc := inv.S("username")
		panel := d.Select("marzban_panel", "*", "name_panel", inv.S("Service_location"))
		var expire int64
		if php.Intval(inv.S("Service_time")) != 0 {
			expire = php.PlusDaysUnix(inv.S("Service_time"))
		}
		out := b.PM.CreateUser(panel.S("name_panel"), userAc, expire, php.Floatval(inv.S("Volume"))*math.Pow(1024, 3), false)
		if !out.Isset("username") {
			c.sendHTML(buyer.S("id"), T("users.sell.ErrorConfig"), c.kbMainFor(buyer.S("id")))
			msg := sprintf("users.buy.errorInCreate", out.MsgJSON(), buyer.S("id"), buyer.S("username"))
			for _, a := range d.AdminIDs() {
				c.sendHTML(a, msg, nil)
				d.Step("home", a)
			}
			return false
		}
		c.deliverAfterPayment(inv.S("id_user"), panel, out, userAc, inv.S("name_product"), inv.S("Service_time"), inv.S("Volume"), inv.S("id_invoice"))

		price := inv.F("price_product")
		discounted := false
		var pricediscount float64
		dis := strings.Split(buyer.S("Processing_value_four"), "_")
		if dis[0] == "dis" && len(dis) > 1 {
			sd := d.Select("DiscountSell", "*", "codeDiscount", dis[1])
			d.Update("DiscountSell", "usedDiscount", php.Intval(sd.S("usedDiscount"))+1, "codeDiscount", dis[1])
			d.Exec("INSERT INTO Giftcodeconsumed (id_user,code) VALUES (?,?)", buyer.S("id"), dis[1])
			pricediscount = price - sd.F("price")/100*price
			discounted = true
			if ch := setting.S("Channel_Report"); ch != "" {
				b.TG.Call("sendMessage", map[string]any{"chat_id": ch, "text": sprintf("users.Report.discountused", buyer.S("username"), buyer.S("id"), dis[1])})
			}
			// consumed: do not apply it again to a later purchase
			d.Update("user", "Processing_value_four", "0", "id", buyer.S("id"))
		}
		base := price
		if discounted {
			base = pricediscount
		}
		c.payCommission(buyer, base)
		// one statement, so two concurrent approvals cannot lose an update
		d.Exec("UPDATE user SET Balance = GREATEST(Balance - ?, 0) WHERE id = ?", price, buyer.S("id"))
		now := d.Select("user", "Balance", "id", inv.S("id_user")).S("Balance")
		if ch := setting.S("Channel_Report"); ch != "" {
			b.TG.Call("sendMessage", map[string]any{"chat_id": ch, "parse_mode": "HTML",
				"text": sprintf("users.Report.reportbuyafterpay", inv.S("username"), inv.S("price_product"), inv.S("Volume"), inv.S("id_user"), buyer.S("number"), inv.S("Service_location"), nfs(now), buyer.S("username"))})
		}
		d.Update("invoice", "status", "active", "username", inv.S("username"))
		if rep.S("Payment_Method") == "cart to cart" {
			d.Update("invoice", "Status", "active", "id_invoice", inv.S("id_invoice"))
		}
		return true
	}
	// one statement, so two concurrent approvals cannot lose an update
	d.Exec("UPDATE user SET Balance = Balance + ? WHERE id = ?", php.Intval(rep.S("price")), rep.S("id_user"))
	d.Update("Payment_report", "payment_Status", "paid", "id_order", rep.S("id_order"))
	if rep.S("Payment_Method") == "cart to cart" && c.callbackQueryID != "" {
		b.TG.AnswerCallback(c.callbackQueryID, T("users.moeny.acceptedcart"), true)
	}
	if r := c.sendHTML(rep.S("id_user"), sprintf("users.moeny.Charged.", php.NumberFormat(rep.F("price"), 0), rep.S("id_order")), nil); !r.OK {
		b.Log.Printf("payment %s: charged %s but the confirmation failed for chat %s: %s", orderID, rep.S("price"), rep.S("id_user"), r.Description)
	}
	return true
}

// TellDeliverFailed reports a payment whose money arrived but whose service
// or credit did not move, so it stays visible instead of being quietly marked
// paid while the admin is told it went through.
func (b *Bot) TellDeliverFailed(idUser, order string) {
	msg := "❌ <b>پول رسید ولی تحویل انجام نشد</b>\n\n" +
		"کاربر: <code>" + idUser + "</code>\n" +
		"کد پیگیری: <code>" + order + "</code>\n\n" +
		"این پرداخت همچنان در انتظار است؛ از «🧾 رسیدهای تایید نشده» دوباره تأییدش کن."
	for _, a := range b.DB.AdminIDs() {
		if r := b.TG.SendMessage(a, msg, nil, "HTML"); !r.OK {
			b.Log.Printf("deliver-failed notice for payment %s: could not reach chat %s: %s", order, a, r.Description)
		}
	}
	if b.DB.Select("user", "id", "id", idUser).Has() {
		b.TG.SendMessage(idUser, sprintf("users.moeny.deliver_failed", order), nil, "HTML")
	}
}

// systemCtx is a Ctx for work that does not come from a Telegram update.
func (b *Bot) systemCtx() *Ctx {
	c := b.newCtx(tg.Fields{})
	c.fromID = "0"
	c.setting = b.DB.Setting()
	c.adminIDs = b.DB.AdminIDs()
	c.user = db.Row{}
	c.loadTexts()
	return c
}
