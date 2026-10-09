#!/usr/bin/env python3
"""Cross-compile Drop and pack the downloads into ./dist (run from the repo root)."""
import os, subprocess, sys, tarfile, zipfile, pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
DIST = ROOT / "dist"
BUILD = DIST / "build"
VERSION = os.environ.get("VERSION", "dev")

PLIST = """<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Drop</string><key>CFBundleDisplayName</key><string>Drop</string>
<key>CFBundleIdentifier</key><string>app.drop.share</string><key>CFBundleExecutable</key><string>Drop</string>
<key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>{v}</string>
<key>CFBundleShortVersionString</key><string>{v}</string>
<key>NSLocalNetworkUsageDescription</key><string>Drop finds your other devices on this network.</string>
<key>NSHighResolutionCapable</key><true/></dict></plist>
"""

def build(goos, goarch, out, gui=False):
    env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
    ld = "-s -w" + (" -H=windowsgui" if gui else "")
    subprocess.check_call(["go", "build", "-trimpath", "-ldflags", ld, "-o", str(BUILD / out), "."], cwd=ROOT, env=env)

def mac(name, exe):
    with zipfile.ZipFile(DIST / name, "w", zipfile.ZIP_DEFLATED) as z:
        for arc, data, mode in [
            ("Drop.app/Contents/Info.plist", PLIST.format(v=VERSION).encode(), 0o644),
            ("Drop.app/Contents/MacOS/Drop", (BUILD / exe).read_bytes(), 0o755),
        ]:
            i = zipfile.ZipInfo(arc, (2026, 1, 1, 0, 0, 0))
            i.external_attr = (0o100000 | mode) << 16
            i.compress_type = zipfile.ZIP_DEFLATED
            z.writestr(i, data)

def main():
    BUILD.mkdir(parents=True, exist_ok=True)
    build("windows", "amd64", "Drop.exe", gui=True)
    build("darwin", "arm64", "Drop-mac-arm64")
    build("darwin", "amd64", "Drop-mac-amd64")
    build("linux", "amd64", "Drop-linux-amd64")
    build("linux", "arm64", "Drop-linux-arm64")
    with zipfile.ZipFile(DIST / "Drop-Windows.zip", "w", zipfile.ZIP_DEFLATED) as z:
        z.write(BUILD / "Drop.exe", "Drop.exe")
    mac("Drop-Mac-AppleSilicon.zip", "Drop-mac-arm64")
    mac("Drop-Mac-Intel.zip", "Drop-mac-amd64")
    for out, exe in [("Drop-Linux-x86_64.tar.gz", "Drop-linux-amd64"), ("Drop-Linux-arm64.tar.gz", "Drop-linux-arm64")]:
        with tarfile.open(DIST / out, "w:gz") as t:
            ti = t.gettarinfo(str(BUILD / exe), "Drop")
            ti.mode = 0o755
            ti.uid = ti.gid = 0
            ti.uname = ti.gname = ""
            with open(BUILD / exe, "rb") as f:
                t.addfile(ti, f)
    print("packed into", DIST)

if __name__ == "__main__":
    sys.exit(main())
