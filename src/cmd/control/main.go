package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"kraken.local/kraken/internal/httpapi"
	"kraken.local/kraken/internal/proxyauth"
	local "kraken.local/kraken/internal/runtime"
	"kraken.local/kraken/internal/store"
	"kraken.local/kraken/web"
)

const baseImage = "ubuntu/squid@sha256:6a097f68bae708cedbabd6188d68c7e2e7a38cedd05a176e1cc0ba29e3bbe029"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func manager() *local.Manager {
	m := local.New(env("KRAKEN_RUNTIME_STATE_DIR", "/data/runtime"), env("KRAKEN_SQUID_BINARY", "/usr/sbin/squid"), env("KRAKEN_ACCESS_LOG", "/data/logs/access.log"), os.Getenv("KRAKEN_PROXY_CERT"), os.Getenv("KRAKEN_PROXY_KEY"), baseImage)
	m.AppDir = env("KRAKEN_APP_DIR", "/data/app")
	m.Options.SquidDir = env("KRAKEN_SQUID_DIR", "/data/squid")
	m.Options.ProxyAccounts = env("KRAKEN_PROXY_ACCOUNTS", "/data/runtime/proxy-accounts.json")
	return m
}
func main() {
	if e := run(); e != nil {
		slog.Error("kraken stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: kraken serve|bootstrap|password-reset|dev-cert|squid-start|check|proxy-auth|service-update")
	}
	cmd := os.Args[1]
	// serve and every CLI subcommand except squid-start/proxy-auth refuse to
	// run as root: a privileged panel process would defeat the per-UID filesystem
	// isolation between Squid (proxy) and the panel (kraken).
	if os.Geteuid() == 0 && cmd != "squid-start" && cmd != "proxy-auth" {
		return errors.New("refusing to run as root")
	}
	m := manager()
	ctx := context.Background()
	switch cmd {
	case "proxy-auth":
		f := flag.NewFlagSet(cmd, flag.ContinueOnError)
		stateOnly := f.Bool("state", false, "check authenticated verifier generation")
		accounts := f.String("accounts", env("KRAKEN_PROXY_ACCOUNTS", "/data/runtime/proxy-accounts.json"), "exported proxy accounts")
		if e := f.Parse(os.Args[2:]); e != nil {
			return e
		}
		if *stateOnly {
			return proxyauth.RunState(*accounts, os.Stdin, os.Stdout)
		}
		return proxyauth.Run(*accounts, os.Stdin, os.Stdout)
	case "squid-start":
		// squid-start runs as the proxy user and never writes runtime state:
		// it waits for serve (kraken) to persist a consistent active.conf
		// and journal, then execs squid with a private umask.
		deadline := time.Now().Add(120 * time.Second)
		for {
			ready, e := m.SquidReady()
			if e != nil {
				return e
			}
			if ready {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("squid-start timed out waiting for active configuration")
			}
			time.Sleep(200 * time.Millisecond)
		}
		syscall.Umask(0o027)
		return syscall.Exec(m.Binary, []string{m.Binary, "-N", "-f", m.ActivePath()}, os.Environ())
	case "service-update":
		if len(os.Args) != 2 {
			return errors.New("usage: kraken service-update")
		}
		st, e := store.Open(env("KRAKEN_DATABASE_PATH", "/data/app/control.sqlite3"))
		if e != nil {
			return e
		}
		defer st.DB.Close()
		operationCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		op, e := httpapi.ApplyServiceUpdate(operationCtx, st, m, baseImage)
		if op.ID != "" {
			fmt.Printf("Service update %s: %s\n", op.ID, op.Status)
		}
		return e
	case "check":
		v, e := m.ReadManifest()
		if e != nil {
			return e
		}
		ctx, c := context.WithTimeout(ctx, 2*time.Second)
		defer c()
		return m.Verify(ctx, v.Hash)
	case "dev-cert":
		return devCert()
	case "bootstrap", "password-reset":
		f := flag.NewFlagSet(cmd, flag.ContinueOnError)
		username := f.String("username", "admin", "administrator username")
		stdin := f.Bool("password-stdin", false, "read new password from stdin instead of a hidden terminal prompt")
		if e := f.Parse(os.Args[2:]); e != nil {
			return e
		}
		password, e := readPassword(*stdin)
		if e != nil {
			return e
		}
		st, e := store.Open(env("KRAKEN_DATABASE_PATH", "/data/app/control.sqlite3"))
		if e != nil {
			return e
		}
		defer st.DB.Close()
		if cmd == "bootstrap" {
			e = httpapi.Bootstrap(st, *username, password)
		} else {
			e = httpapi.ResetPassword(st, *username, password)
		}
		if e != nil {
			return e
		}
		if cmd == "bootstrap" {
			fmt.Println("Administrator created")
		} else {
			fmt.Println("Password reset; previous sessions revoked")
		}
		return nil
	case "serve":
		if e := os.MkdirAll(filepath.Dir(m.Options.LogPath), 0700); e != nil {
			return e
		}
		// Offline recovery first: durable on-disk state is restored without
		// touching the live process so a crashed promotion is rolled back even
		// if Squid has not started yet.
		for i := 0; i < 50; i++ {
			e := m.Recover(ctx, true)
			if errors.Is(e, local.ErrBusy) {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if e != nil {
				return e
			}
			break
		}
		// Online recovery: reload+verify against the running Squid. Squid and
		// serve start concurrently under Pebble, so a transient reload/verify
		// failure is retried for up to 120 s before serve aborts.
		onlineDeadline := time.Now().Add(120 * time.Second)
		for {
			e := m.Recover(ctx, false)
			if e == nil {
				break
			}
			if !errors.Is(e, local.ErrBusy) && time.Now().After(onlineDeadline) {
				return e
			}
			time.Sleep(500 * time.Millisecond)
		}
		st, e := store.Open(env("KRAKEN_DATABASE_PATH", "/data/app/control.sqlite3"))
		if e != nil {
			return e
		}
		defer st.DB.Close()
		s, e := httpapi.New(st, m, env("KRAKEN_PUBLIC_ORIGIN", "https://localhost:8443"), web.Handler(), baseImage)
		if e != nil {
			return e
		}
		s.PanelCert = env("KRAKEN_PANEL_CERT", "/data/certs/panel.crt")
		// Docker forwards the published port to the container interface, not its
		// loopback. Host exposure is restricted by Compose port bindings.
		server := &http.Server{Addr: env("KRAKEN_PANEL_LISTEN", ":8443"), Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, TLSConfig: &tls.Config{
			MinVersion:               tls.VersionTLS12,
			MaxVersion:               tls.VersionTLS13,
			CipherSuites:             []uint16{tls.TLS_AES_128_GCM_SHA256, tls.TLS_AES_256_GCM_SHA384, tls.TLS_CHACHA20_POLY1305_SHA256, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256},
			CurvePreferences:         []tls.CurveID{tls.X25519, tls.CurveP256},
			PreferServerCipherSuites: true,
		}}
		maintenanceCtx, stopMaintenance := context.WithCancel(context.Background())
		defer stopMaintenance()
		go func() {
			timer := time.NewTicker(time.Minute)
			defer timer.Stop()
			for {
				select {
				case <-maintenanceCtx.Done():
					return
				case <-timer.C:
					prune := func() error {
						// 90 days of audit events, last 20 unreferenced revisions.
						// Matches docs/architecture/data-model.md retention.
						if e := st.Prune(time.Now().Add(-90*24*time.Hour), 20); e != nil {
							return e
						}
						// Drop manager-client assignments for clients removed
						// from both draft and active revision.
						return s.PruneManagerClients()
					}
					if e := m.Maintain(maintenanceCtx, prune); e != nil {
						slog.Warn("maintenance tick failed", "error", e)
					}
				}
			}
		}()
		collectorDone := make(chan struct{})
		defer func() { stopMaintenance(); <-collectorDone }()
		go func() {
			defer close(collectorDone)
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				if e := s.CollectEvents(); e != nil {
					slog.Warn("event collection failed", "error", e)
				}
				select {
				case <-maintenanceCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(stop)
		go func() {
			<-stop
			slog.Info("shutdown signal received, draining workers")
			// First grace attempt: HTTP server shutdown with a 10 s budget. Workers
			// can take up to ~135 s (90 s apply ctx + 45 s rollback); signal a
			// second time to force an ungraceful exit if graceful drain stalls.
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			server.Shutdown(c)
			done := make(chan struct{})
			go func() { s.Wait(); close(done) }()
			select {
			case <-done:
			case <-stop:
				slog.Warn("second signal received, forcing exit")
				os.Exit(1)
			}
		}()
		slog.Info("HTTPS panel listening", "address", server.Addr)
		e = server.ListenAndServeTLS(env("KRAKEN_PANEL_CERT", "/data/certs/panel.crt"), env("KRAKEN_PANEL_KEY", "/data/certs/panel.key"))
		s.Wait()
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}
func devCert() error {
	dir := env("KRAKEN_CERT_DIR", "/data/certs")
	if e := os.MkdirAll(dir, 0750); e != nil {
		return e
	}
	for _, n := range []string{"panel.crt", "panel.key", "proxy.crt", "proxy.key"} {
		if _, e := os.Stat(filepath.Join(dir, n)); !errors.Is(e, os.ErrNotExist) {
			return errors.New("certificate files already exist")
		}
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return e
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Kraken local development"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(30 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	cert, e := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if e != nil {
		return e
	}
	der, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return e
	}
	if e = local.Atomic(filepath.Join(dir, "panel.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); e != nil {
		return e
	}
	if e = local.Atomic(filepath.Join(dir, "panel.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})); e != nil {
		return e
	}
	// Separate self-signed proxy certificate: SAN covers the in-network alias
	// "proxy" plus localhost so panel-driven HTTPS-CONNECT tests can pin it.
	proxyKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	proxySerial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return e
	}
	proxyTemplate := x509.Certificate{SerialNumber: proxySerial, Subject: pkix.Name{CommonName: "Kraken proxy"}, DNSNames: []string{"proxy", "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(30 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	proxyCert, e := x509.CreateCertificate(rand.Reader, &proxyTemplate, &proxyTemplate, &proxyKey.PublicKey, proxyKey)
	if e != nil {
		return e
	}
	proxyDER, e := x509.MarshalPKCS8PrivateKey(proxyKey)
	if e != nil {
		return e
	}
	// proxy.* is group_readable (0640, group proxy) so Squid can read it; the
	// setgid runtime/certs directories already inherit group=proxy, the
	// explicit chown keeps correctness if that inheritance is unavailable.
	if e = local.AtomicMode(filepath.Join(dir, "proxy.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: proxyDER}), 0640); e != nil {
		return e
	}
	if e = local.AtomicMode(filepath.Join(dir, "proxy.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxyCert}), 0640); e != nil {
		return e
	}
	for _, n := range []string{"proxy.key", "proxy.crt"} {
		p := filepath.Join(dir, n)
		if e = os.Chmod(p, 0640); e != nil {
			return e
		}
		_ = os.Chown(p, -1, 13)
	}
	fmt.Println("Created self-signed localhost certificate (development only)")
	return nil
}
