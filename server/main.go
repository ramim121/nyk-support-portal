// Command server is the NYK Support Portal signaling + web server. It:
//   - serves the technician console web app,
//   - authenticates technicians (bcrypt) and issues bearer tokens,
//   - accepts agent and console WebSocket connections and routes signaling,
//   - hands out short-lived TURN credentials for each session,
//   - records an audit log of every session.
//
// It holds no media: WebRTC audio/video and input flow peer-to-peer (or via the
// separate coturn relay), never through this process.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"golang.org/x/crypto/acme/autocert"

	"github.com/nyk/support-portal/protocol"
)

type config struct {
	addr       string
	domain     string
	dbPath     string
	orgKey     string
	consoleDir string
	dev        bool
	turn       turnConfig
}

func main() {
	// Subcommand: adduser.
	if len(os.Args) > 1 && os.Args[1] == "adduser" {
		runAddUser(os.Args[2:])
		return
	}

	cfg := parseFlags()

	store, err := OpenStore(cfg.dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if n, _ := store.CountTechs(); n == 0 {
		log.Printf("WARNING: no technician logins exist yet. Create one with:  server adduser <username>")
	}

	hub := NewHub(store, cfg.turn)
	tokens := newTokenStore(12 * time.Hour)
	srv := &httpServer{cfg: cfg, store: store, hub: hub, tokens: tokens}

	mux := http.NewServeMux()
	srv.routes(mux)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpSrv := &http.Server{
		Handler:      logRequests(mux),
		ReadTimeout:  0, // WebSockets are long-lived; no read timeout on the server.
		WriteTimeout: 0,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	if cfg.dev || cfg.domain == "" {
		httpSrv.Addr = cfg.addr
		log.Printf("NYK Support Portal server (dev/plain HTTP) listening on %s", cfg.addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
		return
	}

	// Production: automatic TLS via Let's Encrypt for the configured domain.
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.domain),
		Cache:      autocert.DirCache(cacheDir()),
	}
	httpSrv.Addr = ":443"
	httpSrv.TLSConfig = m.TLSConfig()
	// Port 80 for the ACME http-01 challenge and a redirect to HTTPS.
	go func() {
		h := m.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
		}))
		_ = http.ListenAndServe(":80", h)
	}()
	log.Printf("NYK Support Portal server listening on :443 for %s", cfg.domain)
	if err := httpSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve tls: %v", err)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.addr, "addr", ":8080", "listen address in dev mode")
	flag.StringVar(&cfg.domain, "domain", envOr("NYK_DOMAIN", ""), "public domain for automatic TLS (empty = dev HTTP)")
	flag.StringVar(&cfg.dbPath, "db", envOr("NYK_DB", "nyk.db"), "path to the SQLite database file")
	flag.StringVar(&cfg.orgKey, "orgkey", envOr("NYK_ORG_KEY", ""), "shared secret that agents must present")
	flag.StringVar(&cfg.consoleDir, "console-dir", envOr("NYK_CONSOLE_DIR", "console/frontend/dist"), "directory of the built console web app")
	flag.BoolVar(&cfg.dev, "dev", false, "dev mode: plain HTTP, permissive WebSocket origins")
	flag.StringVar(&cfg.turn.Host, "turn-host", envOr("NYK_TURN_HOST", ""), "TURN server host:port (empty = STUN only)")
	flag.StringVar(&cfg.turn.Secret, "turn-secret", envOr("NYK_TURN_SECRET", ""), "TURN static-auth shared secret")
	flag.StringVar(&cfg.turn.TLSURL, "turn-tls", envOr("NYK_TURN_TLS", ""), "optional turns: URL, e.g. turns:turn.example.com:5349")
	flag.Parse()
	cfg.turn.TTL = 5 * time.Minute

	if cfg.orgKey == "" {
		log.Printf("WARNING: -orgkey is empty; any client may register. Set NYK_ORG_KEY before production.")
	}
	return cfg
}

// httpServer bundles the dependencies the HTTP handlers need.
type httpServer struct {
	cfg    config
	store  *Store
	hub    *Hub
	tokens *tokenStore
}

func (s *httpServer) routes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.authed(s.handleLogout))
	mux.HandleFunc("/api/devices", s.authed(s.handleDevices))
	mux.HandleFunc("/api/nickname", s.authed(s.handleNickname))
	mux.HandleFunc("/api/audit", s.authed(s.handleAudit))
	mux.HandleFunc("/api/config", s.handleClientConfig)
	mux.HandleFunc("/ws/agent", s.handleAgentWS)
	mux.HandleFunc("/ws/console", s.handleConsoleWS)
	s.serveConsole(mux)
}

// --- auth HTTP handlers ---

