//go:build !windows

package main

// startTray: the system-tray icon exists on Windows only. On other systems Drop is used
// through its page (and Quit in the Devices dialog).
func startTray(a *App, url string) {}
