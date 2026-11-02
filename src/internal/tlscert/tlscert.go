// Package tlscert issues the installation's own TLS chain: a local CA limited
// by X.509 name constraints to the installation's names, and server
// certificates for the panel and the TLS proxy listener signed by it.
//
// The CA is the only thing an operator imports into a trust store. Because the
// CA may sign only the names listed in its constraints, its key cannot mint a
// trusted certificate for any other host, even if the volume leaks.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Validity follows browser limits: leaves at most 397 days (Apple/Chrome
// reject longer server certificates), the CA ten years.
const (
	CAValidity   = 10 * 365 * 24 * time.Hour
	LeafValidity = 397 * 24 * time.Hour
)

// Files in the certificate directory. ca.crt is public; keys stay private.
const (
	CACert    = "ca.crt"
	CAKey     = "ca.key"
	PanelCert = "panel.crt"
	PanelKey  = "panel.key"
	ProxyCert = "proxy.crt"
	ProxyKey  = "proxy.key"
)

// Leaves lists the server certificate and key files the panel and Squid use.
var Leaves = []string{PanelCert, PanelKey, ProxyCert, ProxyKey}

// Names is the subject alternative name set shared by both server
// certificates and permitted by the CA constraints.
type Names struct {
	DNS []string
	IPs []netip.Addr
}

var dnsPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

// DefaultNames are always present so the panel works from the host itself.
var DefaultNames = []string{"localhost", "127.0.0.1", "::1"}

// ParseNames turns host names and IP literals into a deduplicated, sorted
// name set. Unspecified addresses (0.0.0.0, ::) are skipped: they are listen
// wildcards, not names a client connects to. Wildcard DNS names, URLs, ports
// and zones are rejected.
func ParseNames(items ...string) (Names, error) {
	var n Names
	for _, raw := range items {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
		if a, e := netip.ParseAddr(v); e == nil {
			if a.Zone() != "" {
				return Names{}, fmt.Errorf("certificate name %q: zoned addresses are not allowed", raw)
			}
			a = a.Unmap()
			if a.IsUnspecified() {
				continue
			}
			if !slices.Contains(n.IPs, a) {
				n.IPs = append(n.IPs, a)
			}
			continue
		}
		v = strings.TrimSuffix(v, ".")
		if len(v) > 253 || !dnsPattern.MatchString(v) {
			return Names{}, fmt.Errorf("certificate name %q is neither an IP address nor a DNS name", raw)
		}
		if !slices.Contains(n.DNS, v) {
			n.DNS = append(n.DNS, v)
		}
	}
	slices.Sort(n.DNS)
	slices.SortFunc(n.IPs, func(a, b netip.Addr) int { return a.Compare(b) })
	if len(n.DNS)+len(n.IPs) == 0 {
		return Names{}, errors.New("no certificate names")
	}
	if len(n.DNS)+len(n.IPs) > 64 {
		return Names{}, errors.New("too many certificate names (64 max)")
	}
	return n, nil
}

// Strings returns the names in display order: DNS names, then addresses.
func (n Names) Strings() []string {
	out := slices.Clone(n.DNS)
	for _, a := range n.IPs {
		out = append(out, a.String())
	}
	return out
}

func (n Names) netIPs() []net.IP {
	out := make([]net.IP, 0, len(n.IPs))
	for _, a := range n.IPs {
		out = append(out, net.IP(a.AsSlice()))
	}
	return out
}

func (n Names) ipRanges() []*net.IPNet {
	out := make([]*net.IPNet, 0, len(n.IPs))
	for _, a := range n.IPs {
		bits := a.BitLen()
		out = append(out, &net.IPNet{IP: net.IP(a.AsSlice()), Mask: net.CIDRMask(bits, bits)})
	}
	return out
}

// File is one PEM file of a bundle with the mode it must be written with.
type File struct {
	Data []byte
	Mode uint32
	// ProxyGroup marks files Squid (group proxy) must read.
	ProxyGroup bool
}

// Bundle maps file names (CACert, PanelCert, ...) to their contents.
type Bundle map[string]File

// CA is a parsed local CA with its key, able to sign server certificates.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	PEM  []byte
}

