package policy

import (
	"strconv"
	"strings"
	"testing"
)

func upstreamSnapshot(u *UpstreamProxy) Snapshot {
	s := Empty()
	v := DefaultSettings()
	v.Upstream = u
	s.Settings = &v
	return s
}

func TestUpstreamProxyContract(t *testing.T) {
	o := Options{StateDir: "/data/runtime", LogPath: "/data/logs/access.log"}
	base, e := Render(Empty(), o)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(base, "sc_upstream") || strings.Contains(base, "never_direct") {
		t.Fatal("default render changed")
	}
	for _, ok := range []*UpstreamProxy{{Address: "8.8.8.8", Port: 3128}, {Address: "2001:4860::8888", Port: 8080, Exclusive: true}, {Address: "10.0.0.5", Port: 3128}, {Address: "192.168.1.1", Port: 1}, {Address: "8.8.4.4", Port: 65535}} {
		c, e := Render(upstreamSnapshot(ok), o)
		if e != nil {
			t.Fatalf("rejected %+v: %v", *ok, e)
		}
		want := "cache_peer " + ok.Address + " parent " + strconv.Itoa(ok.Port) + " 0 no-query no-digest default name=sc_upstream\n"
		if !strings.Contains(c, want) {
			t.Fatalf("missing %q", want)
		}
		if strings.Contains(c, "never_direct allow all") != ok.Exclusive {
			t.Fatalf("never_direct mismatch for %+v", *ok)
		}
	}
	for _, bad := range []*UpstreamProxy{{Address: "127.0.0.1", Port: 3128}, {Address: "::1", Port: 3128}, {Address: "169.254.169.254", Port: 80}, {Address: "::ffff:169.254.169.254", Port: 80}, {Address: "0.0.0.0", Port: 3128}, {Address: "224.0.0.1", Port: 3128}, {Address: "proxy.example", Port: 3128}, {Address: "8.8.8.8", Port: 0}, {Address: "8.8.8.8", Port: 65536}, {Address: "2002:7f00:1::1", Port: 3128}, {Address: "", Port: 3128}} {
		if e := Validate(upstreamSnapshot(bad), Options{}); e == nil {
			t.Fatalf("accepted %+v", *bad)
		}
	}
}
