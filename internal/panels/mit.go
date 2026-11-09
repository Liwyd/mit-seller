package panels

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Liwyd/mit-seller/internal/db"
)

// A Mit-type panel row talks to two servers: Mit Panel itself
// (url_panel/username_panel/password_panel) for every write, so the admin's
// traffic quota is enforced, and the real Marzban behind it
// (marzban_*_direct) for read-only lookups.

func mitBase(p db.Row) string { return trimSlash(p.S("url_panel")) }

// mitLoginPost is one attempt at Mit Panel's /login endpoint. A login has no
// side effects, which is why a caller may run it again when the first one
// never came back.
func mitLoginPost(base, user, pass string) resp {
	return req{method: "POST", url: base + "/login", timeout: 8 * time.Second,
		insecure: true, body: formBody("username", user, "password", pass),
		contentType: "application/x-www-form-urlencoded", headers: map[string]string{"accept": "application/json"}}.do()
}

// MitLogin is mit_login(): {"access_token":..} or {"error":..}.
func (m *Manager) MitLogin(panel db.Row) map[string]any {
	panel = m.panelByID(panel.S("id"))
	cache := loginCache(panel)
	adoptLoginCache(cache)
	if e, ok := cache["mit"].(map[string]any); ok && isset(e, "access_token") {
		if tok, ok := fresh(e, 600); ok {
			return map[string]any{"access_token": tok, "time": e["time"]}
		}
	}
	r := mitLoginPost(mitBase(panel), panel.S("username_panel"), panel.S("password_panel"))
	if r.err != nil {
		// this panel is sometimes seconds behind while its own upstream
		// recovers, and a customer's payment may be waiting on the answer. A
		// login has no side effects, so one more attempt is the cheap way
		// through such a stall instead of a failed delivery.
		r = mitLoginPost(mitBase(panel), panel.S("username_panel"), panel.S("password_panel"))
	}
	if r.err != nil {
		return map[string]any{"error": r.err.Error()}
	}
	body := jsonObj(r.body)
	if d, ok := body["data"].(map[string]any); ok {
		if tok, ok := d["access_token"].(string); ok {
			e := map[string]any{"time": nowStamp(), "access_token": tok}
			cache["mit"] = e
			delete(cache, "nexra") // pre-rebrand key, superseded
			m.DB.Update("marzban_panel", "datelogin", string(marshal(cache)), "name_panel", panel.S("name_panel"))
			return e
		}
	}
	return map[string]any{"error": MitErrorMessage(body, "Mit login failed")}
}

