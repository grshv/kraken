package runtime

import (
	"bufio"
	"context"
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
	token, e := m.retrieveToken()
	if e != nil {
		t.Fatal(e)
	}
	status := make(chan string, 4)
	lines := make(chan string, 4)
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			lines <- strings.TrimSpace(line)
			c.Write([]byte("HTTP/1.0 " + <-status + "\r\nContent-Length: 0\r\n\r\n"))
			c.Close()
		}
	}()
	status <- "200 OK"
	if e := m.Reload(context.Background(), "rotate"); e != nil {
		t.Fatal(e)
	}
	if got := <-lines; got != "GET cache_object://127.0.0.1/rotate@"+token+" HTTP/1.0" {
		t.Fatalf("request line %q", got)
	}
	status <- "403 Forbidden"
	e = m.Reload(context.Background(), "reconfigure")
	<-lines
	if e == nil || strings.Contains(e.Error(), token) {
		t.Fatalf("denied reload: %v", e)
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
	for _, name := range []string{"active.conf", "lkg.json", "journal.json", "manager-auth.conf"} {
		st, e := os.Stat(filepath.Join(m.Dir, name))
		if e != nil || st.Mode().Perm() != 0640 {
			t.Fatalf("%s: %v %v", name, st.Mode(), e)
		}
	}
	if st, e := os.Stat(filepath.Join(m.AppDir, "manager-token")); e != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("token mode: %v", e)
	}
	if e := os.WriteFile(filepath.Join(m.AppDir, "manager-token"), []byte("\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.Recover(context.Background(), true); e == nil {
		t.Fatal("empty manager token accepted")
	}
}
