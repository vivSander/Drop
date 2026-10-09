package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPairingProof(t *testing.T) {
	k := sasKey("123456", "nonce-nonce-nonce")
	good := sasTag(k, "B", "n", "idA", "idB", "fpA", "fpB")
	if !tagEq(good, sasTag(sasKey("123456", "nonce-nonce-nonce"), "B", "n", "idA", "idB", "fpA", "fpB")) {
		t.Fatal("same inputs must give the same proof")
	}
	for name, other := range map[string]string{
		"wrong code":        sasTag(sasKey("123457", "nonce-nonce-nonce"), "B", "n", "idA", "idB", "fpA", "fpB"),
		"other role":        sasTag(k, "A", "n", "idA", "idB", "fpA", "fpB"),
		"swapped identity":  sasTag(k, "B", "n", "idA", "idB", "fpX", "fpB"),
		"swapped identity2": sasTag(k, "B", "n", "idA", "idB", "fpA", "fpY"),
	} {
		if tagEq(good, other) {
			t.Fatalf("%s must not verify", name)
		}
	}
}

func TestCodeShape(t *testing.T) {
	for i := 0; i < 200; i++ {
		c := newCode()
		if len(c) != 6 {
			t.Fatalf("bad code %q", c)
		}
	}
}

func TestResolveStaysInside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("no symlinks here")
	}
	a := &App{root: root}
	for _, p := range []string{"../etc/passwd", "link/secret.txt", "a/../../x", "/etc/passwd", ".thumbs/x", "..\\x"} {
		if full, err := a.resolve(p, false); err == nil && !within(root, full) {
			t.Fatalf("%q escaped to %s", p, full)
		} else if err == nil && (filepath.Base(full) == "secret.txt") {
			t.Fatalf("%q followed a link out of the folder", p)
		}
	}
}

func TestPeerFolderNameIsNeverEmpty(t *testing.T) {
	for _, n := range []string{"", "..", "...", " ", "a/b", "../x"} {
		got := cleanDisplay(n)
		if got == "" || got == "." || got == ".." {
			t.Fatalf("%q -> %q", n, got)
		}
	}
}
