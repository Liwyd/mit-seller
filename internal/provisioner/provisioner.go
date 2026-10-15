// Package provisioner lets Mit Panel install a new bot on this server.
//
// It is deliberately small: one authenticated POST endpoint that runs
// `install.sh new` for a single validated request, and then reports the
// address and the two API keys back. It exists because the panel runs in a
// container with no shell and no SSH, so asking over HTTP is the only thing
// it can do.
//
// Everything the caller sends is treated as hostile. `install.sh new` does no
// sanitizing of its own — it interpolates TOKEN straight into a curl URL — so
// this package rejects anything that is not an exact, boring shape, and hands
// the surviving values to the shell as environment variables rather than as a
// command line. That is the whole reason this is a separate program and not a
// curl one-liner in the panel.
package provisioner

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Liwyd/mit-seller/internal/config"
)

// DefaultPort is where the provisioner listens. It is fixed so the panel only
// needs the server's address and not a second setting to get wrong.
const DefaultPort = 18100

// DefaultInstaller is where install.sh lives on a normal server. The unit file
// install.sh writes passes its own path, so this is only a fallback.
const DefaultInstaller = "/root/install.sh"

// DefaultConfigDir is where the bot configs live (see install.sh ETC).
const DefaultConfigDir = "/etc/mitseller"

var (
	tokenRE     = regexp.MustCompile(`^[0-9]{3,20}:[A-Za-z0-9_-]{30,}$`)
	domainRE    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	adminIDRE   = regexp.MustCompile(`^[0-9]{1,20}$`)
	emailRE     = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	secretRE    = regexp.MustCompile(`^[A-Za-z0-9._@%+=:,/-]{8,128}$`)
	botNumberRE = regexp.MustCompile(`^[0-9]{1,3}$`)
)

// Request is what Mit Panel posts. Every field is optional except the three
// that identify the bot; the rest the server fills in.
type Request struct {
	Token     string `json:"token"`
	Domain    string `json:"domain"`
	AdminID   string `json:"admin_id"`
	BotNumber string `json:"n,omitempty"`
	CertEmail string `json:"cert_email,omitempty"`
	MitSecret string `json:"mit_secret,omitempty"`
	// PanelURL/User/Pass are optional: with them the bot registers itself
	// with Mit Panel before answering, so it shows up there immediately.
	PanelURL  string `json:"panel_url,omitempty"`
	PanelUser string `json:"panel_user,omitempty"`
	PanelPass string `json:"panel_pass,omitempty"`
}

// Result is what comes back on success.
type Result struct {
	N            int    `json:"n"`
	URL          string `json:"url"`
	Domain       string `json:"domain"`
	BotUsername  string `json:"bot_username"`
	OwnerKey     string `json:"owner_key"`
	ManagerKey   string `json:"manager_key"`
	MitSecret    string `json:"mit_secret"`
	Registered   bool   `json:"registered"`
	AssignedTo   string `json:"assigned_to,omitempty"`
	RegisterNote string `json:"register_note,omitempty"`
}

// Server serves /provision and /health.
type Server struct {
	// Token is the bearer token the panel must send. Required.
	Token string
	// Installer is the path to install.sh.
	Installer string
	// ConfigDir holds botN.env.
	ConfigDir string
	// LockPath serializes installs; one at a time.
	LockPath string
	// Log receives one line per request. Never receives the token.
	Logger *log.Logger
	// LockTimeout is how long /provision waits for a concurrent install.
	// Default 10 minutes, which is longer than install.sh new has ever taken.
	LockTimeout time.Duration
	// Run does the actual install; a test replaces it.
	Run func(n int, req Request, secret string) error

	// rate limit for /provision: a plain fixed window, one client (the panel).
	mu       sync.Mutex
	window   time.Time
	requests int
}

func (s *Server) installer() string {
	if s.Installer != "" {
		return s.Installer
	}
	return DefaultInstaller
}

func (s *Server) configDir() string {
	if s.ConfigDir != "" {
		return s.ConfigDir
	}
	return DefaultConfigDir
}

func (s *Server) lockPath() string {
	if s.LockPath != "" {
		return s.LockPath
	}
	return "/run/mitseller-provision.lock"
}

func (s *Server) logger() *log.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return log.Default()
}

