//go:build windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

// A small icon in the Windows notification area (the "hidden icons" next to the clock)
// with a menu: Open Drop / Quit Drop. Pure Go: only the Windows system libraries are used.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassEx   = user32.NewProc("RegisterClassExW")
	pCreateWindowEx    = user32.NewProc("CreateWindowExW")
	pDefWindowProc     = user32.NewProc("DefWindowProcW")
	pGetMessage        = user32.NewProc("GetMessageW")
	pTranslateMessage  = user32.NewProc("TranslateMessage")
	pDispatchMessage   = user32.NewProc("DispatchMessageW")
	pPostMessage       = user32.NewProc("PostMessageW")
	pPostQuitMessage   = user32.NewProc("PostQuitMessage")
	pRegisterWindowMsg = user32.NewProc("RegisterWindowMessageW")
	pCreatePopupMenu   = user32.NewProc("CreatePopupMenu")
	pAppendMenu        = user32.NewProc("AppendMenuW")
	pTrackPopupMenu    = user32.NewProc("TrackPopupMenu")
	pDestroyMenu       = user32.NewProc("DestroyMenu")
	pSetForeground     = user32.NewProc("SetForegroundWindow")
	pGetCursorPos      = user32.NewProc("GetCursorPos")
	pCreateIconInd     = user32.NewProc("CreateIconIndirect")
	pLoadIcon          = user32.NewProc("LoadIconW")
	pShellNotifyIcon   = shell32.NewProc("Shell_NotifyIconW")
	pCreateBitmap      = gdi32.NewProc("CreateBitmap")
	pGetModuleHandle   = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmNull      = 0x0000
	wmDestroy   = 0x0002
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205
	wmTray      = 0x0400 + 1

	nimAdd    = 0
	nimDelete = 2
	nifMsg    = 1
	nifIcon   = 2
	nifTip    = 4

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	idOpen = 1
	idQuit = 2
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type notifyIconData struct {
	size            uint32
	hwnd            uintptr
	id              uint32
	flags           uint32
	callbackMessage uint32
	icon            uintptr
	tip             [128]uint16
	state           uint32
	stateMask       uint32
	info            [256]uint16
	timeout         uint32
	infoTitle       [64]uint16
	infoFlags       uint32
	guid            [16]byte
	balloonIcon     uintptr
}

type point struct{ x, y int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type iconInfo struct {
	isIcon   uint32
	xHotspot uint32
	yHotspot uint32
	mask     uintptr
	color    uintptr
}

func utf16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

// makeIcon draws a 32x32 icon: indigo rounded square with a white arrow pointing down into a tray.
func makeIcon() uintptr {
	const n = 32
	px := make([]byte, n*n*4) // BGRA, hard edges only (no partial transparency)
	set := func(x, y int, r, g, b byte) {
		i := (y*n + x) * 4
		px[i], px[i+1], px[i+2], px[i+3] = b, g, r, 255
	}
	const rad = 7
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := 0, 0
			if x < rad {
				dx = rad - x
			} else if x >= n-rad {
				dx = x - (n - rad - 1)
			}
			if y < rad {
				dy = rad - y
			} else if y >= n-rad {
				dy = y - (n - rad - 1)
			}
			if dx*dx+dy*dy <= rad*rad {
				set(x, y, 0x4B, 0x3F, 0xEF)
			}
		}
	}
	for y := 6; y <= 17; y++ { // shaft
		for x := 14; x <= 17; x++ {
			set(x, y, 255, 255, 255)
		}
	}
	for i := 0; i <= 7; i++ { // head
		for x := 16 - i; x <= 15+i; x++ {
			set(x, 24-i-1+0, 255, 255, 255)
		}
	}
	for x := 8; x <= 23; x++ { // tray
		for y := 25; y <= 26; y++ {
			set(x, y, 255, 255, 255)
		}
	}
	mask := make([]byte, n*n/8)
	color, _, _ := pCreateBitmap.Call(n, n, 1, 32, uintptr(unsafe.Pointer(&px[0])))
	mk, _, _ := pCreateBitmap.Call(n, n, 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	if color == 0 || mk == 0 {
		h, _, _ := pLoadIcon.Call(0, 32512) // IDI_APPLICATION
		return h
	}
	ii := iconInfo{isIcon: 1, mask: mk, color: color}
	h, _, _ := pCreateIconInd.Call(uintptr(unsafe.Pointer(&ii)))
	if h == 0 {
		h, _, _ = pLoadIcon.Call(0, 32512)
	}
	return h
}

func startTray(a *App, url string) {
	go func() {
		runtime.LockOSThread()
		hInst, _, _ := pGetModuleHandle.Call(0)
		icon := makeIcon()
		taskbarCreated, _, _ := pRegisterWindowMsg.Call(uintptr(unsafe.Pointer(utf16("TaskbarCreated"))))

		var nid notifyIconData
		add := func(hwnd uintptr) {
			nid = notifyIconData{hwnd: hwnd, id: 1, flags: nifMsg | nifIcon | nifTip, callbackMessage: wmTray, icon: icon}
			nid.size = uint32(unsafe.Sizeof(nid))
			copy(nid.tip[:], syscall.StringToUTF16("Drop"))
			pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
		}
		quit := func() {
			pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
			a.shutdown()
		}
		menu := func(hwnd uintptr) {
			m, _, _ := pCreatePopupMenu.Call()
			pAppendMenu.Call(m, 0, idOpen, uintptr(unsafe.Pointer(utf16("Open Drop"))))
			pAppendMenu.Call(m, 0, idQuit, uintptr(unsafe.Pointer(utf16("Quit Drop"))))
			var pt point
			pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			pSetForeground.Call(hwnd)
			cmd, _, _ := pTrackPopupMenu.Call(m, tpmRightButton|tpmReturnCmd, uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
			pPostMessage.Call(hwnd, wmNull, 0, 0)
			pDestroyMenu.Call(m)
			switch cmd {
			case idOpen:
				openBrowser(url)
			case idQuit:
				quit()
			}
		}
		proc := syscall.NewCallback(func(hwnd, m, wp, lp uintptr) uintptr {
			switch {
			case m == wmTray:
				switch uint32(lp) & 0xffff {
				case wmLButtonUp:
					openBrowser(url)
				case wmRButtonUp:
					menu(hwnd)
				}
				return 0
			case taskbarCreated != 0 && m == taskbarCreated:
				add(hwnd) // Explorer restarted: put the icon back
				return 0
			case m == wmDestroy:
				pPostQuitMessage.Call(0)
				return 0
			}
			r, _, _ := pDefWindowProc.Call(hwnd, m, wp, lp)
			return r
		})
		class := utf16("DropTrayWindow")
		wc := wndClassEx{wndProc: proc, instance: hInst, className: class}
		wc.size = uint32(unsafe.Sizeof(wc))
		pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		hwnd, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(utf16("Drop"))), 0, 0, 0, 0, 0, 0, 0, hInst, 0)
		if hwnd == 0 {
			return
		}
		add(hwnd)
		var mm msg
		for {
			r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&mm)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&mm)))
			pDispatchMessage.Call(uintptr(unsafe.Pointer(&mm)))
		}
	}()
}
