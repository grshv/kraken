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

	"squid-control.local/squid/internal/httpapi"
	local "squid-control.local/squid/internal/runtime"
	"squid-control.local/squid/internal/store"
	"squid-control.local/squid/web"
)

const baseImage = "ubuntu/squid@sha256:6a097f68bae708cedbabd6188d68c7e2e7a38cedd05a176e1cc0ba29e3bbe029"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func manager() *local.Manager {
	return local.New(env("SC_RUNTIME_STATE_DIR", "/data/runtime"), env("SC_SQUID_BINARY", "/usr/sbin/squid"), env("SC_ACCESS_LOG", "/data/logs/access.log"), os.Getenv("SC_PROXY_CERT"), os.Getenv("SC_PROXY_KEY"), baseImage)
}
func main() {
	if e := run(); e != nil {
		slog.Error("squid-control stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: squid-control serve|bootstrap|password-reset|dev-cert|squid-start|check")
	}
	cmd := os.Args[1]
	m := manager()
	ctx := context.Background()
	switch cmd {
	case "squid-start":
		for i := 0; i < 50; i++ {
			e := m.Recover(ctx, true)
			if errors.Is(e, local.ErrBusy) {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if e != nil {
				return e
			}
			return syscall.Exec(m.Binary, []string{m.Binary, "-N", "-f", m.ActivePath()}, os.Environ())
		}
		return local.ErrBusy
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
		st, e := store.Open(env("SC_DATABASE_PATH", "/data/app/control.sqlite3"))
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
		if os.Geteuid() == 0 {
			return errors.New("refusing to run web as root")
		}
		if e := os.MkdirAll(filepath.Dir(m.Options.LogPath), 0700); e != nil {
			return e
		}
		recovered := false
		for i := 0; i < 50; i++ {
			e := m.Recover(ctx, false)
			if errors.Is(e, local.ErrBusy) {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if e != nil {
				return e
			}
			recovered = true
			break
		}
		if !recovered {
			return local.ErrBusy
		}
		st, e := store.Open(env("SC_DATABASE_PATH", "/data/app/control.sqlite3"))
		if e != nil {
			return e
		}
		defer st.DB.Close()
		s, e := httpapi.New(st, m, env("SC_PUBLIC_ORIGIN", "https://localhost:8443"), web.Handler(), baseImage)
		if e != nil {
			return e
		}
		s.PanelCert = env("SC_PANEL_CERT", "/data/certs/panel.crt")
		server := &http.Server{Addr: env("SC_PANEL_LISTEN", "127.0.0.1:8443"), Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, TLSConfig: &tls.Config{
			MinVersion:           tls.VersionTLS12,
			MaxVersion:           tls.VersionTLS13,
			CipherSuites:         []uint16{tls.TLS_AES_128_GCM_SHA256, tls.TLS_AES_256_GCM_SHA384, tls.TLS_CHACHA20_POLY1305_SHA256, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256},
			CurvePreferences:     []tls.CurveID{tls.X25519, tls.CurveP256},
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
						return st.Prune(time.Now().Add(-90*24*time.Hour), 20)
					}
					if e := m.Maintain(maintenanceCtx, prune); e != nil {
						slog.Warn("maintenance tick failed", "error", e)
					}
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
		e = server.ListenAndServeTLS(env("SC_PANEL_CERT", "/data/certs/panel.crt"), env("SC_PANEL_KEY", "/data/certs/panel.key"))
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
	dir := env("SC_CERT_DIR", "/data/certs")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	for _, n := range []string{"panel.crt", "panel.key"} {
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
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Squid Control local development"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(30 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	fmt.Println("Created self-signed localhost certificate (development only)")
	return nil
}
