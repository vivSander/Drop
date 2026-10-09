package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Drop opens as its own window (the browser already on your computer, without tabs or
// address bar) and ends when that window is closed.

func findAppBrowser() string {
	var paths []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
			base := os.Getenv(env)
			if base == "" {
				continue
			}
			paths = append(paths,
				filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(base, `BraveSoftware\Brave-Browser\Application\brave.exe`),
				filepath.Join(base, `Chromium\Application\chrome.exe`))
		}
	case "darwin":
		paths = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		for _, n := range []string{"google-chrome", "google-chrome-stable", "microsoft-edge", "chromium", "chromium-browser", "brave-browser"} {
			if p, err := exec.LookPath(n); err == nil {
				paths = append(paths, p)
			}
		}
	}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func (a *App) windowCmd(exe, url string) *exec.Cmd {
	profile := filepath.Join(filepath.Dir(a.cfgPath), "window")
	return exec.Command(exe, "--app="+url, "--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check", "--window-size=1180,780")
}

// openWindow opens the page without waiting for it (used when Drop is already running).
func (a *App) openWindow(url string) {
	if exe := findAppBrowser(); exe != "" {
		if a.windowCmd(exe, url).Start() == nil {
			return
		}
	}
	openBrowser(url)
}

// runWindow opens Drop's window and quits Drop when it is closed. Without a suitable
// browser, or when the browser hands the window to a program that was already open,
// Drop falls back to noticing that its page has gone.
func (a *App) runWindow(url string) {
	exe := findAppBrowser()
	if exe == "" {
		openBrowser(url)
		a.watchPage()
		return
	}
	cmd := a.windowCmd(exe, url)
	start := time.Now()
	if cmd.Start() != nil {
		openBrowser(url)
		a.watchPage()
		return
	}
	_ = cmd.Wait()
	if time.Since(start) < 4*time.Second {
		a.watchPage()
		return
	}
	a.shutdown()
}

// watchPage ends Drop once its page is closed: immediately after a goodbye from the page
// (unless it comes back within a few seconds, for example on reload), or after minutes of silence.
func (a *App) watchPage() {
	a.mu.Lock()
	a.lastPing = time.Now()
	a.mu.Unlock()
	go func() {
		for range time.Tick(2 * time.Second) {
			a.mu.Lock()
			last, bye := a.lastPing, a.byeAt
			a.mu.Unlock()
			if !bye.IsZero() && bye.After(last) && time.Since(bye) > 8*time.Second {
				a.shutdown()
			}
			if time.Since(last) > 3*time.Minute {
				a.shutdown()
			}
		}
	}()
}
