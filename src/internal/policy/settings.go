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
func validateSettings(v Settings) error {
	if v.HTTPPort < 1024 || v.HTTPPort > 65535 || v.TLSPort < 1024 || v.TLSPort > 65535 || v.HTTPPort == v.TLSPort || v.HTTPPort == 3130 || v.TLSPort == 3130 || v.HTTPPort == 8443 || v.TLSPort == 8443 {
		return errors.New("proxy ports must be distinct, 1024–65535; 3130 and 8443 are reserved")
	}
	if len(v.SafePorts) == 0 || len(v.SafePorts) > 64 || len(v.ConnectPorts) == 0 || len(v.ConnectPorts) > 64 {
		return errors.New("invalid allowed port list")
	}
	safe := map[int]bool{}
	for _, port := range v.SafePorts {
		if port < 1 || port > 65535 || port == 25 || safe[port] {
			return errors.New("invalid/duplicate safe port (SMTP 25 is prohibited)")
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

// Expert config owns the user section, never process lifecycle paths or command execution.
func validateExpert(text string) error {
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
	forbidden := map[string]bool{"include": true, "pid_filename": true, "cache_log": true, "access_log": true, "cache_store_log": true, "logfile_daemon": true, "cache_effective_user": true, "cache_effective_group": true, "chroot": true, "chdir": true, "coredump_dir": true, "workers": true, "debug_options": true, "external_acl_type": true, "auth_param": true, "ecap_service": true, "loadable_modules": true, "sslcrtd_program": true, "security_file_certgen": true, "reply_header_add": true, "http_port": true, "https_port": true, "cache_dir": true, "mime_table": true, "error_directory": true}
	for i, line := range strings.Split(logical, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		directive := strings.ToLower(strings.Trim(strings.ReplaceAll(fields[0], "\\", ""), "\"'"))
		managedPath:=false
        for _,suffix:=range []string{"_log","_file","_filename","_directory","_dir","_path","_program","_helper"}{if strings.HasSuffix(directive,suffix){managedPath=true}}
        if forbidden[directive]||managedPath {
			return fmt.Errorf("line %d: %s is managed by runtime or requires a separately provisioned helper/storage profile", i+1, directive)
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
