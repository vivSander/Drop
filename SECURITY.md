# Security

## Reporting a problem

Please report vulnerabilities privately through GitHub: **Security → Report a vulnerability** on this repository. Please do not open a public issue for a security problem. You will get an answer as soon as possible.

## Design

Drop has two separate network surfaces.

**1. The page on your own computer (`127.0.0.1`).** The interface and all of its actions live here. It only listens on the loopback address, so other devices cannot reach it. Requests need a long random token (kept in an `HttpOnly`, `SameSite=Strict` cookie), a `Host` of `127.0.0.1` or `localhost` (against DNS rebinding) and, for changes, a matching `Origin` (against other websites making requests). The page has a restrictive Content-Security-Policy and files are served with `nosniff`; HTML, SVG and XML are forced to download, everything else except PDF is sandboxed.

**2. The device port on the network you chose.** It serves only `/peer/…`, over TLS 1.3 with a certificate generated on the device. There is no web interface there. Apart from `/peer/info` (device id and name) and the pairing endpoints, every route requires a 192-bit key that is created when two devices connect.

### Connecting two devices

Devices never trust certificate authorities. At connection time:

1. Device A learns B's certificate fingerprint from the TLS handshake and shows a random 6-digit code.
2. The person types the code on B.
3. B proves it knows the code with `HMAC(PBKDF2(code, nonce), role, ids, both fingerprints)`; A checks it against the fingerprint it actually saw, then sends its own proof, which B checks against the fingerprint it was told. Only then do both sides store each other's fingerprint and exchange the access keys.

From then on every outgoing connection verifies that the peer's certificate is exactly the stored one; if not, nothing (not even a key) is sent. A man-in-the-middle during connecting produces different fingerprints than the ones the proof covers, so the connection fails, even when the person types the correct code (I tested this with a proxy that intercepts the TLS connection). A wrong code gives exactly one failed attempt and then the request is dead.

Removing a device deletes the keys on both sides.

## Limits you should know

- **Short code.** A 6-digit code is not a strong secret on its own. An attacker has to be on your network at the very moment you connect, in the middle of the traffic, and then must guess the code offline against a deliberately slow function (PBKDF2, 200,000 rounds) inside the 2 minutes the request is valid. That is expensive but not impossible for a determined, resourced attacker. Connect devices on a network you trust.
- **Connected means trusted.** A connected device can list, read and download everything in the Drop folder and write files into it (into a folder named after it). There are no per-file permissions.
- **Discovery is unauthenticated.** Anyone on the network can see that Drop runs and the device name, and can send a connection request (which does nothing unless you type their code; at most 5 can be pending, they expire after 3 minutes).
- **Same-user and physical access.** Anyone who can read your user's files (including `config.json`, which holds the device's keys) or use your logged-in session has full access.
- The downloads are not code-signed (see the README). Check the checksums or build from source.

## Dependencies

Drop uses only the Go standard library and has no third-party dependencies for the computer apps or the phone service. The Android shell uses the Android framework only.