// MitErrorMessage extracts a readable message from a Mit/FastAPI reply.
func MitErrorMessage(body map[string]any, fallback string) string {
	msg := fallback
	if s, ok := body["message"].(string); ok && s != "" {
		msg = s
	} else if s, ok := body["detail"].(string); ok && s != "" {
		msg = s
	} else if arr, ok := body["detail"].([]any); ok {
		var parts []string
		for _, it := range arr {
			if im, ok := it.(map[string]any); ok {
				if s, ok := im["msg"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		if len(parts) > 0 {
			msg = strings.Join(parts, "; ")
		}
	}
	if strings.Contains(strings.ToLower(msg), "insufficient traffic") {
		return "❌ حجم پنل شما در Mit کافی نیست. لطفاً حجم پنل را شارژ کنید.\n(" + msg + ")"
	}
	return msg
}

func (m *Manager) mitRequest(panel db.Row, method, path string, payload any) map[string]any {
	auth := m.MitLogin(panel)
	tok, ok := auth["access_token"].(string)
	if !ok {
		e := str(auth, "error")
		if e == "" {
			e = "auth failed"
		}
		return map[string]any{"detail": e}
	}
	h := map[string]string{"Accept": "application/json", "Authorization": "Bearer " + tok}
	var body []byte
	ct := ""
	if payload != nil {
		body = marshal(payload)
		ct = "application/json"
	}
	r := req{method: method, url: mitBase(panel) + path, insecure: true, body: body, contentType: ct, headers: h, timeout: 10 * time.Second}.do()
	if r.err != nil {
		// mark the difference between "the panel refused" and "we never got an
		// answer at all", which a create has to tell apart
		return map[string]any{"detail": r.err.Error(), "transport_error": true}
	}
	return jsonObj(r.body)
}

// isTransport reports whether a mitRequest reply is the client's own network
// failure rather than an answer the panel sent back.
func isTransport(res map[string]any) bool {
	v, _ := res["transport_error"].(bool)
	return v
}

// mitDashboardPath is the reseller's dashboard in Mit Panel.
//
// The trailing slash is part of the address, not a detail: Mit Panel serves
// its web page at "/dashboard" and its JSON at "/dashboard/", so asking for
// the one without the slash answered with a page of HTML and every "mit"
// panel looked broken — the traffic figure never arrived and the panel's
// own connection test failed.
const mitDashboardPath = "/dashboard/"

// MitDashboard is mit_dashboard().
func (m *Manager) MitDashboard(panel db.Row) map[string]any {
	auth := m.MitLogin(panel)
	tok, ok := auth["access_token"].(string)
	if !ok {
		e := str(auth, "error")
		if e == "" {
			e = "auth failed"
		}
		return map[string]any{"detail": e}
	}
	r := req{url: mitBase(panel) + mitDashboardPath, insecure: true, timeout: 10 * time.Second,
		headers: map[string]string{"Accept": "application/json", "Authorization": "Bearer " + tok}}.do()
	if r.err != nil {
		return map[string]any{"detail": r.err.Error()}
	}
	body := jsonObj(r.body)
	if v, _ := body["success"].(bool); !v {
		return map[string]any{"detail": MitErrorMessage(body, "Mit dashboard request failed")}
	}
	d, _ := body["data"].(map[string]any)
	if d == nil {
		d = map[string]any{}
	}
	return d
}

func (m *Manager) mitDirectToken(panel db.Row) map[string]any {
	panel = m.panelByID(panel.S("id"))
	cache := loginCache(panel)
	if e, ok := cache["marzban"].(map[string]any); ok && isset(e, "access_token") {
		if tok, ok := fresh(e, 3600); ok {
			return map[string]any{"access_token": tok}
		}
	}
	r := req{method: "POST", url: trimSlash(panel.S("marzban_url_direct")) + "/api/admin/token", timeout: 8 * time.Second,
		body:        formBody("username", panel.S("marzban_username_direct"), "password", panel.S("marzban_password_direct")),
		contentType: "application/x-www-form-urlencoded", headers: map[string]string{"accept": "application/json"}}.do()
	if r.err != nil {
		return map[string]any{"error": r.err.Error()}
	}
	body := jsonObj(r.body)
	if tok, ok := body["access_token"].(string); ok {
		e := map[string]any{"time": nowStamp(), "access_token": tok}
		cache["marzban"] = e
		m.DB.Update("marzban_panel", "datelogin", string(marshal(cache)), "name_panel", panel.S("name_panel"))
		return e
	}
	d := str(body, "detail")
	if d == "" {
		d = "Marzban direct login failed"
	}
	return map[string]any{"error": d}
}

func (m *Manager) mitDirectGetUser(panel db.Row, username string) map[string]any {
	tok := m.mitDirectToken(panel)
	t, ok := tok["access_token"].(string)
	if !ok {
		e := str(tok, "error")
		if e == "" {
			e = "auth failed"
		}
		return map[string]any{"detail": e}
	}
	r := req{url: trimSlash(panel.S("marzban_url_direct")) + "/api/user/" + username, timeout: 8 * time.Second,
		headers: map[string]string{"Accept": "application/json", "Authorization": "Bearer " + t}}.do()
	return jsonObj(r.body)
}

func phpUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// mitCreateOut shapes the user the real Marzban reported into the same
// create-shaped reply a panel success would have produced.
func mitCreateOut(panel db.Row, username string, created map[string]any) Out {
	sub := str(created, "subscription_url")
	if !subURLRe.MatchString(sub) {
		sub = trimSlash(panel.S("marzban_url_direct")) + "/" + strings.TrimLeft(sub, "/")
	}
	links := strList(created["links"])
	if links == nil {
		links = []string{}
	}
	return Out{"status": "successful", "username": username, "subscription_url": sub, "configs": links}
}

func (m *Manager) mitCreate(panel db.Row, username string, expire int64, dataLimit float64) Out {
	exp := int64(0)
	if expire != 0 {
		exp = expire * 1000
	}
	payload := map[string]any{
		"email": username, "id": phpUUID(), "enable": true,
		"expiry_time": exp, "total": dataLimit, "sub_id": username, "flow": "",
	}
	res := m.mitRequest(panel, "POST", "/admin/user", payload)
	if v, _ := res["success"].(bool); !v {
		// The panel's answer can get stuck on its own upstream, and the client
		// sees "context deadline exceeded" while the user was still created.
		// Read the real Marzban directly: whatever it has for this username is
		// deliverable and must not be reported as a failure.
		checkExisting := func() Out {
			created := m.mitDirectGetUser(panel, username)
			if isset(created, "username") && !isset(created, "detail") {
				return mitCreateOut(panel, username, created)
			}
			return nil
		}
		if out := checkExisting(); out != nil {
			return out
		}
		// Only a create the panel never answered at all is re-run (the payload
		// is the same user, so the second answer is either success or an
		// "already exists" the direct check catches). One extra try keeps the
		// worst case bounded; a refused create is reported as before.
		if !isTransport(res) {
			return unsuccessful(MitErrorMessage(res, "خطا در ساخت یوزر روی Mit"))
		}
		res = m.mitRequest(panel, "POST", "/admin/user", payload)
		if v, _ := res["success"].(bool); !v {
			if out := checkExisting(); out != nil {
				return out
			}
			return unsuccessful(MitErrorMessage(res, "خطا در ساخت یوزر روی Mit"))
		}
	}
	created := m.mitDirectGetUser(panel, username)
	if isset(created, "detail") || !isset(created, "username") {
		return Out{"status": "successful", "username": username, "subscription_url": "", "configs": []string{}}
	}
	return mitCreateOut(panel, username, created)
}

func (m *Manager) mitData(panel db.Row, username string) Out {
	u := m.mitDirectGetUser(panel, username)
	if isset(u, "detail") || !isset(u, "username") {
		d := u["detail"]
		if d == nil {
			d = "User not found"
		}
		return unsuccessful(d)
	}
	sub := str(u, "subscription_url")
	if !subURLRe.MatchString(sub) {
		sub = trimSlash(panel.S("marzban_url_direct")) + "/" + strings.TrimLeft(sub, "/")
	}
	expire := u["expire"]
	if str(u, "status") == "on_hold" {
		expire = 0
	}
	links := strList(u["links"])
	if links == nil {
		links = []string{}
	}
	return Out{
		"status": u["status"], "username": u["username"], "data_limit": u["data_limit"],
		"expire": expire, "online_at": u["online_at"], "used_traffic": u["used_traffic"],
		"links": links, "subscription_url": sub,
	}
}

func (m *Manager) mitRemove(panel db.Row, username string) Out {
	res := m.mitRequest(panel, "DELETE", "/admin/user/"+url.PathEscape(username), nil)
	if v, _ := res["success"].(bool); !v {
		return unsuccessful(MitErrorMessage(res, "خطا در حذف یوزر روی Mit"))
	}
	return Out{"status": "successful", "username": username}
}

func (m *Manager) mitReset(panel db.Row, username string) {
	m.mitRequest(panel, "PUT", "/admin/user/"+url.PathEscape(username)+"/reset", nil)
}

// mitModify mirrors Modifyuser_mit: unspecified fields are filled from
// the live Marzban record. Errors come back as {"detail": ...}.
func (m *Manager) mitModify(panel db.Row, username string, data map[string]any) Out {
	cur := m.mitDirectGetUser(panel, username)
	if isset(cur, "detail") || !isset(cur, "username") {
		d := cur["detail"]
		if d == nil {
			d = "User not found"
		}
		return Out{"detail": d}
	}
	var expire int64
	if v, ok := data["expire"]; ok {
		if toF(v) != 0 {
			expire = int64(toF(v)) * 1000
		}
	} else {
		expire = int64(toF(cur["expire"])) * 1000
	}
	total := toF(cur["data_limit"])
	if v, ok := data["data_limit"]; ok {
		total = toF(v)
	}
	enable := str(cur, "status") != "disabled"
	if v, ok := data["status"]; ok {
		enable = fmt.Sprint(v) != "disabled"
	}
	payload := map[string]any{
		"email": username, "enable": enable, "expiry_time": expire,
		"total": total, "sub_id": username, "flow": "",
	}
	res := m.mitRequest(panel, "PUT", "/admin/user/"+url.PathEscape(username), payload)
	if v, _ := res["success"].(bool); !v {
		return Out{"detail": MitErrorMessage(res, "خطا در ویرایش یوزر روی Mit")}
	}
	return Out{"username": username, "success": true}
}