// Handler wires the routes and the authentication around them. /health is
// open: it answers a bare liveness probe with {"ok":true} and nothing else.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.Handle("POST /provision", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeErr(w, http.StatusUnauthorized, "not authorized")
			return
		}
		s.provision(w, r)
	}))
	return mux
}

func (s *Server) authorized(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	got = strings.TrimSpace(got)
	if got == "" || s.Token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	// No version banner: this port is reachable from the internet and its only
	// job is to say "I am here" to a caller that already knows the token.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) provision(w http.ResponseWriter, r *http.Request) {
	if !s.allowProvision() {
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	if r.ContentLength > 16*1024 {
		writeErr(w, http.StatusRequestEntityTooLarge, "request is too large")
		return
	}
	var req Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		s.logger().Printf("provision request for %s (n=%s)", req.Domain, orDash(req.BotNumber))
		_ = h
	}

	unlock, err := s.acquireLock()
	if err != nil {
		writeErr(w, http.StatusConflict, "another install is running: "+err.Error())
		return
	}
	defer unlock()

	n, err := s.resolveNumber(req.BotNumber)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	secret := req.MitSecret
	if secret == "" {
		secret, err = newSecret()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not make a secret")
			return
		}
	}

	if err := s.run(n, req, secret); err != nil {
		s.logger().Printf("provision failed for %s: %v", req.Domain, scrub(err.Error(), req.Token))
		writeErr(w, http.StatusBadGateway, scrub(err.Error(), req.Token))
		return
	}

	res := Result{N: n, Domain: req.Domain, URL: "https://" + req.Domain, MitSecret: secret}
	cfg, err := config.Load(filepath.Join(s.configDir(), fmt.Sprintf("bot%d.env", n)))
	if err != nil {
		s.logger().Printf("installed bot %d but could not read its config: %v", n, err)
		writeErr(w, http.StatusInternalServerError, "installed, but reading its config failed: "+err.Error())
		return
	}
	res.OwnerKey = cfg.APIOwnerKey
	res.ManagerKey = cfg.APIManagerKey
	res.BotUsername = cfg.BotUsername
	if cfg.PublicURL != "" {
		res.URL = cfg.PublicURL
	}

	if req.PanelURL != "" && req.PanelUser != "" && req.PanelPass != "" {
		res.Registered, res.AssignedTo, res.RegisterNote = s.register(n, req)
	}

	s.logger().Printf("provisioned bot %d for %s (@%s)", n, req.Domain, res.BotUsername)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": res})
}

// allowProvision is a fixed-window limit: at most 10 provisioning calls a
// minute. The panel is the only caller and it asks once per bot, so this is
// generous; it exists to stop a leaked token from being a machine.
func (s *Server) allowProvision() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Sub(s.window) >= time.Minute {
		s.window = now
		s.requests = 0
	}
	s.requests++
	return s.requests <= 10
}

// acquireLock takes an exclusive flock so two panels (or a retried request)
// cannot run install.sh new at once: certbot, nginx and mysql all misbehave
// under concurrency.
func (s *Server) acquireLock() (func(), error) {
	path := s.lockPath()
	if dir := filepath.Dir(path); dir != "" && dir != "/" {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock file: %w", err)
	}
	timeout := s.LockTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("timed out waiting for %s", path)
		}
		time.Sleep(2 * time.Second)
	}
}

// resolveNumber picks the bot number: the caller's if given and free, otherwise
// the lowest one that is not installed yet.
func (s *Server) resolveNumber(want string) (int, error) {
	installed, err := s.installedNumbers()
	if err != nil {
		return 0, err
	}
	taken := map[int]bool{}
	for _, n := range installed {
		taken[n] = true
	}
	if want != "" {
		n, err := strconv.Atoi(want)
		if err != nil || n < 1 || n > 999 {
			return 0, fmt.Errorf("bot number %q is not a number between 1 and 999", want)
		}
		if taken[n] {
			return 0, fmt.Errorf("bot %d is already installed here", n)
		}
		if _, err := os.Stat(filepath.Join(s.configDir(), fmt.Sprintf("bot%d.env", n))); err == nil {
			return 0, fmt.Errorf("bot %d is already installed here", n)
		}
		return n, nil
	}
	n := 1
	for taken[n] {
		n++
	}
	return n, nil
}

