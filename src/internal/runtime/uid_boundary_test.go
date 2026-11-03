package runtime

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kraken.local/kraken/internal/policy"
)

// Squid authorizes the action only with the exact token; any other status is a failure.
func TestDefaultReloadSendsTokenAndRejectsDenied(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:3131")
	if e != nil {
		t.Skipf("cannot bind 3131: %v", e)
	}
	defer ln.Close()
	m := testManager(t)
	m.Reload = m.defaultReload
	token := m.secret
	if token == "" || !managerToken.MatchString(token) {
		t.Fatalf("no valid secret after recover: %q", token)
	}
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("kraken:"+token))
	status := make(chan string, 4)
	reqs := make(chan string, 4)
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			br := bufio.NewReader(c)
			var head strings.Builder
			for {
				line, err := br.ReadString('\n')
				head.WriteString(line)
				if err != nil || strings.TrimSpace(line) == "" {
					break
				}
			}
			reqs <- head.String()
			c.Write([]byte("HTTP/1.1 " + <-status + "\r\nContent-Length: 0\r\n\r\n"))
			c.Close()
		}
	}()
	// 200 OK: verify the exact request line and the Authorization header.
	status <- "200 OK"
	if e := m.Reload(context.Background(), "rotate"); e != nil {
		t.Fatal(e)
	}
	got := <-reqs
	if reqLine := strings.SplitN(got, "\r\n", 2)[0]; reqLine != "GET /squid-internal-mgr/rotate HTTP/1.1" {
		t.Fatalf("request line %q", reqLine)
	}
	authLine := ""
	for _, l := range strings.Split(got, "\r\n") {
		if strings.HasPrefix(l, "Authorization: ") {
			authLine = l
		}
	}
	if authLine != "Authorization: "+expectedAuth {
		t.Fatalf("authorization %q, want %q", authLine, "Authorization: "+expectedAuth)
	}
	// 403 Forbidden: reload must fail without leaking the secret.
	status <- "403 Forbidden"
	e = m.Reload(context.Background(), "reconfigure")
	<-reqs
	if e == nil || strings.Contains(e.Error(), token) {
		t.Fatalf("denied reload (403): %v", e)
	}
	// 401 Unauthorized: same — error without the secret.
	status <- "401 Unauthorized"
	e = m.Reload(context.Background(), "rotate")
	<-reqs
	if e == nil || strings.Contains(e.Error(), token) {
		t.Fatalf("denied reload (401): %v", e)
	}
}

// Squid (another UID, same group) must be able to read everything it is handed.
func TestSquidReadableStateModes(t *testing.T) {
	m := testManager(t)
	s := policy.Empty()
	s.TLSProfiles[0].Name = "Changed"
	if out, e := m.Apply(context.Background(), "op", "rev", "", s); e != nil {
		t.Fatal(out, e)
	}
	for _, name := range []string{"squid.conf", "manager-auth.conf"} {
		st, e := os.Stat(filepath.Join(m.Dir, name))
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		if st.Mode().Perm() != 0640 {
			t.Fatalf("%s: mode %o", name, st.Mode().Perm())
		}
	}
	// No manager token file is persisted anywhere on disk; the secret lives
	// only in memory and the manager-auth.conf include.
	if _, e := os.Stat(filepath.Join(m.Dir, "manager-token")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("manager-token file must not exist")
	}
	// A malformed manager auth include must be rejected on recovery.
	if e := os.WriteFile(filepath.Join(m.Dir, "manager-auth.conf"), []byte("\n"), 0640); e != nil {
		t.Fatal(e)
	}
	if e := m.Recover(context.Background(), true); e == nil {
		t.Fatal("malformed manager auth include accepted")
	}
}
