package panels

import (
	"time"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/php"
)

// Alireza0's x-ui fork: same idea as 3x-ui under /xui/API, with a fresh login
// before each call like loginalireza().

// AlirezaLogin is loginalireza(); returns the cookie header and the reply.
func (m *Manager) AlirezaLogin(panel db.Row) (string, map[string]any) {
	r := req{method: "POST", url: panel.S("url_panel") + "/login", follow: true, timeout: 6 * time.Second,
		body:        formBody("username", panel.S("username_panel"), "password", panel.S("password_panel")),
		contentType: "application/x-www-form-urlencoded"}.do()
	if r.err != nil {
		return "", map[string]any{"errror": r.err.Error()}
	}
	return cookieHeader(cookieJarText(hostOf(panel.S("url_panel")), r.cookies)), jsonObj(r.body)
}

func (m *Manager) arCall(panel db.Row, method, path string, payload []byte) []byte {
	cookie, _ := m.AlirezaLogin(panel)
	ct := ""
	if payload != nil {
		ct = "application/json"
	}
	return req{method: method, url: panel.S("url_panel") + path, body: payload, contentType: ct, cookie: cookie,
		follow: true, insecure: true, headers: map[string]string{"Accept": "application/json"}, timeout: 10 * time.Second}.do().body
}

func (m *Manager) alirezaTraffic(username string, panel db.Row) map[string]any {
	o, _ := jsonObj(m.arCall(panel, "GET", "/xui/API/inbounds/getClientTraffics/"+username, nil))["obj"].(map[string]any)
	return o
}

// alirezaClient is get_clinetsalireza(): the client entry from the inbound settings.
func (m *Manager) alirezaClient(username string, panel db.Row) map[string]any {
	list, _ := jsonObj(m.arCall(panel, "GET", "/xui/API/inbounds", nil))["obj"].([]any)
	out := map[string]any{}
	for _, in := range list {
		im, _ := in.(map[string]any)
		s := jsonObj([]byte(str(im, "settings")))
		cl, _ := s["clients"].([]any)
		for _, c := range cl {
			cm, _ := c.(map[string]any)
			if str(cm, "email") == username {
				out = cm
				break
			}
		}
	}
	return out
}

func (m *Manager) alirezaOnline(panel db.Row, username string) string {
	r := jsonObj(m.arCall(panel, "POST", "/xui/API/inbounds/onlines", nil))
	list, ok := r["obj"].([]any)
	if !ok {
		return "offline"
	}
	for _, x := range list {
		if s, _ := x.(string); s == username {
			return "online"
		}
	}
	return "offline"
}

func (m *Manager) alirezaCreate(panel db.Row, username string, expire int64, dataLimit float64) Out {
	subID := randHex(8)
	cfg := map[string]any{
		"id": php.Intval(panel.S("inboundid")),
		"settings": xuiClientSettings([]map[string]any{{
			"id": phpUUID(), "flow": "", "email": username, "totalGB": jsonNumber(dataLimit), "expiryTime": expire * 1000,
			"enable": true, "tgId": "", "subId": subID, "reset": 0,
		}}, true),
	}
	res := jsonObj(m.arCall(panel, "POST", "/xui/API/inbounds/addClient", marshal(cfg)))
	if v, _ := res["success"].(bool); !v {
		return unsuccessful(res["msg"])
	}
	link := panel.S("linksubx") + "/" + subID + "?name=" + username
	return Out{"status": "successful", "username": username, "subscription_url": link, "configs": []string{OutputLink(link)}}
}

func (m *Manager) alirezaData(panel db.Row, username string) Out {
	t := m.alirezaTraffic(username, panel)
	c := m.alirezaClient(username, panel)
	if !truthy(t["id"]) {
		return unsuccessful(t["msg"])
	}
	status := "disabled"
	if truthy(t["enable"]) {
		status = "active"
	}
	link := panel.S("linksubx") + "/" + str(c, "subId") + "?name=" + username
	return Out{
		"status": status, "username": t["email"], "data_limit": t["total"], "expire": num(t, "expiryTime") / 1000,
		"online_at": m.alirezaOnline(panel, username), "used_traffic": num(t, "up") + num(t, "down"),
		"links": []string{OutputLink(link)}, "subscription_url": link,
	}
}

func (m *Manager) alirezaUpdate(panel db.Row, username string, cfg map[string]any) map[string]any {
	c := m.alirezaClient(username, panel)
	return jsonObj(m.arCall(panel, "POST", "/xui/API/inbounds/updateClient/"+str(c, "id"), marshal(cfg)))
}

func (m *Manager) alirezaRevoke(panel db.Row, username string) Out {
	c := m.alirezaClient(username, panel)
	subID := randHex(8)
	link := panel.S("linksubx") + "/" + subID + "/?name=" + username
	cfg := map[string]any{
		"id": php.Intval(panel.S("inboundid")),
		"settings": xuiClientSettings([]map[string]any{{
			"id": phpUUID(), "flow": c["flow"], "email": c["email"], "totalGB": c["totalGB"], "expiryTime": c["expiryTime"],
			"enable": true, "subId": subID,
		}}, false),
	}
	m.alirezaUpdate(panel, username, cfg)
	if len(c) == 0 {
		return unsuccessful("Unsuccessful")
	}
	return Out{"status": "successful", "configs": OutputLink(link), "subscription_url": link}
}

func (m *Manager) alirezaModify(panel db.Row, username string, config map[string]any) Out {
	c := m.alirezaClient(username, panel)
	base := map[string]any{
		"clients": []any{map[string]any{
			"id": c["id"], "flow": c["flow"], "email": c["email"], "totalGB": c["totalGB"], "expiryTime": c["expiryTime"],
			"enable": true, "subId": c["subId"],
		}},
		"decryption": "none",
		"fallbacks":  []any{},
	}
	if s, ok := config["settings"].(string); ok {
		if patch := jsonObj([]byte(s)); patch != nil {
			base = replaceRecursive(base, patch).(map[string]any)
		}
	}
	cfg := map[string]any{"id": php.Intval(panel.S("inboundid")), "settings": string(marshal(base))}
	return Out(m.alirezaUpdate(panel, username, cfg))
}

func (m *Manager) alirezaReset(panel db.Row, username string) {
	c := m.alirezaClient(username, panel)
	m.arCall(panel, "POST", "/xui/API/inbounds/"+panel.S("inboundid")+"/resetClientTraffic/"+str(c, "email"), []byte{})
}

func (m *Manager) alirezaRemove(panel db.Row, username string) Out {
	c := m.alirezaClient(username, panel)
	r := jsonObj(m.arCall(panel, "POST", "/xui/API/inbounds/"+panel.S("inboundid")+"/delClient/"+str(c, "id"), nil))
	if v, _ := r["success"].(bool); !v {
		return unsuccessful(r["msg"])
	}
	return Out{"status": "successful", "username": username}
}
