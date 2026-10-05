package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/MHBehzadian/nexra-mirzabot/internal/bot"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

func (a *API) listPayments(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	limit, offset := pageArgs(r)
	var conds []string
	var args []any
	if st := r.URL.Query().Get("status"); st != "" {
		conds = append(conds, "payment_Status = ?")
		args = append(args, st)
	}
	if m := r.URL.Query().Get("method"); m != "" {
		conds = append(conds, "Payment_Method = ?")
		args = append(args, m)
	}
	if u := r.URL.Query().Get("user"); u != "" {
		conds = append(conds, "id_user = ?")
		args = append(args, u)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	total := d.Count("SELECT COUNT(*) FROM Payment_report"+where, args...)
	rows := d.MustQuery("SELECT id, id_user, id_order, time, price, dec_not_confirmed, Payment_Method, payment_Status, invoice FROM Payment_report"+where+
		" ORDER BY id DESC LIMIT "+strconv.Itoa(limit)+" OFFSET "+strconv.Itoa(offset), args...)
	items := rowsMap(rows)
	for _, it := range items {
		order, _ := it["id_order"].(string)
		_, has := d.KVOk("receipt_" + order)
		it["has_receipt"] = has
		inv, _ := it["invoice"].(string)
		it["for_purchase"] = strings.HasPrefix(inv, "getconfigafterpay|")
	}
	ok(w, map[string]any{"total": total, "items": items})
}

func (a *API) approvePayment(w http.ResponseWriter, r *http.Request) {
	order := r.PathValue("order")
	if !a.B.ApprovePayment(order, nil, "nexra-panel", nil) {
		fail(w, 409, "this payment was already reviewed or does not exist")
		return
	}
	ok(w, nil)
}

func (a *API) rejectPayment(w http.ResponseWriter, r *http.Request) {
	var f fields
	_ = decode(r, &f)
	reason := f.str("reason")
	if reason == "" {
		reason = "-"
	}
	if !a.B.RejectPayment(r.PathValue("order"), reason) {
		fail(w, 409, "this payment was already reviewed or does not exist")
		return
	}
	ok(w, nil)
}

func (a *API) receipt(w http.ResponseWriter, r *http.Request) {
	fileID, has := a.B.DB.KVOk("receipt_" + r.PathValue("order"))
	if !has || fileID == "" {
		fail(w, 404, "no receipt stored for this payment")
		return
	}
	data, ctype, err := a.B.TG.DownloadFile(fileID)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	if ctype == "" || ctype == "application/octet-stream" {
		ctype = "image/jpeg"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(data)
}

var paySettingKeys = map[string]string{
	"card_text":         "CartDescription",
	"nowpayments_key":   "apinowpayment",
	"aqayepardakht_pin": "merchant_id_aqayepardakht",
}

var payToggles = map[string]struct{ key, on, off string }{
	"card":          {"Cartstatus", "oncard", "offcard"},
	"nowpayments":   {"nowpaymentstatus", "onnowpayment", "offnowpayment"},
	"rial_gateway":  {"digistatus", "ondigi", "offdigi"},
	"aqayepardakht": {"statusaqayepardakht", "onaqayepardakht", "offaqayepardakht"},
}

func (a *API) getPaySettings(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	out := map[string]any{}
	for k, col := range paySettingKeys {
		out[k] = d.PaySetting(col)
	}
	en := map[string]bool{}
	for k, t := range payToggles {
		en[k] = d.PaySetting(t.key) == t.on
	}
	out["enabled"] = en
	ok(w, out)
}

func (a *API) putPaySettings(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	d := a.B.DB
	for k, col := range paySettingKeys {
		if f.has(k) {
			d.Exec("INSERT INTO PaySetting (NamePay, ValuePay) VALUES (?, ?) ON DUPLICATE KEY UPDATE ValuePay = VALUES(ValuePay)", col, f.str(k))
		}
	}
	if en, okE := f["enabled"].(map[string]any); okE {
		for k, t := range payToggles {
			if _, has := en[k]; has {
				v := t.off
				if fields(en).boolean(k) {
					v = t.on
				}
				d.Exec("INSERT INTO PaySetting (NamePay, ValuePay) VALUES (?, ?) ON DUPLICATE KEY UPDATE ValuePay = VALUES(ValuePay)", t.key, v)
			}
		}
	}
	a.getPaySettings(w, r)
}

func (a *API) listCancelRequests(w http.ResponseWriter, r *http.Request) {
	ok(w, rowsMap(a.B.DB.MustQuery("SELECT id, id_user, username, description, status FROM cancel_service ORDER BY id DESC LIMIT 200")))
}

func (a *API) getAutopay(w http.ResponseWriter, r *http.Request) {
	b := a.B
	d := b.DB
	s := b.AutopaySettings()
	mode := "off"
	switch {
	case s.S("status") == "on":
		mode = "sms"
	case b.CronOn("card"):
		mode = "no_review"
	}
	ok(w, map[string]any{
		"mode":        mode,
		"enabled":     s.S("status") == "on",
		"last_seen":   s.S("last_seen"),
		"device":      s.S("device_info"),
		"pairing":     b.AutopayPairing(),
		"endpoint":    "https://" + b.Cfg.Domain + "/autopay.php",
		"open_orders": rowsMap(d.MustQuery("SELECT id, id_user, base_price, amount, status, id_order, created_at FROM autopay_order WHERE status = 'open' ORDER BY id DESC LIMIT 20")),
		"recent_sms":  rowsMap(d.MustQuery("SELECT id, sender, amount, direction, card, sent_at, received_at, status, id_order FROM autopay_sms ORDER BY id DESC LIMIT 20")),
		"paid_count":  d.Count("SELECT COUNT(*) FROM autopay_order WHERE status = 'paid'"),
		"open_count":  d.Count("SELECT COUNT(*) FROM autopay_order WHERE status = 'open'"),
		"sms_count":   d.Count("SELECT COUNT(*) FROM autopay_sms"),
	})
}

func (a *API) putAutopay(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	// mode picks one of the two kinds of automatic confirmation (they would
	// fight over the same receipts): "off", "no_review" (every receipt a
	// minute later, unchecked) or "sms" (the phone app matches bank SMS)
	if f.has("mode") {
		switch f.str("mode") {
		case "off":
			a.B.AutopaySet("status", "off")
			a.B.SetCron("card", false)
		case "no_review":
			a.B.AutopaySet("status", "off")
			a.B.SetCron("card", true)
		case "sms":
			a.B.SetCron("card", false)
			a.B.AutopaySet("status", "on")
		default:
			fail(w, 400, "mode must be off, no_review or sms")
			return
		}
	} else if f.has("enabled") {
		if f.boolean("enabled") {
			a.B.AutopaySet("status", "on")
		} else {
			a.B.AutopaySet("status", "off")
		}
	}
	a.getAutopay(w, r)
}

func (a *API) autopayNewKey(w http.ResponseWriter, r *http.Request) {
	a.B.AutopaySet("device_key", randHex(24))
	a.B.AutopaySet("last_seen", nil)
	a.getAutopay(w, r)
}

func (a *API) getAffiliates(w http.ResponseWriter, r *http.Request) {
	af := a.B.DB.Select("affiliates", "*", "", nil)
	ok(w, map[string]any{
		"enabled":           af.S("affiliatesstatus") == "onaffiliates",
		"commission":        af.S("status_commission") == "oncommission",
		"percent":           af.S("affiliatespercentage"),
		"start_gift":        af.S("Discount") == "onDiscountaffiliates",
		"start_gift_amount": af.S("price_Discount"),
		"description":       af.S("description"),
		"has_banner":        af.S("id_media") != "" && af.S("id_media") != "none",
	})
}

func (a *API) putAffiliates(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	d := a.B.DB
	flag := func(k, col, on, off string) {
		if f.has(k) {
			v := off
			if f.boolean(k) {
				v = on
			}
			d.Update("affiliates", col, v, "", nil)
		}
	}
	flag("enabled", "affiliatesstatus", "onaffiliates", "offaffiliates")
	flag("commission", "status_commission", "oncommission", "offcommission")
	flag("start_gift", "Discount", "onDiscountaffiliates", "offDiscountaffiliates")
	if f.has("percent") {
		v := f.str("percent")
		if !isDigits(v) || php.Intval(v) > 100 {
			fail(w, 400, "percent must be 0-100")
			return
		}
		d.Update("affiliates", "affiliatespercentage", v, "", nil)
	}
	if f.has("start_gift_amount") {
		v := f.str("start_gift_amount")
		if !isDigits(v) {
			fail(w, 400, "start_gift_amount must be a number")
			return
		}
		d.Update("affiliates", "price_Discount", v, "", nil)
	}
	if f.has("description") {
		d.Update("affiliates", "description", f.str("description"), "", nil)
	}
	if f.boolean("remove_banner") {
		d.Update("affiliates", "id_media", "none", "", nil)
	}
	a.getAffiliates(w, r)
}

// ---------------------------------------------------------------- messaging

func (a *API) broadcastStatus(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"pending": a.B.BroadcastPending(), "running": a.B.DB.KV("broadcast_info") != ""})
}

