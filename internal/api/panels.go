package api

import (
	"net/http"

	"github.com/Liwyd/mit-seller/internal/bot"
	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/php"
)

var panelTypes = map[string]bool{"marzban": true, "x-ui_single": true, "marzneshin": true, "alireza": true, "s_ui": true, "wgdashboard": true, "mikrotik": true, "mit": true, "nexra": true}

func panelPublic(r db.Row, owner bool) map[string]any {
	m := rowMap(r, "id", "name_panel", "type", "status", "statusTest", "sublink", "configManual", "onholdstatus", "MethodUsername")
	if owner {
		for k, v := range rowMap(r, "url_panel", "username_panel", "inboundid", "linksubx", "marzban_url_direct", "marzban_username_direct") {
			m[k] = v
		}
		m["password_set"] = r.S("password_panel") != ""
		m["marzban_password_set"] = r.S("marzban_password_direct") != ""
	}
	return m
}

func (a *API) listPanels(w http.ResponseWriter, r *http.Request) {
	owner := a.role(r) == roleOwner
	var out []map[string]any
	for _, p := range a.B.DB.MustQuery("SELECT * FROM marzban_panel ORDER BY id") {
		out = append(out, panelPublic(p, owner))
	}
	if out == nil {
		out = []map[string]any{}
	}
	ok(w, out)
}

func validURL(s string) bool {
	return len(s) > 8 && (s[:7] == "http://" || s[:8] == "https://")
}

func (a *API) createPanel(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	d := a.B.DB
	name, typ, url := f.str("name"), f.str("type"), f.str("url")
	switch {
	case name == "":
		fail(w, 400, "name is required")
		return
	case !panelTypes[typ]:
		fail(w, 400, "unknown panel type")
		return
	case !validURL(url):
		fail(w, 400, "url must start with http:// or https://")
		return
	case d.Exists("marzban_panel", "name_panel", name):
		fail(w, 409, "a panel with this name already exists")
		return
	}
	user := f.str("username")
	if typ == "s_ui" || typ == "wgdashboard" {
		user = "none"
	}
	if panels.IsMitType(typ) {
		mu := f.str("marzban_url_direct")
		if !validURL(mu) {
			fail(w, 400, "marzban_url_direct is required for a Mit panel")
			return
		}
		mUser, mPass := f.str("marzban_username_direct"), f.str("marzban_password_direct")
		if mUser == "" {
			mUser = user
		}
		if mPass == "" {
			mPass = f.str("password")
		}
		d.Exec("INSERT INTO marzban_panel (name_panel,url_panel,username_panel,password_panel,type,inboundid,sublink,configManual,MethodUsername,statusTest,status,onholdstatus,marzban_url_direct,marzban_username_direct,marzban_password_direct) VALUES (?, ?, ?, ?, ?,?,?,?,?,?,?,?,?,?,?)",
			name, url, user, f.str("password"), typ, "0", "onsublink", "offconfig", bot.T("users.customidAndRandom"), "ontestshowpanel", "activepanel", "offonhold", mu, mUser, mPass)
	} else {
		inbound := f.str("inboundid")
		if inbound == "" {
			inbound = "0"
		}
		d.Exec("INSERT INTO marzban_panel (name_panel,url_panel,username_panel,password_panel,type,inboundid,sublink,configManual,MethodUsername,statusTest,status,onholdstatus) VALUES (?, ?, ?, ?, ?,?,?,?,?,?,?,?)",
			name, url, user, f.str("password"), typ, inbound, "onsublink", "offconfig", bot.T("users.customidAndRandom"), "ontestshowpanel", "activepanel", "offonhold")
		if ls := f.str("linksubx"); ls != "" {
			d.Update("marzban_panel", "linksubx", ls, "name_panel", name)
		}
	}
	ok(w, panelPublic(d.Select("marzban_panel", "*", "name_panel", name), true))
}

