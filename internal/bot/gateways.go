package bot

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Liwyd/mit-seller/internal/php"
)

// NowPaymentsIPN is payment/nowpayments/back.php.
func (b *Bot) NowPaymentsIPN(paymentID string) {
	pay := b.paymentStatus(paymentID)
	if pay.s("payment_status") != "finished" {
		return
	}
	d := b.DB
	rep := d.Select("Payment_report", "*", "id_order", pay.s("order_id"))
	if rep == nil || rep.S("payment_Status") == "paid" {
		return
	}
	b.lockOrder(rep.S("id_order"), func() {
		rep = d.Select("Payment_report", "*", "id_order", pay.s("order_id"))
		if rep.S("payment_Status") == "paid" {
			return
		}
		b.DirectPayment(rep.S("id_order"), nil)
		uid := rep.S("id_user")
		d.Update("user", "Processing_value", "0", "id", uid)
		d.Update("user", "Processing_value_one", "0", "id", uid)
		d.Update("user", "Processing_value_tow", "0", "id", uid)
		d.Update("Payment_report", "payment_Status", "paid", "id_order", rep.S("id_order"))
		if ch := d.Setting().S("Channel_Report"); ch != "" {
			b.TG.SendMessage(ch, sprintf("Admin.Report.nowpayment", uid, rep.S("price")), nil, "HTML")
		}
	})
}

func postJSON(u string, v any) jmap {
	body, _ := json.Marshal(v)
	cl := &http.Client{Timeout: 20 * time.Second}
	res, err := cl.Post(u, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var m map[string]any
	if d.Decode(&m) != nil {
		return nil
	}
	return m
}

var aqaCreateErrors = map[string]string{
	"-1": "amount نمی تواند خالی باشد", "-2": "کد پین درگاه نمی تواند خالی باشد", "-3": "callback نمی تواند خالی باشد",
	"-4": "amount باید عددی باشد", "-5": "amount باید بین 1,000 تا 100,000,000 تومان باشد", "-6": "کد پین درگاه اشتباه هست",
	"-7": "transid نمی تواند خالی باشد", "-8": "تراکنش مورد نظر وجود ندارد", "-9": "کد پین درگاه با درگاه تراکنش مطابقت ندارد",
	"-10": "مبلغ با مبلغ تراکنش مطابقت ندارد", "-11": "درگاه درانتظار تایید و یا غیر فعال است",
	"-12": "امکان ارسال درخواست برای این پذیرنده وجود ندارد", "-13": "شماره کارت باید 16 رقم چسبیده بهم باشد",
	"-14": "درگاه برروی سایت دیگری درحال استفاده است",
}

// AqayepardakhtCreate is payment/aqayepardakht/aqayepardakht.php. It returns
// the redirect location, or a message to show.
func (b *Bot) AqayepardakhtCreate(amount, order string) (string, string) {
	price := b.DB.Select("Payment_report", "price", "id_order", order).S("price")
	if !php.LooseEq(price, amount) {
		return "", T("users.moeny.invalidprice")
	}
	res := postJSON("https://panel.aqayepardakht.ir/api/v2/create", map[string]any{
		"pin": b.DB.PaySetting("merchant_id_aqayepardakht"), "amount": amount,
		"callback": b.Cfg.Domain + "/payment/aqayepardakht/back.php", "invoice_id": order,
	})
	if res.s("status") == "success" {
		return "https://panel.aqayepardakht.ir/startpay/" + res.s("transid"), ""
	}
	return "", aqaCreateErrors[res.s("code")]
}

// AqayepardakhtVerify is payment/aqayepardakht/back.php; returns the page's
// status line, description and amount.
func (b *Bot) AqayepardakhtVerify(order, transid string) (string, string, string) {
	d := b.DB
	price := d.Select("Payment_report", "price", "id_order", order).S("price")
	res := postJSON("https://panel.aqayepardakht.ir/api/v2/verify", map[string]any{
		"pin": d.PaySetting("merchant_id_aqayepardakht"), "amount": price, "transid": transid,
	})
	if res.s("code") == "1" {
		rep := d.Select("Payment_report", "*", "id_order", order)
		if rep != nil && rep.S("payment_Status") != "paid" {
			b.lockOrder(order, func() {
				rep = d.Select("Payment_report", "*", "id_order", order)
				if rep.S("payment_Status") == "paid" {
					return
				}
				b.DirectPayment(order, nil)
				uid := rep.S("id_user")
				d.Update("user", "Processing_value", "0", "id", uid)
				d.Update("user", "Processing_value_one", "0", "id", uid)
				d.Update("user", "Processing_value_tow", "0", "id", uid)
				d.Update("Payment_report", "payment_Status", "paid", "id_order", order)
				if ch := d.Setting().S("Channel_Report"); ch != "" {
					b.TG.SendMessage(ch, sprintf("Admin.Report.aqayepardakht", uid, price), nil, "HTML")
				}
			})
		}
		return T("users.moeny.payment_success"), T("users.moeny.payment_success_dec"), price
	}
	status := map[string]string{"0": "پرداخت انجام نشد", "2": "تراکنش قبلا وریفای و پرداخت شده است"}[res.s("code")]
	return status, "", price
}

// lockOrder serialises work on one payment so two callbacks (or a callback
// and an admin click) can never credit it twice.
func (b *Bot) lockOrder(order string, fn func()) {
	l := b.userLock(-int64(hashString(order)))
	l.Lock()
	defer l.Unlock()
	fn()
}

func hashString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h | 1
}
