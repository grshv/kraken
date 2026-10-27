package httpapi

import (
	"encoding/json"
	"net/http"
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
	if e == nil && s.Store.Get("draft", &d) == nil {
		changed = policy.Hash(d.Snapshot) != policy.Hash(m.Snapshot)
	}
	result["has_unapplied_changes"] = changed
	respond(w, 200, result)
}
func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request, v session) {
	rows, e := s.Store.DB.Query(`SELECT value,
 EXISTS(SELECT 1 FROM operations WHERE json_extract(body,'$.revision_id')=json_extract(kv.value,'$.id') AND json_extract(body,'$.status')='succeeded'),
 COALESCE((SELECT json_extract(body,'$.status') FROM operations WHERE json_extract(body,'$.revision_id')=json_extract(kv.value,'$.id') ORDER BY rowid DESC LIMIT 1),'draft')
 FROM kv WHERE key LIKE 'revision:%' ORDER BY rowid DESC LIMIT 100`)
	if e != nil {
		fail(w, 503, "storage_unavailable", "История недоступна")
		return
	}
	defer rows.Close()
	current, me := s.Runtime.ReadManifest()
	items := []RevisionSummary{}
	for rows.Next() {
		var raw, status string
		var applied bool
		if e = rows.Scan(&raw, &applied, &status); e != nil {
			fail(w, 503, "storage_unavailable", "История недоступна")
			return
		}
		var rev Revision
		if e = json.Unmarshal([]byte(raw), &rev); e != nil {
			fail(w, 503, "storage_unavailable", "Повреждённая запись ревизии")
			return
		}
		isCurrent := me == nil && current.Revision == rev.ID
		items = append(items, RevisionSummary{ID: rev.ID, Created: rev.Created, Hash: rev.Hash, Compiler: rev.Compiler, Applied: applied, Current: isCurrent, CanRollback: applied && !isCurrent, Status: status})
	}
	if e = rows.Err(); e != nil {
		fail(w, 503, "storage_unavailable", "История недоступна")
		return
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
func semanticDiff(a, b policy.Snapshot) []string {
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
	if len(out) == 0 {
		out = append(out, "Пользовательские настройки совпадают; служебный конфиг может измениться при обновлении компилятора")
	}
	if len(out) > 100 {
		out = append(out[:100], "… остальные изменения показаны в diff")
	}
	return out
}
