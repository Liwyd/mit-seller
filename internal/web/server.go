// Package web serves everything the PHP bot served over HTTP, at the same
// paths, so nginx can be pointed at this process without touching the
// Telegram webhook, payment callbacks or the paired Android app.
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MHBehzadian/nexra-mirzabot/internal/bot"
	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
	"github.com/MHBehzadian/nexra-mirzabot/internal/tg"
)

type Server struct {
	B   *bot.Bot
	API http.Handler // management API (mounted at /api/)
}

var telegramNets = mustCIDRs("149.154.160.0/20", "91.108.4.0/22")

func mustCIDRs(cs ...string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range cs {
		_, n, err := net.ParseCIDR(c)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

// clientIP honours X-Real-IP / X-Forwarded-For only from a local proxy.
func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			if p := net.ParseIP(strings.TrimSpace(xr)); p != nil {
				return p
			}
		}
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			parts := strings.Split(xf, ",")
			if p := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); p != nil {
				return p
			}
		}
	}
	return ip
}

func fromTelegram(ip net.IP) bool {
	for _, n := range telegramNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/index.php", s.webhook)
	mux.HandleFunc("/autopay.php", s.autopay)
	mux.HandleFunc("/payment/nowpayments/back.php", s.nowpaymentsBack)
	mux.HandleFunc("/payment/nowpayments/index.php", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "illegal access") })
	mux.HandleFunc("/payment/aqayepardakht/aqayepardakht.php", s.aqayepardakhtStart)
	mux.HandleFunc("/payment/aqayepardakht/back.php", s.aqayepardakhtBack)
	mux.HandleFunc("/cron/", func(w http.ResponseWriter, r *http.Request) {
		// the jobs run inside the bot now; old crontab lines are harmless
		io.WriteString(w, "")
	})
	mux.HandleFunc("/table.php", func(w http.ResponseWriter, r *http.Request) {
		err := s.B.DB.EnsureSchema(s.B.Cfg.AdminID, nil)
		if err != nil {
			http.Error(w, "schema error", 500)
			return
		}
		io.WriteString(w, "ok")
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.B.DB.Ping(); err != nil {
			http.Error(w, "db down", 503)
			return
		}
		io.WriteString(w, "ok nexrabot "+bot.Version)
	})
	if s.API != nil {
		mux.Handle("/api/", s.API)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && r.Method == http.MethodPost {
			s.webhook(w, r)
			return
		}
		http.NotFound(w, r)
	})
	return mux
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		io.WriteString(w, "")
		return
	}
	cfg := s.B.Cfg
	given := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	ok := false
	switch {
	case cfg.WebhookSecret != "" && given != "":
		ok = subtle.ConstantTimeCompare([]byte(given), []byte(cfg.WebhookSecret)) == 1
	case cfg.CheckTGIP:
		// a webhook registered by the PHP bot carries no secret; it is
		// accepted by source address, as checktelegramip() did
		ok = fromTelegram(clientIP(r))
	default:
		ok = cfg.WebhookSecret == ""
	}
	if !ok {
		http.Error(w, "Unauthorized access", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return
	}
	var u tg.Update
	if json.Unmarshal(body, &u) != nil {
		io.WriteString(w, "")
		return
	}
	if cfg.SyncUpdates {
		s.B.Handle(&u)
	} else {
		s.B.Enqueue(&u)
	}
	io.WriteString(w, "")
}

// ---------------------------------------------------------------- autopay.php

func autopayOut(w http.ResponseWriter, code int, v map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	b, _ := json.Marshal(v)
	w.Write(b)
}

