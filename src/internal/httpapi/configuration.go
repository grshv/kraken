package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"kraken.local/kraken/internal/policy"
)

type RevisionSummary struct {
	ID          string `json:"id"`
	Number      int64  `json:"number"`
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
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var d Draft
	m, e := s.Runtime.ReadManifest()
	var changed any
	var runtimeUpdate any
	if e == nil && s.Store.Get(ctx, "draft", &d) == nil {
		changed = len(semanticDiff(m.Snapshot, d.Snapshot)) > 0
		if config, renderErr := policy.Render(m.Snapshot, s.Runtime.Options); renderErr == nil {
			runtimeUpdate = config != m.Config
		}
	}
	var number any
	if e == nil && m.Revision != "" {
		if n, ne := s.Store.RevisionNumber(ctx, m.Revision); ne == nil && n > 0 {
			number = n
		}
	}
	result["applied_revision_number"] = number
	result["has_unapplied_changes"] = changed
	result["runtime_update_available"] = runtimeUpdate
	result["instance"] = s.instanceStatus(r.Context())
	result["ingestion"] = s.ingestion()
	respond(w, 200, result)
}
func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request, v session) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	rows, e := s.Store.ListRevisions(ctx)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
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
		items = append(items, RevisionSummary{ID: rev.ID, Number: rev.Number, Created: rev.Created, Hash: rev.Hash, Compiler: rev.Compiler, Applied: row.Applied, Current: isCurrent, CanRollback: row.Applied && !isCurrent && me == nil && rev.Compiler == policy.CompilerVersion && rev.Image == s.Image && sameInstallationPorts(current.Snapshot, rev.Snapshot), Status: row.LatestStatus})
	}
	respond(w, 200, map[string]any{"items": items})
}
func (s *Server) expertTemplate(w http.ResponseWriter, r *http.Request, v session) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var d Draft
	if e := s.Store.Get(ctx, "draft", &d); e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
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

// Compare directives, excluding only the generated fingerprint. The compiler
// version remains visible. The full unmodified configuration is returned separately.
func diffConfigLines(config string) []string {
	if config == "" {
		return []string{}
	}
	out := []string{}
	for _, line := range strings.Split(strings.TrimSuffix(config, "\n"), "\n") {
		if strings.HasPrefix(line, "reply_header_add X-Kraken-Revision ") {
			line = "reply_header_add X-Kraken-Revision <generated> all"
		}
		if strings.HasPrefix(line, "# kraken compiler=") {
			if i := strings.Index(line, " revision="); i >= 0 {
				line = line[:i]
			}
		}
		out = append(out, line)
	}
	return out
}

// Exact line LCS with bounded memory/work, followed by unified three-line
// context hunks. Never represent unchanged middle lines as deleted/added.
func configDiff(before, after string) []DiffLine {
	a, b := diffConfigLines(before), diffConfigLines(after)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-suffix-1] == b[len(b)-suffix-1] {
		suffix++
	}
	n, m := len(a)-prefix-suffix, len(b)-prefix-suffix
	if n == 0 && m == 0 {
		return []DiffLine{}
	}
	// At most 8 MB of LCS storage. Large dissimilar inputs get an explicit
	// omission instead of an inaccurate replacement block or unbounded allocation.
	if (n + 1) > 4000000/(m+1) {
		return []DiffLine{{"context", "Сравнение слишком большое. Откройте полный squid.conf."}}
	}
	width := m + 1
	lengths := make([]uint16, (n+1)*width)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[prefix+i] == b[prefix+j] {
				lengths[i*width+j] = 1 + lengths[(i+1)*width+j+1]
			} else {
				lengths[i*width+j] = max(lengths[(i+1)*width+j], lengths[i*width+j+1])
			}
		}
	}
	edits := make([]DiffLine, 0, len(a)+len(b))
	for _, line := range a[:prefix] {
		edits = append(edits, DiffLine{"context", line})
	}
	i, j := 0, 0
	for i < n || j < m {
		if i < n && j < m && a[prefix+i] == b[prefix+j] {
			edits = append(edits, DiffLine{"context", a[prefix+i]})
			i++
			j++
		} else if i < n && (j == m || lengths[(i+1)*width+j] >= lengths[i*width+j+1]) {
			edits = append(edits, DiffLine{"removed", a[prefix+i]})
			i++
		} else {
			edits = append(edits, DiffLine{"added", b[prefix+j]})
			j++
		}
	}
	for _, line := range a[len(a)-suffix:] {
		edits = append(edits, DiffLine{"context", line})
	}
	out := []DiffLine{}
	oldLine, newLine := 1, 1
	for pos := 0; pos < len(edits); {
		next := pos
		for next < len(edits) && edits[next].Kind == "context" {
			next++
		}
		if next == len(edits) {
			break
		}
		start := max(pos, next-3)
		for ; pos < start; pos++ {
			oldLine++
			newLine++
		}
		end := next + 1
		for end < len(edits) {
			following := end
			for following < len(edits) && edits[following].Kind == "context" {
				following++
			}
			if following-end > 6 || following == len(edits) {
				end = min(end+3, len(edits))
				break
			}
			end = following + 1
		}
		if len(out)+end-start+1 > 400 {
			out = append(out, DiffLine{"context", "… сравнение сокращено; откройте полный squid.conf"})
			break
		}
		oldCount, newCount := 0, 0
		for _, line := range edits[start:end] {
			if line.Kind != "added" {
				oldCount++
			}
			if line.Kind != "removed" {
				newCount++
			}
		}
		oldStart, newStart := oldLine, newLine
		if oldCount == 0 {
			oldStart--
		}
		if newCount == 0 {
			newStart--
		}
		out = append(out, DiffLine{"context", fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount)})
		out = append(out, edits[start:end]...)
		oldLine += oldCount
		newLine += newCount
		pos = end
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
		// Own domains and attached sets are sets: their order is not a change.
		c.Destinations = sortedDestinations(c.Destinations)
		c.PolicyIDs = append([]string{}, c.PolicyIDs...)
		sort.Strings(c.PolicyIDs)
	}
	for i := range s.Policies {
		p := &s.Policies[i]
		p.Destinations = sortedDestinations(p.Destinations)
	}
	sort.Slice(s.Clients, func(i, j int) bool { return s.Clients[i].ID < s.Clients[j].ID })
	sort.Slice(s.Policies, func(i, j int) bool { return s.Policies[i].ID < s.Policies[j].ID })
	sort.Slice(s.TLSProfiles, func(i, j int) bool { return s.TLSProfiles[i].ID < s.TLSProfiles[j].ID })
	return s
}
func sortedDestinations(in []policy.Destination) []policy.Destination {
	out := append([]policy.Destination{}, in...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Domain == b.Domain {
			return !a.IncludeSubdomains && b.IncludeSubdomains
		}
		return a.Domain < b.Domain
	})
	return out
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
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var draft Draft
	if e := s.Store.Get(ctx, "draft", &draft); e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
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
