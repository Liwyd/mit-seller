package panels

import (
	"encoding/json"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
)

// MarzbanToken is token_panel(): a cached bearer token or the login response
// (which then carries "detail" or "errror").
func (m *Manager) MarzbanToken(panel db.Row) map[string]any {
	panel = m.panelByID(panel.S("id"))
	if panel.S("datelogin") != "" {
		if tok, ok := fresh(loginCache(panel), 3600); ok {
			return map[string]any{"access_token": tok}
		}
	}
	r := req{method: "POST", url: panel.S("url_panel") + "/api/admin/token", timeout: 6 * time.Second,
		body:        formBody("username", panel.S("username_panel"), "password", panel.S("password_panel")),
		contentType: "application/x-www-form-urlencoded", headers: map[string]string{"accept": "application/json"}}.do()
	if r.err != nil {
		return map[string]any{"errror": r.err.Error()}
	}
	body := jsonObj(r.body)
	if tok, ok := body["access_token"].(string); ok {
		m.DB.Update("marzban_panel", "datelogin", string(marshal(map[string]any{"time": nowStamp(), "access_token": tok})), "name_panel", panel.S("name_panel"))
	}
	if body == nil {
		body = map[string]any{}
	}
	return body
}

func (m *Manager) marzbanCall(panel db.Row, method, path string, payload []byte) []byte {
	tok := m.MarzbanToken(panel)
	h := map[string]string{"Accept": "application/json", "Authorization": "Bearer " + str(tok, "access_token")}
	ct := ""
	if payload != nil {
		ct = "application/json"
	}
	r := req{method: method, url: panel.S("url_panel") + path, body: payload, contentType: ct, headers: h}.do()
	return r.body
}

// MarzbanGetUser is getuser().
func (m *Manager) MarzbanGetUser(username string, panel db.Row) map[string]any {
	return jsonObj(m.marzbanCall(panel, "GET", "/api/user/"+username, nil))
}

// MarzbanSystemStats is Get_System_Stats().
func (m *Manager) MarzbanSystemStats(panel db.Row) map[string]any {
	return jsonObj(m.marzbanCall(panel, "GET", "/api/system", nil))
}

var versionRe = regexp.MustCompile(`(\d+\.\d+\.\d+)`)

func (m *Manager) marzbanAbove084(panel db.Row) bool {
	name := panel.S("name_panel")
	m.mu.Lock()
	if e, ok := m.versions[name]; ok && time.Since(e.at) < 10*time.Minute {
		m.mu.Unlock()
		return e.above084
	}
	m.mu.Unlock()
	stats := m.MarzbanSystemStats(panel)
	above := false
	if v, ok := stats["version"].(string); ok {
		n := v
		if mm := versionRe.FindStringSubmatch(v); mm != nil {
			n = mm[1]
		}
		above = VersionCompare(n, "0.8.4") > 0
	}
	m.mu.Lock()
	m.versions[name] = versionEntry{above084: above, at: time.Now()}
	m.mu.Unlock()
	return above
}

func (m *Manager) marzbanInboundTags(panel db.Row) []string {
	cores := jsonObj(m.marzbanCall(panel, "GET", "/api/cores", nil))
	var tags []string
	seen := map[string]bool{}
	if list, ok := cores["cores"].([]any); ok {
		for _, c := range list {
			cm, _ := c.(map[string]any)
			cfg, _ := cm["config"].(map[string]any)
			inb, _ := cfg["inbounds"].([]any)
			for _, i := range inb {
				im, _ := i.(map[string]any)
				if t, ok := im["tag"].(string); ok && !seen[t] {
					seen[t] = true
					tags = append(tags, t)
				}
			}
		}
	}
	if len(tags) > 0 {
		return tags
	}
	inb, _ := jsonAny(m.marzbanCall(panel, "GET", "/api/inbounds", nil)).([]any)
	for _, i := range inb {
		im, _ := i.(map[string]any)
		if t, ok := im["tag"].(string); ok {
			tags = append(tags, t)
		}
	}
	return tags
}

func groupsList(v any) []map[string]any {
	var arr []any
	switch x := v.(type) {
	case map[string]any:
		arr, _ = x["groups"].([]any)
	case []any:
		arr = x
	}
	out := []map[string]any{}
	for _, g := range arr {
		if gm, ok := g.(map[string]any); ok {
			out = append(out, gm)
		}
	}
	return out
}

func (m *Manager) marzbanGroups(panel db.Row) any {
	return jsonAny(m.marzbanCall(panel, "GET", "/api/groups", nil))
}

