package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func peerBase(p *Peer) string { return "https://" + net.JoinHostPort(p.IP, strconv.Itoa(p.Port)) }

// target finds a connected device that is reachable right now.
func (a *App) target(id string) (base, key string, cl *http.Client, name string, err error) {
	a.mu.Lock()
	p, k := a.peers[id], a.cfg.Out[id]
	a.mu.Unlock()
	if k == nil {
		return "", "", nil, "", errors.New("That device isn't connected")
	}
	if p == nil || (!p.Manual && time.Since(p.Seen) > 8*time.Second) {
		return "", "", nil, k.Name, errors.New("Can't reach " + k.Name)
	}
	return peerBase(p), k.Key, a.clientFor(k.FP), k.Name, nil
}

// ---------------------------------------------------------------- browser-facing API

func (a *App) apiPeers(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	type PeerOut struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		State string `json:"state"`
		Code  string `json:"code,omitempty"`
		Off   bool   `json:"offline,omitempty"`
		Recv  int    `json:"recvAge"` // seconds since this device last sent us a file, -1 if never
	}
	list := []PeerOut{}
	for id, p := range a.peers {
		if !p.Manual && time.Since(p.Seen) > 8*time.Second {
			continue
		}
		po := PeerOut{ID: id, Name: p.Name, State: "new", Recv: a.recvAge(id)}
		if k := a.cfg.Out[id]; k != nil {
			po.State = "paired"
		} else if j := a.jobs[id]; j != nil {
			po.State, po.Code = j.State, j.Code
		}
		list = append(list, po)
	}
	for id, k := range a.cfg.Out {
		seen := false
		for _, x := range list {
			if x.ID == id {
				seen = true
			}
		}
		if !seen {
			list = append(list, PeerOut{ID: id, Name: k.Name, State: "paired", Off: true, Recv: a.recvAge(id)})
		}
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	inc := []map[string]string{}
	for id, in := range a.incoming {
		if in.State == "pending" {
			inc = append(inc, map[string]string{"id": id, "name": in.Name, "ip": in.IP, "ref": in.Nonce[:6]})
		}
	}
	ifs := listIfaces()
	if ifs == nil {
		ifs = []NetIf{}
	}
	cur := ""
	ip := ""
	if a.cur != nil {
		cur, ip = a.cur.Name, a.cur.IP
	}
	writeJSON(w, 200, map[string]any{
		"me":       map[string]string{"id": a.cfg.ID, "name": a.cfg.Name},
		"peers":    list,
		"incoming": inc,
		"net":      map[string]any{"ifaces": ifs, "cur": cur, "ip": ip, "port": a.port, "error": a.lanErr},
		"received": a.received,
		"auto":     !a.cfg.AutoOff,
		"autoSent": a.autoSent,
		"autoLast": a.autoLast,
		"autoWait": a.autoWait,
		"lastFrom": a.lastFrom,
		"headless": *flagHeadless,
		"os":       runtime.GOOS,
	})
}

func (a *App) apiNet(w http.ResponseWriter, r *http.Request) {
	var q struct{ Name string }
	_ = readJSON(r, &q)
	a.pickNet(q.Name)
	a.mu.Lock()
	if a.cur != nil {
		a.cfg.Iface = a.cur.Name
	}
	a.saveLocked()
	a.mu.Unlock()
	a.startNet()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) apiPeerAdd(w http.ResponseWriter, r *http.Request) {
	var q struct{ Address string }
	_ = readJSON(r, &q)
	s := strings.TrimSpace(q.Address)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	s = strings.TrimSuffix(s, "/")
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		host, port = s, "47865"
	}
	if host == "" || strings.ContainsAny(host, "/ ?#@") {
		apiErr(w, 400, "That doesn't look like an address. Try 192.168.1.20:47865")
		return
	}
	resp, err := unpinnedClient.Get("https://" + net.JoinHostPort(host, port) + "/peer/info")
	if err != nil {
		apiErr(w, 502, "Can't reach that address. Check that Drop is open there and both devices are on the same network.")
		return
	}
	defer resp.Body.Close()
	var info struct {
		App, ID, Name string
		Port          int
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&info) != nil || info.App != "drop" || info.ID == "" || info.ID == a.cfg.ID {
		apiErr(w, 502, "That address isn't running Drop.")
		return
	}
	pn, _ := strconv.Atoi(port)
	a.mu.Lock()
	a.peers[info.ID] = &Peer{ID: info.ID, Name: cleanDisplay(info.Name), IP: host, Port: pn, Seen: time.Now(), Manual: true}
	a.mu.Unlock()
	writeJSON(w, 200, map[string]string{"id": info.ID})
}