// NewCA creates a name-constrained CA for n.
func NewCA(n Names, now time.Time) (*CA, []byte, error) {
	// An empty permitted subtree would leave that name type unconstrained.
	if len(n.DNS) == 0 || len(n.IPs) == 0 {
		return nil, nil, errors.New("CA needs at least one DNS name and one IP address")
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, e
	}
	serial, e := serialNumber()
	if e != nil {
		return nil, nil, e
	}
	first := n.Strings()[0]
	tpl := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: "Kraken local CA " + first, Organization: []string{"Kraken"}},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.Add(CAValidity),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLen:                  0,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         n.DNS,
		PermittedIPRanges:           n.ipRanges(),
	}
	der, e := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if e != nil {
		return nil, nil, e
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		return nil, nil, e
	}
	keyPEM, e := encodeKey(key)
	if e != nil {
		return nil, nil, e
	}
	return &CA{Cert: cert, Key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, keyPEM, nil
}

// LoadCA parses ca.crt and ca.key.
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("invalid CA PEM")
	}
	cert, e := x509.ParseCertificate(cb.Bytes)
	if e != nil {
		return nil, e
	}
	k, e := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if e != nil {
		return nil, e
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || !cert.IsCA || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("CA key does not match CA certificate")
	}
	return &CA{Cert: cert, Key: key, PEM: certPEM}, nil
}

// Permits reports whether the CA constraints cover every name in n.
func (ca *CA) Permits(n Names) bool {
	for _, d := range n.DNS {
		ok := false
		for _, p := range ca.Cert.PermittedDNSDomains {
			if d == p || strings.HasSuffix(d, "."+p) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, a := range n.IPs {
		ok := false
		for _, r := range ca.Cert.PermittedIPRanges {
			if r.Contains(net.IP(a.AsSlice())) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Leaf signs a server certificate for n and returns certificate and key PEM.
func (ca *CA) Leaf(commonName string, n Names, now time.Time) ([]byte, []byte, error) {
	if !ca.Permits(n) {
		return nil, nil, errors.New("names are outside the CA constraints")
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, e
	}
	serial, e := serialNumber()
	if e != nil {
		return nil, nil, e
	}
	notAfter := now.Add(LeafValidity)
	if notAfter.After(ca.Cert.NotAfter) {
		notAfter = ca.Cert.NotAfter
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"Kraken"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              n.DNS,
		IPAddresses:           n.netIPs(),
	}
	der, e := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
	if e != nil {
		return nil, nil, e
	}
	keyPEM, e := encodeKey(key)
	if e != nil {
		return nil, nil, e
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM, nil
}

// Issue builds the full bundle. With a non-nil ca that permits n only the
// server certificates are renewed and ca.crt stays valid in client trust
// stores; otherwise a new CA is created (newCA reports which).
func Issue(ca *CA, n Names, now time.Time) (b Bundle, newCA bool, e error) {
	b = Bundle{}
	if ca == nil || !ca.Permits(n) || now.Add(LeafValidity).After(ca.Cert.NotAfter) {
		var keyPEM []byte
		if ca, keyPEM, e = NewCA(n, now); e != nil {
			return nil, false, e
		}
		b[CAKey] = File{Data: keyPEM, Mode: 0600}
		newCA = true
	}
	b[CACert] = File{Data: ca.PEM, Mode: 0644, ProxyGroup: true}
	first := n.Strings()[0]
	crt, key, e := ca.Leaf(first, n, now)
	if e != nil {
		return nil, false, e
	}
	b[PanelCert] = File{Data: crt, Mode: 0644}
	b[PanelKey] = File{Data: key, Mode: 0600}
	crt, key, e = ca.Leaf(first, n, now)
	if e != nil {
		return nil, false, e
	}
	b[ProxyCert] = File{Data: crt, Mode: 0640, ProxyGroup: true}
	b[ProxyKey] = File{Data: key, Mode: 0640, ProxyGroup: true}
	return b, newCA, nil
}

func encodeKey(k *ecdsa.PrivateKey) ([]byte, error) {
	der, e := x509.MarshalPKCS8PrivateKey(k)
	if e != nil {
		return nil, e
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func serialNumber() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}
