package main

import "testing"

func TestRequiresPrivilegeDrop(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"serve", true},
		{"bootstrap", true},
		{"password-reset", true},
		{"user", true},
		{"sessions", true},
		{"status", true},
		{"backup", true},
		{"cert", true},
		{"check", true},
		{"service-update", true},
		{"help", true},
		{"squid-start", false},
		{"proxy-auth", false},
		{"entrypoint", false},
		{"unknown-command", true},
	}
	for _, c := range cases {
		if got := requiresPrivilegeDrop(c.cmd); got != c.want {
			t.Errorf("requiresPrivilegeDrop(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestVersionText(t *testing.T) {
	oldVersion, oldImage := version, baseImage
	t.Cleanup(func() { version, baseImage = oldVersion, oldImage })
	version, baseImage = "0.1.0", "alpine:3.24@sha256:aa+squid:7.8@sha256:bb"
	want := "Kraken 0.1.0\nОбраз: alpine:3.24@sha256:aa+squid:7.8@sha256:bb\n"
	if got := versionText(); got != want {
		t.Fatalf("versionText() = %q, want %q", got, want)
	}
}