// marzbanEnsureGroups is ensure_default_groups(): Marzban > 0.8.4 needs the
// mirza_paid / mirza_test groups to exist.
func (m *Manager) marzbanEnsureGroups(panel db.Row) {
	if !m.marzbanAbove084(panel) {
		return
	}
	have := map[string]bool{}
	for _, g := range groupsList(m.marzbanGroups(panel)) {
		if n, ok := g["name"].(string); ok {
			have[strings.ToLower(n)] = true
		}
	}
	var tags []string
	for _, name := range []string{"mirza_paid", "mirza_test"} {
		if have[name] {
			continue
		}
		if tags == nil {
			tags = m.marzbanInboundTags(panel)
		}
		data := map[string]any{"name": name}
		if len(tags) > 0 {
			data["inbound_tags"] = tags
		}
		r := req{method: "POST", url: trimSlash(panel.S("url_panel")) + "/api/group", body: marshal(data), contentType: "application/json",
			headers: map[string]string{"Accept": "application/json", "Authorization": "Bearer " + str(m.MarzbanToken(panel), "access_token")}}.do()
		if r.code != 200 && r.code != 201 {
			log.Printf("create_group %s failed: HTTP %d %s", name, r.code, string(r.body))
		}
	}
}

func rawJSONOrNull(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("null")
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(s)
}

func (m *Manager) marzbanCreate(panel db.Row, username string, expire int64, dataLimit float64, isTest bool) Out {
	m.marzbanEnsureGroups(panel)
	data := map[string]any{
		"proxies":    rawJSONOrNull(panel.S("proxies")),
		"data_limit": jsonNumber(dataLimit),
		"username":   username,
	}
	if m.marzbanAbove084(panel) {
		want := "mirza_paid"
		if isTest {
			want = "mirza_test"
		}
		if gm, ok := m.marzbanGroups(panel).(map[string]any); ok {
			for _, g := range groupsList(gm) {
				if g["name"] == want && g["id"] != nil {
					data["group_ids"] = []any{g["id"]}
					break
				}
			}
		}
	}
	if in := panel.S("inbounds"); !panel.IsNull("inbounds") && in != "null" {
		data["inbounds"] = rawJSONOrNull(in)
	}
	if expire == 0 {
		data["expire"] = 0
	} else if panel.S("onholdstatus") == "ononhold" {
		data["expire"] = 0
		data["status"] = "on_hold"
		data["on_hold_expire_duration"] = expire - time.Now().Unix()
	} else {
		data["expire"] = expire
	}
	out := jsonObj(m.marzbanCall(panel, "POST", "/api/user", marshal(data)))
	if truthy(out["detail"]) {
		return unsuccessful(out["detail"])
	}
	res := Out{"status": "successful", "username": nil, "subscription_url": "", "configs": nil}
	if out != nil {
		res["username"] = out["username"]
		res["subscription_url"] = fixSubURL(str(out, "subscription_url"), panel.S("url_panel"))
		res["configs"] = strList(out["links"])
	}
	return res
}

func (m *Manager) marzbanData(panel db.Row, username string) Out {
	u := m.MarzbanGetUser(username, panel)
	if truthy(u["detail"]) {
		return unsuccessful(u["detail"])
	}
	if !isset(u, "username") {
		return unsuccessful(u["detail"])
	}
	expire := u["expire"]
	if str(u, "status") == "on_hold" {
		expire = 0
	}
	return Out{
		"status":           u["status"],
		"username":         u["username"],
		"data_limit":       u["data_limit"],
		"expire":           expire,
		"online_at":        u["online_at"],
		"used_traffic":     u["used_traffic"],
		"links":            strList(u["links"]),
		"subscription_url": fixSubURL(str(u, "subscription_url"), panel.S("url_panel")),
	}
}

func (m *Manager) marzbanRevoke(panel db.Row, username string) Out {
	r := jsonObj(m.marzbanCall(panel, "POST", "/api/user/"+username+"/revoke_sub", nil))
	if truthy(r["detail"]) {
		return unsuccessful(r["detail"])
	}
	d := m.DataUser(panel.S("name_panel"), username)
	return Out{"status": "successful", "configs": d["links"], "subscription_url": fixSubURL(d.S("subscription_url"), panel.S("url_panel"))}
}

func (m *Manager) marzbanRemove(panel db.Row, username string) Out {
	r := jsonObj(m.marzbanCall(panel, "DELETE", "/api/user/"+username, nil))
	if truthy(r["detail"]) {
		return unsuccessful(r["detail"])
	}
	return Out{"status": "successful", "username": username}
}

func (m *Manager) marzbanReset(panel db.Row, username string) {
	m.marzbanCall(panel, "POST", "/api/user/"+username+"/reset", nil)
}

func (m *Manager) marzbanModify(panel db.Row, username string, data map[string]any) map[string]any {
	return jsonObj(m.marzbanCall(panel, "PUT", "/api/user/"+username, marshal(data)))
}

// jsonNumber renders a PHP number for JSON: integers stay integers.
func jsonNumber(f float64) any {
	if f == float64(int64(f)) {
		return int64(f)
	}
	return f
}
