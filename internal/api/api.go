// Package api is the management API Nexra Panel uses to run a bot: products,
// categories, discounts, texts, buttons, payments, users, autopay and — with
// the owner key only — the VPN panels the bot sells from.
//
// Auth: "Authorization: Bearer <key>". API_OWNER_KEY may do everything;
// API_MANAGER_KEY may do everything except panel (server) management.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/MHBehzadian/nexra-mirzabot/internal/bot"
	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
)

type role int

const (
	roleNone role = iota
	roleManager
	roleOwner
)

type API struct {
	B   *bot.Bot
	mux *http.ServeMux
}

func New(b *bot.Bot) *API {
	a := &API{B: b, mux: http.NewServeMux()}
	a.routes()
	return a
}

func (a *API) role(r *http.Request) role {
	key := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if key == "" {
		key = r.Header.Get("X-Api-Key")
	}
	if key == "" {
		return roleNone
	}
	eq := func(k string) bool { return k != "" && subtle.ConstantTimeCompare([]byte(key), []byte(k)) == 1 }
	switch {
	case eq(a.B.Cfg.APIOwnerKey):
		return roleOwner
	case eq(a.B.Cfg.APIManagerKey):
		return roleManager
	}
	return roleNone
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.role(r) == roleNone {
		fail(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			a.B.Log.Printf("api panic %s %s: %v", r.Method, r.URL.Path, rec)
			fail(w, 500, "internal error")
		}
	}()
	a.mux.ServeHTTP(w, r)
}

type handler func(w http.ResponseWriter, r *http.Request)

func (a *API) handle(pattern string, need role, h handler) {
	a.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if a.role(r) < need {
			fail(w, http.StatusForbidden, "this action needs the owner key")
			return
		}
		h(w, r)
	})
}

func ok(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid JSON body: " + err.Error())
	}
	return nil
}

// fields is a loosely typed JSON object: numbers and strings both read as strings.
type fields map[string]any

func (f fields) has(k string) bool { _, ok := f[k]; return ok }

func (f fields) str(k string) string {
	switch v := f[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "1"
		}
		return "0"
	case nil:
		return ""
	}
	b, _ := json.Marshal(f[k])
	return string(b)
}

func (f fields) boolean(k string) bool {
	switch v := f[k].(type) {
	case bool:
		return v
	case string:
		return v == "1" || v == "true" || v == "on"
	case json.Number:
		return v.String() != "0"
	}
	return false
}

func rowMap(r db.Row, cols ...string) map[string]any {
	if r == nil {
		return nil
	}
	out := map[string]any{}
	if len(cols) == 0 {
		for k, v := range r {
			if v.Null {
				out[k] = nil
			} else {
				out[k] = v.S
			}
		}
		return out
	}
	for _, c := range cols {
		v, ok := r[c]
		if !ok || v.Null {
			out[c] = nil
		} else {
			out[c] = v.S
		}
	}
	return out
}

func rowsMap(rs []db.Row, cols ...string) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, rowMap(r, cols...))
	}
	return out
}

func pageArgs(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
