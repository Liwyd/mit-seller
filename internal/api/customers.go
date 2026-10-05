package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Liwyd/mit-seller/internal/bot"
	"github.com/Liwyd/mit-seller/internal/php"
)

const userCols = "id, username, number, Balance, User_Status, description_blocking, limit_usertest, verify, affiliates, affiliatescount, last_message_time, step"

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	limit, offset := pageArgs(r)
	var conds []string
	var args []any
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		conds = append(conds, "(id = ? OR username LIKE ? OR number LIKE ?)")
		args = append(args, q, "%"+strings.TrimPrefix(q, "@")+"%", "%"+q+"%")
	}
	if st := r.URL.Query().Get("status"); st == "block" || st == "Active" {
		conds = append(conds, "User_Status = ?")
		args = append(args, st)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	total := d.Count("SELECT COUNT(*) FROM user"+where, args...)
	rows := d.MustQuery("SELECT "+userCols+" FROM user"+where+" ORDER BY CAST(last_message_time AS UNSIGNED) DESC LIMIT "+strconv.Itoa(limit)+" OFFSET "+strconv.Itoa(offset), args...)
	ok(w, map[string]any{"total": total, "items": rowsMap(rows)})
}

func (a *API) getUser(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	id := r.PathValue("id")
	u := d.One("SELECT "+userCols+" FROM user WHERE id = ?", id)
	if u == nil {
		fail(w, 404, "user not found")
		return
	}
	ok(w, map[string]any{
		"user":     rowMap(u),
		"services": rowsMap(d.MustQuery("SELECT id_invoice, username, Service_location, name_product, price_product, Volume, Service_time, time_sell, Status FROM invoice WHERE id_user = ? ORDER BY CAST(time_sell AS UNSIGNED) DESC LIMIT 200", id)),
		"payments": rowsMap(d.MustQuery("SELECT id_order, time, price, Payment_Method, payment_Status, dec_not_confirmed FROM Payment_report WHERE id_user = ? ORDER BY id DESC LIMIT 100", id)),
		"paid_sum": php.Floatval(d.Scalar("SELECT SUM(price) FROM Payment_report WHERE payment_Status = 'paid' AND id_user = ?", id)),
	})
}

func (a *API) userBalance(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	d := a.B.DB
	id := r.PathValue("id")
	u := d.Select("user", "*", "id", id)
	if u == nil {
		fail(w, 404, "user not found")
		return
	}
	amount := f.str("amount")
	if !isDigits(amount) || php.Intval(amount) > 100000000 {
		fail(w, 400, "amount must be a whole number up to 100,000,000")
		return
	}
	mode := f.str("mode")
	n := php.Intval(amount)
	switch mode {
	case "add":
		d.Exec("UPDATE user SET Balance = Balance + ? WHERE id = ?", n, id)
		if f.boolean("notify") {
			a.B.TG.SendMessage(id, php.Sprintf(bot.T("Admin.Balance.AddedBalance"), php.NumberFormat(float64(n), 0)), nil, "HTML")
		}
	case "sub":
		d.Exec("UPDATE user SET Balance = Balance - ? WHERE id = ?", n, id)
		if f.boolean("notify") {
			a.B.TG.SendMessage(id, php.Sprintf(bot.T("Admin.Balance.ReduceBalance"), php.NumberFormat(float64(n), 0)), nil, "HTML")
		}
	case "set":
		d.Exec("UPDATE user SET Balance = ? WHERE id = ?", n, id)
	default:
		fail(w, 400, "mode must be add, sub or set")
		return
	}
	ok(w, map[string]any{"balance": d.Select("user", "Balance", "id", id).S("Balance")})
}

func (a *API) userBlock(w http.ResponseWriter, r *http.Request) {
	var f fields
	_ = decode(r, &f)
	id := r.PathValue("id")
	if !a.B.DB.Exists("user", "id", id) {
		fail(w, 404, "user not found")
		return
	}
	if a.B.DB.IsAdmin(id) {
		fail(w, 400, "an admin cannot be blocked")
		return
	}
	a.B.DB.Update("user", "User_Status", "block", "id", id)
	a.B.DB.Update("user", "description_blocking", f.str("reason"), "id", id)
	ok(w, nil)
}

func (a *API) userUnblock(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.B.DB.Update("user", "User_Status", "Active", "id", id)
	a.B.DB.Update("user", "description_blocking", "", "id", id)
	a.B.DB.Update("user", "message_count", "0", "id", id)
	ok(w, nil)
}

func (a *API) userVerify(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	v := "0"
	if f.boolean("verified") {
		v = "1"
	}
	a.B.DB.Update("user", "verify", v, "id", r.PathValue("id"))
	ok(w, nil)
}

func (a *API) userTestLimit(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	v := f.str("limit")
	if !isDigits(v) {
		fail(w, 400, "limit must be a number")
		return
	}
	a.B.DB.Update("user", "limit_usertest", v, "id", r.PathValue("id"))
	ok(w, nil)
}

func (a *API) userMessage(w http.ResponseWriter, r *http.Request) {
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
	res := a.B.TG.SendMessage(r.PathValue("id"), php.Sprintf(bot.T("Admin.systemsms.sendedmessagetouser"), txt), nil, "HTML")
	if !res.OK {
		fail(w, 502, "telegram refused: "+res.Description)
		return
	}
	ok(w, nil)
}

func (a *API) listServices(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	limit, offset := pageArgs(r)
	var conds []string
	var args []any
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		conds = append(conds, "(username LIKE ? OR id_user = ? OR id_invoice = ?)")
		args = append(args, "%"+q+"%", q, q)
	}
	if st := r.URL.Query().Get("status"); st != "" {
		conds = append(conds, "Status = ?")
		args = append(args, st)
	}
	if loc := r.URL.Query().Get("panel"); loc != "" {
		conds = append(conds, "Service_location = ?")
		args = append(args, loc)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	total := d.Count("SELECT COUNT(*) FROM invoice"+where, args...)
	rows := d.MustQuery("SELECT id_invoice, id_user, username, Service_location, name_product, price_product, Volume, Service_time, time_sell, Status FROM invoice"+where+
		" ORDER BY CAST(time_sell AS UNSIGNED) DESC LIMIT "+strconv.Itoa(limit)+" OFFSET "+strconv.Itoa(offset), args...)
	ok(w, map[string]any{"total": total, "items": rowsMap(rows)})
}

func (a *API) getService(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	inv := d.Select("invoice", "*", "username", r.PathValue("username"))
	if inv == nil {
		fail(w, 404, "service not found")
		return
	}
	live := a.B.PM.DataUser(inv.S("Service_location"), inv.S("username"))
	delete(live, "links")
	ok(w, map[string]any{"invoice": rowMap(inv, "id_invoice", "id_user", "username", "Service_location", "name_product", "price_product", "Volume", "Service_time", "time_sell", "Status"), "live": live})
}

func (a *API) deleteService(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	name := r.PathValue("username")
	inv := d.Select("invoice", "*", "username", name)
	if inv == nil {
		fail(w, 404, "service not found")
		return
	}
	var panelResult any
	if a.B.PM.DataUser(inv.S("Service_location"), name).Isset("status") {
		panelResult = a.B.PM.RemoveUser(inv.S("Service_location"), name)
	}
	if r.URL.Query().Get("keep_record") == "1" {
		d.Update("invoice", "Status", "removedbyadmin", "username", name)
	} else {
		d.Exec("DELETE FROM invoice WHERE username = ?", name)
	}
	ok(w, map[string]any{"panel": panelResult})
}
