package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Drop doesn't keep what other devices send: it lives in a folder named after the device
// and is deleted when that device is removed, when it has been gone for a minute, and when
// Drop closes (or starts, after an unclean exit).

const goneAfter = 60 * time.Second

// wipeInbox deletes the folder holding what the named device sent.
func (a *App) wipeInbox(name string) {
	d := cleanDisplay(name)
	if d == "" || d == "." || d == ".." || strings.ContainsAny(d, `/\`) {
		return
	}
	root := filepath.Clean(a.root)
	p := filepath.Join(root, d)
	if filepath.Dir(p) != root {
		return
	}
	_ = os.RemoveAll(p)
}

// deviceNames lists the names of all devices this one is connected to (a.mu held).
func (a *App) deviceNames() map[string]string {
	m := map[string]string{}
	for id, k := range a.cfg.Out {
		m[id] = k.Name
	}
	for id, k := range a.cfg.Trusted {
		m[id] = k.Name
	}
	return m
}

func (a *App) wipeAll() {
	a.mu.Lock()
	names := a.deviceNames()
	a.mu.Unlock()
	for _, n := range names {
		a.wipeInbox(n)
	}
}

// purgeLoop deletes what a device sent once it has been gone for goneAfter.
func (a *App) purgeLoop() {
	gone := map[string]time.Time{}
	done := map[string]bool{}
	for range time.Tick(5 * time.Second) {
		a.mu.Lock()
		names := a.deviceNames()
		now := time.Now()
		var wipe []string
		for id, name := range names {
			p := a.peers[id]
			online := p != nil && (p.Manual || now.Sub(p.Seen) < 8*time.Second)
			if t, ok := a.recvAt[id]; ok && now.Sub(t) < 2*time.Minute {
				online = true // still receiving
			}
			if online {
				delete(gone, id)
				delete(done, id)
				continue
			}
			if _, ok := gone[id]; !ok {
				gone[id] = now
			}
			if now.Sub(gone[id]) > goneAfter && !done[id] {
				done[id] = true
				wipe = append(wipe, name)
			}
		}
		for id := range gone {
			if _, ok := names[id]; !ok {
				delete(gone, id)
				delete(done, id)
			}
		}
		a.mu.Unlock()
		for _, n := range wipe {
			a.wipeInbox(n)
		}
	}
}