func (s *httpServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.store.VerifyTech(req.Username, req.Password) {
		time.Sleep(500 * time.Millisecond) // throttle brute force a little
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	tok := s.tokens.issue(req.Username)
	s.store.Audit("tech_login", req.Username, "", "", "")
	writeJSON(w, map[string]string{"token": tok, "username": req.Username})
}

func (s *httpServer) handleLogout(w http.ResponseWriter, r *http.Request, _ string) {
	s.tokens.revoke(bearer(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *httpServer) handleDevices(w http.ResponseWriter, r *http.Request, _ string) {
	devices, err := s.store.ListDevices()
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, devices)
}

func (s *httpServer) handleNickname(w http.ResponseWriter, r *http.Request, _ string) {
	var req struct {
		ID       string `json:"id"`
		Nickname string `json:"nickname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.store.SetNickname(req.ID, req.Nickname); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *httpServer) handleAudit(w http.ResponseWriter, r *http.Request, _ string) {
	entries, err := s.store.RecentAudit(200)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, entries)
}

// handleClientConfig lets the agent/console discover non-secret settings. It
// never reveals the org key or TURN secret.
func (s *httpServer) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"name":    "NYK Support Portal",
		"version": Version,
	})
}

// --- WebSocket handlers ---

func (s *httpServer) acceptOpts() *websocket.AcceptOptions {
	if s.cfg.dev {
		return &websocket.AcceptOptions{InsecureSkipVerify: true} // any origin in dev
	}
	// In production, only the server's own origin (the hosted console) and
	// native apps (which send no Origin) are allowed.
	return &websocket.AcceptOptions{OriginPatterns: []string{s.cfg.domain}}
}

func (s *httpServer) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, s.acceptOpts())
	if err != nil {
		return
	}
	ctx := context.Background()
	c := newConn(ws, protocol.RoleAgent)

	// First frame must be a valid hello with the org key.
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	var first protocol.Envelope
	err = wsjson.Read(readCtx, ws, &first)
	cancel()
	if err != nil || first.Type != protocol.TypeHello || first.Hello == nil {
		ws.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	if s.cfg.orgKey != "" && first.OrgKey != s.cfg.orgKey {
		ws.Close(websocket.StatusPolicyViolation, "bad org key")
		return
	}
	if len(first.Hello.DeviceID) != 9 {
		ws.Close(websocket.StatusPolicyViolation, "bad device id")
		return
	}
	c.deviceID = first.Hello.DeviceID
	c.hello = first.Hello

	s.runConn(ctx, c, func() { s.hub.addAgent(c) })
}

func (s *httpServer) handleConsoleWS(w http.ResponseWriter, r *http.Request) {
	// Browsers cannot set Authorization on a WebSocket, so the token comes as a
	// query parameter over TLS.
	tok := r.URL.Query().Get("token")
	username, ok := s.tokens.lookup(tok)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, s.acceptOpts())
	if err != nil {
		return
	}
	c := newConn(ws, protocol.RoleConsole)
	c.techName = username
	s.runConn(context.Background(), c, func() { s.hub.addConsole(c) })
}

// runConn starts the write loop, registers the connection, then reads until the
// socket closes.
func (s *httpServer) runConn(ctx context.Context, c *conn, register func()) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h_writeLoop(s.hub, ctx, c)
	register()
	defer s.hub.removeConn(c)
	defer c.ws.Close(websocket.StatusNormalClosure, "")

	for {
		var env protocol.Envelope
		if err := wsjson.Read(ctx, c.ws, &env); err != nil {
			return
		}
		select {
		case <-c.done:
			return
		default:
		}
		s.hub.handle(c, env)
	}
}

// h_writeLoop adapts Hub.writeLoop (kept a method for testability).
func h_writeLoop(h *Hub, ctx context.Context, c *conn) { h.writeLoop(ctx, c) }

// --- console static files ---

func (s *httpServer) serveConsole(mux *http.ServeMux) {
	dir := s.cfg.consoleDir
	fs := http.FileServer(http.Dir(dir))
	handler := func(w http.ResponseWriter, r *http.Request) {
		// SPA fallback: unknown non-API paths serve index.html.
		p := r.URL.Path
		if p != "/" && !strings.HasPrefix(p, "/assets") && !strings.Contains(p, ".") {
			http.ServeFile(w, r, dir+"/index.html")
			return
		}
		fs.ServeHTTP(w, r)
	}
	mux.HandleFunc("/console/", http.StripPrefix("/console", http.HandlerFunc(handler)).ServeHTTP)
	mux.HandleFunc("/", handler)
}

// --- middleware & helpers ---

// authed wraps a handler that requires a valid bearer token, passing the
// technician username through.
func (s *httpServer) authed(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := s.tokens.lookup(bearer(r))
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r, username)
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	return strings.TrimPrefix(h, "Bearer ")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/ws/") {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func cacheDir() string {
	if d := os.Getenv("NYK_TLS_CACHE"); d != "" {
		return d
	}
	return "./tls-cache"
}

// Version is overridden at build time with -ldflags "-X main.Version=...".
var Version = "dev"
