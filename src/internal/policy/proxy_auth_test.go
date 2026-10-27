package policy

import (
	"strings"
	"testing"
)

func authFixture() (Snapshot, Options) {
	s := fixture()
	s.TLSProfiles[0].Transport = "tls"
	ref := "proxy-server"
	s.TLSProfiles[0].CertRef = &ref
	s.Clients[0].ProxyUsername = "build.agent"
	return s, Options{StateDir: "/data/runtime", LogPath: "/data/logs/access.log", ProxyCert: "/data/certs/proxy.crt", ProxyKey: "/data/certs/proxy.key", AuthDatabase: "/data/app/control.sqlite3"}
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
	missing.AuthDatabase = ""
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
func TestAuthenticatedRenderIsManagedAndFailClosed(t *testing.T) {
	s, o := authFixture()
	s.Clients[0].ProxyUsername = "REQUIRED"
	config, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"auth_param basic program /usr/local/bin/squid-control proxy-auth --database /data/app/control.sqlite3", "auth_param basic credentialsttl 1 seconds", "auth_param basic casesensitive on", "acl sc_proxy_user_0 proxy_auth_regex ^REQUIRED$", "http_access deny sc_auth_sources sc_http_listener", "http_access deny sc_auth_sources sc_tls_listener !sc_proxy_authenticated", "http_access allow client_0 sc_tls_listener destinations_0 sc_proxy_user_0"} {
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
