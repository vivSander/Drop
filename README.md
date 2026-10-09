# Drop

Move files between your computer and your phone over your own Wi-Fi. Open Drop on both, connect them once, then drag a file onto the other device and it arrives. Files go straight from one device to the other: no cloud, no account, no internet needed, nothing is uploaded anywhere.

[English](README.md) · [Nederlands](README.nl.md)

## Download

Get the file for your device from the **[latest release](../../releases/latest)**.

| You have | Download | Then |
|---|---|---|
| Windows | `Drop-Windows.zip` | Unzip, double-click `Drop.exe` |
| Mac (M1, M2, M3, M4…) | `Drop-Mac-AppleSilicon.zip` | Unzip, open `Drop.app` |
| Mac (older, Intel) | `Drop-Mac-Intel.zip` | Unzip, open `Drop.app` |
| Linux | `Drop-Linux-x86_64.tar.gz` or `Drop-Linux-arm64.tar.gz` | `tar xzf` it, run `./Drop` |
| Android phone | `Drop.apk` | Open it and install |

There is nothing to configure. Drop opens its window in your browser by itself (on a phone it is its own app).

### "Unsafe app" / "unknown publisher" warnings

Drop is free and open source, and the downloads are **not code-signed**: signing certificates cost money per year and I have not bought one. Windows, macOS and Android therefore warn about any app from a developer they do not know yet. That warning is about the missing signature, not about something found in the file. Because you should not just click through warnings, here is how to be sure you have the real thing:

1. Download only from this repository's Releases page.
2. Compare the checksum with `SHA256SUMS.txt` from the same release (`sha256sum Drop-Windows.zip`, or `Get-FileHash Drop-Windows.zip` on Windows).
3. Optional, stronger: every release carries a signed build attestation that proves it was built from this source by GitHub Actions: `gh attestation verify Drop-Windows.zip --repo <owner>/<repo>`.
4. Or skip the downloads and [build it yourself](#build-from-source). It is one command.

Then, per system:

- **Windows:** "Windows protected your PC" → **More info** → **Run anyway**. When the firewall asks, allow **private networks** only.
- **Mac:** if it says the app cannot be opened, go to **System Settings → Privacy & Security**, scroll down and press **Open Anyway**. Allow the "local network" question.
- **Android:** allow installing from the app you opened the file with. If Play Protect warns about an unknown developer, choose **More details → Install anyway**. Allow notifications (Android needs one to keep Drop running).

## Use it

1. Open Drop on both devices, on the **same Wi-Fi** (or the same hotspot or cable network). Use **Devices** to pick which network to share over.
2. Tap the other device. A 6-digit code appears on one device; **type it on the other**. You only do this once per pair.
3. Tap the device again to see its files. Drop files or folders on it, or press **Send files**, and they are sent immediately.

Things that are good to know:

- **Automatic sending:** anything you add to your Drop folder is sent to all your connected devices within a couple of seconds. Switch it off under **Devices**.
- **Where files land:** in a folder with the sender's device name, for example `Pixel/photo.jpg`, next to your own files. Your Drop folder is `Drop` in your home folder (on Android: inside the app's own storage; use **Download** on a file to put a copy in your phone's Downloads).
- **Remove a device:** press the ✕ next to it. It then loses all access immediately.
- **Quit:** press **Devices → Quit Drop** (computer), or **Stop** in the notification (Android). The Windows version has no window of its own, so use this or end `Drop.exe` in Task Manager.

## Is it safe?

Drop was built so that you can leave it running without worrying.

- **Nothing is reachable from the network except other Drop devices you approved.** The page you use runs on your own computer only (`127.0.0.1`) and cannot be opened from another device or from a website you visit.
- **Everything between devices is encrypted** (TLS 1.3). When you connect two devices, each one remembers the other's certificate and from then on refuses to talk to anything else, so someone on the same Wi-Fi cannot pretend to be your phone or read what you send.
- **Connecting needs a code you type.** The code is shown on one device and typed on the other, and it is mixed into a cryptographic proof of both devices' identities. A stranger on your network who asks to connect has nothing to type, and a man-in-the-middle cannot forge the proof. A wrong code connects nothing.
- **Only your Drop folder is ever shared**, never the rest of your disk, and only with devices you connected. Links that point outside the folder are refused.
- **No account, no tracking, no telemetry, no outgoing internet connections.** Drop never contacts any server.
- Files other people send you are saved, never opened or run. Browsers get HTML/SVG files as plain downloads, not as web pages.

What Drop cannot do for you:

- A device you connected can **see everything in your Drop folder and put files in it**. Connect only devices you own or trust, and remove them when you are done.
- Be careful opening files you received from anyone you don't know, as with any download.
- Anyone using your computer account can use Drop as you. Lock your devices.
- Other devices on the network can see that Drop is running and the device name you chose (that is how finding each other works).
- Do the connect step on a network you trust (home), not on a stranger's public Wi-Fi.

More detail, and how to report a problem privately: [SECURITY.md](SECURITY.md).

## Troubleshooting

- **The devices don't see each other.** They must be on the same network. Guest Wi-Fi and some hotels and offices block devices from talking to each other ("client isolation"); use a hotspot from your phone instead. Check the firewall allowed Drop on private networks.
- **Wrong network picked.** **Devices → Share over** lets you choose, and Drop follows you if the connection changes.
- **Typing the address:** under **Devices → Add by address** you can type the other device's address, shown on its Devices screen (for example `192.168.1.20:8765`).
- **Connecting failed with "code didn't match".** Nothing was connected. Start again and type the new code carefully.

## Build from source

You need [Go](https://go.dev/dl/) 1.24 or newer. Nothing else: Drop has no third-party dependencies.

```sh
git clone <this repository>
cd <folder>
go build -o drop .        # for your own computer
go test ./...             # the tests
python3 scripts/package.py   # every computer download into ./dist
./android/build.sh           # the Android app (needs the Android SDK, see the script)
```

Releases are built by GitHub Actions when a version tag such as `v1.0.0` is pushed (see `.github/workflows/release.yml`). For Android updates to install over older versions, every release has to be signed with the same key: store it as the repository secrets `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD` and `ANDROID_KEY_ALIAS`, and never commit it.

## How it works

One small program written in Go, with a web page built in. It listens in two places: on your own computer for the page you see, and on the network you chose for other Drop devices (encrypted, and only answering approved devices). Devices find each other by sending a short "I'm here" message on the local network. The Android app is a thin shell (a web view and a background service) around the same program.

## License

[MIT](LICENSE).
