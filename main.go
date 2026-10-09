// Drop: move files between your computer and your phone over the network you choose.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

const discoPort = 48655

var (
	flagName      = flag.String("name", "", "name other devices see (default: this computer's name)")
	flagDir       = flag.String("dir", "", "folder to share (default: ~/Drop)")
	flagConfig    = flag.String("config", "", "settings file (default: in your user settings folder)")
	flagPort      = flag.Int("port", 47865, "port to use")
	flagNoBrowser = flag.Bool("no-browser", false, "don't open the app window")
	flagHeadless  = flag.Bool("headless", false, "run as a background service (used by the Android app)")
)

type Trust struct {
	Name string `json:"name"`
	Key  string `json:"key"`
	FP   string `json:"fp"` // fingerprint of that device's certificate
}

type Config struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Token   string            `json:"token"`
	Iface   string            `json:"iface"`
	Trusted map[string]*Trust `json:"trusted"` // devices allowed to send to me; Key is what they must present
	Out     map[string]*Trust `json:"out"`     // keys I present when sending to them
	AutoOff bool              `json:"autoOff"` // true when new files are NOT sent automatically
	Cert    string            `json:"cert"`
	CertKey string            `json:"certKey"`
}

type NetIf struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	Bits int    `json:"bits"`
}

type Peer struct {
	ID     string
	Name   string
	IP     string
	Port   int
	Seen   time.Time
	Manual bool
}

type Incoming struct {
	ID, Name, Nonce, Offer, MyKey, FP, IP, Tag string
	K                                          []byte
	State                                      string // pending, answered, allowed, denied, mismatch
	Made                                       time.Time
}

type PairJob struct {
	Code, State string
}

type App struct {
	mu       sync.Mutex
	cfg      Config
	cfgPath  string
	root     string
	port     int
	cur      *NetIf
	lan      *http.Server
	lanErr   string
	stop     chan struct{}
	peers    map[string]*Peer
	incoming map[string]*Incoming
	jobs     map[string]*PairJob
	inflight map[string]bool
	received int
	lastFrom string
	recvAt   map[string]time.Time
	thumbSem chan struct{}
	tlsCert  tls.Certificate
	fp       string
	clients  map[string]*http.Client
	lastPing time.Time
	byeAt    time.Time
	focus    func()
	autoSent int
	autoLast string
	autoWait int
}

func randStr(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func newApp() *App {
	a := &App{
		peers: map[string]*Peer{}, incoming: map[string]*Incoming{}, jobs: map[string]*PairJob{},
		inflight: map[string]bool{}, thumbSem: make(chan struct{}, 2), clients: map[string]*http.Client{},
	}
	a.cfgPath = *flagConfig
	if a.cfgPath == "" {
		d, err := os.UserConfigDir()
		if err != nil {
			d = os.TempDir()
		}
		a.cfgPath = filepath.Join(d, "Drop", "config.json")
	}
	if b, err := os.ReadFile(a.cfgPath); err == nil {
		_ = json.Unmarshal(b, &a.cfg)
	}
	if a.cfg.ID == "" {
		a.cfg.ID = randStr(9)
	}
	if a.cfg.Token == "" {
		a.cfg.Token = randStr(12)
	}
	if t := os.Getenv("DROP_TOKEN"); t != "" {
		a.cfg.Token = t
	}
	if a.cfg.Trusted == nil {
		a.cfg.Trusted = map[string]*Trust{}
	}
	if a.cfg.Out == nil {
		a.cfg.Out = map[string]*Trust{}
	}
	for id, t := range a.cfg.Out { // connections made before encryption existed must be made again
		if t == nil || t.FP == "" || a.cfg.Trusted[id] == nil || a.cfg.Trusted[id].FP == "" {
			delete(a.cfg.Out, id)
			delete(a.cfg.Trusted, id)
		}
	}
	a.ensureCert()
	if *flagName != "" {
		a.cfg.Name = *flagName
	}
	if a.cfg.Name == "" {
		h, _ := os.Hostname()
		h = strings.TrimSuffix(h, ".local")
		if h == "" {
			h = "My device"
		}
		a.cfg.Name = h
	}
	dir := *flagDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Drop")
	}
	abs, _ := filepath.Abs(dir)
	_ = os.MkdirAll(abs, 0o755)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	a.root = abs
	a.save()
	return a
}

