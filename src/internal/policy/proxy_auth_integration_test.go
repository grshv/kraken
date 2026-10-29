package policy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in parse against the pinned runtime; no host deployment or persistent
// application volume is touched. The full request test lives in network smoke.
func TestRealSquidParseProxyAuthentication(t *testing.T) {
	image := os.Getenv("KRAKEN_SQUID_TEST_IMAGE")
	if image == "" {
		t.Skip("set KRAKEN_SQUID_TEST_IMAGE for Docker parse")
	}
	dir := t.TempDir()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "proxy.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "proxy.key"), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); e != nil {
		t.Fatal(e)
	}
	s, o := authFixture()
	o.StateDir = "/fixture"
	o.SquidDir = "/fixture"
	o.LogPath = "/tmp/proxy-access.log"
	o.ProxyCert = "/fixture/proxy.crt"
	o.ProxyKey = "/fixture/proxy.key"
	if e = os.WriteFile(filepath.Join(dir, "manager-auth.conf"), []byte("cachemgr_passwd fixture-token reconfigure rotate\n"), 0640); e != nil {
		t.Fatal(e)
	}
	other := s.Clients[0]
	other.ID = "second"
	other.ProxyUsername = "REQUIRED"
	s.Clients = append(s.Clients, other)
	config, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "proxy.conf"), []byte(config), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("docker", "run", "--rm", "-v", dir+":/fixture:ro", "--entrypoint", "/usr/sbin/squid", image, "-f", "/fixture/proxy.conf", "-k", "parse")
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("real Squid rejected auth policy: %v\n%s", e, output)
	}
}
