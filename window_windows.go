//go:build windows

package main

import (
	"path/filepath"
	"runtime"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// On Windows Drop has its own window without the standard title bar: the page draws its
// own header with minimise, maximise and close buttons (see the "native" block in index.html).

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	pSetProcessDPI    = user32.NewProc("SetProcessDPIAware")
	pGetWindowRect    = user32.NewProc("GetWindowRect")
	pSetWindowPos     = user32.NewProc("SetWindowPos")
	pShowWindow       = user32.NewProc("ShowWindow")
	pGetWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	pMonitorFromWin   = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfo   = user32.NewProc("GetMonitorInfoW")
	pForeground       = user32.NewProc("SetForegroundWindow")
	pIsIconic         = user32.NewProc("IsIconic")
	pSystemDPI        = user32.NewProc("GetDpiForSystem")
)

type rect struct{ Left, Top, Right, Bottom int32 }
type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

const (
	gwlStyle      = ^uintptr(15) // -16
	wsCaption     = 0xC00000
	wsThickFrame  = 0x40000
	swpNoZOrder   = 0x4
	swpNoActivate = 0x10
	swpFrame      = 0x20
	swMinimize    = 6
	swRestore     = 9
)

// webview2Installed reports whether the WebView2 runtime (part of Windows 11 and of
// current Windows 10, and of Edge) is present.
func webview2Installed() bool {
	const id = `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	for _, c := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`},
		{registry.LOCAL_MACHINE, id},
		{registry.CURRENT_USER, id},
	} {
		k, err := registry.OpenKey(c.root, c.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("pv")
		k.Close()
		if err == nil && v != "" && v != "0.0.0.0" {
			return true
		}
	}
	return false
}

// nativeWindow shows Drop in its own window and returns true once that window has been
// closed (Drop then quits). It returns false when the window can't be made, so the caller
// can fall back to the browser window.
func (a *App) nativeWindow(url string) bool {
	if !webview2Installed() {
		return false
	}
	runtime.LockOSThread()
	pSetProcessDPI.Call()
	scale := 1.0
	if dpi, _, _ := pSystemDPI.Call(); dpi >= 96 {
		scale = float64(dpi) / 96
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  filepath.Join(filepath.Dir(a.cfgPath), "webview"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: "Drop", Width: uint(1180 * scale), Height: uint(780 * scale), Center: true,
		},
	})
	if w == nil {
		runtime.UnlockOSThread()
		return false
	}
	hwnd := uintptr(w.Window())
	// no title bar and no native border: the page draws its own header and resize edges
	st, _, _ := pGetWindowLongPtr.Call(hwnd, gwlStyle)
	pSetWindowLongPtr.Call(hwnd, gwlStyle, st&^(wsCaption|wsThickFrame))
	pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, 0x2|0x1|swpNoZOrder|swpNoActivate|swpFrame) // NOMOVE|NOSIZE|...

	var maxed bool
	var restore rect
	getRect := func() rect {
		var r rect
		pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		return r
	}
	setRect := func(r rect) {
		pSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), swpNoZOrder|swpNoActivate)
	}
	minW, minH := int32(720*scale), int32(480*scale)

	_ = w.Bind("dropNative", func() bool { return true })
	_ = w.Bind("dropWin", func(action string) bool {
		switch action {
		case "min":
			pShowWindow.Call(hwnd, swMinimize)
		case "max":
			if maxed {
				setRect(restore)
				maxed = false
			} else {
				restore = getRect()
				mon, _, _ := pMonitorFromWin.Call(hwnd, 2) // nearest monitor
				mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
				if r, _, _ := pGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); r != 0 {
					setRect(mi.Work)
					maxed = true
				}
			}
		case "close":
			w.Destroy()
		}
		return maxed
	})
	_ = w.Bind("dropRect", func() []int32 {
		r := getRect()
		return []int32{r.Left, r.Top, r.Right - r.Left, r.Bottom - r.Top}
	})
	_ = w.Bind("dropSetRect", func(x, y, wd, ht int32) {
		if maxed {
			return
		}
		if wd < minW {
			wd = minW
		}
		if ht < minH {
			ht = minH
		}
		setRect(rect{x, y, x + wd, y + ht})
	})
	a.focus = func() {
		w.Dispatch(func() {
			if r, _, _ := pIsIconic.Call(hwnd); r != 0 {
				pShowWindow.Call(hwnd, swRestore)
			}
			pForeground.Call(hwnd)
		})
	}
	w.SetTitle("Drop")
	w.Navigate(url)
	w.Run()
	a.shutdown()
	return true
}
