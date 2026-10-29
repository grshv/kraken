package policy

import (
	"strings"
	"testing"
)

func expertWith(text string, allowPrivate bool) error {
	s := Empty()
	v := DefaultSettings()
	v.AllowPrivateDestinations = allowPrivate
	s.Settings = &v
	s.Mode = "expert"
	s.Expert = text
	return Validate(s, Options{})
}

func TestExpertBoundaryRejects(t *testing.T) {
	for _, text := range []string{
		"err_page_stylesheet /run/secrets/proxy.key",
		"ftp_port 2121",
		"acl x src \"/etc/hosts\"",
		"acl x dstdomain PARAMETERS(\"/etc/hosts\")",
		"deny_info /run/secrets/proxy.key all",
		"deny_info ERR_ACCESS_DENIED",
		"deny_info err_access_denied office",
		"deny_info ftp://example.org/ office",
		"cache_peer 169.254.169.254 parent 80 0 no-query originserver",
		"cache_peer 127.0.0.1 parent 8443 0",
		"cache_peer ::1 parent 8443 0",
		"cache_peer ::ffff:127.0.0.1 parent 80 0",
		"cache_peer ::ffff:169.254.169.254 parent 80 0",
		"cache_peer 2002:7f00:1:: parent 80 0",
		"cache_peer 10.0.0.5 parent 3128 0",
		"cache_peer peer.example parent 3128 0",
		"cache_peer 8.8.8.8 parent 3128 0 tls-cert=/run/secrets/proxy.key",
		"HTTP_ACCESS allow all",
		"\"acl\" x src 192.0.2.1",
		"http_acce\\ss allow all",
		"hosts_file /etc/hosts",
		"configuration_includes_quoted_values on",
		"visible_hostname x",
		"url_rewrite_program /bin/sh",
		"include /data/*",
		"request_header_add X a",
		"reply_header_access X-Squid-Control-Revision deny all",
		"acl sc_safe_ports port 25",
		"if true\nhttp_access allow all",
	} {
		if e := expertWith(text, false); e == nil {
			t.Errorf("accepted %q", text)
		}
	}
	// protected stays protected even with private destinations allowed
	for _, text := range []string{"cache_peer 169.254.169.254 parent 80 0", "cache_peer 127.0.0.1 parent 80 0", "cache_peer 64:ff9b:1::1 parent 80 0"} {
		if e := expertWith(text, true); e == nil {
			t.Errorf("accepted protected peer with private allowed: %q", text)
		}
	}
}

func TestExpertBoundaryAllows(t *testing.T) {
	for _, text := range []string{
		"acl office src 192.0.2.0/24\nhttp_access allow office",
		"acl office src 192.0.2.0/24\ndeny_info ERR_ACCESS_DENIED office\nhttp_access deny office",
		"acl office src 192.0.2.0/24\ndeny_info 302:https://example.org/blocked office",
		"acl office src 192.0.2.0/24\ndeny_info TCP_RESET office",
		"acl office src 192.0.2.0/24\ndeny_info https://example.org/blocked office",
		"cache_peer 8.8.8.8 parent 3128 0 no-query default\nnever_direct allow all",
		"delay_pools 1\ndelay_class 1 1\ndelay_parameters 1 64000/64000",
		"refresh_pattern -i \\.(deb|rpm)$ 0 20% 4320",
		"acl api urlpath_regex /api\nhttp_access deny api",
		"if ${process_number} = 1\nhttp_access deny all\nelse\nhttp_access deny all\nendif",
		"acl office src 192.0.2.0/24\nhttp_access allow office sc_http_listener",
		"# comment only\nhttp_access deny all",
	} {
		if e := expertWith(text, false); e != nil {
			t.Errorf("rejected %q: %v", text, e)
		}
	}
	if e := expertWith("cache_peer 10.0.0.5 parent 3128 0 no-query", true); e != nil {
		t.Errorf("private peer rejected despite allow_private_destinations: %v", e)
	}
}

func TestProtectedIPv6RangesAndCompiler(t *testing.T) {
	if CompilerVersion != "8" {
		t.Fatalf("compiler version %q", CompilerVersion)
	}
	c, e := Render(Empty(), Options{StateDir: "/data", SquidDir: "/data/squid", LogPath: "/data/access.log"})
	if e != nil {
		t.Fatal(e)
	}
	var line string
	for _, l := range strings.Split(c, "\n") {
		if strings.HasPrefix(l, "acl sc_protected_dst dst ") {
			line = l
		}
	}
	for _, want := range []string{" 127.0.0.0/8", " 169.254.0.0/16", " ::ffff:0:0/96", " 2001::/32", " 2002::/16", " 64:ff9b:1::/48"} {
		if !strings.Contains(line+" ", want+" ") {
			t.Errorf("sc_protected_dst lacks %s: %q", want, line)
		}
	}
	if !strings.Contains(c, "http_access deny sc_private_dst") || !strings.Contains(c, "acl sc_private_dst dst 10.0.0.0/8") {
		t.Error("private destinations ACL changed")
	}
}

func TestUserSectionLineBounds(t *testing.T) {
	s := Empty()
	s.Mode = "expert"
	s.Expert = "acl a src 192.0.2.1\nacl b src 192.0.2.2\nhttp_access allow a"
	c, e := Render(s, Options{StateDir: "/data", LogPath: "/data/access.log"})
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(c, "\n")
	begin := -1
	end := -1
	for i, l := range lines {
		if l == "# BEGIN user policy" {
			begin = i + 1
		}
		if l == "# END user policy" {
			end = i + 1
		}
	}
	if begin < 0 || end < 0 {
		t.Fatal("markers missing")
	}
	if n, ok := UserSectionLine(c, begin+1); !ok || n != 1 {
		t.Errorf("first user line -> %d %v", n, ok)
	}
	if n, ok := UserSectionLine(c, begin+3); !ok || n != 3 {
		t.Errorf("third user line -> %d %v", n, ok)
	}
	for _, l := range []int{0, 1, begin, end, end + 1, len(lines) + 5} {
		if _, ok := UserSectionLine(c, l); ok {
			t.Errorf("line %d outside section mapped", l)
		}
	}
}
