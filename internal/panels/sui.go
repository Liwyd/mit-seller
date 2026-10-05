package panels

import (
	"bytes"
	"math/rand/v2"
	"mime/multipart"
	"strings"
	"time"

	"github.com/Liwyd/mit-seller/internal/db"
)

// S-UI authenticates with an API token kept in password_panel.

func (m *Manager) suiGet(panel db.Row, path string) map[string]any {
	return jsonObj(req{url: panel.S("url_panel") + path, follow: true, timeout: 4 * time.Second,
		headers: map[string]string{"Token": panel.S("password_panel")}}.do().body)
}

// suiSave posts a multipart form to /apiv2/save like CURLOPT_POSTFIELDS(array) did.
func (m *Manager) suiSave(panel db.Row, fields map[string]string) map[string]any {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, k := range []string{"object", "action", "data"} {
		if v, ok := fields[k]; ok {
			_ = w.WriteField(k, v)
		}
	}
	_ = w.Close()
	return jsonObj(req{method: "POST", url: panel.S("url_panel") + "/apiv2/save", body: buf.Bytes(), contentType: w.FormDataContentType(),
		follow: true, timeout: 4 * time.Second, headers: map[string]string{"Token": panel.S("password_panel")}}.do().body)
}

// SUIGetClient is GetClientsS_UI(): the full client, or nil.
func (m *Manager) SUIGetClient(username string, panel db.Row) map[string]any {
	list := m.suiGet(panel, "/apiv2/clients")
	if v, _ := list["success"].(bool); !v {
		return nil
	}
	obj, _ := list["obj"].(map[string]any)
	clients, _ := obj["clients"].([]any)
	var id string
	for _, c := range clients {
		cm, _ := c.(map[string]any)
		if str(cm, "name") == username {
			id = str(cm, "id")
			break
		}
	}
	if id == "" {
		return nil
	}
	one := m.suiGet(panel, "/apiv2/clients?id="+id)
	if v, _ := one["success"].(bool); !v {
		return nil
	}
	o, _ := one["obj"].(map[string]any)
	cl, _ := o["clients"].([]any)
	if len(cl) == 0 {
		return nil
	}
	c, _ := cl[0].(map[string]any)
	return c
}

func (m *Manager) suiSettings(panel db.Row) map[string]any {
	o, _ := m.suiGet(panel, "/apiv2/settings")["obj"].(map[string]any)
	return o
}

