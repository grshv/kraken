package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	local "kraken.local/kraken/internal/runtime"
	"kraken.local/kraken/internal/tlscert"
)

func certDir() string { return env("KRAKEN_CERT_DIR", filepath.Join(stateDir(), "certs")) }

// splitList splits a comma- or whitespace-separated environment value.
func splitList(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}

// certNames collects every name a client may use to reach the panel or the
// TLS proxy: localhost, the public origin host, the host addresses Compose
// publishes on (KRAKEN_MANAGEMENT_BIND, KRAKEN_PROXY_BIND; 0.0.0.0 is skipped)
// and KRAKEN_CERT_NAMES for extra DNS names or addresses.
func certNames() (tlscert.Names, error) {
	items := append([]string{}, tlscert.DefaultNames...)
	if u, e := url.Parse(env("KRAKEN_PUBLIC_ORIGIN", "https://localhost:8443")); e == nil && u.Hostname() != "" {
		items = append(items, u.Hostname())
	}
	for _, k := range []string{"KRAKEN_MANAGEMENT_BIND", "KRAKEN_PROXY_BIND", "KRAKEN_CERT_NAMES"} {
		items = append(items, splitList(os.Getenv(k))...)
	}
	return tlscert.ParseNames(items...)
}

func exists(p string) bool {
	_, e := os.Lstat(p)
	return e == nil
}

// loadCA returns the stored CA, or nil when there is none (or it is unusable,
// in which case a new one is issued).
func loadCA(dir string) *tlscert.CA {
	c, e1 := os.ReadFile(filepath.Join(dir, tlscert.CACert))
	k, e2 := os.ReadFile(filepath.Join(dir, tlscert.CAKey))
	if e1 != nil || e2 != nil {
		return nil
	}
	ca, e := tlscert.LoadCA(c, k)
	if e != nil {
		return nil
	}
	return ca
}

// writeBundle stores the files atomically; keys first, panel certificate
// last. Squid-readable files get group proxy (the setgid directory already
// gives it; the chown keeps it right if inheritance is missing).
func writeBundle(dir string, b tlscert.Bundle) error {
	if e := os.MkdirAll(dir, 0750); e != nil {
		return e
	}
	for _, name := range []string{tlscert.CAKey, tlscert.CACert, tlscert.ProxyKey, tlscert.ProxyCert, tlscert.PanelKey, tlscert.PanelCert} {
		f, ok := b[name]
		if !ok {
			continue
		}
		p := filepath.Join(dir, name)
		if e := local.AtomicMode(p, f.Data, os.FileMode(f.Mode)); e != nil {
			return e
		}
		if f.ProxyGroup {
			_ = local.ChownProxyGroup(p)
		}
		if e := os.Chmod(p, os.FileMode(f.Mode)); e != nil {
			return e
		}
	}
	return nil
}

// issueCertificates signs fresh panel and proxy certificates, reusing the
// stored CA when it permits the names.
func issueCertificates(dir string) (tlscert.Names, bool, error) {
	n, e := certNames()
	if e != nil {
		return n, false, e
	}
	b, newCA, e := tlscert.Issue(loadCA(dir), n, time.Now())
	if e != nil {
		return n, false, e
	}
	return n, newCA, writeBundle(dir, b)
}

// ensureCertificates runs at serve start. A fresh installation has no server
// certificates: they are issued instead of failing (and restart-looping)
// until an operator runs a command. A partial set is an operator error and is
// reported, never silently replaced.
func ensureCertificates() error {
	dir := certDir()
	var present, missing []string
	for _, n := range tlscert.Leaves {
		if exists(filepath.Join(dir, n)) {
			present = append(present, n)
		} else {
			missing = append(missing, n)
		}
	}
	if len(present) == 0 {
		n, _, e := issueCertificates(dir)
		if e != nil {
			return fmt.Errorf("cannot issue certificates: %w", e)
		}
		fmt.Fprintf(os.Stderr, "Выпущены сертификаты панели и прокси (локальный CA %s) для: %s. Доверие: kraken cert ca > kraken-ca.crt\n", filepath.Join(dir, tlscert.CACert), strings.Join(n.Strings(), ", "))
		return nil
	}
	// Explicit external files replace the defaults; only the defaults in use
	// must be complete.
	var needed []string
	for _, n := range missing {
		if strings.HasPrefix(n, "panel.") && os.Getenv("KRAKEN_PANEL_CERT") != "" {
			continue
		}
		if strings.HasPrefix(n, "proxy.") && os.Getenv("KRAKEN_PROXY_CERT") != "" {
			continue
		}
		needed = append(needed, n)
	}
	if len(needed) > 0 {
		return fmt.Errorf("incomplete certificates in %s: missing %s; run kraken cert renew", dir, strings.Join(needed, ", "))
	}
	return nil
}