func (a *API) updatePanel(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	p := d.Select("marzban_panel", "*", "id", r.PathValue("id"))
	if p == nil {
		fail(w, 404, "panel not found")
		return
	}
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	id := p.S("id")
	set := func(col, v string) { d.Update("marzban_panel", col, v, "id", id) }
	credsChanged := false
	for _, k := range []string{"url_panel", "marzban_url_direct"} {
		if f.has(k) {
			if !validURL(f.str(k)) {
				fail(w, 400, k+" must start with http:// or https://")
				return
			}
			set(k, f.str(k))
			credsChanged = true
		}
	}
	for _, k := range []string{"username_panel", "password_panel", "marzban_username_direct", "marzban_password_direct"} {
		if f.has(k) && f.str(k) != "" {
			set(k, f.str(k))
			credsChanged = true
		}
	}
	for _, k := range []string{"inboundid", "linksubx", "MethodUsername"} {
		if f.has(k) {
			set(k, f.str(k))
		}
	}
	enum := map[string][2]string{
		"status":       {"activepanel", "disablepanel"},
		"statusTest":   {"ontestshowpanel", "offtestshowpanel"},
		"sublink":      {"onsublink", "offsublink"},
		"configManual": {"onconfig", "offconfig"},
		"onholdstatus": {"ononhold", "offonhold"},
	}
	for k, vals := range enum {
		if f.has(k) {
			v := vals[1]
			if f.boolean(k) || f.str(k) == vals[0] {
				v = vals[0]
			}
			set(k, v)
		}
	}
	if credsChanged {
		d.Exec("UPDATE marzban_panel SET datelogin = NULL WHERE id = ?", id)
	}
	if f.has("name") {
		nn := f.str("name")
		old := p.S("name_panel")
		if nn == "" {
			fail(w, 400, "name cannot be empty")
			return
		}
		if nn != old {
			if d.Exists("marzban_panel", "name_panel", nn) {
				fail(w, 409, "a panel with this name already exists")
				return
			}
			set("name_panel", nn)
			d.Exec("UPDATE invoice SET Service_location = ? WHERE Service_location = ?", nn, old)
			d.Exec("UPDATE product SET Location = ? WHERE Location = ?", nn, old)
		}
	}
	ok(w, panelPublic(d.Select("marzban_panel", "*", "id", id), true))
}

func (a *API) deletePanel(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	p := d.Select("marzban_panel", "*", "id", r.PathValue("id"))
	if p == nil {
		fail(w, 404, "panel not found")
		return
	}
	d.Exec("DELETE FROM marzban_panel WHERE id = ?", p.S("id"))
	ok(w, nil)
}

func (a *API) testPanel(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	pm := a.B.PM
	p := d.Select("marzban_panel", "*", "id", r.PathValue("id"))
	if p == nil {
		fail(w, 404, "panel not found")
		return
	}
	res := map[string]any{"type": p.S("type")}
	switch p.S("type") {
	case "marzban":
		tok := pm.MarzbanToken(p)
		if _, okT := tok["access_token"]; okT {
			st := pm.MarzbanSystemStats(p)
			res["ok"], res["version"], res["users"] = true, st["version"], st["total_user"]
		} else {
			res["ok"], res["error"] = false, tok
		}
	case "mit", "nexra":
		dash := pm.MitDashboard(p)
		if e, bad := dash["detail"]; bad {
			res["ok"], res["error"] = false, e
		} else {
			res["ok"] = true
			res["remaining_gb"] = php.Round(php.Floatval(jmapS(dash, "remaining_traffic"))/(1024*1024*1024), 2)
		}
	case "marzneshin":
		tok := pm.MarzneshinToken(p)
		_, okT := tok["access_token"]
		res["ok"] = okT
		if !okT {
			res["error"] = tok
		}
	case "x-ui_single":
		_, body := pm.XUILogin(p, false)
		res["ok"], res["reply"] = body["success"] == true, body
	case "alireza":
		_, body := pm.AlirezaLogin(p)
		res["ok"], res["reply"] = body["success"] == true, body
	case "mikrotik":
		_, bad := pm.MikrotikLogin(p)["error"]
		res["ok"] = !bad
	default:
		res["ok"] = nil
		res["note"] = "no connection test for this panel type"
	}
	ok(w, res)
}

func jmapS(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return jsonOf(v)
	}
}