// apiPair starts asking another device for permission. This device shows a code;
// the person types it on the other device.
func (a *App) apiPair(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	_ = readJSON(r, &q)
	a.mu.Lock()
	p := a.peers[q.ID]
	if p == nil {
		a.mu.Unlock()
		apiErr(w, 404, "That device isn't nearby any more")
		return
	}
	if j := a.jobs[q.ID]; j != nil && j.State == "waiting" {
		a.mu.Unlock()
		writeJSON(w, 200, map[string]string{"code": j.Code})
		return
	}
	nonce, offer, code := randStr(16), randStr(24), newCode()
	job := &PairJob{Code: code, State: "waiting"}
	a.jobs[q.ID] = job
	hostport := net.JoinHostPort(p.IP, strconv.Itoa(p.Port))
	name := p.Name
	a.mu.Unlock()
	go a.runPair(q.ID, name, hostport, nonce, code, offer, job)
	writeJSON(w, 200, map[string]string{"code": code})
}

func (a *App) setJob(job *PairJob, state string) {
	a.mu.Lock()
	job.State = state
	a.mu.Unlock()
}

func (a *App) runPair(id, name, hostport, nonce, code, offer string, job *PairJob) {
	fpB, err := fetchFP(hostport)
	if err != nil {
		a.setJob(job, "timeout")
		return
	}
	cl := a.clientFor(fpB) // from here on only this exact device is spoken to
	base := "https://" + hostport
	post := func(path string, v any) (map[string]string, error) {
		b, _ := json.Marshal(v)
		resp, err := cl.Post(base+path, "application/json", strings.NewReader(string(b)))
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		out := map[string]string{}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&out)
		return out, nil
	}
	k := sasKey(code, nonce)
	req := map[string]string{"id": a.cfg.ID, "name": a.cfg.Name, "nonce": nonce, "key": offer, "fp": a.fp}
	for i := 0; i < 120; i++ {
		out, err := post("/peer/pair", req)
		if err == nil {
			switch out["state"] {
			case "answered":
				if !tagEq(out["tag"], sasTag(k, "B", nonce, a.cfg.ID, id, a.fp, fpB)) || len(out["key"]) < 16 {
					_, _ = post("/peer/pair/confirm", map[string]string{"id": a.cfg.ID, "nonce": nonce, "tag": ""})
					a.setJob(job, "mismatch")
					return
				}
				res, err := post("/peer/pair/confirm", map[string]string{"id": a.cfg.ID, "nonce": nonce, "tag": sasTag(k, "A", nonce, a.cfg.ID, id, a.fp, fpB)})
				if err != nil || res["state"] != "ok" {
					a.setJob(job, "mismatch")
					return
				}
				a.mu.Lock()
				a.cfg.Out[id] = &Trust{Name: cleanDisplay(name), Key: out["key"], FP: fpB}
				a.cfg.Trusted[id] = &Trust{Name: cleanDisplay(name), Key: offer, FP: fpB}
				a.saveLocked()
				job.State = "paired"
				a.mu.Unlock()
				return
			case "denied":
				a.setJob(job, "denied")
				return
			}
		}
		time.Sleep(time.Second)
	}
	a.setJob(job, "timeout")
}

// apiPairAnswer: the person on this device typed the code from the other device (or said no).
func (a *App) apiPairAnswer(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID    string
		Allow bool
		Code  string
	}
	_ = readJSON(r, &q)
	a.mu.Lock()
	in := a.incoming[q.ID]
	if in == nil || in.State != "pending" {
		a.mu.Unlock()
		apiErr(w, 404, "That request has expired")
		return
	}
	if !q.Allow {
		in.State = "denied"
		a.mu.Unlock()
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	nonce, idA, fpA := in.Nonce, in.ID, in.FP
	a.mu.Unlock()
	code := strings.Join(strings.Fields(q.Code), "")
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		apiErr(w, 400, "Type the 6 digits shown on the other device")
		return
	}
	k := sasKey(code, nonce) // slow on purpose
	tag := sasTag(k, "B", nonce, idA, a.cfg.ID, fpA, a.fp)
	a.mu.Lock()
	defer a.mu.Unlock()
	if in != a.incoming[q.ID] || in.State != "pending" {
		apiErr(w, 404, "That request has expired")
		return
	}
	in.K, in.Tag, in.MyKey, in.State = k, tag, randStr(24), "answered" // one try: a wrong code cannot be retried
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// apiSend pushes files that already live in the shared folder to a connected device.
func (a *App) apiSend(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Peer  string
		Paths []string
	}
	_ = readJSON(r, &q)
	base, key, cl, name, err := a.target(q.Peer)
	if err != nil {
		apiErr(w, 404, err.Error())
		return
	}
	sent := 0
	for _, s := range q.Paths {
		full, err := a.resolve(s, false)
		if err != nil || full == a.root {
			continue
		}
		dir := filepath.Dir(full)
		err = filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !visible(info.Name()) {
				return nil
			}
			rel, _ := filepath.Rel(dir, path)
			if e := a.pushFile(base, key, cl, filepath.ToSlash(rel), path, info.Size()); e != nil {
				return e
			}
			sent++
			return nil
		})
		if err != nil {
			apiErr(w, 502, "Couldn't finish sending to "+name+": "+err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]int{"sent": sent})
}