func runCert(args []string) error {
	usage := func() error {
		return fail("Использование: kraken cert ca | renew | install panel|proxy < cert-and-key.pem")
	}
	dir := certDir()
	switch {
	case len(args) == 1 && args[0] == "ca":
		b, e := os.ReadFile(filepath.Join(dir, tlscert.CACert))
		if errors.Is(e, os.ErrNotExist) {
			return fail("Локального CA нет: установлены свои сертификаты")
		}
		if e != nil {
			return fail("Не удалось прочитать CA: " + e.Error())
		}
		_, e = os.Stdout.Write(b)
		return e
	case len(args) == 1 && args[0] == "renew":
		n, newCA, e := issueCertificates(dir)
		if e != nil {
			return fail("Не удалось выпустить сертификаты: " + e.Error())
		}
		fmt.Printf("Сертификаты панели и прокси выпущены для: %s\n", strings.Join(n.Strings(), ", "))
		if newCA {
			fmt.Println("Создан новый локальный CA: импортируйте его заново (kraken cert ca > kraken-ca.crt).")
		}
		fmt.Println("Чтобы панель и Squid загрузили их, перезапустите контейнер: docker compose restart kraken")
		return nil
	case len(args) == 2 && args[0] == "install" && (args[1] == "panel" || args[1] == "proxy"):
		if e := installCertificate(dir, args[1], os.Stdin, time.Now()); e != nil {
			return fail("Сертификат не установлен: " + e.Error())
		}
		fmt.Printf("Сертификат %s установлен. Перезапустите контейнер: docker compose restart kraken\n", args[1])
		return nil
	}
	return usage()
}

// maxInstallPEM bounds stdin: a chain and a key fit easily.
const maxInstallPEM = 1 << 20

// installCertificate stores an operator certificate read from stdin as PEM:
// the server certificate, optional intermediates, and the private key. The
// key never passes through argv, env or a host mount. The pair must match,
// be valid now and allow server authentication; otherwise nothing is
// written.
func installCertificate(dir, purpose string, r io.Reader, now time.Time) error {
	b, e := io.ReadAll(io.LimitReader(r, maxInstallPEM+1))
	if e != nil {
		return e
	}
	if len(b) > maxInstallPEM {
		return errors.New("input larger than 1 MiB")
	}
	var certs, keys []byte
	for rest := b; ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		switch {
		case blk.Type == "CERTIFICATE":
			certs = append(certs, pem.EncodeToMemory(blk)...)
		case strings.HasSuffix(blk.Type, "PRIVATE KEY"):
			if keys != nil {
				return errors.New("more than one private key")
			}
			keys = pem.EncodeToMemory(blk)
		default:
			return fmt.Errorf("unexpected PEM block %q", blk.Type)
		}
	}
	if certs == nil || keys == nil {
		return errors.New("expected PEM certificate and private key on stdin")
	}
	pair, e := tls.X509KeyPair(certs, keys)
	if e != nil {
		return fmt.Errorf("certificate and key do not match: %w", e)
	}
	leaf, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return e
	}
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("certificate is not valid now (%s – %s)", leaf.NotBefore.Format(time.DateOnly), leaf.NotAfter.Format(time.DateOnly))
	}
	if leaf.IsCA {
		return errors.New("a CA certificate cannot be a server certificate")
	}
	if len(leaf.ExtKeyUsage) > 0 && !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) && !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageAny) {
		return errors.New("certificate does not allow server authentication")
	}
	if len(leaf.DNSNames)+len(leaf.IPAddresses) == 0 {
		return errors.New("certificate has no subject alternative names")
	}
	b2 := tlscert.Bundle{}
	if purpose == "panel" {
		b2[tlscert.PanelCert] = tlscert.File{Data: certs, Mode: 0644}
		b2[tlscert.PanelKey] = tlscert.File{Data: keys, Mode: 0600}
	} else {
		b2[tlscert.ProxyCert] = tlscert.File{Data: certs, Mode: 0640, ProxyGroup: true}
		b2[tlscert.ProxyKey] = tlscert.File{Data: keys, Mode: 0640, ProxyGroup: true}
	}
	return writeBundle(dir, b2)
}