func (s *Server) installedNumbers() ([]int, error) {
	entries, err := os.ReadDir(s.configDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", s.configDir(), err)
	}
	var out []int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "bot") || !strings.HasSuffix(name, ".env") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "bot"), ".env"))
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// defaultRun shells out to install.sh new. The values travel as environment
// variables, never as part of a command line, so a value that somehow slipped
// past Validate is still just data.
func (s *Server) defaultRun(n int, req Request, secret string) error {
	cmd := exec.Command(s.installer(), "new")
	cmd.Dir = filepath.Dir(s.installer())
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("N=%d", n),
		"TOKEN="+req.Token,
		"DOMAIN="+req.Domain,
		"ADMIN="+req.AdminID,
		"MIT_SECRET="+secret,
		"CERT_EMAIL="+req.CertEmail,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install.sh new failed: %v: %s", err, strings.TrimSpace(scrub(string(out), req.Token)))
	}
	return nil
}

func (s *Server) run(n int, req Request, secret string) error {
	if s.Run != nil {
		return s.Run(n, req, secret)
	}
	return s.defaultRun(n, req, secret)
}

// register has the fresh bot connect to Mit Panel the same way install.sh's
// `register` does, so it appears there without anyone having to.
func (s *Server) register(n int, req Request) (bool, string, string) {
	cmd := exec.Command(s.installer(), "register")
	cmd.Dir = filepath.Dir(s.installer())
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("N=%d", n),
		"MIT_PANEL_URL="+req.PanelURL,
		"MIT_PANEL_USER="+req.PanelUser,
		"MIT_PANEL_PASS="+req.PanelPass,
	)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(scrub(string(out), req.Token))
	if err != nil {
		return false, "", fmt.Sprintf("the bot is installed; registering it with Mit Panel failed: %v: %s", err, text)
	}
	assigned := ""
	if i := strings.LastIndex(text, "given to "); i >= 0 {
		assigned = strings.TrimSpace(text[i+len("given to "):])
		if j := strings.IndexAny(assigned, " ("); j >= 0 {
			assigned = assigned[:j]
		}
	}
	return true, assigned, text
}

// Validate is the security boundary. Anything that does not match exactly is
// refused before it can reach a shell.
func (r *Request) Validate() error {
	switch {
	case r.Token == "":
		return fmt.Errorf("token is required")
	case len(r.Token) > 200 || !tokenRE.MatchString(r.Token):
		return fmt.Errorf("token is not a BotFather token")
	case r.Domain == "":
		return fmt.Errorf("domain is required")
	case len(r.Domain) > 253 || !domainRE.MatchString(r.Domain):
		return fmt.Errorf("domain is not a hostname")
	case strings.Contains(r.Domain, "--"):
		// a hostname may contain "--", but certbot takes anything that
		// starts with a dash as a flag, so it is never worth the risk
		return fmt.Errorf("domain must not contain --")
	case r.AdminID == "" || !adminIDRE.MatchString(r.AdminID):
		return fmt.Errorf("admin_id must be a numeric Telegram id")
	case r.BotNumber != "" && !botNumberRE.MatchString(r.BotNumber):
		return fmt.Errorf("n must be a number")
	case r.CertEmail != "" && (len(r.CertEmail) > 254 || !emailRE.MatchString(r.CertEmail)):
		return fmt.Errorf("cert_email is not an email address")
	case r.MitSecret != "" && !secretRE.MatchString(r.MitSecret):
		return fmt.Errorf("mit_secret must be 8-128 letters, digits or ._-@%%+=:,/")
	case len(r.PanelURL) > 512 || len(r.PanelUser) > 256 || len(r.PanelPass) > 256:
		return fmt.Errorf("panel credentials are too long")
	}
	if r.PanelURL != "" && !strings.HasPrefix(r.PanelURL, "http://") && !strings.HasPrefix(r.PanelURL, "https://") {
		return fmt.Errorf("panel_url must start with http:// or https://")
	}
	for _, v := range []string{r.PanelURL, r.PanelUser, r.PanelPass, r.CertEmail} {
		if strings.ContainsAny(v, "\r\n\t\x00") {
			return fmt.Errorf("control characters are not allowed")
		}
	}
	return nil
}

func newSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// scrub removes a secret from text before it is logged or returned.
func scrub(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "<token>")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func writeJSON(w http.ResponseWriter, code int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}
