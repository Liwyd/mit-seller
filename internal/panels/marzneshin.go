package panels

import (
	"strings"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

// MarzneshinToken is token_panelm(). Like the PHP code it looks the panel up
// by its URL and caches the token for 10 minutes.
func (m *Manager) MarzneshinToken(panel db.Row) map[string]any {
	p := m.DB.Select("marzban_panel", "*", "url_panel", panel.S("url_panel"))
	if p.S("datelogin") != "" {
		if tok, ok := fresh(loginCache(p), 600); ok {
			return map[string]any{"access_token": tok}
		}
	}
	r := req{method: "POST", url: panel.S("url_panel") + "/api/admins/token", timeout: 6 * time.Second,
		body:        formBody("username", panel.S("username_panel"), "password", panel.S("password_panel")),
		contentType: "application/x-www-form-urlencoded", headers: map[string]string{"accept": "application/json"}}.do()
	if r.err != nil {
		return map[string]any{"errror": r.err.Error()}
	}
	body := jsonObj(r.body)
	if tok, ok := body["access_token"].(string); ok {
		m.DB.Update("marzban_panel", "datelogin", string(marshal(map[string]any{"time": nowStamp(), "access_token": tok})), "name_panel", p.S("name_panel"))
	}
	if body == nil {
		body = map[string]any{}
	}
	return body
}

func (m *Manager) mzCall(panel db.Row, method, path string, payload []byte) []byte {
	tok := m.MarzneshinToken(panel)
	h := map[string]string{"Accept": "application/json", "Authorization": "Bearer " + str(tok, "access_token")}
	ct := ""
	if payload != nil {
		ct = "application/json"
	}
	return req{method: method, url: panel.S("url_panel") + path, body: payload, contentType: ct, headers: h}.do().body
}

// MarzneshinGetUser is getuserm(); nil when no token could be obtained.
func (m *Manager) MarzneshinGetUser(username string, panel db.Row) map[string]any {
	tok := m.MarzneshinToken(panel)
	if !isset(tok, "access_token") {
		return nil
	}
	return jsonObj(m.mzCall(panel, "GET", "/api/users/"+username, nil))
}

// MarzneshinStats is Get_System_Statsm().
func (m *Manager) MarzneshinStats(panel db.Row) map[string]any {
	return jsonObj(m.mzCall(panel, "GET", "/api/system/stats/users", nil))
}

func subLinks(sub string) []string {
	l := DecodeIfBase64(OutputLink(sub))
	return strings.Split(strings.TrimSpace(l), "\n")
}

func (m *Manager) marzneshinCreate(panel db.Row, username string, ts int64, dataLimit float64) Out {
	data := map[string]any{
		"service_ids": rawJSONOrNull(panel.S("proxies")),
		"data_limit":  jsonNumber(dataLimit),
		"username":    username,
	}
	if panel.S("onholdstatus") == "offonhold" {
		if ts == 0 {
			data["expire_date"] = nil
			data["expire_strategy"] = "never"
		} else {
			data["expire_date"] = php.Date("c", ts)
			data["expire_strategy"] = "fixed_date"
		}
	} else {
		if ts == 0 {
			data["expire_date"] = nil
			data["expire_strategy"] = "never"
		} else {
			data["expire_date"] = nil
			data["expire_strategy"] = "start_on_first_use"
			data["usage_duration"] = ts - time.Now().Unix()
		}
	}
	out := jsonObj(m.mzCall(panel, "POST", "/api/users", marshal(data)))
	if truthy(out["detail"]) {
		return unsuccessful(out["detail"])
	}
	sub := fixSubURL(str(out, "subscription_url"), panel.S("url_panel"))
	return Out{"status": "successful", "username": out["username"], "subscription_url": sub, "configs": subLinks(sub)}
}

func (m *Manager) marzneshinData(panel db.Row, username string) Out {
	u := m.MarzneshinGetUser(username, panel)
	if truthy(u["detail"]) {
		return unsuccessful(u["detail"])
	}
	if !isset(u, "username") {
		return unsuccessful("")
	}
	sub := fixSubURL(str(u, "subscription_url"), panel.S("url_panel"))
	status := "active"
	switch {
	case !truthy(u["enabled"]):
		status = "disabled"
	case str(u, "expire_strategy") == "start_on_first_use":
		status = "on_hold"
	case truthy(u["expired"]):
		status = "expired"
	case num(u, "data_limit")-num(u, "used_traffic") <= 0:
		status = "limtied"
	}
	var expire int64
	if isset(u, "expire_date") {
		expire, _ = php.Strtotime(str(u, "expire_date"))
	}
	return Out{
		"status": status, "username": u["username"], "data_limit": u["data_limit"], "expire": expire,
		"online_at": u["online_at"], "used_traffic": u["used_traffic"], "links": subLinks(sub),
		"subscription_url": sub, "sub_updated_at": u["sub_updated_at"], "sub_last_user_agent": u["sub_last_user_agent"], "uuid": nil,
	}
}

func (m *Manager) marzneshinRevoke(panel db.Row, username string) Out {
	r := jsonObj(m.mzCall(panel, "POST", "/api/users/"+username+"/revoke_sub", nil))
	if truthy(r["detail"]) {
		return unsuccessful(r["detail"])
	}
	d := m.DataUser(panel.S("name_panel"), username)
	links := []string{DecodeIfBase64(OutputLink(d.S("subscription_url")))}
	return Out{"status": "successful", "configs": links, "subscription_url": d.S("subscription_url")}
}

func (m *Manager) marzneshinRemove(panel db.Row, username string) Out {
	r := jsonObj(m.mzCall(panel, "DELETE", "/api/users/"+username, nil))
	if truthy(r["detail"]) {
		return unsuccessful(r["detail"])
	}
	return Out{"status": "successful", "username": username}
}

func (m *Manager) marzneshinReset(panel db.Row, username string) {
	m.mzCall(panel, "POST", "/api/users/"+username+"/reset", nil)
}

func (m *Manager) marzneshinModify(panel db.Row, username string, config map[string]any) Out {
	u := m.MarzneshinGetUser(username, panel)
	if _, ok := config["expire_date"]; !ok {
		config["expire_date"] = u["expire_date"]
	}
	config["expire_strategy"] = u["expire_strategy"]
	config["username"] = username
	return Out(jsonObj(m.mzCall(panel, "PUT", "/api/users/"+username, marshal(config))))
}
