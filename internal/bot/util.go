package bot

import (
	crand "crypto/rand"
	"encoding/hex"
	"math"
	"strings"

	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/php"
)

// formatBytes is functions.php formatBytes() (log-based, PHP float output).
func formatBytes(bytes float64) string {
	suffixes := []string{T("users.format.byte"), T("users.format.kilobyte"), T("users.format.MBbyte"), T("users.format.GBbyte"), T("users.format.TBbyte")}
	base := math.Log(bytes) / math.Log(1024)
	power := 0.0
	if bytes > 0 {
		power = math.Floor(base)
	}
	v := php.Round(math.Pow(1024, base-power), 2)
	suf := ""
	if p := int(power); p >= 0 && p < len(suffixes) {
		suf = suffixes[p]
	}
	return php.FloatToString(v) + " " + suf
}

// serviceInfo is the status block several handlers computed from DataUser.
type serviceInfo struct {
	status, statusVar, expiration, lastTraffic, remaining, used, day string
}

func statusLabel(status string) string {
	switch status {
	case "active":
		return T("users.status.active")
	case "limited":
		return T("users.status.limited")
	case "disabled":
		return T("users.status.disabled")
	case "expired":
		return T("users.status.expired")
	case "on_hold":
		return T("users.status.onhold")
	}
	return ""
}

func describe(o panels.Out, dayPlusOne bool) serviceInfo {
	var s serviceInfo
	s.status = o.S("status")
	s.statusVar = statusLabel(s.status)
	expire := o.F("expire")
	if o.Truthy("expire") {
		s.expiration = php.Jdate("Y/m/d", int64(expire))
	} else {
		s.expiration = T("users.status.Unlimited")
	}
	limit := o.F("data_limit")
	if o.Truthy("data_limit") {
		s.lastTraffic = formatBytes(limit)
		s.remaining = formatBytes(limit - o.F("used_traffic"))
	} else {
		s.lastTraffic = T("users.status.Unlimited")
		s.remaining = T("users.unlimited")
	}
	if o.Truthy("used_traffic") {
		s.used = formatBytes(o.F("used_traffic"))
	} else {
		s.used = T("users.status.Notconsumed")
	}
	if o.Truthy("expire") {
		d := math.Floor((expire - float64(nowUnix())) / 86400)
		if dayPlusOne {
			d++
		}
		s.day = php.FloatToString(d) + T("users.status.day")
	} else {
		s.day = T("users.status.Unlimited")
	}
	return s
}

// filterValidateURL approximates filter_var($s, FILTER_VALIDATE_URL).
func filterValidateURL(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	i := strings.Index(s, ":")
	if i <= 0 {
		return false
	}
	scheme := s[:i]
	for k, r := range scheme {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || (k > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'))) {
			return false
		}
	}
	rest := s[i+1:]
	ls := strings.ToLower(scheme)
	if ls == "mailto" || ls == "news" || ls == "file" {
		return rest != ""
	}
	if !strings.HasPrefix(rest, "//") {
		return false
	}
	hostPart := rest[2:]
	if j := strings.IndexAny(hostPart, "/?#"); j >= 0 {
		hostPart = hostPart[:j]
	}
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	host := hostPart
	if strings.HasPrefix(host, "[") {
		end := strings.Index(host, "]")
		return end > 1
	}
	if j := strings.LastIndex(host, ":"); j >= 0 {
		port := host[j+1:]
		for _, r := range port {
			if r < '0' || r > '9' {
				return false
			}
		}
		host = host[:j]
	}
	if host == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return false
		}
		for k, r := range label {
			ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || (r == '-' && k > 0 && k < len(label)-1) || r == '_'
			if !ok {
				return false
			}
		}
	}
	return true
}

// validServiceUsername is preg_match('~(?!_)^[a-z][a-z\d_]{2,32}(?<!_)$~i').
func validServiceUsername(s string) bool {
	s = strings.TrimSuffix(s, "\n")
	if len(s) < 3 || len(s) > 33 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i] | 0x20
		isLetter := ch >= 'a' && ch <= 'z'
		isDigit := s[i] >= '0' && s[i] <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !(isLetter || isDigit || s[i] == '_') {
			return false
		}
	}
	return s[len(s)-1] != '_'
}

// wordName is preg_match('/^\w{3,32}$/').
func wordName(s string) bool {
	s = strings.TrimSuffix(s, "\n")
	if len(s) < 3 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_') {
			return false
		}
	}
	return true
}

// generateUsername is functions.php generateUsername(); "" for an unknown method.
func (c *Ctx) generateUsername(method, tgUsername, random, typed string) string {
	switch method {
	case T("users.customidAndRandom"):
		return c.fromID + "_" + random
	case T("users.customusernameandorder"):
		return tgUsername + "_" + random
	case T("users.customusernameorder"):
		n := c.db().Count("SELECT COUNT(id_user) FROM invoice WHERE id_user = ?", c.fromID) + 1
		return tgUsername + "_" + itoa(n)
	case T("users.customusername"):
		return typed
	case T("users.customtextandrandom"):
		return c.setting.S("namecustome") + "_" + random
	}
	return ""
}

// randHex is bin2hex(random_bytes(n)).
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}