func (a *API) broadcast(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	txt := f.str("text")
	if txt == "" {
		fail(w, 400, "text is required")
		return
	}
	if a.B.DB.KV("broadcast_info") != "" {
		fail(w, 409, "a broadcast is already running")
		return
	}
	n := a.B.StartBroadcast(jsonOf(map[string]any{"text": txt, "id_admin": a.B.Cfg.AdminID}))
	ok(w, map[string]any{"recipients": n})
}

func (a *API) cancelBroadcast(w http.ResponseWriter, r *http.Request) {
	a.B.CancelBroadcast()
	ok(w, nil)
}

func (a *API) listAdmins(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"admins": a.B.DB.AdminIDs(), "main": a.B.Cfg.AdminID})
}

func (a *API) addAdmin(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	id := f.str("id")
	if !isDigits(id) {
		fail(w, 400, "id must be a numeric Telegram id")
		return
	}
	a.B.DB.Exec("INSERT IGNORE INTO admin (id_admin) VALUES (?)", id)
	a.listAdmins(w, r)
}

func (a *API) removeAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if php.Intval(id) == php.Intval(a.B.Cfg.AdminID) {
		fail(w, 400, "the main admin cannot be removed")
		return
	}
	a.B.DB.Exec("DELETE FROM admin WHERE id_admin = ?", id)
	a.listAdmins(w, r)
}

var _ = bot.T
