//go:build !windows

package main

// Only Windows has its own native window; elsewhere Drop uses the browser's app window.
func (a *App) nativeWindow(url string) bool { return false }
