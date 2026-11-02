package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"
)

func mustNames(t *testing.T, items ...string) Names {
	t.Helper()
	n, e := ParseNames(items...)
	if e != nil {
		t.Fatal(e)
	}
	return n
}

func parse(t *testing.T, b []byte) *x509.Certificate {
	t.Helper()
	blk, _ := pem.Decode(b)
	if blk == nil {
		t.Fatal("no PEM")
	}
	c, e := x509.ParseCertificate(blk.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestParseNames(t *testing.T) {
	n := mustNames(t, " Kraken.Corp.Example. ", "10.0.0.5", "[fd00::1]", "0.0.0.0", "::", "localhost", "10.0.0.5", "::ffff:192.168.1.5")
	got := n.Strings()
	want := []string{"kraken.corp.example", "localhost", "10.0.0.5", "192.168.1.5", "fd00::1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	for _, bad := range []string{"*.example.com", "https://example.com", "example.com:443", "fe80::1%eth0", "a b", "-bad.example"} {
		if _, e := ParseNames(bad); e == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, e := ParseNames("", "0.0.0.0"); e == nil {
		t.Error("empty name set accepted")
	}
}

// The chain must validate like a browser sees it: CA in the trust store,
// leaf with server auth for every configured name.
func TestIssueChainVerifiesForEveryName(t *testing.T) {
	now := time.Now()
	n := mustNames(t, append(DefaultNames, "kraken.corp.example", "10.0.0.5", "192.168.1.5", "fd00::5")...)
	b, newCA, e := Issue(nil, n, now)
	if e != nil {
		t.Fatal(e)
	}
	if !newCA {
		t.Fatal("first issue must create a CA")
	}
	ca := parse(t, b[CACert].Data)
	if !ca.IsCA || !ca.MaxPathLenZero || ca.KeyUsage&x509.KeyUsageCertSign == 0 || !ca.PermittedDNSDomainsCritical {
		t.Fatalf("CA flags: %+v", ca)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for _, leafName := range []string{PanelCert, ProxyCert} {
		leaf := parse(t, b[leafName].Data)
		if leaf.IsCA || leaf.NotAfter.Sub(now) > LeafValidity {
			t.Fatalf("%s: leaf must not be CA and must fit browser validity limits", leafName)
		}
		if len(leaf.AuthorityKeyId) == 0 || string(leaf.AuthorityKeyId) != string(ca.SubjectKeyId) {
			t.Fatalf("%s: authority key id must reference the CA", leafName)
		}
		for _, host := range n.Strings() {
			if _, e := leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); e != nil {
				t.Errorf("%s for %s: %v", leafName, host, e)
			}
		}
		if _, e := leaf.Verify(x509.VerifyOptions{DNSName: "other.example", Roots: roots}); e == nil {
			t.Errorf("%s verifies for a foreign name", leafName)
		}
	}
	if b[PanelKey].Mode != 0600 || b[CAKey].Mode != 0600 || b[PanelKey].ProxyGroup || b[CAKey].ProxyGroup {
		t.Fatal("panel and CA keys must be private to the panel user")
	}
	if !b[ProxyKey].ProxyGroup || b[ProxyKey].Mode != 0640 {
		t.Fatal("proxy key must be readable by group proxy only")
	}
}

// A leaked CA key must not mint a trusted certificate for another host: the
// name constraints are enforced by the verifier.
func TestCAConstraintsRejectForeignLeaf(t *testing.T) {
	now := time.Now()
	n := mustNames(t, DefaultNames...)
	b, _, e := Issue(nil, n, now)
	if e != nil {
		t.Fatal(e)
	}
	ca, e := LoadCA(b[CACert].Data, b[CAKey].Data)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e := ca.Leaf("bank.example", mustNames(t, "bank.example"), now); e == nil {
		t.Fatal("Leaf signed a name outside the constraints")
	}
	// Bypass Leaf's own check and sign directly with the CA key.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := serialNumber()
	tpl := &x509.Certificate{SerialNumber: serial, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), DNSNames: []string{"bank.example"}, IPAddresses: []net.IP{net.ParseIP("8.8.8.8")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
	if e != nil {
		t.Fatal(e)
	}
	forged, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, e := forged.Verify(x509.VerifyOptions{DNSName: "bank.example", Roots: roots}); e == nil {
		t.Fatal("forged certificate for a foreign name verified")
	}
}

func TestIssueRenewsLeavesWithExistingCA(t *testing.T) {
	now := time.Now()
	n := mustNames(t, append(DefaultNames, "kraken.corp.example", "10.0.0.5")...)
	first, _, e := Issue(nil, n, now)
	if e != nil {
		t.Fatal(e)
	}
	ca, e := LoadCA(first[CACert].Data, first[CAKey].Data)
	if e != nil {
		t.Fatal(e)
	}
	// Subset of names: keep the CA clients already trust.
	renewed, newCA, e := Issue(ca, mustNames(t, DefaultNames...), now)
	if e != nil || newCA {
		t.Fatalf("renew with permitted names must keep the CA: newCA=%v err=%v", newCA, e)
	}
	if string(renewed[CACert].Data) != string(first[CACert].Data) {
		t.Fatal("CA certificate changed")
	}
	if _, ok := renewed[CAKey]; ok {
		t.Fatal("CA key rewritten without a new CA")
	}
	// A new name the CA cannot sign forces a new CA.
	other, newCA, e := Issue(ca, mustNames(t, append(DefaultNames, "new.example")...), now)
	if e != nil || !newCA || string(other[CACert].Data) == string(first[CACert].Data) {
		t.Fatalf("new name must create a new CA: newCA=%v err=%v", newCA, e)
	}
}

func TestLoadCARejectsMismatchedKey(t *testing.T) {
	n := mustNames(t, DefaultNames...)
	a, _, _ := Issue(nil, n, time.Now())
	b, _, _ := Issue(nil, n, time.Now())
	if _, e := LoadCA(a[CACert].Data, b[CAKey].Data); e == nil {
		t.Fatal("mismatched CA key accepted")
	}
}
