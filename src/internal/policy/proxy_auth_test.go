package policy

import (
	"path/filepath"
	"strings"
	"testing"
)

func authFixture() (Snapshot, Options) {
	s := fixture()
	s.TLSProfiles[0].Transport = "tls"
	ref := "proxy-server"
	s.TLSProfiles[0].CertRef = &ref
	s.Clients[0].ProxyUsername = "build.agent"
	return s, Options{StateDir: "/data/runtime", SquidDir: "/data/squid", LogPath: "/data/logs/access.log", ProxyCert: "/data/certs/proxy.crt", ProxyKey: "/data/certs/proxy.key", ProxyAccounts: "/data/runtime/proxy-accounts.json"}
}
func TestAuthenticatedPolicyRequiresTLSAndInstalledDatabase(t *testing.T) {
	s, o := authFixture()
	if e := Validate(s, o); e != nil {
		t.Fatal(e)
	}
	plain := s
	plain.TLSProfiles = append([]TLSProfile(nil), s.TLSProfiles...)
	plain.TLSProfiles[0].Transport = "http"
	plain.TLSProfiles[0].CertRef = nil
	if e := Validate(plain, o); e == nil {
		t.Fatal("Basic over plaintext allowed")
	}
	missing := o
	missing.ProxyAccounts = ""
	if e := Validate(s, missing); e == nil {
		t.Fatal("authentication without installed DB allowed")
	}
	s.Clients[0].ProxyUsername = "build\nhttp_access allow all"
	if e := Validate(s, o); e == nil {
		t.Fatal("username injection allowed")
	}
	s.Clients[0].ProxyUsername = "build"
	s.Mode = "expert"
	s.Expert = "http_access allow all"
	if e := Validate(s, o); e == nil {
		t.Fatal("expert bypass of authenticated clients allowed")
	}
}
func TestAuthenticatedNATClientsNeedDistinctCredentials(t *testing.T) {
	s, o := authFixture()
	other := s.Clients[0]
	other.ID = "second"
	other.ProxyUsername = "other"
	s.Clients = append(s.Clients, other)
	if e := Validate(s, o); e != nil {
		t.Fatal("NAT identities rejected", e)
	}
	s.Clients[1].ProxyUsername = s.Clients[0].ProxyUsername
	if e := Validate(s, o); e == nil {
		t.Fatal("same credential overlap accepted")
	}
	s.Clients[1].ProxyUsername = ""
	if e := Validate(s, o); e == nil {
		t.Fatal("IP-only bypass overlap accepted")
	}
}
func TestRenderManagerPolicyLayout(t *testing.T) {
	s, o := authFixture()
	c, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(c, "pid_filename "+o.SquidDir+"/squid.pid\n") {
		t.Fatal("missing pid_filename in the Squid run directory")
	}
	if !strings.Contains(c, "cache_log "+filepath.Dir(o.LogPath)+"/cache.log\n") {
		t.Fatal("missing cache_log derived from log path")
	}
	inc := strings.Index(c, "include "+o.StateDir+"/manager-auth.conf\n")
	dis := strings.Index(c, "cachemgr_passwd disable all\n")
	if inc < 0 || dis < 0 || inc > dis {
		t.Fatalf("include must precede cachemgr_passwd disable all: inc=%d dis=%d", inc, dis)
	}
	ctrl := strings.Index(c, "acl sc_manager_control urlpath_regex")
	allowCtrl := strings.Index(c, "http_access allow sc_probe_source sc_manager_listener sc_manager_control manager\n")
	denyListener := strings.Index(c, "http_access deny sc_manager_listener\n")
	if ctrl < 0 || allowCtrl < 0 || denyListener < 0 || !(ctrl < allowCtrl && allowCtrl < denyListener) {
		t.Fatal("sc_manager_control ACL and allow must come before deny sc_manager_listener")
	}
	// Token-protected actions live in the included manager-auth.conf, never in
	// the rendered config itself.
	for _, line := range strings.Split(c, "\n") {
		if strings.HasPrefix(line, "cachemgr_passwd ") && (strings.Contains(line, "reconfigure") || strings.Contains(line, "rotate")) {
			t.Fatalf("manager token action leaked into rendered config: %s", line)
		}
	}
}

func TestAuthenticatedRenderIsManagedAndFailClosed(t *testing.T) {
	s, o := authFixture()
	s.Clients[0].ProxyUsername = "REQUIRED"
	config, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"auth_param basic program /usr/local/bin/kraken proxy-auth --accounts /data/runtime/proxy-accounts.json", "auth_param basic credentialsttl 1 seconds", "auth_param basic casesensitive on", "acl sc_proxy_user_0 proxy_auth_regex ^REQUIRED$", "http_access deny sc_auth_sources sc_http_listener", "http_access deny sc_auth_sources sc_tls_listener !sc_proxy_authenticated", "http_access allow client_0 sc_tls_listener destinations_0 sc_proxy_user_0"} {
		if !strings.Contains(config, want) {
			t.Fatalf("missing managed rule: %s", want)
		}
	}
	if strings.Index(config, "http_access deny sc_protected_dst") > strings.Index(config, "!sc_proxy_authenticated") {
		t.Fatal("authentication precedes protected-destination deny")
	}
	s.Clients[0].ProxyUsername = "build.agent"
	config, e = Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(config, "proxy_auth_regex ^build[.]agent$") {
		t.Fatal("regex metacharacter not literal")
	}
	if strings.Contains(config, "password") {
		t.Fatal("password material in configuration")
	}
}
