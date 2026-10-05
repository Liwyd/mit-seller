package panels

import (
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

// MikroTik User Manager over the REST API with basic auth.

func (m *Manager) mtCall(panel db.Row, method, path string, payload any) []byte {
	var body []byte
	if payload != nil {
		body = marshal(payload)
	}
	return req{method: method, url: panel.S("url_panel") + path, body: body, contentType: "application/json", follow: true,
		basicUser: panel.S("username_panel"), basicPass: panel.S("password_panel")}.do().body
}

// MikrotikLogin is login_mikrotik(): error when the router is unreachable.
func (m *Manager) MikrotikLogin(panel db.Row) map[string]any {
	r := req{url: panel.S("url_panel") + "/rest/system/resource", follow: true, timeout: 3 * time.Second,
		basicUser: panel.S("username_panel"), basicPass: panel.S("password_panel")}.do()
	if r.err != nil || r.code != 200 {
		return map[string]any{"error": 404}
	}
	o := jsonObj(r.body)
	if o == nil {
		o = map[string]any{}
	}
	return o
}

func (m *Manager) mikrotikUser(panel db.Row, username string) map[string]any {
	arr, _ := jsonAny(m.mtCall(panel, "GET", "/rest/user-manager/user?name="+username, nil)).([]any)
	if len(arr) == 0 {
		return nil
	}
	u, _ := arr[0].(map[string]any)
	return u
}

func (m *Manager) mikrotikCreate(panel db.Row, username string) Out {
	password := randHex(6)
	res := jsonObj(m.mtCall(panel, "POST", "/rest/user-manager/user/add", map[string]any{"name": username, "password": password}))
	m.mtCall(panel, "POST", "/rest/user-manager/user-profile/add", map[string]any{"user": username, "profile": panel.S("inboundid")})
	if isset(res, "error") {
		return unsuccessful(res["msg"])
	}
	return Out{"status": "successful", "username": username, "subscription_url": password, "configs": []string{}}
}

func (m *Manager) mikrotikData(panel db.Row, username string) Out {
	u := m.mikrotikUser(panel, username)
	if isset(u, "error") {
		return unsuccessful(u["msg"])
	}
	inv := m.DB.Select("invoice", "*", "username", username)
	mon, _ := jsonAny(m.mtCall(panel, "POST", "/rest/user-manager/user/monitor", map[string]any{"once": true, ".id": u[".id"]})).([]any)
	var t map[string]any
	if len(mon) > 0 {
		t, _ = mon[0].(map[string]any)
	}
	used := num(t, "total-upload") + num(t, "total-download")
	return Out{
		"status": "active", "username": inv.S("username"),
		"data_limit": php.Floatval(inv.S("Volume")) * 1024 * 1024 * 1024,
		"expire":     php.Floatval(inv.S("time_sell")) + php.Floatval(inv.S("Service_time"))*86400,
		"online_at":  nil, "used_traffic": used, "links": []string{}, "subscription_url": u["password"],
	}
}

func (m *Manager) mikrotikRemove(panel db.Row, username string) Out {
	u := m.mikrotikUser(panel, username)
	if isset(u, "error") {
		return unsuccessful(u["msg"])
	}
	m.mtCall(panel, "POST", "/rest/user-manager/user/remove", map[string]any{".id": u[".id"]})
	return Out{"status": "successful", "username": username}
}
