package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"squid-control.local/squid/internal/policy"
)

type RevisionSummary struct {
	ID          string `json:"id"`
	Created     string `json:"created_at"`
	Hash        string `json:"policy_hash"`
	Compiler    string `json:"compiler_version"`
	Applied     bool   `json:"was_applied"`
	Current     bool   `json:"is_current"`
	CanRollback bool   `json:"can_rollback"`
	Status      string `json:"latest_status"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request, v session) {
	result := s.Runtime.Status()
	if s.persistenceFailed.Load() {
		result["runtime"] = map[string]any{"state": "degraded", "detail": "Operation outcome could not be persisted; recovery required", "observed_at": stamp(), "stale": false}
	}
	var d Draft
	m, e := s.Runtime.ReadManifest()
	var changed any
	var runtimeUpdate any
	if e == nil && s.Store.Get("draft", &d) == nil {
		changed = len(semanticDiff(m.Snapshot, d.Snapshot)) > 0
		if config, renderErr := policy.Render(m.Snapshot, s.Runtime.Options); renderErr == nil {
			runtimeUpdate = config != m.Config
		}
	}
	result["has_unapplied_changes"] = changed
	result["runtime_update_available"] = runtimeUpdate
	respond(w, 200, result)
}
func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request, v session) {
	rows, e := s.Store.ListRevisions()
	if e != nil {
		fail(w, 503, "storage_unavailable", "История недоступна")
		return
	}
	current, me := s.Runtime.ReadManifest()
	items := []RevisionSummary{}
	for _, row := range rows {
		var rev Revision
		if e = json.Unmarshal([]byte(row.Body), &rev); e != nil {
			fail(w, 503, "storage_unavailable", "Повреждённая запись ревизии")
			return
		}
		isCurrent := me == nil && current.Revision == rev.ID
		items = append(items, RevisionSummary{ID: rev.ID, Created: rev.Created, Hash: rev.Hash, Compiler: rev.Compiler, Applied: row.Applied, Current: isCurrent, CanRollback: row.Applied && !isCurrent && me == nil && sameInstallationPorts(current.Snapshot, rev.Snapshot), Status: row.LatestStatus})
	}
	respond(w, 200, map[string]any{"items": items})
}
func (s *Server) expertTemplate(w http.ResponseWriter, r *http.Request, v session) {
	var d Draft
	if e := s.Store.Get("draft", &d); e != nil {
		fail(w, 503, "storage_unavailable", "Черновик недоступен")
		return
	}
	body, e := policy.ExpertTemplate(d.Snapshot, s.Runtime.Options)
	if e != nil {
		fail(w, 422, "invalid_policy", e.Error())
		return
	}
	respond(w, 200, map[string]string{"expert_config": body})
}

type DiffLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Linear bounded diff: exact common prefix/suffix, changed blocks, no quadratic allocation.
func configDiff(before, after string) []DiffLine {
	if before == after {
		return []DiffLine{}
	}
	a, b := strings.Split(before, "\n"), strings.Split(after, "\n")
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	end := 0
	for end < len(a)-start && end < len(b)-start && a[len(a)-1-end] == b[len(b)-1-end] {
		end++
	}
	out := []DiffLine{}
	appendLine := func(kind, text string) {
		if len(out) < 400 {
			out = append(out, DiffLine{kind, text})
		}
	}
	if start > 0 {
		appendLine("context", a[start-1])
	}
	for _, line := range a[start : len(a)-end] {
		appendLine("removed", line)
	}
	for _, line := range b[start : len(b)-end] {
		appendLine("added", line)
	}
	if len(a)+len(b)-2*start-2*end > 398 {
		out = append(out, DiffLine{"context", "… diff truncated; view complete generated configuration"})
	}
	if end > 0 {
		appendLine("context", a[len(a)-end])
	}
	return out
}

// comparisonSnapshot expands omitted defaults without changing stored policy hashes.
// Sorting here is safe for keyed records and set-like ACL fields; DNS server order
// and expert text remain significant.
func comparisonSnapshot(input policy.Snapshot) policy.Snapshot {
	raw, _ := json.Marshal(input)
	var s policy.Snapshot
	_ = json.Unmarshal(raw, &s)
	if s.Mode == "" {
		s.Mode = "structured"
	}
	settings := policy.EffectiveSettings(s)
	settings.SafePorts = append([]int{}, settings.SafePorts...)
	settings.ConnectPorts = append([]int{}, settings.ConnectPorts...)
	settings.DNSNameservers = append([]string{}, settings.DNSNameservers...)
	sort.Ints(settings.SafePorts)
	sort.Ints(settings.ConnectPorts)
	for i, address := range settings.DNSNameservers {
		if a, e := netip.ParseAddr(address); e == nil {
			settings.DNSNameservers[i] = a.String()
		}
	}
	s.Settings = &settings
	s.Clients = append([]policy.Client{}, s.Clients...)
	s.Policies = append([]policy.Policy{}, s.Policies...)
	s.TLSProfiles = append([]policy.TLSProfile{}, s.TLSProfiles...)
	for i := range s.Clients {
		c := &s.Clients[i]
		if c.Kind == "" {
			c.Kind = "host"
		}
		c.Networks = append([]string{}, c.Networks...)
		for j, raw := range c.Networks {
			if address, e := netip.ParseAddr(raw); e == nil {
				c.Networks[j] = netip.PrefixFrom(address, address.BitLen()).String()
			} else if prefix, e := netip.ParsePrefix(raw); e == nil {
				c.Networks[j] = prefix.Masked().String()
			}
		}
		sort.Strings(c.Networks)
	}
	for i := range s.Policies {
		p := &s.Policies[i]
		p.Destinations = append([]policy.Destination{}, p.Destinations...)
		sort.Slice(p.Destinations, func(i, j int) bool {
			a, b := p.Destinations[i], p.Destinations[j]
			if a.Domain == b.Domain {
				return !a.IncludeSubdomains && b.IncludeSubdomains
			}
			return a.Domain < b.Domain
		})
	}
	sort.Slice(s.Clients, func(i, j int) bool { return s.Clients[i].ID < s.Clients[j].ID })
	sort.Slice(s.Policies, func(i, j int) bool { return s.Policies[i].ID < s.Policies[j].ID })
	sort.Slice(s.TLSProfiles, func(i, j int) bool { return s.TLSProfiles[i].ID < s.TLSProfiles[j].ID })
	return s
}
func semanticDiff(a, b policy.Snapshot) []string {
	a, b = comparisonSnapshot(a), comparisonSnapshot(b)
	out := []string{}
	old := map[string]policy.Client{}
	for _, c := range a.Clients {
		old[c.ID] = c
	}
	for _, c := range b.Clients {
		previous, exists := old[c.ID]
		if !exists {
			out = append(out, "Добавлен клиент: "+c.Name)
		} else {
			aa, _ := json.Marshal(previous)
			bb, _ := json.Marshal(c)
			if string(aa) != string(bb) {
				out = append(out, "Изменён клиент: "+c.Name)
			}
		}
		delete(old, c.ID)
	}
	for _, c := range a.Clients {
		if _, ok := old[c.ID]; ok {
			out = append(out, "Удалён клиент: "+c.Name)
		}
	}
	pa, _ := json.Marshal(a.Policies)
	pb, _ := json.Marshal(b.Policies)
	if string(pa) != string(pb) {
		out = append(out, "Изменены политики доступа")
	}
	sa, _ := json.Marshal(policy.EffectiveSettings(a))
	sb, _ := json.Marshal(policy.EffectiveSettings(b))
	if string(sa) != string(sb) {
		out = append(out, "Изменены настройки Squid")
	}
	if a.Mode != b.Mode || a.Expert != b.Expert {
		out = append(out, "Изменён режим конфигурации или экспертный squid.conf")
	}
	ta, _ := json.Marshal(a.TLSProfiles)
	tb, _ := json.Marshal(b.TLSProfiles)
	if string(ta) != string(tb) {
		out = append(out, "Изменены TLS-профили")
	}
	if len(out) > 100 {
		out = append(out[:100], "… остальные изменения показаны в diff")
	}
	return out
}

// configurationPreview renders the saved draft without creating a durable revision.
func (s *Server) configurationPreview(w http.ResponseWriter, r *http.Request, v session) {
	var draft Draft
	if e := s.Store.Get("draft", &draft); e != nil {
		fail(w, 503, "storage_unavailable", "Черновик недоступен")
		return
	}
	result, e := s.previewSnapshot(draft.Snapshot)
	if e != nil {
		s.previewFailure(w, e)
		return
	}
	result["draft_version"] = draft.Version
	result["policy_hash"] = policy.Hash(draft.Snapshot)
	respond(w, 200, result)
}
func (s *Server) previewSnapshot(snapshot policy.Snapshot) (map[string]any, error) {
	config, e := policy.Render(snapshot, s.Runtime.Options)
	if e != nil {
		return nil, e
	}
	manifest, e := s.Runtime.ReadManifest()
	if e != nil {
		return nil, errPreviewRuntime
	}
	summary := semanticDiff(manifest.Snapshot, snapshot)
	userChanges := len(summary) > 0
	appliedConfig, renderErr := policy.Render(manifest.Snapshot, s.Runtime.Options)
	runtimeChanges := renderErr != nil || appliedConfig != manifest.Config
	var previous any
	if manifest.Revision != "" {
		previous = manifest.Revision
	}
	lines := []DiffLine{}
	if userChanges || runtimeChanges {
		lines = configDiff(manifest.Config, config)
	}
	return map[string]any{"summary": summary, "user_changes": userChanges, "runtime_changes": runtimeChanges, "config_diff": lines, "generated_config": config, "warnings": []string{}, "expected_applied_revision": previous}, nil
}

func sameInstallationPorts(a, b policy.Snapshot) bool {
	installed, requested := policy.EffectiveSettings(a), policy.EffectiveSettings(b)
	return installed.HTTPPort == requested.HTTPPort && installed.TLSPort == requested.TLSPort
}

var errPreviewRuntime = errors.New("preview runtime unavailable")

func (s *Server) previewFailure(w http.ResponseWriter, e error) {
	if errors.Is(e, errPreviewRuntime) {
		fail(w, 503, "runtime_unavailable", "Нет рабочей конфигурации")
		return
	}
	fail(w, 422, "invalid_policy", e.Error())
}