func (a *App) pushFile(base, key string, cl *http.Client, rel, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var body io.Reader = f
	if size == 0 {
		body = http.NoBody
	}
	req, err := http.NewRequest("PUT", base+"/peer/send/"+(&url.URL{Path: rel}).EscapedPath(), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("X-Drop-Id", a.cfg.ID)
	req.Header.Set("X-Drop-Key", key)
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return errors.New("the other device refused " + rel)
	}
	return nil
}

// apiSendTo streams a file picked in the page to another device.
func (a *App) apiSendTo(w http.ResponseWriter, r *http.Request) {
	base, key, cl, name, err := a.target(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err.Error())
		return
	}
	if r.ContentLength < 0 {
		apiErr(w, 411, "Length required")
		return
	}
	var body io.Reader = r.Body
	if r.ContentLength == 0 {
		body = http.NoBody
	}
	req, err := http.NewRequest("PUT", base+"/peer/send/"+(&url.URL{Path: r.PathValue("path")}).EscapedPath(), body)
	if err != nil {
		apiErr(w, 400, "Bad path")
		return
	}
	req.ContentLength = r.ContentLength
	req.Header.Set("X-Drop-Id", a.cfg.ID)
	req.Header.Set("X-Drop-Key", key)
	resp, err := cl.Do(req)
	if err != nil {
		apiErr(w, 502, "Can't reach "+name)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		if e.Error == "" {
			e.Error = name + " refused the file"
		}
		apiErr(w, 502, e.Error)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- device-to-device API

func (a *App) peerInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"app": "drop", "id": a.cfg.ID, "name": a.cfg.Name, "port": a.port, "os": runtime.GOOS})
}

func cleanDisplay(s string) string {
	s = cleanName(s)
	if len(s) > 60 {
		s = s[:60]
	}
	if s == "" {
		s = "Another device"
	}
	return s
}

func (a *App) peerPair(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID, Name, Nonce, Key, FP string }
	if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&q) != nil || q.ID == "" || q.ID == a.cfg.ID || len(q.Nonce) < 16 || len(q.Key) < 16 || len(q.FP) != 64 {
		apiErr(w, 400, "Bad request")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	defer a.mu.Unlock()
	pending := 0
	for id, x := range a.incoming {
		if time.Since(x.Made) > 3*time.Minute && x.State != "allowed" {
			delete(a.incoming, id)
		} else if x.State == "pending" || x.State == "answered" {
			pending++
		}
	}
	in := a.incoming[q.ID]
	if in == nil || in.Nonce != q.Nonce || in.FP != q.FP {
		if pending >= 5 {
			apiErr(w, 429, "Too many requests")
			return
		}
		in = &Incoming{ID: q.ID, Name: cleanDisplay(q.Name), Nonce: q.Nonce, Offer: q.Key, FP: q.FP, IP: ip, State: "pending", Made: time.Now()}
		a.incoming[q.ID] = in
	}
	switch in.State {
	case "answered":
		writeJSON(w, 200, map[string]string{"state": "answered", "key": in.MyKey, "tag": in.Tag})
	case "denied", "mismatch":
		writeJSON(w, 200, map[string]string{"state": "denied"})
	case "allowed":
		writeJSON(w, 200, map[string]string{"state": "done"})
	default:
		writeJSON(w, 200, map[string]string{"state": "pending"})
	}
}

// peerPairConfirm: the asking device proves it knows the code. Only now is it trusted.
func (a *App) peerPairConfirm(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID, Nonce, Tag string }
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&q) != nil {
		apiErr(w, 400, "Bad request")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	in := a.incoming[q.ID]
	if in == nil || in.State != "answered" || in.Nonce != q.Nonce {
		writeJSON(w, 200, map[string]string{"state": "denied"})
		return
	}
	if q.Tag == "" || !tagEq(q.Tag, sasTag(in.K, "A", in.Nonce, in.ID, a.cfg.ID, in.FP, a.fp)) {
		in.State = "mismatch"
		writeJSON(w, 200, map[string]string{"state": "denied"})
		return
	}
	a.cfg.Trusted[q.ID] = &Trust{Name: in.Name, Key: in.MyKey, FP: in.FP}
	a.cfg.Out[q.ID] = &Trust{Name: in.Name, Key: in.Offer, FP: in.FP}
	in.State = "allowed"
	a.saveLocked()
	writeJSON(w, 200, map[string]string{"state": "ok"})
}

