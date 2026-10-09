package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWipeInboxOnlyRemovesDeviceFolder(t *testing.T) {
	root := t.TempDir()
	a := &App{root: root}
	os.MkdirAll(filepath.Join(root, "Pixel", "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "Pixel", "sub", "a.png"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "mine.txt"), []byte("keep"), 0o644)
	os.MkdirAll(filepath.Join(root, "Other"), 0o755)
	a.wipeInbox("Pixel")
	if _, err := os.Stat(filepath.Join(root, "Pixel")); !os.IsNotExist(err) {
		t.Fatal("device folder still there")
	}
	for _, keep := range []string{"mine.txt", "Other"} {
		if _, err := os.Stat(filepath.Join(root, keep)); err != nil {
			t.Fatal(keep, "was removed")
		}
	}
	for _, bad := range []string{"..", ".", "../x", "a/b"} {
		a.wipeInbox(bad)
	}
	if _, err := os.Stat(filepath.Join(root, "mine.txt")); err != nil {
		t.Fatal("unsafe name wiped the root")
	}
}
