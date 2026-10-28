package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

const CompilerVersion = "5"

// Squid Basic TTL zero never becomes valid. A separate uncached generation
// check on every allowed request enforces reset/disable despite Basic caching.
const ProxyCredentialTTLSeconds = 1

type Destination struct {
	Domain            string `json:"domain"`
	IncludeSubdomains bool   `json:"include_subdomains"`
}
type Client struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	Networks      []string `json:"source_networks"`
	Enabled       bool     `json:"enabled"`
	PolicyID      string   `json:"policy_id"`
	ProxyUsername string   `json:"proxy_username,omitempty"`
}
type Policy struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Mode         string        `json:"mode"`
	Destinations []Destination `json:"destinations"`
	TLSProfileID string        `json:"tls_profile_id"`
}
type TLSProfile struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Mode      string  `json:"mode"`
	Transport string  `json:"proxy_transport"`
	CertRef   *string `json:"cert_ref"`
}
type Snapshot struct {
	Settings    *Settings    `json:"settings,omitempty"`
	Mode        string       `json:"configuration_mode,omitempty"`
	Expert      string       `json:"expert_config,omitempty"`
	Version     int          `json:"schema_version"`
	Clients     []Client     `json:"clients"`
	Policies    []Policy     `json:"policies"`
	TLSProfiles []TLSProfile `json:"tls_profiles"`
}
type Options struct {
	StateDir     string
	LogPath      string
	ProxyCert    string
	ProxyKey     string
	AuthDatabase string
}

