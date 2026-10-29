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
	"runtime"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"kraken.local/kraken/internal/admin"
	"kraken.local/kraken/internal/httpapi"
	"kraken.local/kraken/internal/proxyauth"
	local "kraken.local/kraken/internal/runtime"
	"kraken.local/kraken/internal/store"
	"kraken.local/kraken/internal/telemetry"
	"kraken.local/kraken/web"

	"golang.org/x/term"
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
		if !errors.Is(e, errQuiet) {
			slog.Error("kraken stopped", "error", e)
		}
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 || os.Args[1] == "help" {
		printHelp()
		return nil
	}
	cmd := os.Args[1]
	// Every CLI subcommand except squid-start and proxy-auth drops root to the
	// kraken user (UID/GID 10013, no supplementary groups) before opening any
	// file. docker exec runs as root by default; the panel and Squid keep their
	// per-UID filesystem isolation (ADR 0015). Non-root invocations are unchanged.
	if requiresPrivilegeDrop(cmd) && os.Geteuid() == 0 {
		if e := dropPrivileges(); e != nil {
			return e
		}
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
		// A forced kill (SIGKILL, OOM, power loss) leaves a stale squid.pid that
		// makes Squid abort with "already running" on the next launch. Remove it
		// before exec unless it still names a live squid; this is the only runtime
		// state squid-start mutates, and it never logs the file contents.
		if removed, e := local.RemoveStalePID(filepath.Join(m.Options.SquidDir, "squid.pid"), local.ProcComm); e != nil {
			return e
		} else if removed {
			slog.Info("removed stale squid pid file")
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
		defer st.Close()
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
		defer st.Close()
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
		defer st.Close()
		telDir := env("KRAKEN_TELEMETRY_DIR", filepath.Join(filepath.Dir(env("KRAKEN_DATABASE_PATH", "/data/app/control.sqlite3")), "telemetry"))
		retentionDays, _ := strconv.Atoi(env("KRAKEN_EVENTS_RETENTION_DAYS", "90"))
		budgetMB, _ := strconv.Atoi(env("KRAKEN_EVENTS_BUDGET_MB", "5120"))
		if retentionDays <= 0 {
			retentionDays = 90
		}
		if budgetMB <= 0 {
			budgetMB = 5120
		}
		tel, e := telemetry.Open(telemetry.Options{Dir: telDir, RawRetention: time.Duration(retentionDays) * 24 * time.Hour, RawBudgetBytes: int64(budgetMB) * 1024 * 1024})
		if e != nil {
			return e
		}
		s, e := httpapi.New(st, m, env("KRAKEN_PUBLIC_ORIGIN", "https://localhost:8443"), web.Handler(), baseImage, tel)
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
			checkpoint := time.NewTicker(10 * time.Minute)
			defer checkpoint.Stop()
			optimize := time.NewTicker(24 * time.Hour)
			defer optimize.Stop()
			for {
				select {
				case <-maintenanceCtx.Done():
					return
				case <-timer.C:
					prune := func() error {
						// Audit retention is age-based (store.AuditRetention, 3y);
						// keep the last 20 unreferenced revisions.
						if e := st.Prune(time.Now().Add(-store.AuditRetention), 20); e != nil {
							return e
						}
						// Drop manager-client assignments for clients removed
						// from both draft and active revision.
						return s.PruneManagerClients()
					}
					if e := m.Maintain(maintenanceCtx, prune); e != nil {
						slog.Warn("maintenance tick failed", "error", e)
					}
					if e := tel.Maintain(maintenanceCtx); e != nil {
						slog.Warn("telemetry maintenance failed", "error", e)
					}
				case <-checkpoint.C:
					if e := st.Checkpoint(); e != nil {
						slog.Warn("wal checkpoint failed", "error", e)
					}
				case <-optimize.C:
					if e := st.Optimize(); e != nil {
						slog.Warn("optimize failed", "error", e)
					}
				}
			}
		}()
		collectorDone := make(chan struct{})
		defer func() { stopMaintenance(); <-collectorDone; tel.Close() }()
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
	case "user":
		return runUser(os.Args[2:])
	case "sessions":
		return runSessions(os.Args[2:])
	case "status":
		return runStatus(m)
	case "backup":
		return runBackup()
	default:
		return fmt.Errorf("unknown command: %s; run 'kraken help'", cmd)
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

// errQuiet is returned after a command has already printed a Russian message
// to stderr: main exits non-zero without adding an English slog line.
var errQuiet = errors.New("")

func fail(msg string) error {
	fmt.Fprintln(os.Stderr, msg)
	return errQuiet
}

func openStore() (*store.Store, error) {
	st, e := store.Open(env("KRAKEN_DATABASE_PATH", "/data/app/control.sqlite3"))
	if e != nil {
		return nil, fail("Не удалось открыть базу данных: " + e.Error())
	}
	return st, nil
}

func failStore(e error) error {
	return fail("Хранилище недоступно: " + e.Error())
}

const (
	krakenUID = 10013
	krakenGID = 10013
)

// requiresPrivilegeDrop reports whether cmd must drop root privileges before
// doing any work. squid-start and proxy-auth keep their dedicated credentials.
func requiresPrivilegeDrop(cmd string) bool {
	switch cmd {
	case "squid-start", "proxy-auth":
		return false
	}
	return true
}

// dropPrivileges irrevocably drops to the kraken user (UID/GID 10013) with no
// supplementary groups. Linux only: the container runs as root and every panel
// CLI command drops before opening files.
func dropPrivileges() error {
	if runtime.GOOS != "linux" {
		return errors.New("privilege drop is supported only on linux")
	}
	if e := syscall.Setgroups([]int{}); e != nil {
		return fmt.Errorf("setgroups: %w", e)
	}
	if e := syscall.Setgid(krakenGID); e != nil {
		return fmt.Errorf("setgid: %w", e)
	}
	if e := syscall.Setuid(krakenUID); e != nil {
		return fmt.Errorf("setuid: %w", e)
	}
	return nil
}

func printHelp() {
	fmt.Println(`Команды:
  serve            Запустить HTTPS-панель (под Pebble)
  bootstrap        Создать первого администратора
  password-reset   Сменить пароль и отозвать сессии
  user             Учётные записи: list, enable, disable, role
  sessions         Отзыв сессий: revoke [--user <имя>]
  status           Ревизия, Squid, пользователи, место на /data
  backup           Онлайн-копия данных в tar
  dev-cert         Самоподписанные dev-сертификаты (только разработка)
  check            Проверка образа и работающего Squid
  service-update   Служебное обновление squid.conf из LKG
  squid-start      Запуск Squid (под пользователем proxy)
  proxy-auth       Helper аутентификации прокси`)
}

func runUser(args []string) error {
	if len(args) == 0 {
		return fail("Использование: kraken user list|enable|disable|role ...")
	}
	sub := args[0]
	st, e := openStore()
	if e != nil {
		return e
	}
	defer st.Close()
	switch sub {
	case "list":
		rows, e := admin.ListUsers(st)
		if e != nil {
			return failStore(e)
		}
		if len(rows) == 0 {
			fmt.Println("Нет пользователей.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ИМЯ\tРОЛЬ\tВКЛЮЧ\tСЕССИЙ")
		for _, r := range rows {
			en := "нет"
			if r.Enabled {
				en = "да"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", r.Username, r.Role, en, r.Sessions)
		}
		return w.Flush()
	case "enable":
		if len(args) != 2 {
			return fail("Использование: kraken user enable <имя>")
		}
		if e := admin.SetEnabled(st, args[1], true, "cli"); e != nil {
			return mapUserErr(e)
		}
		fmt.Println("Учётная запись включена.")
		return nil
	case "disable":
		if len(args) != 2 {
			return fail("Использование: kraken user disable <имя>")
		}
		if e := admin.SetEnabled(st, args[1], false, "cli"); e != nil {
			return mapUserErr(e)
		}
		fmt.Println("Учётная запись отключена, сессии отозваны.")
		return nil
	case "role":
		if len(args) != 3 || (args[2] != "admin" && args[2] != "manager") {
			return fail("Использование: kraken user role <имя> admin|manager")
		}
		if e := admin.SetRole(st, args[1], args[2], "cli"); e != nil {
			return mapUserErr(e)
		}
		fmt.Printf("Роль изменена на %s, сессии отозваны.\n", args[2])
		return nil
	default:
		return fail("Неизвестная подкоманда user: " + sub)
	}
}

func mapUserErr(e error) error {
	switch {
	case errors.Is(e, admin.ErrLastAdmin):
		return fail("Нельзя отключить или понизить последнего включённого администратора.")
	case errors.Is(e, admin.ErrNotFound):
		return fail("Пользователь не найден.")
	default:
		return fail("Не удалось изменить пользователя: " + e.Error())
	}
}

func runSessions(args []string) error {
	if len(args) == 0 || args[0] != "revoke" {
		return fail("Использование: kraken sessions revoke [--user <имя>] [--yes]")
	}
	fs := flag.NewFlagSet("sessions revoke", flag.ContinueOnError)
	user := fs.String("user", "", "отозвать сессии одного пользователя (по умолчанию все)")
	yes := fs.Bool("yes", false, "не спрашивать подтверждение для отзыва всех сессий")
	if e := fs.Parse(args[1:]); e != nil {
		return errQuiet
	}
	if *user == "" && !*yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fail("Отзыв всех сессий в неинтерактивном режиме требует --yes.")
		}
		fmt.Fprint(os.Stderr, "Отозвать ВСЕ сессии? Введите yes: ")
		var resp string
		fmt.Fscanln(os.Stdin, &resp)
		if resp != "yes" {
			return fail("Отменено.")
		}
	}
	st, e := openStore()
	if e != nil {
		return e
	}
	defer st.Close()
	n, e := admin.RevokeSessions(st, *user, "cli")
	if e != nil {
		return failStore(e)
	}
	if *user == "" {
		fmt.Printf("Отозвано сессий: %d\n", n)
	} else {
		fmt.Printf("Отозвано сессий пользователя %s: %d\n", *user, n)
	}
	return nil
}

func runStatus(m *local.Manager) error {
	ctx := context.Background()
	rev, e := m.ReadManifest()
	revision := "(нет)"
	if e == nil && rev.Revision != "" {
		revision = rev.Revision
	}
	fmt.Printf("Ревизия: %s\n", revision)
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	squid := "ok"
	if ve := m.Verify(vctx, rev.Hash); ve != nil {
		squid = "ошибка: " + ve.Error()
	}
	fmt.Printf("Squid: %s\n", squid)
	if st, e := store.Open(env("KRAKEN_DATABASE_PATH", "/data/app/control.sqlite3")); e == nil {
		var n int
		_ = st.Read.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
		st.Close()
		fmt.Printf("Пользователей: %d\n", n)
	} else {
		fmt.Printf("Пользователей: (база недоступна)\n")
	}
	dataDir := env("KRAKEN_DATA_DIR", "/data")
	fmt.Printf("Свободно на %s: %s\n", dataDir, humanBytes(diskFree(dataDir)))
	return nil
}

func runBackup() error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fs.String("out", "", "путь к tar-архиву (по умолчанию /data/backups/kraken-<UTC>.tar)")
	if e := fs.Parse(os.Args[2:]); e != nil {
		return errQuiet
	}
	dataDir := env("KRAKEN_DATA_DIR", "/data")
	st, e := openStore()
	if e != nil {
		return e
	}
	defer st.Close()
	target := *out
	if target == "" {
		target = filepath.Join(dataDir, "backups", "kraken-"+time.Now().UTC().Format("20060102-150405")+".tar")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if e := admin.Backup(ctx, st, dataDir, target); e != nil {
		return fail("Ошибка копии: " + e.Error())
	}
	fmt.Println(target)
	return nil
}

func diskFree(path string) uint64 {
	var s syscall.Statfs_t
	if e := syscall.Statfs(path, &s); e != nil {
		return 0
	}
	return uint64(s.Bavail) * uint64(s.Bsize)
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
