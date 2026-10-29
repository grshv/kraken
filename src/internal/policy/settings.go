package policy

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Settings holds actual Squid options, separate from client access policies.
type Settings struct {
	HTTPPort                 int      `json:"http_port"`
	TLSPort                  int      `json:"tls_port"`
	SafePorts                []int    `json:"safe_ports"`
	ConnectPorts             []int    `json:"connect_ports"`
	CacheEnabled             bool     `json:"cache_enabled"`
	CacheMemoryMB            int      `json:"cache_memory_mb"`
	MaximumObjectKB          int      `json:"maximum_object_kb"`
	DNSNameservers           []string `json:"dns_nameservers"`
	PositiveDNSTTL           int      `json:"positive_dns_ttl_seconds"`
	NegativeDNSTTL           int      `json:"negative_dns_ttl_seconds"`
	ConnectTimeout           int      `json:"connect_timeout_seconds"`
	ReadTimeout              int      `json:"read_timeout_seconds"`
	RequestTimeout           int      `json:"request_timeout_seconds"`
	MaxRequestBodyMB         int      `json:"max_request_body_mb"`
	LogRotations             int      `json:"log_rotations"`
	AllowPrivateDestinations bool     `json:"allow_private_destinations"`
}

func DefaultSettings() Settings {
	return Settings{HTTPPort: 3128, TLSPort: 3129, SafePorts: []int{80, 443}, ConnectPorts: []int{443}, CacheMemoryMB: 64, MaximumObjectKB: 512, DNSNameservers: []string{}, PositiveDNSTTL: 21600, NegativeDNSTTL: 60, ConnectTimeout: 60, ReadTimeout: 900, RequestTimeout: 300, LogRotations: 3}
}
func EffectiveSettings(s Snapshot) Settings {
	if s.Settings == nil {
		return DefaultSettings()
	}
	return *s.Settings
}

// dangerousPorts are service ports all_web must never expose via CONNECT or
// HTTP fetch, regardless of admin policy. SMTP 25 is included too.
var dangerousPorts = map[int]bool{
	22:   true, // ssh
	23:   true, // telnet
	25:   true, // smtp
	110:  true, // pop3
	143:  true, // imap
	465:  true, // smtps
	587:  true, // submission
	993:  true, // imaps
	995:  true, // pop3s
	9050: true, // tor
	9051: true, // tor control
}

func validateSettings(v Settings) error {
	if v.HTTPPort < 1024 || v.HTTPPort > 65535 || v.TLSPort < 1024 || v.TLSPort > 65535 || v.HTTPPort == v.TLSPort || v.HTTPPort == 3130 || v.TLSPort == 3130 || v.HTTPPort == 3131 || v.TLSPort == 3131 || v.HTTPPort == 8443 || v.TLSPort == 8443 {
		return errors.New("proxy ports must be distinct, 1024–65535; 3130, 3131 and 8443 are reserved")
	}
	if len(v.SafePorts) == 0 || len(v.SafePorts) > 64 || len(v.ConnectPorts) == 0 || len(v.ConnectPorts) > 64 {
		return errors.New("invalid allowed port list")
	}
	safe := map[int]bool{}
	for _, port := range v.SafePorts {
		if port < 1 || port > 65535 || dangerousPorts[port] || safe[port] {
			return errors.New("invalid/duplicate safe port; ssh, telnet, mail and tor ports are prohibited")
		}
		safe[port] = true
	}
	seen := map[int]bool{}
	for _, port := range v.ConnectPorts {
		if !safe[port] || seen[port] {
			return errors.New("CONNECT ports must be unique and included in safe ports")
		}
		seen[port] = true
	}
	if v.CacheMemoryMB < 1 || v.CacheMemoryMB > 1024 || v.MaximumObjectKB < 1 || v.MaximumObjectKB > v.CacheMemoryMB*1024 {
		return errors.New("invalid memory cache size/object limit")
	}
	if len(v.DNSNameservers) > 8 {
		return errors.New("too many DNS servers")
	}
	for _, server := range v.DNSNameservers {
		a, e := netip.ParseAddr(server)
		if e != nil || a.IsUnspecified() || a.IsMulticast() {
			return errors.New("DNS nameservers must be unicast IP addresses")
		}
	}
	if v.PositiveDNSTTL < 1 || v.PositiveDNSTTL > 604800 || v.NegativeDNSTTL < 1 || v.NegativeDNSTTL >= v.PositiveDNSTTL {
		return errors.New("invalid positive/negative DNS TTL")
	}
	for _, timeout := range []int{v.ConnectTimeout, v.ReadTimeout, v.RequestTimeout} {
		if timeout < 1 || timeout > 86400 {
			return errors.New("timeouts must be 1–86400 seconds")
		}
	}
	if v.MaxRequestBodyMB < 0 || v.MaxRequestBodyMB > 10240 || v.LogRotations < 0 || v.LogRotations > 30 {
		return errors.New("invalid request body limit/log rotations")
	}
	return nil
}
func portList(ports []int) string {
	v := make([]string, len(ports))
	for i, p := range ports {
		v[i] = strconv.Itoa(p)
	}
	return strings.Join(v, " ")
}
func renderSettings(b *strings.Builder, v Settings) {
	fmt.Fprintf(b, "cache_mem %d MB\nmaximum_object_size_in_memory %d KB\npositive_dns_ttl %d seconds\nnegative_dns_ttl %d seconds\nconnect_timeout %d seconds\nread_timeout %d seconds\nrequest_timeout %d seconds\nrequest_body_max_size %d MB\nlogfile_rotate %d\n", v.CacheMemoryMB, v.MaximumObjectKB, v.PositiveDNSTTL, v.NegativeDNSTTL, v.ConnectTimeout, v.ReadTimeout, v.RequestTimeout, v.MaxRequestBodyMB, v.LogRotations)
	if len(v.DNSNameservers) > 0 {
		fmt.Fprintf(b, "dns_nameservers %s\n", strings.Join(v.DNSNameservers, " "))
	}

}

