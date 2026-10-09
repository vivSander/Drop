package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"time"
)

// Every device has its own certificate. Devices never trust a certificate authority:
// when two devices pair, each remembers the other's certificate fingerprint and from
// then on refuses to talk to anything else. Traffic between devices is TLS 1.3.

func (a *App) ensureCert() {
	if a.cfg.Cert != "" && a.cfg.CertKey != "" {
		if c, err := tls.X509KeyPair([]byte(a.cfg.Cert), []byte(a.cfg.CertKey)); err == nil {
			a.setCert(c)
			return
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Drop " + a.cfg.ID},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(30, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	a.cfg.Cert = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	a.cfg.CertKey = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
	c, _ := tls.X509KeyPair([]byte(a.cfg.Cert), []byte(a.cfg.CertKey))
	a.setCert(c)
	a.saveLocked()
}

func fpOf(der []byte) string { s := sha256.Sum256(der); return hex.EncodeToString(s[:]) }

func (a *App) setCert(c tls.Certificate) {
	a.tlsCert = c
	a.fp = fpOf(c.Certificate[0])
}

func (a *App) serverTLS() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{a.tlsCert}, MinVersion: tls.VersionTLS13}
}

func newTransport(verify func([][]byte, [][]*x509.Certificate) error) *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{
			MinVersion:            tls.VersionTLS13,
			InsecureSkipVerify:    true, // identity is checked below by fingerprint, not by a certificate authority
			VerifyPeerCertificate: verify,
		},
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
	}
}

// clientFor returns a client that only talks to the device whose certificate has this fingerprint.
func (a *App) clientFor(fp string) *http.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c := a.clients[fp]; c != nil {
		return c
	}
	tr := newTransport(func(raw [][]byte, _ [][]*x509.Certificate) error {
		if fp == "" || len(raw) == 0 || fpOf(raw[0]) != fp {
			return errors.New("this is not the device you connected to")
		}
		return nil
	})
	c := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	a.clients[fp] = c
	return c
}

// fetchFP learns the certificate fingerprint of a device that is not paired yet.
func fetchFP(hostport string) (string, error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	c, err := tls.DialWithDialer(d, "tcp", hostport, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13})
	if err != nil {
		return "", err
	}
	defer c.Close()
	certs := c.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("no certificate")
	}
	return fpOf(certs[0].Raw), nil
}

// unpinnedClient is only for asking an unpaired device who it is; nothing private goes through it.
var unpinnedClient = &http.Client{Timeout: 5 * time.Second, Transport: newTransport(nil)}

// Pairing: the person types the code shown on the other device. The code is a shared
// secret that is mixed with both certificate fingerprints, so somebody sitting in the
// middle of the connection cannot produce a valid proof without knowing it.

func sasKey(code, nonce string) []byte {
	k, _ := pbkdf2.Key(sha256.New, code, []byte("drop-pair-v1|"+nonce), 200000, 32)
	return k
}

// role is "A" (the device that asked) or "B" (the device that was asked).
func sasTag(k []byte, role, nonce, idA, idB, fpA, fpB string) string {
	m := hmac.New(sha256.New, k)
	fmt.Fprintf(m, "%s|%s|%s|%s|%s|%s", role, nonce, idA, idB, fpA, fpB)
	return hex.EncodeToString(m.Sum(nil))
}

func tagEq(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }

func newCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}
