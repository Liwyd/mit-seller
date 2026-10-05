package panels

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

// Out mirrors the associative arrays ManagePanel returned in PHP.
type Out map[string]any

func (o Out) S(k string) string {
	if o == nil {
		return ""
	}
	switch v := o[k].(type) {
	case string:
		return v
	case nil:
		return ""
	case json.Number:
		return v.String()
	case float64:
		return php.NumStr(v)
	case int64:
		return php.NumStr(float64(v))
	case int:
		return php.NumStr(float64(v))
	case bool:
		if v {
			return "1"
		}
		return ""
	default:
		return string(marshal(v))
	}
}

// Isset is PHP isset($o[k]).
func (o Out) Isset(k string) bool {
	if o == nil {
		return false
	}
	v, ok := o[k]
	return ok && v != nil
}

func (o Out) F(k string) float64 {
	if o == nil {
		return 0
	}
	return toF(o[k])
}

// Truthy is PHP's truthiness of $o[k].
func (o Out) Truthy(k string) bool {
	if o == nil {
		return false
	}
	return truthy(o[k])
}

func (o Out) List(k string) []string {
	if o == nil {
		return nil
	}
	switch v := o[k].(type) {
	case []string:
		return v
	case []any:
		return strList(v)
	}
	return nil
}

// IsArrayList reports whether $o[k] is an array (is_array).
func (o Out) IsArrayList(k string) bool {
	if o == nil {
		return false
	}
	switch o[k].(type) {
	case []string, []any:
		return true
	}
	return false
}

// MsgJSON is json_encode($o['msg']).
func (o Out) MsgJSON() string {
	if o == nil {
		return "null"
	}
	return string(marshal(o["msg"]))
}

func unsuccessful(msg any) Out { return Out{"status": "Unsuccessful", "msg": msg} }

// Manager is ManagePanel plus the panel-type drivers.
type Manager struct {
	DB *db.DB

	mu       sync.Mutex
	versions map[string]versionEntry // Marzban version per panel, short-lived
}

type versionEntry struct {
	above084 bool
	at       time.Time
}

func New(d *db.DB) *Manager { return &Manager{DB: d, versions: map[string]versionEntry{}} }

func (m *Manager) panelByName(name string) db.Row {
	return m.DB.Select("marzban_panel", "*", "name_panel", name)
}

func (m *Manager) panelByID(id string) db.Row {
	return m.DB.Select("marzban_panel", "*", "id", id)
}

// --------------------------------------------------------------------------
// datelogin cache helpers (same JSON layout the PHP drivers wrote)

func loginCache(panel db.Row) map[string]any {
	c := jsonObj([]byte(panel.S("datelogin")))
	if c == nil {
		c = map[string]any{}
	}
	return c
}

func fresh(entry map[string]any, maxAge int64) (string, bool) {
	if entry == nil {
		return "", false
	}
	t, ok := entry["time"].(string)
	if !ok {
		return "", false
	}
	ts, ok := php.Strtotime(t)
	if !ok {
		return "", false
	}
	if time.Now().Unix()-ts > maxAge {
		return "", false
	}
	tok, _ := entry["access_token"].(string)
	return tok, true
}

func nowStamp() string { return php.DateNow("Y/m/d H:i:s") }

func (m *Manager) saveLogin(field, val string, panel db.Row, data any) {
	m.DB.Update("marzban_panel", "datelogin", string(marshal(data)), field, val)
	_ = panel
}

// --------------------------------------------------------------------------
// ManagePanel API

// CreateUser is ManagePanel::createUser.
func (m *Manager) CreateUser(namePanel, username string, expire int64, dataLimit float64, isTest bool) Out {
	p := m.panelByName(namePanel)
	switch p.S("type") {
	case "marzban":
		return m.marzbanCreate(p, username, expire, dataLimit, isTest)
	case "marzneshin":
		return m.marzneshinCreate(p, username, expire, dataLimit)
	case "x-ui_single":
		return m.xuiCreate(p, username, expire, dataLimit)
	case "alireza":
		return m.alirezaCreate(p, username, expire, dataLimit)
	case "s_ui":
		return m.suiCreate(p, username, expire, dataLimit)
	case "wgdashboard":
		return m.wgCreate(p, username, expire, dataLimit)
	case "mikrotik":
		return m.mikrotikCreate(p, username)
	case "nexra":
		return m.nexraCreate(p, username, expire, dataLimit)
	}
	return unsuccessful("Panel Not Found")
}

