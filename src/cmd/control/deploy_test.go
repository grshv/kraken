package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/tlscert"
)

func TestUnexpectedEntriesGuardsForeignDirectories(t *testing.T) {
	if bad := unexpectedEntries([]string{"data", "certs", "log", "backups", "lost+found"}); len(bad) != 0 {
		t.Fatalf("layout rejected: %v", bad)
	}
	if bad := unexpectedEntries([]string{"data", ".ssh", "etc"}); !slices.Equal(bad, []string{".ssh", "etc"}) {
		t.Fatalf("foreign names not reported: %v", bad)
	}
}

func certEnv(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "certs")
	t.Setenv("KRAKEN_CERT_DIR", dir)
	t.Setenv("KRAKEN_PUBLIC_ORIGIN", "https://kraken.corp.example:8443")
	t.Setenv("KRAKEN_MANAGEMENT_BIND", "10.0.0.5")
	t.Setenv("KRAKEN_PROXY_BIND", "0.0.0.0")
	t.Setenv("KRAKEN_CERT_NAMES", "proxy.corp.example 172.16.0.9")
	t.Setenv("KRAKEN_PANEL_CERT", "")
	t.Setenv("KRAKEN_PROXY_CERT", "")
	return dir
}

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	blk, _ := pem.Decode(b)
	c, e := x509.ParseCertificate(blk.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

// A fresh installation must start without an operator step: serve issues
// the chain instead of failing on missing files.
func TestEnsureCertificatesIssuesOnFreshInstall(t *testing.T) {
	dir := certEnv(t)
	if e := ensureCertificates(); e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(readCert(t, filepath.Join(dir, tlscert.CACert)))
	for _, name := range []string{tlscert.PanelCert, tlscert.ProxyCert} {
		c := readCert(t, filepath.Join(dir, name))
		for _, host := range []string{"kraken.corp.example", "proxy.corp.example", "localhost", "10.0.0.5", "172.16.0.9", "127.0.0.1"} {
			if _, e := c.Verify(x509.VerifyOptions{DNSName: host, Roots: roots}); e != nil {
				t.Errorf("%s for %s: %v", name, host, e)
			}
		}
		if slices.ContainsFunc(c.IPAddresses, func(ip net.IP) bool { return ip.IsUnspecified() }) {
			t.Errorf("%s contains the 0.0.0.0 bind wildcard", name)
		}
	}
	for name, mode := range map[string]os.FileMode{tlscert.PanelKey: 0600, tlscert.CAKey: 0600, tlscert.ProxyKey: 0640, tlscert.CACert: 0644} {
		fi, e := os.Stat(filepath.Join(dir, name))
		if e != nil || fi.Mode().Perm() != mode {
			t.Errorf("%s mode = %v, want %v (%v)", name, fi.Mode().Perm(), mode, e)
		}
	}
	before, _ := os.ReadFile(filepath.Join(dir, tlscert.PanelCert))
	if e := ensureCertificates(); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(dir, tlscert.PanelCert))
	if string(before) != string(after) {
		t.Fatal("restart replaced an existing certificate")
	}
}

func TestEnsureCertificatesRejectsPartialSet(t *testing.T) {
	dir := certEnv(t)
	if e := ensureCertificates(); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(dir, tlscert.PanelKey)); e != nil {
		t.Fatal(e)
	}
	e := ensureCertificates()
	if e == nil || !strings.Contains(e.Error(), "panel.key") {
		t.Fatalf("partial set must be reported, got %v", e)
	}
	if _, e := os.Stat(filepath.Join(dir, tlscert.PanelKey)); e == nil {
		t.Fatal("partial set silently replaced")
	}
}