var reservedName = regexp.MustCompile(`(?i)\bsc_[a-z0-9_]+\b`)

// expertDirectiveToken matches the raw (pre-normalization) form a directive
// token must take before it is ever consulted against the allowlist: lowercase
// ASCII letters, digits and underscores only. Quotes, backslashes and upper
// case are rejected so a quoted/continued token cannot slip past Squid's own
// tokenizer.
var expertDirectiveToken = regexp.MustCompile(`^[a-z0-9_]+$`)

// expertDirectives is the exhaustive allowlist of directives the expert
// configuration may use. Anything not listed here is rejected at draft save.
var expertDirectives = map[string]bool{
	"acl": true, "http_access": true, "http_reply_access": true, "miss_access": true,
	"always_direct": true, "never_direct": true, "prefer_direct": true,
	"nonhierarchical_direct": true, "cache": true, "send_hit": true, "store_miss": true,
	"refresh_pattern": true, "cache_peer": true, "cache_peer_access": true,
	"deny_info": true, "forwarded_for": true, "via": true,
	"request_header_access": true, "request_header_replace": true,
	"tcp_outgoing_address": true, "tcp_outgoing_tos": true, "tcp_outgoing_mark": true,
	"dns_nameservers": true, "dns_timeout": true, "dns_retransmit_interval": true,
	"positive_dns_ttl": true, "negative_dns_ttl": true, "connect_timeout": true,
	"peer_connect_timeout": true, "read_timeout": true, "write_timeout": true,
	"request_timeout": true, "request_start_timeout": true,
	"client_idle_pconn_timeout": true, "server_idle_pconn_timeout": true,
	"client_lifetime": true, "forward_timeout": true, "forward_max_tries": true,
	"connect_retries": true, "happy_eyeballs_connect_timeout": true,
	"cache_mem": true, "maximum_object_size_in_memory": true,
	"memory_replacement_policy": true, "memory_cache_mode": true,
	"request_body_max_size": true, "reply_body_max_size": true,
	"request_header_max_size": true, "reply_header_max_size": true,
	"quick_abort_min": true, "quick_abort_max": true, "quick_abort_pct": true,
	"range_offset_limit": true, "collapsed_forwarding": true,
	"client_persistent_connections": true, "server_persistent_connections": true,
	"half_closed_clients": true, "detect_broken_pconn": true,
	"pipeline_prefetch": true, "delay_pools": true, "delay_class": true,
	"delay_access": true, "delay_parameters": true, "delay_initial_bucket_level": true,
	"client_delay_pools": true, "client_delay_parameters": true,
	"client_delay_access": true, "strip_query_terms": true, "uri_whitespace": true,
	"httpd_suppress_version_string": true, "on_unsupported_protocol": true,
	"if": true, "else": true, "endif": true,
}

// denyInfoTarget limits deny_info's first argument to an ERR_ template name, a
// 3xx redirect URL or TCP_RESET — anything else (notably a bare path) would let
// Squid read an arbitrary error page from disk.
var denyInfoTarget = regexp.MustCompile(`^(ERR_[A-Z_]+|(30[1-8]:)?https?://[^\s"]+|TCP_RESET)$`)