func (a *App) peerReceive(w http.ResponseWriter, r *http.Request) {
	id, key := r.Header.Get("X-Drop-Id"), r.Header.Get("X-Drop-Key")
	a.mu.Lock()
	t := a.cfg.Trusted[id]
	a.mu.Unlock()
	if t == nil || subtle.ConstantTimeCompare([]byte(t.Key), []byte(key)) != 1 {
		apiErr(w, 403, "Not connected. Connect this device first.")
		return
	}
	if r.ContentLength < 0 {
		apiErr(w, 411, "Length required")
		return
	}
	rel := filepath.ToSlash(filepath.Join(cleanDisplay(t.Name), r.PathValue("path")))
	full, err := a.resolve(rel, true)
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if _, err := a.saveStream(full, r.Body); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	a.mu.Lock()
	a.received++
	a.lastFrom = t.Name
	if a.recvAt == nil {
		a.recvAt = map[string]time.Time{}
	}
	a.recvAt[id] = time.Now()
	a.mu.Unlock()
	writeJSON(w, 201, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- browsing another device

// peerAuth lets a paired device read this device's Drop folder.
func (a *App) peerAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, key := r.Header.Get("X-Drop-Id"), r.Header.Get("X-Drop-Key")
		a.mu.Lock()
		t := a.cfg.Trusted[id]
		a.mu.Unlock()
		if t == nil || subtle.ConstantTimeCompare([]byte(t.Key), []byte(key)) != 1 {
			apiErr(w, 403, "Not connected. Connect this device first.")
			return
		}
		h(w, r)
	}
}

func (a *App) remoteDo(id, path string, q url.Values, hdr http.Header) (*http.Response, error) {
	base, key, cl, _, err := a.target(id)
	if err != nil {
		return nil, err
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if v := hdr.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.Header.Set("X-Drop-Id", a.cfg.ID)
	req.Header.Set("X-Drop-Key", key)
	resp, err := cl.Do(req)
	if err != nil {
		return nil, errors.New("Can't reach that device")
	}
	if resp.StatusCode == 302 && strings.HasPrefix(resp.Header.Get("Location"), "/f/") { // thumbnail fallback
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		return a.remoteDo(id, "/peer"+loc, nil, hdr)
	}
	return resp, nil
}

// remoteProxy streams a read-only view of another device's files to the local page.
func (a *App) remoteProxy(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		id := q.Get("id")
		q.Del("id")
		path := "/peer/" + kind
		if rest := r.PathValue("path"); rest != "" {
			path += "/" + (&url.URL{Path: rest}).EscapedPath()
		}
		resp, err := a.remoteDo(id, path, q, r.Header)
		if err != nil {
			apiErr(w, 502, err.Error())
			return
		}
		defer resp.Body.Close()
		for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Content-Disposition", "Cache-Control", "Etag", "Last-Modified", "X-Content-Type-Options", "Content-Security-Policy"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if r.Method != "HEAD" {
			_, _ = io.Copy(w, resp.Body)
		}
	}
}

