package panels

import (
	"crypto/rand"
	"encoding/base64"
	"math"
	"net/url"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/php"
)

// WGDashboard: API key in password_panel, configuration name in inboundid.

func (m *Manager) wgCall(panel db.Row, method, path string, payload any) []byte {
	var body []byte
	ct := ""
	if payload != nil {
		body = marshal(payload)
		ct = "application/json"
	}
	return req{method: method, url: panel.S("url_panel") + path, body: body, contentType: ct, follow: true, insecure: true,
		headers: map[string]string{"Accept": "application/json", "wg-dashboard-apikey": panel.S("password_panel")}}.do().body
}

// WGGetUser is get_userwg(): the peer named username (empty map when absent).
func (m *Manager) WGGetUser(username, namePanel string) map[string]any {
	panel := m.panelByName(namePanel)
	r := jsonObj(m.wgCall(panel, "GET", "/api/getWireguardConfigurationInfo?configurationName="+panel.S("inboundid"), nil))
	if r == nil {
		return nil
	}
	data, _ := r["data"].(map[string]any)
	var peers []any
	if a, ok := data["configurationPeers"].([]any); ok {
		peers = append(peers, a...)
	}
	if a, ok := data["configurationRestrictedPeers"].([]any); ok {
		peers = append(peers, a...)
	}
	for _, p := range peers {
		pm, _ := p.(map[string]any)
		if str(pm, "name") == username {
			return pm
		}
	}
	return map[string]any{}
}

func (m *Manager) wgNextIP(panel db.Row) any {
	r := jsonObj(m.wgCall(panel, "GET", "/api/getAvailableIPs/"+panel.S("inboundid"), nil))
	data, _ := r["data"].(map[string]any)
	for _, v := range data {
		if arr, ok := v.([]any); ok && len(arr) > 0 {
			return arr[0]
		}
		break
	}
	return nil
}

func (m *Manager) wgDownload(panel db.Row, publicKey string) string {
	r := jsonObj(m.wgCall(panel, "GET", "/api/downloadPeer/"+panel.S("inboundid")+"?id="+url.QueryEscape(publicKey), nil))
	d, _ := r["data"].(map[string]any)
	return str(d, "file")
}

func wgKeys() (priv, pub, psk string) {
	sk := make([]byte, 32)
	_, _ = rand.Read(sk)
	pk, _ := curve25519.X25519(sk, curve25519.Basepoint)
	ps := make([]byte, 32)
	_, _ = rand.Read(ps)
	return base64.StdEncoding.EncodeToString(sk), base64.StdEncoding.EncodeToString(pk), base64.StdEncoding.EncodeToString(ps)
}

func (m *Manager) wgSetJob(panel db.Row, field, value, publicKey string) {
	m.wgCall(panel, "POST", "/api/savePeerScheduleJob", map[string]any{"Job": map[string]any{
		"JobID": phpUUID(), "Configuration": panel.S("inboundid"), "Peer": publicKey, "Field": field,
		"Operator": "lgt", "Value": value, "CreationDate": "", "ExpireDate": nil, "Action": "restrict",
	}})
}

// WGSetJob is setjob() by panel name.
func (m *Manager) WGSetJob(namePanel, field, value, publicKey string) {
	m.wgSetJob(m.panelByName(namePanel), field, value, publicKey)
}

// WGDeleteJob is deletejob().
func (m *Manager) WGDeleteJob(namePanel string, cfg map[string]any) {
	m.wgCall(m.panelByName(namePanel), "POST", "/api/deletePeerScheduleJob", cfg)
}

// WGResetData is ResetUserDataUsagewg().
func (m *Manager) WGResetData(publicKey, namePanel string) {
	p := m.panelByName(namePanel)
	m.wgCall(p, "POST", "/api/resetPeerData/"+p.S("inboundid"), map[string]any{"id": publicKey, "type": "total"})
}