func (m *Manager) suiOnline(panel db.Row, username string) string {
	r := m.suiGet(panel, "/apiv2/onlines")
	obj, _ := r["obj"].(map[string]any)
	list, ok := obj["user"].([]any)
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

func (m *Manager) suiSubURL(panel db.Row, username string) string {
	s := m.suiSettings(panel)
	parts := strings.Split(panel.S("url_panel"), ":")
	for len(parts) < 2 {
		parts = append(parts, "")
	}
	return parts[0] + ":" + parts[1] + ":" + str(s, "subPort") + str(s, "subPath") + username
}

const authChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// generateAuthStr: 10 characters drawn without repetition, like str_shuffle.
func generateAuthStr() string {
	b := []byte(authChars)
	rand.Shuffle(len(b), func(i, j int) { b[i], b[j] = b[j], b[i] })
	return string(b[:10])
}

func suiConfig(name string) map[string]any {
	pw := randHex(16)
	return map[string]any{
		"mixed":         map[string]any{"username": name, "password": generateAuthStr()},
		"socks":         map[string]any{"username": name, "password": generateAuthStr()},
		"http":          map[string]any{"username": name, "password": generateAuthStr()},
		"shadowsocks":   map[string]any{"name": name, "password": pw},
		"shadowsocks16": map[string]any{"name": name, "password": pw},
		"shadowtls":     map[string]any{"name": name, "password": pw},
		"vmess":         map[string]any{"name": name, "uuid": phpUUID(), "alterId": 0},
		"vless":         map[string]any{"name": name, "uuid": phpUUID(), "flow": ""},
		"trojan":        map[string]any{"name": name, "password": generateAuthStr()},
		"naive":         map[string]any{"username": name, "password": generateAuthStr()},
		"hysteria":      map[string]any{"name": name, "auth_str": generateAuthStr()},
		"tuic":          map[string]any{"name": name, "uuid": phpUUID(), "password": generateAuthStr()},
		"hysteria2":     map[string]any{"name": name, "password": generateAuthStr()},
	}
}

func (m *Manager) suiCreate(panel db.Row, username string, expire int64, dataLimit float64) Out {
	if username == "" {
		return unsuccessful("error")
	}
	inbounds := rawJSONOrNull(panel.S("proxies"))
	data := map[string]any{
		"enable": true, "name": username, "config": suiConfig(username), "inbounds": inbounds,
		"links": []any{}, "volume": jsonNumber(dataLimit), "expiry": expire, "desc": "",
	}
	res := m.suiSave(panel, map[string]string{"object": "clients", "action": "new", "data": string(marshal(data))})
	if v, _ := res["success"].(bool); !v {
		return unsuccessful(res["msg"])
	}
	sub := m.suiSubURL(panel, username)
	return Out{"status": "successful", "username": username, "subscription_url": sub, "configs": []string{OutputLink(sub)}}
}

func (m *Manager) suiData(panel db.Row, username string) Out {
	c := m.SUIGetClient(username, panel)
	online := m.suiOnline(panel, username)
	if !isset(c, "id") {
		return unsuccessful(nil)
	}
	links := []string{}
	if arr, ok := c["links"].([]any); ok {
		for _, l := range arr {
			lm, _ := l.(map[string]any)
			links = append(links, str(lm, "uri"))
		}
	}
	limit := num(c, "volume")
	used := num(c, "up") + num(c, "down")
	expire := num(c, "expiry")
	status := "disabled"
	switch {
	case truthy(c["enable"]):
		status = "active"
	case limit != 0 && limit-used < 0:
		status = "limited"
	case expire-float64(time.Now().Unix()) < 0 && expire != 0:
		status = "expired"
	}
	return Out{
		"status": status, "username": c["name"], "data_limit": limit, "expire": expire, "online_at": online,
		"used_traffic": used, "links": links, "subscription_url": m.suiSubURL(panel, username),
		"sub_updated_at": nil, "sub_last_user_agent": nil,
	}
}

func (m *Manager) suiRevoke(panel db.Row, username string) Out {
	c := m.SUIGetClient(username, panel)
	data := map[string]any{
		"id": c["id"], "enable": c["enable"], "name": username, "config": suiConfig(username),
		"inbounds": c["inbounds"], "links": []any{}, "volume": c["volume"], "expiry": c["expiry"], "desc": c["desc"],
	}
	res := m.suiSave(panel, map[string]string{"object": "clients", "action": "edit", "data": string(marshal(data))})
	if v, _ := res["success"].(bool); !v {
		return unsuccessful("Unsuccessful")
	}
	sub := m.suiSubURL(panel, username)
	return Out{"status": "successful", "configs": []string{OutputLink(sub)}, "subscription_url": sub}
}

func (m *Manager) suiModify(panel db.Row, username string, config map[string]any) Out {
	c := m.SUIGetClient(username, panel)
	if len(c) == 0 {
		return Out{}
	}
	data := map[string]any{
		"id": c["id"], "enable": c["enable"], "name": username, "config": c["config"], "inbounds": c["inbounds"],
		"links": c["links"], "volume": c["volume"], "expiry": c["expiry"], "desc": c["desc"],
	}
	for k, v := range config {
		data[k] = v
	}
	return Out(m.suiSave(panel, map[string]string{"object": "clients", "action": "edit", "data": string(marshal(data))}))
}

func (m *Manager) suiReset(panel db.Row, username string) {
	c := m.SUIGetClient(username, panel)
	data := map[string]any{
		"id": c["id"], "enable": c["enable"], "name": c["name"], "config": c["config"], "inbounds": c["inbounds"],
		"links": c["links"], "volume": c["volume"], "expiry": c["expiry"], "desc": c["desc"], "up": 0, "down": 0,
	}
	m.suiSave(panel, map[string]string{"object": "clients", "action": "edit", "data": string(marshal(data))})
}

func (m *Manager) suiRemove(panel db.Row, username string) Out {
	c := m.SUIGetClient(username, panel)
	res := m.suiSave(panel, map[string]string{"object": "clients", "action": "del", "data": str(c, "id")})
	if v, _ := res["success"].(bool); !v {
		return unsuccessful(res["msg"])
	}
	return Out{"status": "successful", "username": username}
}
