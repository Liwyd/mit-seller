// Package panels is the Go version of panels.php and the per-panel driver
// files. Each driver keeps the request shapes, token caching (in the
// marzban_panel.datelogin column) and result conventions of the PHP code.
package panels

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

type req struct {
	method      string
	url         string
	body        []byte
	contentType string
	headers     map[string]string
	timeout     time.Duration
	insecure    bool
	follow      bool
	basicUser   string
	basicPass   string
	cookie      string
	userAgent   string
}

type resp struct {
	code    int
	body    []byte
	cookies []*http.Cookie
	err     error
}

var (
	secureTr   = &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
	insecureTr = &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
)

// defaultTimeout replaces curl's "no timeout" so one dead panel cannot hang
// the bot forever.
const defaultTimeout = 30 * time.Second

func (r req) do() resp {
	if r.timeout == 0 {
		r.timeout = defaultTimeout
	}
	tr := secureTr
	if r.insecure {
		tr = insecureTr
	}
	client := &http.Client{Transport: tr, Timeout: r.timeout}
	if !r.follow {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	method := r.method
	if method == "" {
		method = "GET"
	}
	hr, err := http.NewRequestWithContext(ctx, method, r.url, body)
	if err != nil {
		return resp{err: err}
	}
	if r.contentType != "" {
		hr.Header.Set("Content-Type", r.contentType)
	}
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	if r.basicUser != "" || r.basicPass != "" {
		hr.SetBasicAuth(r.basicUser, r.basicPass)
	}
	if r.cookie != "" {
		hr.Header.Set("Cookie", r.cookie)
	}
	if r.userAgent != "" {
		hr.Header.Set("User-Agent", r.userAgent)
	}
	res, err := client.Do(hr)
	if err != nil {
		return resp{err: err}
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	return resp{code: res.StatusCode, body: b, cookies: res.Cookies(), err: err}
}

// jsonObj decodes a JSON object; nil when the body is not an object.
func jsonObj(b []byte) map[string]any {
	m, _ := jsonAny(b).(map[string]any)
	return m
}

func jsonAny(b []byte) any {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return nil
	}
	return v
}

func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func formBody(kv ...string) []byte {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return []byte(v.Encode())
}

// helpers for reading decoded JSON
func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	switch v := m[k].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return php.NumStr(v)
	case bool:
		if v {
			return "1"
		}
	}
	return ""
}

func num(m map[string]any, k string) float64 {
	if m == nil {
		return 0
	}
	return toF(m[k])
}

func toF(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := json.Number(x).Float64()
		return f
	case bool:
		if x {
			return 1
		}
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != "" && x != "0"
	case float64:
		return x != 0
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

func isset(m map[string]any, k string) bool {
	if m == nil {
		return false
	}
	v, ok := m[k]
	return ok && v != nil
}

func strList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		switch s := x.(type) {
		case string:
			out = append(out, s)
		case nil:
		default:
			out = append(out, string(marshal(s)))
		}
	}
	return out
}

var subURLRe = regexp.MustCompile(`^(https?://)?([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}(:\d+)?((/[^\s/]+)+)?$`)

// fixSubURL prefixes a relative subscription path with the panel URL.
func fixSubURL(sub, base string) string {
	if !subURLRe.MatchString(sub) {
		return base + "/" + strings.TrimLeft(sub, "/")
	}
	return sub
}

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

// OutputLink is outputlink(): fetch a subscription URL like a browser.
func OutputLink(u string) string {
	if u == "" {
		return ""
	}
	r := req{url: u, follow: true, insecure: true, userAgent: browserUA, timeout: 20 * time.Second}.do()
	if r.err != nil {
		return ""
	}
	return string(r.body)
}

// IsBase64 is isBase64(): strict decode that re-encodes to the same string.
func IsBase64(s string) bool {
	d, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// PHP's strict base64_decode tolerates missing padding only if the
		// re-encode still matches, which then fails anyway.
		return false
	}
	return base64.StdEncoding.EncodeToString(d) == s
}

// DecodeIfBase64 returns the decoded text when s is base64.
func DecodeIfBase64(s string) string {
	if IsBase64(s) {
		d, _ := base64.StdEncoding.DecodeString(s)
		return string(d)
	}
	return s
}

var errNoPanel = errors.New("panel not found")