func (m *Manager) invoicePublicKey(username string) string {
	info := jsonObj([]byte(m.DB.Select("invoice", "user_info", "username", username).S("user_info")))
	return str(info, "public_key")
}

func (m *Manager) wgPeers(namePanel, username, action string) map[string]any {
	p := m.panelByName(namePanel)
	return jsonObj(m.wgCall(p, "POST", "/api/"+action+"/"+p.S("inboundid"), map[string]any{"peers": []any{m.invoicePublicKey(username)}}))
}

// WGAllowAccess is allowAccessPeers().
func (m *Manager) WGAllowAccess(namePanel, username string) {
	m.wgPeers(namePanel, username, "allowAccessPeers")
}

// WGRestrict is restrictPeers().
func (m *Manager) WGRestrict(namePanel, username string) {
	m.wgPeers(namePanel, username, "restrictPeers")
}

func (m *Manager) wgCreate(panel db.Row, username string, expire int64, dataLimit float64) Out {
	gb := php.Round(dataLimit/(1024*1024*1024), 2)
	priv, pub, psk := wgKeys()
	cfg := map[string]any{"name": username, "allowed_ips": []any{m.wgNextIP(panel)}, "private_key": priv, "public_key": pub, "preshared_key": psk}
	resp := jsonObj(m.wgCall(panel, "POST", "/api/addPeers/"+panel.S("inboundid"), cfg))
	var out map[string]any
	if st, ok := resp["status"]; ok && !truthy(st) {
		out = resp
	} else {
		cfg["status"] = true
		out = cfg
	}
	if gb != 0 {
		m.wgSetJob(panel, "total_data", php.FloatToString(gb), str(out, "public_key"))
	}
	if expire != 0 {
		m.wgSetJob(panel, "date", php.Date("Y-m-d H:i:s", expire), str(out, "public_key"))
	}
	m.DB.Update("invoice", "user_info", string(marshal(out)), "username", username)
	if !truthy(out["status"]) {
		return unsuccessful(out["msg"])
	}
	return Out{"status": "successful", "username": username, "subscription_url": m.wgDownload(panel, str(out, "public_key")), "configs": []string{}}
}

func (m *Manager) wgData(panel db.Row, username string) Out {
	u := m.WGGetUser(username, panel.S("name_panel"))
	inv := m.DB.Select("invoice", "*", "username", username)
	info := jsonObj([]byte(inv.S("user_info")))
	if !isset(u, "id") {
		return unsuccessful(u["msg"])
	}
	var jobTime, jobVolume map[string]any
	if jobs, ok := u["jobs"].([]any); ok {
		for _, j := range jobs {
			jm, _ := j.(map[string]any)
			switch str(jm, "Field") {
			case "total_data":
				jobVolume = jm
			case "date":
				jobTime = jm
			}
		}
	}
	var expire int64
	if php.Intval(inv.S("Service_time")) != 0 && isset(jobTime, "Value") {
		expire, _ = php.Strtotime(str(jobTime, "Value"))
	}
	status := "active"
	if expire != 0 && expire-time.Now().Unix() < 0 {
		status = "expired"
	}
	gib := math.Pow(1024, 3)
	usage := num(u, "total_data")*gib + num(u, "cumu_data")*gib
	limit := php.Floatval(str(jobVolume, "Value")) * gib
	if limit < usage {
		status = "limited"
	}
	return Out{
		"status": status, "username": u["name"], "data_limit": limit, "expire": expire, "online_at": nil,
		"used_traffic": usage, "links": []string{}, "subscription_url": m.wgDownload(panel, str(info, "public_key")),
		"sub_updated_at": nil, "sub_last_user_agent": nil,
	}
}

func (m *Manager) wgRemove(panel db.Row, username string) Out {
	m.WGAllowAccess(panel.S("name_panel"), username)
	r := m.wgPeers(panel.S("name_panel"), username, "deletePeers")
	if !truthy(r["status"]) {
		return unsuccessful(r["msg"])
	}
	return Out{"status": "successful", "username": username}
}