// shutdown ends Drop (used by the tray menu).
func (a *App) shutdown() { os.Exit(0) }

func (a *App) save() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.saveLocked()
}

func (a *App) saveLocked() {
	_ = os.MkdirAll(filepath.Dir(a.cfgPath), 0o700)
	b, _ := json.MarshalIndent(a.cfg, "", "  ")
	tmp := a.cfgPath + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, a.cfgPath)
	}
}

// ---------------------------------------------------------------- networks

func listIfaces() []NetIf {
	var out []NetIf
	if f := os.Getenv("DROP_IFACES_FILE"); f != "" { // the Android app writes the list here
		if b, err := os.ReadFile(f); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				p := strings.Fields(line)
				if len(p) == 3 {
					bits, _ := strconv.Atoi(p[2])
					if net.ParseIP(p[1]).To4() != nil && bits > 0 {
						out = append(out, NetIf{p[0], p[1], bits})
					}
				}
			}
			return out
		}
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || ip4.IsLinkLocalUnicast() {
				continue
			}
			bits, _ := ipn.Mask.Size()
			out = append(out, NetIf{ifc.Name, ip4.String(), bits})
		}
	}
	return out
}

func outboundIP() string {
	c, err := net.Dial("udp4", "10.255.255.255:1")
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func (a *App) pickNet(want string) {
	ifs := listIfaces()
	var pick *NetIf
	for i := range ifs {
		if want != "" && ifs[i].Name == want {
			pick = &ifs[i]
		}
	}
	if pick == nil {
		out := outboundIP()
		for i := range ifs {
			if ifs[i].IP == out {
				pick = &ifs[i]
			}
		}
	}
	if pick == nil && len(ifs) > 0 {
		pick = &ifs[0]
	}
	a.mu.Lock()
	a.cur = pick
	a.mu.Unlock()
}

// startNet (re)starts the LAN listener and discovery on the chosen connection.
func (a *App) startNet() {
	a.mu.Lock()
	if a.stop != nil {
		close(a.stop)
		a.stop = nil
	}
	if a.lan != nil {
		_ = a.lan.Close()
		a.lan = nil
	}
	a.lanErr = ""
	a.peers = map[string]*Peer{}
	cur := a.cur
	a.mu.Unlock()
	if cur == nil {
		a.mu.Lock()
		a.lanErr = "Not connected to a network"
		a.mu.Unlock()
		return
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(cur.IP, strconv.Itoa(a.port)))
	if err != nil {
		a.mu.Lock()
		a.lanErr = "Can't share on this network: " + err.Error()
		a.mu.Unlock()
		return
	}
	srv := &http.Server{Handler: a.peerRouter(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	ln = tls.NewListener(ln, a.serverTLS())
	stop := make(chan struct{})
	a.mu.Lock()
	a.lan, a.stop = srv, stop
	a.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
	a.discover(*cur, stop)
}

type beacon struct {
	App  string `json:"app"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Port int    `json:"port"`
}

func (a *App) discover(cur NetIf, stop chan struct{}) {
	ip := net.ParseIP(cur.IP).To4()
	mask := net.CIDRMask(cur.Bits, 32)
	bc := make(net.IP, 4)
	for i := range bc {
		bc[i] = ip[i] | ^mask[i]
	}
	go func() {
		conn, err := net.DialUDP("udp4", &net.UDPAddr{IP: ip}, &net.UDPAddr{IP: bc, Port: discoPort})
		if err != nil {
			return
		}
		defer conn.Close()
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			msg, _ := json.Marshal(beacon{"drop", a.cfg.ID, a.cfg.Name, a.port})
			_, _ = conn.Write(msg)
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()
	go func() {
		pc, err := net.ListenUDP("udp4", &net.UDPAddr{Port: discoPort})
		if err != nil {
			return
		}
		go func() { <-stop; pc.Close() }()
		buf := make([]byte, 2048)
		for {
			n, src, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var b beacon
			if json.Unmarshal(buf[:n], &b) != nil || b.App != "drop" || b.ID == a.cfg.ID || b.Port <= 0 {
				continue
			}
			s4 := src.IP.To4()
			if s4 == nil || !s4.Mask(mask).Equal(ip.Mask(mask)) { // only the connection you chose
				continue
			}
			a.mu.Lock()
			p := a.peers[b.ID]
			if p == nil {
				p = &Peer{ID: b.ID}
				a.peers[b.ID] = p
			}
			p.Name, p.IP, p.Port, p.Seen = b.Name, s4.String(), b.Port, time.Now()
			a.mu.Unlock()
		}
	}()
}

// watchNet follows the connection: Wi-Fi reconnects, a hotspot starts, a cable is unplugged.
func (a *App) watchNet() {
	for {
		time.Sleep(4 * time.Second)
		ifs := listIfaces()
		a.mu.Lock()
		cur, want := a.cur, a.cfg.Iface
		a.mu.Unlock()
		present, wanted := false, false
		for _, i := range ifs {
			if cur != nil && i == *cur {
				present = true
			}
			if want != "" && i.Name == want {
				wanted = true
			}
		}
		// switch when our connection vanished or changed address, or the one you chose came back
		if present && (cur.Name == want || !wanted) {
			continue
		}
		if cur == nil && len(ifs) == 0 {
			continue
		}
		a.pickNet(want)
		a.mu.Lock()
		nc := a.cur
		changed := (nc == nil) != (cur == nil) || (nc != nil && cur != nil && *nc != *cur)
		a.mu.Unlock()
		if changed {
			a.startNet()
		}
	}
}

// ---------------------------------------------------------------- startup

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// askFocus asks a running Drop with its own window to come to the front.
func (a *App) askFocus(p int) bool {
	req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/focus", p), nil)
	if err != nil {
		return false
	}
	req.AddCookie(&http.Cookie{Name: "k", Value: a.cfg.Token})
	r, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	r.Body.Close()
	return r.StatusCode == 204
}

func (a *App) runningInstance() bool {
	cl := &http.Client{Timeout: 700 * time.Millisecond}
	for p := *flagPort; p < *flagPort+10; p++ {
		r, err := cl.Get(fmt.Sprintf("http://127.0.0.1:%d/peer/info", p))
		if err != nil {
			continue
		}
		var info struct{ App, ID string }
		_ = json.NewDecoder(r.Body).Decode(&info)
		r.Body.Close()
		if info.App == "drop" && info.ID == a.cfg.ID {
			if !a.askFocus(p) {
				a.openWindow(fmt.Sprintf("http://127.0.0.1:%d/?k=%s", p, a.cfg.Token))
			}
			return true
		}
	}
	return false
}

func main() {
	flag.Parse()
	if *flagHeadless {
		log := os.Stderr
		_ = log
	}
	a := newApp()
	if !*flagHeadless && !*flagNoBrowser && a.runningInstance() {
		return
	}
	var ln net.Listener
	var err error
	for p := *flagPort; p < *flagPort+25; p++ {
		ln, err = net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			a.port = p
			break
		}
	}
	if ln == nil {
		fmt.Fprintln(os.Stderr, "Drop can't find a free port:", err)
		os.Exit(1)
	}
	a.pickNet(a.cfg.Iface)
	a.startNet()
	go a.watchNet()
	go a.autoLoop()
	local := fmt.Sprintf("http://127.0.0.1:%d/?k=%s", a.port, a.cfg.Token)
	fmt.Printf("Drop is running\n  sharing: %s\n  open:    %s\n", a.root, local)
	if !*flagNoBrowser && !*flagHeadless {
		go func() { time.Sleep(300 * time.Millisecond); a.runWindow(local) }()
	}
	srv := &http.Server{Handler: a.local(a.router()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	_ = srv.Serve(ln)
}

// ---------------------------------------------------------------- plumbing

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (a *App) authed(r *http.Request) bool {
	c, err := r.Cookie("k")
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.cfg.Token)) == 1
}

func (a *App) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			apiErr(w, 403, "Open the full link shown in Drop on your computer.")
			return
		}
		h(w, r)
	}
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(v)
}

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	if k := r.URL.Query().Get("k"); k != "" && subtle.ConstantTimeCompare([]byte(k), []byte(a.cfg.Token)) == 1 {
		http.SetCookie(w, &http.Cookie{Name: "k", Value: k, Path: "/", MaxAge: 31536000, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if !a.authed(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(403)
		_, _ = w.Write([]byte("Open Drop from its own window."))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:; media-src 'self'; frame-src 'self'; connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(indexHTML)
}

func (a *App) router() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", a.index)
	m.HandleFunc("GET /api/ls", a.guard(a.apiLs))
	m.HandleFunc("GET /api/tree", a.guard(a.apiTree))
	m.HandleFunc("GET /api/text", a.guard(a.apiTextGet))
	m.HandleFunc("POST /api/text", a.guard(a.apiTextSet))
	m.HandleFunc("POST /api/mkdir", a.guard(a.apiMkdir))
	m.HandleFunc("POST /api/rename", a.guard(a.apiRename))
	m.HandleFunc("POST /api/move", a.guard(a.apiMove))
	m.HandleFunc("POST /api/delete", a.guard(a.apiDelete))
	m.HandleFunc("GET /f/{path...}", a.guard(a.getFile))
	m.HandleFunc("HEAD /f/{path...}", a.guard(a.getFile))
	m.HandleFunc("PUT /f/{path...}", a.guard(a.putFile))
	m.HandleFunc("GET /thumb/{path...}", a.guard(a.getThumb))
	m.HandleFunc("GET /zip", a.guard(a.getZip))
	m.HandleFunc("GET /api/peers", a.guard(a.apiPeers))
	m.HandleFunc("POST /api/net", a.guard(a.apiNet))
	m.HandleFunc("POST /api/pair", a.guard(a.apiPair))
	m.HandleFunc("POST /api/pair/answer", a.guard(a.apiPairAnswer))
	m.HandleFunc("POST /api/peer/add", a.guard(a.apiPeerAdd))
	m.HandleFunc("POST /api/send", a.guard(a.apiSend))
	m.HandleFunc("POST /api/quit", a.guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); os.Exit(0) }()
	}))
	m.HandleFunc("GET /peer/info", a.peerInfo)
	m.HandleFunc("POST /api/forget", a.guard(a.apiForget))
	m.HandleFunc("POST /api/auto", a.guard(a.apiAuto))
	m.HandleFunc("POST /api/focus", a.guard(func(w http.ResponseWriter, r *http.Request) {
		if a.focus == nil {
			w.WriteHeader(404)
			return
		}
		a.focus()
		w.WriteHeader(204)
	}))
	m.HandleFunc("POST /api/ping", a.guard(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.lastPing = time.Now()
		a.mu.Unlock()
		w.WriteHeader(204)
	}))
	m.HandleFunc("POST /api/bye", a.guard(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.byeAt = time.Now()
		a.mu.Unlock()
		w.WriteHeader(204)
	}))
	m.HandleFunc("PUT /api/sendto/{id}/{path...}", a.guard(a.apiSendTo))
	m.HandleFunc("GET /api/remote/ls", a.guard(a.remoteProxy("ls")))
	m.HandleFunc("GET /api/remote/f/{path...}", a.guard(a.remoteProxy("f")))
	m.HandleFunc("HEAD /api/remote/f/{path...}", a.guard(a.remoteProxy("f")))
	m.HandleFunc("GET /api/remote/thumb/{path...}", a.guard(a.remoteProxy("thumb")))
	m.HandleFunc("GET /api/remote/zip", a.guard(a.remoteProxy("zip")))
	m.HandleFunc("POST /api/remote/pull", a.guard(a.apiPull))
	return m
}

// peerRouter is the only thing reachable from the network: encrypted, and every route
// except /peer/info and /peer/pair needs the key that was handed out when pairing.
func (a *App) peerRouter() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /peer/info", a.peerInfo)
	m.HandleFunc("POST /peer/pair", a.peerPair)
	m.HandleFunc("POST /peer/pair/confirm", a.peerPairConfirm)
	m.HandleFunc("PUT /peer/send/{path...}", a.peerReceive)
	m.HandleFunc("POST /peer/forget", a.peerAuth(a.peerForget))
	m.HandleFunc("GET /peer/ls", a.peerAuth(a.apiLs))
	m.HandleFunc("GET /peer/f/{path...}", a.peerAuth(a.getFile))
	m.HandleFunc("HEAD /peer/f/{path...}", a.peerAuth(a.getFile))
	m.HandleFunc("GET /peer/thumb/{path...}", a.peerAuth(a.getThumb))
	m.HandleFunc("GET /peer/zip", a.peerAuth(a.getZip))
	return m
}

// local protects the page on this computer: only this computer may talk to it, and only
// from its own page (a website you have open cannot make it do things).
func (a *App) local(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || (host != "127.0.0.1" && host != "localhost") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		h.ServeHTTP(w, r)
	})
}
