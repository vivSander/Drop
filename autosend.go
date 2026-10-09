package main

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var autoMu sync.Mutex

type fstate struct {
	size int64
	mod  int64
}

// scan lists the files that should be shared automatically: everything except
// hidden files and folders that hold what other devices sent to me.
func (a *App) scan(skip map[string]bool) map[string]fstate {
	out := map[string]fstate{}
	_ = filepath.WalkDir(a.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == a.root {
			return nil
		}
		rel, e := filepath.Rel(a.root, path)
		if e != nil {
			return nil
		}
		if !visible(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if filepath.Dir(rel) == "." && skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = fstate{info.Size(), info.ModTime().UnixNano()}
		return nil
	})
	return out
}

// autoLoop sends every new file in the shared folder to every connected device
// as soon as it has finished being written.
func (a *App) autoLoop() {
	prev := map[string]fstate{}
	queued := map[string]fstate{}
	pending := map[string]map[string]int{} // device -> file -> failed attempts
	busy := map[string]bool{}
	first := true
	for range time.Tick(time.Second) {
		a.mu.Lock()
		skip := map[string]bool{"Received": true}
		for _, t := range a.cfg.Trusted {
			skip[cleanName(t.Name)] = true
		}
		for _, t := range a.cfg.Out {
			skip[cleanName(t.Name)] = true
		}
		off := a.cfg.AutoOff
		ids := []string{}
		for id := range a.cfg.Out {
			ids = append(ids, id)
		}
		a.mu.Unlock()

		cur := a.scan(skip)
		if first { // what is already there is not sent; only what is added from now on
			first = false
			for k, v := range cur {
				queued[k] = v
			}
			prev = cur
			continue
		}
		now := time.Now()
		for rel, st := range cur {
			if queued[rel] == st {
				continue
			}
			if p, ok := prev[rel]; !ok || p != st { // still changing
				continue
			}
			if now.Sub(time.Unix(0, st.mod)) < 800*time.Millisecond {
				continue
			}
			queued[rel] = st
			if off {
				continue
			}
			autoMu.Lock()
			for _, id := range ids {
				if pending[id] == nil {
					pending[id] = map[string]int{}
				}
				pending[id][rel] = 0
			}
			autoMu.Unlock()
		}
		prev = cur
		autoMu.Lock()
		for id := range pending { // forgotten devices
			ok := false
			for _, x := range ids {
				if x == id {
					ok = true
				}
			}
			if !ok || off {
				delete(pending, id)
			}
		}
		n := 0
		for _, set := range pending {
			n += len(set)
		}
		a.mu.Lock()
		a.autoWait = n
		a.mu.Unlock()

		for id, set := range pending {
			if len(set) == 0 || busy[id] {
				continue
			}
			a.mu.Lock()
			a.mu.Unlock()
			target, key, cl, _, terr := a.target(id)
			if terr != nil {
				continue
			}
			files := make([]string, 0, len(set))
			for rel := range set {
				files = append(files, rel)
			}
			busy[id] = true
			go func(id, target, key string, cl *http.Client, files []string) {
				for _, rel := range files {
					full := filepath.Join(a.root, filepath.FromSlash(rel))
					st, err := os.Stat(full)
					done := false
					if err != nil || st.IsDir() {
						done = true // deleted meanwhile
					} else if err := a.pushFile(target, key, cl, rel, full, st.Size()); err == nil {
						done = true
						a.mu.Lock()
						a.autoSent++
						a.autoLast = rel
						a.mu.Unlock()
					}
					autoMu.Lock()
					if done {
						delete(pending[id], rel)
					} else if pending[id] != nil {
						pending[id][rel]++
						if pending[id][rel] >= 5 {
							delete(pending[id], rel)
						}
					}
					autoMu.Unlock()
					if !done {
						break
					}
				}
				autoMu.Lock()
				delete(busy, id)
				autoMu.Unlock()
			}(id, target, key, cl, files)
		}
		autoMu.Unlock()
	}
}