func Empty() Snapshot {
	v := DefaultSettings()
	return Snapshot{Version: 1, Settings: &v, Mode: "structured", Clients: []Client{}, Policies: []Policy{}, TLSProfiles: []TLSProfile{{ID: "00000000-0000-4000-8000-000000000001", Name: "HTTP proxy / CONNECT", Mode: "tunnel", Transport: "http"}}}
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`)
var proxyUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
var domainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func Validate(s Snapshot, o Options) error {
	if (o.ProxyCert == "") != (o.ProxyKey == "") {
		return errors.New("both proxy certificate and key are required")
	}
	if s.Version != 1 {
		return errors.New("unsupported schema_version")
	}
	if e := validateSettings(EffectiveSettings(s)); e != nil {
		return e
	}
	if s.Mode != "" && s.Mode != "structured" && s.Mode != "expert" {
		return errors.New("unknown configuration mode")
	}
	if e := validateExpert(s.Expert); e != nil {
		return e
	}
	if len(s.Clients) > 1024 || len(s.Policies) > 1024 || len(s.TLSProfiles) > 16 {
		return errors.New("configuration limit exceeded")
	}
	profiles := map[string]TLSProfile{}
	policies := map[string]Policy{}
	clients := map[string]bool{}
	for _, p := range s.TLSProfiles {
		if !idPattern.MatchString(p.ID) || profiles[p.ID].ID != "" {
			return errors.New("invalid/duplicate TLS profile id")
		}
		if p.Mode != "tunnel" {
			return errors.New("HTTPS requires tunnel mode")
		}
		if p.Transport != "http" && p.Transport != "tls" {
			return errors.New("invalid proxy transport")
		}
		if p.Transport == "tls" && (o.ProxyCert == "" || o.ProxyKey == "" || p.CertRef == nil || *p.CertRef != "proxy-server") {
			return errors.New("TLS proxy requires installed proxy-server certificate")
		}
		if p.Transport == "http" && p.CertRef != nil {
			return errors.New("HTTP profile must not reference certificate")
		}
		profiles[p.ID] = p
	}
	for _, p := range s.Policies {
		if !idPattern.MatchString(p.ID) || policies[p.ID].ID != "" || strings.TrimSpace(p.Name) == "" {
			return errors.New("invalid/duplicate policy")
		}
		if _, ok := profiles[p.TLSProfileID]; !ok {
			return errors.New("missing TLS profile")
		}
		if p.Mode != "all_web" && p.Mode != "allowlist" {
			return errors.New("invalid access mode")
		}
		if len(p.Destinations) > 1024 || p.Mode == "all_web" && len(p.Destinations) > 0 {
			return errors.New("invalid destination list")
		}
		seen := map[string]bool{}
		for _, d := range p.Destinations {
			if len(d.Domain) > 253 || !domainPattern.MatchString(d.Domain) || seen[d.Domain] {
				return fmt.Errorf("invalid/duplicate canonical domain: %q", d.Domain)
			}
			if _, err := netip.ParseAddr(d.Domain); err == nil {
				return errors.New("IP destination is not a domain")
			}
			seen[d.Domain] = true
		}
		// Squid splay-tree lists must not contain overlapping parent/subdomain entries.
		for i, a := range p.Destinations {
			for j, b := range p.Destinations {
				if i != j && a.IncludeSubdomains && strings.HasSuffix(b.Domain, "."+a.Domain) {
					return errors.New("redundant overlapping destination domains")
				}
			}
		}
		policies[p.ID] = p
	}
	type source struct {
		prefix   netip.Prefix
		owner    string
		username string
	}
	var sources []source
	for _, c := range s.Clients {
		if c.Kind != "" && c.Kind != "host" && c.Kind != "network" && c.Kind != "group" {
			return errors.New("invalid client kind")
		}
		if !idPattern.MatchString(c.ID) || clients[c.ID] || strings.TrimSpace(c.Name) == "" || len(c.Name) > 128 || len(c.Description) > 1024 || len(c.Networks) == 0 || len(c.Networks) > 64 {
			return errors.New("invalid/duplicate client")
		}
		clients[c.ID] = true
		if _, ok := policies[c.PolicyID]; !ok {
			return errors.New("missing client policy")
		}
		if c.ProxyUsername != "" {
			if !proxyUsernamePattern.MatchString(c.ProxyUsername) {
				return errors.New("invalid proxy username")
			}
			if o.AuthDatabase == "" || !strings.HasPrefix(o.AuthDatabase, "/") {
				return errors.New("proxy authentication requires installed database")
			}
			if profiles[policies[c.PolicyID].TLSProfileID].Transport != "tls" {
				return errors.New("password proxy authentication requires TLS transport")
			}
			if c.Enabled && s.Mode == "expert" {
				return errors.New("proxy authentication clients require structured mode")
			}
		}
		for _, raw := range c.Networks {
			n, err := netip.ParsePrefix(raw)
			if err != nil || n.Bits() == 0 || n != n.Masked() || n.Addr().Is4In6() || n.Addr().IsUnspecified() || n.Addr().IsMulticast() {
				return fmt.Errorf("invalid source CIDR: %q", raw)
			}
			if c.Enabled {
				for _, other := range sources {
					if n.Overlaps(other.prefix) {
						// Distinct credentials distinguish clients sharing a NAT source.
						// IP-only and identical-credential overlaps remain ambiguous.
						if c.ProxyUsername == "" || other.username == "" || c.ProxyUsername == other.username {
							return errors.New("overlapping enabled source networks")
						}
					}
				}
				sources = append(sources, source{n, c.ID, c.ProxyUsername})
			}
		}
	}
	for _, p := range []string{o.StateDir, o.LogPath, o.ProxyCert, o.ProxyKey, o.AuthDatabase} {
		if strings.ContainsAny(p, "\r\n\t \"\\") {
			return errors.New("unsafe installed filesystem path")
		}
	}
	return nil
}
func Hash(s Snapshot) string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Fingerprint binds the live canary to compiler and installed runtime inputs too.
func Fingerprint(s Snapshot, o Options) string {
	b, _ := json.Marshal(struct {
		Compiler string
		Snapshot Snapshot
		Options  Options
	}{CompilerVersion, s, o})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func Render(s Snapshot, o Options) (string, error) {
	if err := Validate(s, o); err != nil {
		return "", err
	}
	h := Fingerprint(s, o)
	var b strings.Builder
	settings := EffectiveSettings(s)
	fmt.Fprintf(&b, "# squid-control compiler=%s revision=%s\nvisible_hostname squid-control\nhttp_port 127.0.0.1:3130\nhttp_port %d\npid_filename %s/squid.pid\ncache_log %s/cache.log\n", CompilerVersion, h, settings.HTTPPort, o.StateDir, o.StateDir)
	if o.ProxyCert != "" && o.ProxyKey != "" {
		fmt.Fprintf(&b, "https_port %d tls-cert=%s tls-key=%s\n", settings.TLSPort, o.ProxyCert, o.ProxyKey)
	}
	b.WriteString("acl sc_probe_source src 127.0.0.1/32 ::1/128\nacl sc_probe_listener localport 3130\n")
	fmt.Fprintf(&b, "access_log stdio:%s squid !sc_probe_source\ncache_store_log none\npinger_enable off\nshutdown_lifetime 2 seconds\n", o.LogPath)
	renderSettings(&b, settings)
	fmt.Fprintf(&b, "acl sc_safe_ports port %s\nacl sc_ssl_ports port %s\nacl sc_http_listener localport %d\nacl sc_tls_listener localport %d\n", portList(settings.SafePorts), portList(settings.ConnectPorts), settings.HTTPPort, settings.TLSPort)
	b.WriteString("acl sc_protected_dst dst 0.0.0.0/8 127.0.0.0/8 169.254.0.0/16 224.0.0.0/4 240.0.0.0/4 ::/128 ::1/128 ::ffff:0:0/96 64:ff9b::/96 100::/64 fe80::/10 ff00::/8\n")
	b.WriteString("acl sc_private_dst dst 10.0.0.0/8 100.64.0.0/10 172.16.0.0/12 192.0.0.0/24 192.0.2.0/24 192.168.0.0/16 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 2001:db8::/32 fc00::/7\n")
	b.WriteString("http_access deny sc_probe_listener\nhttp_access deny !sc_safe_ports\nhttp_access deny CONNECT !sc_ssl_ports\nhttp_access deny manager\nhttp_access deny sc_protected_dst\n")
	if !settings.AllowPrivateDestinations {
		b.WriteString("http_access deny sc_private_dst\n")
	}
	fmt.Fprintf(&b, "reply_header_add X-Squid-Control-Revision %s all\n", h)
	var authenticatedSources []string
	for _, c := range s.Clients {
		if c.Enabled && c.ProxyUsername != "" {
			authenticatedSources = append(authenticatedSources, c.Networks...)
		}
	}
	if len(authenticatedSources) > 0 {
		sort.Strings(authenticatedSources)
		unique := authenticatedSources[:0]
		for _, network := range authenticatedSources {
			if len(unique) == 0 || unique[len(unique)-1] != network {
				unique = append(unique, network)
			}
		}
		authenticatedSources = unique
		fmt.Fprintf(&b, "auth_param basic program /usr/local/bin/squid-control proxy-auth --database %s\nauth_param basic children 2 startup=0 idle=1 queue-size=8 on-persistent-overload=ERR\nauth_param basic realm Squid Control\nauth_param basic casesensitive on\nauth_param basic credentialsttl 1 seconds\n", o.AuthDatabase)
		fmt.Fprintf(&b, "external_acl_type sc_identity_state ttl=0 negative_ttl=0 cache=0 children-max=2 children-startup=0 children-idle=1 queue-size=8 %%LOGIN %%{sc_generation_}note %%DATA /usr/local/bin/squid-control proxy-auth --state --database %s\nacl sc_proxy_current external sc_identity_state\n", o.AuthDatabase)
		fmt.Fprintf(&b, "acl sc_auth_sources src %s\nacl sc_proxy_authenticated proxy_auth REQUIRED\nhttp_access deny sc_auth_sources sc_http_listener\nhttp_access deny sc_auth_sources sc_tls_listener !sc_proxy_authenticated\n", strings.Join(authenticatedSources, " "))
	}
	b.WriteString("# BEGIN user policy\n")
	if s.Mode == "expert" {
		b.WriteString(s.Expert)
		b.WriteString("\n# END user policy\n")
		renderCacheFallback(&b, settings)
		b.WriteString("http_access deny all\n")
		return b.String(), nil
	}
	profiles := map[string]TLSProfile{}
	for _, p := range s.TLSProfiles {
		profiles[p.ID] = p
	}
	policies := map[string]Policy{}
	for _, p := range s.Policies {
		policies[p.ID] = p
	}
	cc := append([]Client(nil), s.Clients...)
	sort.Slice(cc, func(i, j int) bool { return cc[i].ID < cc[j].ID })
	for i, c := range cc {
		if !c.Enabled {
			continue
		}
		p := policies[c.PolicyID]
		tp := profiles[p.TLSProfileID]
		listener := "sc_http_listener"
		if tp.Transport == "tls" {
			listener = "sc_tls_listener"
		}
		fmt.Fprintf(&b, "acl client_%d src %s\n", i, strings.Join(c.Networks, " "))
		authCondition := ""
		if c.ProxyUsername != "" {
			fmt.Fprintf(&b, "acl sc_proxy_user_%d proxy_auth_regex ^%s$\n", i, strings.ReplaceAll(c.ProxyUsername, ".", "[.]"))
			authCondition = fmt.Sprintf(" sc_proxy_user_%d sc_proxy_current", i)
		}
		if p.Mode == "all_web" {
			fmt.Fprintf(&b, "http_access allow client_%d %s%s\n", i, listener, authCondition)
			continue
		}
		if len(p.Destinations) == 0 {
			continue
		}
		var ds []string
		for _, d := range p.Destinations {
			v := d.Domain
			if d.IncludeSubdomains {
				v = "." + v
			}
			ds = append(ds, v)
		}
		sort.Strings(ds)
		fmt.Fprintf(&b, "acl destinations_%d dstdomain -n %s\nhttp_access allow client_%d %s destinations_%d%s\n", i, strings.Join(ds, " "), i, listener, i, authCondition)
	}
	b.WriteString("# END user policy\n")
	renderCacheFallback(&b, settings)
	b.WriteString("http_access deny all\n")
	return b.String(), nil
}

func ExpertTemplate(s Snapshot, o Options) (string, error) {
	s.Mode = "structured"
	config, e := Render(s, o)
	if e != nil {
		return "", e
	}
	_, rest, _ := strings.Cut(config, "# BEGIN user policy\n")
	body, _, _ := strings.Cut(rest, "# END user policy")
	if strings.TrimSpace(body) == "" {
		body = "http_access deny all\n"
	}
	return body, nil
}

func renderCacheFallback(b *strings.Builder, settings Settings) {
	if settings.CacheEnabled {
		b.WriteString("cache allow all\n")
	} else {
		b.WriteString("cache deny all\n")
	}
}