func (s *Server) autopay(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if len(raw) == 0 {
		autopayOut(w, 400, map[string]any{"ok": false, "error": "empty body"})
		return
	}
	b := s.B
	set := b.AutopaySettings()
	mac := hmac.New(sha256.New, []byte(set.S("device_key")))
	mac.Write(raw)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Autopay-Signature"))) {
		autopayOut(w, 401, map[string]any{"ok": false, "error": "bad signature"})
		return
	}
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil || in["action"] == nil {
		autopayOut(w, 400, map[string]any{"ok": false, "error": "bad payload"})
		return
	}
	b.AutopaySet("last_seen", php.DateNow("Y-m-d H:i:s"))
	if dev, ok := in["device"]; ok {
		ds := php.ToString(dev)
		if utf8.RuneCountInString(ds) > 190 {
			ds = string([]rune(ds)[:190])
		}
		b.AutopaySet("device_info", ds)
	}
	str := func(m map[string]any, k string) string {
		if v, ok := m[k]; ok && v != nil {
			switch x := v.(type) {
			case string:
				return x
			default:
				return php.ToString(x)
			}
		}
		return ""
	}
	action := str(in, "action")
	switch action {
	case "ping", "heartbeat":
		autopayOut(w, 200, map[string]any{"ok": true, "enabled": b.AutopayEnabled(), "server": php.DateNow("Y-m-d H:i:s")})
	case "sms":
		if !b.AutopayEnabled() {
			autopayOut(w, 200, map[string]any{"ok": true, "result": "disabled"})
			return
		}
		body := str(in, "body")
		sentAt := str(in, "sent_at")
		if _, ok := in["sent_at"]; !ok {
			sentAt = php.DateNow("Y-m-d H:i:s")
		}
		if strings.TrimSpace(body) == "" {
			autopayOut(w, 400, map[string]any{"ok": false, "error": "empty sms"})
			return
		}
		res := b.AutopayHandleSMS(body, str(in, "sender"), sentAt)
		res["ok"] = true
		autopayOut(w, 200, res)
	case "batch":
		if !b.AutopayEnabled() {
			autopayOut(w, 200, map[string]any{"ok": true, "result": "disabled"})
			return
		}
		items, _ := in["items"].([]any)
		results := []any{}
		for _, it := range items {
			m, _ := it.(map[string]any)
			body := str(m, "body")
			if strings.TrimSpace(body) == "" {
				continue
			}
			sentAt := str(m, "sent_at")
			if _, ok := m["sent_at"]; !ok {
				sentAt = php.DateNow("Y-m-d H:i:s")
			}
			results = append(results, b.AutopayHandleSMS(body, str(m, "sender"), sentAt))
		}
		autopayOut(w, 200, map[string]any{"ok": true, "count": len(results), "results": results})
	default:
		autopayOut(w, 400, map[string]any{"ok": false, "error": "unknown action"})
	}
}

// ---------------------------------------------------------------- payments

func (s *Server) nowpaymentsBack(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	if php.ToString(data["payment_status"]) != "finished" {
		return
	}
	s.B.NowPaymentsIPN(php.ToString(data["payment_id"]))
}

func (s *Server) aqayepardakhtStart(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	amount := template.HTMLEscapeString(q.Get("price"))
	order := template.HTMLEscapeString(q.Get("order_id"))
	loc, msg := s.B.AqayepardakhtCreate(amount, order)
	if loc != "" {
		http.Redirect(w, r, loc, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, template.HTMLEscapeString(msg))
}

var backPage = template.Must(template.New("back").Parse(`<html>
<head>
    <meta charset="utf-8">
    <title>{{.Title}}</title>
    <style>
        body { font-family: vazir, Tahoma, sans-serif; background-color: #f2f2f2; margin: 0; padding: 20px; display: flex; justify-content: center; align-items: center; min-height: 100vh; direction: rtl; }
        .confirmation-box { background-color: #ffffff; border-radius: 8px; width: 25%; min-width: 260px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); padding: 40px; text-align: center; }
        h1 { color: #333333; margin-bottom: 20px; }
        p { color: #666666; margin-bottom: 10px; }
        .btn { display: block; margin: 10px 0; padding: 10px 20px; background-color: #49b200; color: #fff; text-decoration: none; border-radius: 10px; }
    </style>
</head>
<body>
<div class="confirmation-box">
    <h1>{{.Status}}</h1>
    <p>{{.TxLabel}}<span>{{.Invoice}}</span></p>
    <p>{{.AmountLabel}} <span>{{.Price}}</span>{{.Currency}}</p>
    <p>{{.DateLabel}} <span>{{.Date}}</span></p>
    <p>{{.Dec}}</p>
    <a class="btn" href="https://t.me/{{.Bot}}">{{.Back}}</a>
</div>
</body>
</html>`))

func (s *Server) aqayepardakhtBack(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	invoice := template.HTMLEscapeString(r.PostForm.Get("invoice_id"))
	transid := template.HTMLEscapeString(r.PostForm.Get("transid"))
	status, dec, price := s.B.AqayepardakhtVerify(invoice, transid)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = backPage.Execute(w, map[string]string{
		"Title":       bot.T("users.moeny.invoice_title"),
		"Status":      status,
		"TxLabel":     bot.T("users.moeny.transaction_number"),
		"Invoice":     invoice,
		"AmountLabel": bot.T("users.moeny.payment_amount"),
		"Price":       price,
		"Currency":    bot.T("users.moeny.currency"),
		"DateLabel":   bot.T("users.moeny.date_label"),
		"Date":        php.JdateNow("Y/m/d"),
		"Dec":         dec,
		"Bot":         s.B.Cfg.BotUsername,
		"Back":        bot.T("users.moeny.back_to_bot"),
	})
}

// ListenAndServe runs the HTTP server until it fails.
func (s *Server) Run(addr string) *http.Server {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 15 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second}
	return srv
}

var _ = url.Values{}
