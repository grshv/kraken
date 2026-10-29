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
		{"dev-cert", true},
		{"check", true},
		{"service-update", true},
		{"help", true},
		{"squid-start", false},
		{"proxy-auth", false},
		{"unknown-command", true},
	}
	for _, c := range cases {
		if got := requiresPrivilegeDrop(c.cmd); got != c.want {
			t.Errorf("requiresPrivilegeDrop(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}