// apiPull copies files or folders from another device into <device>/.
func (a *App) apiPull(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID    string
		Paths []string
	}
	_ = readJSON(r, &q)
	a.mu.Lock()
	p := a.peers[q.ID]
	a.mu.Unlock()
	if p == nil {
		apiErr(w, 404, "That device isn't connected")
		return
	}
	dest := filepath.ToSlash(cleanName(p.Name))
	n := 0
	var walk func(rel string, dir bool) error
	walk = func(rel string, dir bool) error {
		if dir {
			resp, err := a.remoteDo(q.ID, "/peer/ls", url.Values{"p": {rel}}, http.Header{})
			if err != nil {
				return err
			}
			var d struct{ Entries []Entry }
			err = json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&d)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 {
				return errors.New("couldn't read the folder " + rel)
			}
			if len(d.Entries) == 0 {
				if full, err := a.resolve(dest+"/"+rel, true); err == nil {
					_ = os.MkdirAll(full, 0o755)
				}
			}
			for _, e := range d.Entries {
				if err := walk(rel+"/"+e.Name, e.Dir); err != nil {
					return err
				}
			}
			return nil
		}
		resp, err := a.remoteDo(q.ID, "/peer/f/"+(&url.URL{Path: rel}).EscapedPath(), url.Values{"dl": {"1"}}, http.Header{})
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return errors.New("couldn't read " + rel)
		}
		full, err := a.resolve(dest+"/"+rel, true)
		if err != nil {
			return err
		}
		if _, err := a.saveStream(full, resp.Body); err != nil {
			return err
		}
		n++
		return nil
	}
	for _, s := range q.Paths {
		s = strings.Trim(filepath.ToSlash(s), "/")
		if s == "" {
			continue
		}
		dir := false
		if strings.HasPrefix(s, "dir:") {
			dir, s = true, strings.TrimPrefix(s, "dir:")
		}
		if err := walk(s, dir); err != nil {
			apiErr(w, 502, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{"saved": n, "to": dest})
}

// ---------------------------------------------------------------- removing devices

func (a *App) dropTrust(id string) {
	name := a.deviceNames()[id]
	if name != "" {
		go a.wipeInbox(name)
	}
	if k := a.cfg.Out[id]; k != nil {
		delete(a.clients, k.FP)
	}
	delete(a.cfg.Out, id)
	delete(a.cfg.Trusted, id)
	delete(a.jobs, id)
	delete(a.incoming, id)
	if p := a.peers[id]; p != nil && p.Manual {
		delete(a.peers, id)
	}
	a.saveLocked()
}

// apiForget removes a device: it stops seeing my files and I stop sending to it.
func (a *App) apiForget(w http.ResponseWriter, r *http.Request) {
	var q struct{ ID string }
	_ = readJSON(r, &q)
	base, key, cl, _, terr := a.target(q.ID)
	a.mu.Lock()
	a.dropTrust(q.ID)
	a.mu.Unlock()
	if terr == nil { // tell the other device too, so it forgets me
		go func() {
			req, err := http.NewRequest("POST", base+"/peer/forget", http.NoBody)
			if err != nil {
				return
			}
			req.Header.Set("X-Drop-Id", a.cfg.ID)
			req.Header.Set("X-Drop-Key", key)
			if resp, err := cl.Do(req); err == nil {
				resp.Body.Close()
			}
		}()
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) peerForget(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-Drop-Id")
	a.mu.Lock()
	a.dropTrust(id)
	a.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) apiAuto(w http.ResponseWriter, r *http.Request) {
	var q struct{ On bool }
	_ = readJSON(r, &q)
	a.mu.Lock()
	a.cfg.AutoOff = !q.On
	a.saveLocked()
	a.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// recvAge says how many seconds ago the device last sent a file (call with a.mu held); -1 if never.
func (a *App) recvAge(id string) int {
	t, ok := a.recvAt[id]
	if !ok {
		return -1
	}
	return int(time.Since(t).Seconds())
}

// peerRemove deletes something this device received from the caller (it was deleted on the sender).
func (a *App) peerRemove(w http.ResponseWriter, r *http.Request) {
	id, key := r.Header.Get("X-Drop-Id"), r.Header.Get("X-Drop-Key")
	a.mu.Lock()
	t := a.cfg.Trusted[id]
	a.mu.Unlock()
	if t == nil || subtle.ConstantTimeCompare([]byte(t.Key), []byte(key)) != 1 {
		apiErr(w, 403, "Not connected. Connect this device first.")
		return
	}
	folder := cleanDisplay(t.Name)
	full, err := a.resolve(filepath.ToSlash(filepath.Join(folder, r.PathValue("path"))), true)
	home, err2 := a.resolve(folder, true)
	if err != nil || err2 != nil || full == home || !within(home, full) {
		apiErr(w, 400, "Invalid path")
		return
	}
	_ = os.RemoveAll(full)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// removeOnDevices tells the connected devices to delete what was sent to them and then deleted here.
func (a *App) removeOnDevices(rels []string) {
	a.mu.Lock()
	ids := make([]string, 0, len(a.cfg.Out))
	for id := range a.cfg.Out {
		ids = append(ids, id)
	}
	a.mu.Unlock()
	for _, id := range ids {
		base, key, cl, _, err := a.target(id)
		if err != nil {
			continue
		}
		for _, rel := range rels {
			req, err := http.NewRequest("DELETE", base+"/peer/send/"+(&url.URL{Path: rel}).EscapedPath(), http.NoBody)
			if err != nil {
				continue
			}
			req.Header.Set("X-Drop-Id", a.cfg.ID)
			req.Header.Set("X-Drop-Key", key)
			if resp, err := cl.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
}