// Expert config owns the user section, never process lifecycle paths or command execution.
func validateExpert(text string, allowPrivate bool) error {
	if len(text) > 128*1024 || strings.ContainsAny(text, "\x00\r") {
		return errors.New("expert config must be UTF-8 LF text, at most 128 KiB")
	}
	physical := []string{}
	for _, line := range strings.Split(text, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) != "" {
			physical = append(physical, line)
		}
	}
	logical := strings.ReplaceAll(strings.Join(physical, "\n"), "\\\n", "")
	if strings.HasSuffix(strings.TrimSpace(text), "\\") {
		return errors.New("unfinished expert continuation")
	}
	depth := 0
	for i, line := range strings.Split(logical, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		directive := fields[0]
		if !expertDirectiveToken.MatchString(directive) || !expertDirectives[directive] {
			return fmt.Errorf("line %d: directive %q is not allowed in expert configuration", i+1, directive)
		}
		for _, tok := range fields[1:] {
			if strings.HasPrefix(tok, "\"/") || strings.Contains(strings.ToLower(tok), "parameters(") {
				return fmt.Errorf("line %d: file-backed parameters are not allowed in expert configuration", i+1)
			}
		}
		if directive == "deny_info" {
			if len(fields) < 3 {
				return fmt.Errorf("line %d: deny_info requires an ACL name", i+1)
			}
			if !denyInfoTarget.MatchString(fields[1]) {
				return fmt.Errorf("line %d: deny_info target must be an ERR_ template, redirect URL or TCP_RESET", i+1)
			}
		}
		if directive == "cache_peer" {
			if e := validateCachePeer(fields, allowPrivate, i+1); e != nil {
				return e
			}
		}
		matches := reservedName.FindAllString(strings.ToLower(strings.ReplaceAll(line, "\\", "")), -1)
		allowedRefs := directive == "http_access"
		for _, name := range matches {
			if name != "sc_http_listener" && name != "sc_tls_listener" {
				allowedRefs = false
			}
		}
		if len(matches) > 0 && !allowedRefs {
			return fmt.Errorf("line %d: sc_ names are reserved for runtime protection", i+1)
		}
		if directive == "if" {
			depth++
		}
		if directive == "else" && depth == 0 {
			return errors.New("unmatched expert else")
		}
		if directive == "endif" {
			depth--
			if depth < 0 {
				return errors.New("unmatched expert endif")
			}
		}
	}
	if depth != 0 {
		return errors.New("unclosed expert conditional")
	}
	return nil
}

// validateCachePeer enforces the security boundary that the public ACL list
// applies to request destinations: a peer host is reachable as soon as Squid
// forwards traffic to it, so the host itself must be statically inspectable
// and outside protected/private ranges unless the operator opted in.
func validateCachePeer(fields []string, allowPrivate bool, lineNo int) error {
	if len(fields) < 5 {
		return fmt.Errorf("line %d: cache_peer requires host type proxy-port icp-port", lineNo)
	}
	addr, e := netip.ParseAddr(fields[1])
	if e != nil {
		return fmt.Errorf("line %d: cache_peer host must be a literal IP address", lineNo)
	}
	if !peerAddrAllowed(addr, allowPrivate) {
		return fmt.Errorf("line %d: cache_peer host is in a protected or private range", lineNo)
	}
	for _, opt := range fields[5:] {
		if eq := strings.IndexByte(opt, '='); eq >= 0 && strings.HasPrefix(opt[eq+1:], "/") {
			return fmt.Errorf("line %d: file-backed parameters are not allowed in expert configuration", lineNo)
		}
	}
	return nil
}

// peerAddrAllowed rejects the address (or its unmapped IPv4 form) in protected
// ranges always and in private ranges unless allowPrivate. The same prefix lists
// render the runtime ACLs.
func peerAddrAllowed(addr netip.Addr, allowPrivate bool) bool {
	candidates := []netip.Addr{addr, addr.Unmap()}
	for _, a := range candidates {
		for _, cidr := range protectedDestinations {
			if p, e := netip.ParsePrefix(cidr); e == nil && p.Contains(a) {
				return false
			}
		}
		if !allowPrivate {
			for _, cidr := range privateDestinations {
				if p, e := netip.ParsePrefix(cidr); e == nil && p.Contains(a) {
					return false
				}
			}
		}
	}
	return true
}