// DataUser is ManagePanel::DataUser.
func (m *Manager) DataUser(namePanel, username string) Out {
	p := m.panelByName(namePanel)
	if p == nil {
		return unsuccessful("")
	}
	switch p.S("type") {
	case "marzban":
		return m.marzbanData(p, username)
	case "marzneshin":
		return m.marzneshinData(p, username)
	case "x-ui_single":
		return m.xuiData(p, username)
	case "alireza":
		return m.alirezaData(p, username)
	case "s_ui":
		return m.suiData(p, username)
	case "wgdashboard":
		return m.wgData(p, username)
	case "mikrotik":
		return m.mikrotikData(p, username)
	case "nexra":
		return m.nexraData(p, username)
	}
	return unsuccessful("Panel Not Found")
}

// RevokeSub is ManagePanel::Revoke_sub.
func (m *Manager) RevokeSub(namePanel, username string) Out {
	p := m.panelByName(namePanel)
	switch p.S("type") {
	case "marzban":
		return m.marzbanRevoke(p, username)
	case "marzneshin":
		return m.marzneshinRevoke(p, username)
	case "x-ui_single":
		return m.xuiRevoke(p, username)
	case "alireza":
		return m.alirezaRevoke(p, username)
	case "s_ui":
		return m.suiRevoke(p, username)
	case "nexra":
		return unsuccessful("قابلیت تمدید لینک (Revoke Sub) برای پنل Nexra پشتیبانی نمی‌شود")
	}
	return unsuccessful("Panel Not Found")
}

// RemoveUser is ManagePanel::RemoveUser.
func (m *Manager) RemoveUser(namePanel, username string) Out {
	p := m.panelByName(namePanel)
	switch p.S("type") {
	case "marzban":
		return m.marzbanRemove(p, username)
	case "marzneshin":
		return m.marzneshinRemove(p, username)
	case "x-ui_single":
		return m.xuiRemove(p, username)
	case "alireza":
		// panels.php had no alireza branch here, so the service stayed on
		// the panel; removing it is what every caller expects.
		return m.alirezaRemove(p, username)
	case "s_ui":
		return m.suiRemove(p, username)
	case "wgdashboard":
		return m.wgRemove(p, username)
	case "mikrotik":
		return m.mikrotikRemove(p, username)
	case "nexra":
		return m.nexraRemove(p, username)
	}
	return unsuccessful("Panel Not Found")
}

// ResetUserDataUsage is ManagePanel::ResetUserDataUsage.
func (m *Manager) ResetUserDataUsage(namePanel, username string) {
	p := m.panelByName(namePanel)
	switch p.S("type") {
	case "marzban":
		m.marzbanReset(p, username)
	case "marzneshin":
		m.marzneshinReset(p, username)
	case "x-ui_single":
		m.xuiReset(p, username)
	case "alireza":
		m.alirezaReset(p, username)
	case "s_ui":
		m.suiReset(p, username)
	case "wgdashboard":
		m.WGAllowAccess(namePanel, username)
		u := m.WGGetUser(username, namePanel)
		m.WGResetData(str(u, "id"), namePanel)
	case "nexra":
		m.nexraReset(p, username)
	}
}

// Modifyuser is ManagePanel::Modifyuser. config holds the PHP $config array.
func (m *Manager) Modifyuser(username, namePanel string, config map[string]any) Out {
	p := m.panelByName(namePanel)
	switch p.S("type") {
	case "marzban":
		return Out(m.marzbanModify(p, username, config))
	case "marzneshin":
		return m.marzneshinModify(p, username, config)
	case "x-ui_single":
		return m.xuiModify(p, username, config)
	case "alireza":
		return m.alirezaModify(p, username, config)
	case "s_ui":
		return m.suiModify(p, username, config)
	case "nexra":
		return m.nexraModify(p, username, config)
	}
	return nil
}

// PanelType returns the type of a named panel ("" when unknown).
func (m *Manager) PanelType(name string) string { return m.panelByName(name).S("type") }

func trimSlash(s string) string { return strings.TrimRight(s, "/") }
