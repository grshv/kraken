package policy

import "testing"

func TestDomainMatchesLikeDstdomain(t *testing.T) {
	for _, c := range []struct {
		host, domain string
		sub, want    bool
	}{
		{"example.com", "example.com", false, true},
		{"API.Example.com", "example.com", false, false},
		{"API.Example.com", "example.com", true, true},
		{"example.com", "example.com", true, true},
		{"badexample.com", "example.com", true, false},
		{"10.0.0.1", "example.com", true, false},
		{"", "example.com", true, false},
	} {
		if got := DomainMatches(c.host, c.domain, c.sub); got != c.want {
			t.Errorf("DomainMatches(%q, %q, %v) = %v, want %v", c.host, c.domain, c.sub, got, c.want)
		}
	}
}

func TestProtectionDenialPortsAndAddresses(t *testing.T) {
	s := Settings{SafePorts: []int{80, 443}, ConnectPorts: []int{443}}
	for _, c := range []struct {
		host    string
		port    int
		connect bool
		want    bool
	}{
		{"example.com", 443, true, false},
		{"example.com", 80, true, true},
		{"example.com", 22, false, true},
		{"127.0.0.1", 443, true, true},
		{"10.0.0.5", 443, true, true},
		{"93.184.216.34", 443, true, false},
	} {
		if got := ProtectionDenial(c.host, c.port, c.connect, s); got != c.want {
			t.Errorf("ProtectionDenial(%q, %d, %v) = %v, want %v", c.host, c.port, c.connect, got, c.want)
		}
	}
}