func TestCertRenewKeepsCAForPermittedNames(t *testing.T) {
	dir := certEnv(t)
	if e := ensureCertificates(); e != nil {
		t.Fatal(e)
	}
	ca, _ := os.ReadFile(filepath.Join(dir, tlscert.CACert))
	panel, _ := os.ReadFile(filepath.Join(dir, tlscert.PanelCert))
	if _, newCA, e := issueCertificates(dir); e != nil || newCA {
		t.Fatalf("renew: newCA=%v err=%v", newCA, e)
	}
	ca2, _ := os.ReadFile(filepath.Join(dir, tlscert.CACert))
	panel2, _ := os.ReadFile(filepath.Join(dir, tlscert.PanelCert))
	if string(ca) != string(ca2) || string(panel) == string(panel2) {
		t.Fatal("renew must keep the CA and replace the server certificate")
	}
	t.Setenv("KRAKEN_CERT_NAMES", "new.example")
	if _, newCA, e := issueCertificates(dir); e != nil || !newCA {
		t.Fatalf("new name must create a new CA: newCA=%v err=%v", newCA, e)
	}
}

type pemCert struct {
	cert, key []byte
}

func operatorCert(t *testing.T, mutate func(*x509.Certificate)) pemCert {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "kraken.corp.example"}, DNSNames: []string{"kraken.corp.example"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if mutate != nil {
		mutate(tpl)
	}
	der, e := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	k, _ := x509.MarshalPKCS8PrivateKey(key)
	return pemCert{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k})}
}

func TestCertInstallWritesMatchingPair(t *testing.T) {
	dir := certEnv(t)
	if e := ensureCertificates(); e != nil {
		t.Fatal(e)
	}
	c := operatorCert(t, nil)
	if e := installCertificate(dir, "proxy", bytes.NewReader(append(append([]byte{}, c.cert...), c.key...)), time.Now()); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(filepath.Join(dir, tlscert.ProxyCert))
	if !bytes.Equal(got, c.cert) {
		t.Fatal("proxy certificate not installed")
	}
	for name, mode := range map[string]os.FileMode{tlscert.ProxyCert: 0640, tlscert.ProxyKey: 0640} {
		if fi, _ := os.Stat(filepath.Join(dir, name)); fi.Mode().Perm() != mode {
			t.Errorf("%s mode %v", name, fi.Mode().Perm())
		}
	}
	panel, _ := os.ReadFile(filepath.Join(dir, tlscert.PanelCert))
	if e := installCertificate(dir, "panel", bytes.NewReader(append(append([]byte{}, c.key...), c.cert...)), time.Now()); e != nil {
		t.Fatalf("key before certificate: %v", e)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, tlscert.PanelCert)); bytes.Equal(after, panel) {
		t.Fatal("panel certificate not replaced")
	}
	if fi, _ := os.Stat(filepath.Join(dir, tlscert.PanelKey)); fi.Mode().Perm() != 0600 {
		t.Fatalf("panel key mode %v", fi.Mode().Perm())
	}
}

// Nothing is written unless the input is a usable server pair.
func TestCertInstallRejectsUnusableInput(t *testing.T) {
	dir := certEnv(t)
	if e := ensureCertificates(); e != nil {
		t.Fatal(e)
	}
	good := operatorCert(t, nil)
	other := operatorCert(t, nil)
	expired := operatorCert(t, func(c *x509.Certificate) { c.NotAfter = time.Now().Add(-time.Minute) })
	clientOnly := operatorCert(t, func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} })
	ca := operatorCert(t, func(c *x509.Certificate) { c.IsCA, c.BasicConstraintsValid = true, true })
	noSAN := operatorCert(t, func(c *x509.Certificate) { c.DNSNames = nil })
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	cases := map[string][]byte{
		"empty":          nil,
		"cert only":      good.cert,
		"key only":       good.key,
		"mismatched key": join(good.cert, other.key),
		"two keys":       join(good.cert, good.key, other.key),
		"expired":        join(expired.cert, expired.key),
		"client auth":    join(clientOnly.cert, clientOnly.key),
		"ca":             join(ca.cert, ca.key),
		"no san":         join(noSAN.cert, noSAN.key),
		"foreign block":  join(good.cert, good.key, pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE"})),
		"too large":      bytes.Repeat([]byte("x"), maxInstallPEM+1),
	}
	before, _ := os.ReadFile(filepath.Join(dir, tlscert.ProxyKey))
	for name, in := range cases {
		if e := installCertificate(dir, "proxy", bytes.NewReader(in), time.Now()); e == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(dir, tlscert.ProxyKey)); !bytes.Equal(before, after) {
		t.Fatal("rejected input changed the installed key")
	}
}
