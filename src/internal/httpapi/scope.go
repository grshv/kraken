package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/netip"
	"sort"

	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/store"
)

// managerScope loads the manager's networks and client IDs. Admins get nil
// (no filtering). Reading within a tx keeps the view consistent with the
// request that triggered it.
func (s *Server) managerScope(v session) (store.Scope, error) {
	if v.Role != "manager" {
		return store.Scope{}, nil
	}
	tx, e := s.Store.BeginTx()
	if e != nil {
		return store.Scope{}, e
	}
	defer tx.Rollback()
	scope, e := s.Store.UserScope(tx, v.User)
	if e != nil {
		return store.Scope{}, e
	}
	return scope, nil
}

// activeRevisionSnapshot returns the snapshot of the last successfully applied
// revision, or the draft snapshot if nothing has been applied yet. Used to
// decide which objects are "changed" by the manager's draft edit.
func (s *Server) activeRevisionSnapshot() (policy.Snapshot, error) {
	manifest, e := s.Runtime.ReadManifest()
	if e == nil {
		return manifest.Snapshot, nil
	}
	var d Draft
	if e := s.Store.Get("draft", &d); e != nil {
		return policy.Snapshot{}, e
	}
	return d.Snapshot, nil
}

// draftSnapshot loads the current draft snapshot.
func (s *Server) draftSnapshot() (policy.Snapshot, int, error) {
	var d Draft
	if e := s.Store.Get("draft", &d); e != nil {
		return policy.Snapshot{}, 0, e
	}
	return d.Snapshot, d.Version, nil
}

// existsInCurrent reports whether a client ID exists in the current draft.
func existsInCurrent(clients []policy.Client, id string) bool {
	for _, c := range clients {
		if c.ID == id {
			return true
		}
	}
	return false
}

// inScope reports whether a client ID is in the manager's scope.
func inScope(scope store.Scope, clientID string) bool {
	for _, id := range scope.ClientIDs {
		if id == clientID {
			return true
		}
	}
	return false
}

// networkInScope reports whether a CIDR is contained within one of the
// manager's networks.
func networkInScope(scope store.Scope, raw string) bool {
	p, e := netip.ParsePrefix(raw)
	if e != nil {
		if a, ae := netip.ParseAddr(raw); ae == nil {
			p = netip.PrefixFrom(a, a.BitLen())
		} else {
			return false
		}
	}
	p = p.Masked()
	for _, n := range scope.Networks {
		net, e := netip.ParsePrefix(n)
		if e != nil {
			continue
		}
		net = net.Masked()
		if net.Bits() <= p.Bits() && net.Contains(p.Addr()) {
			return true
		}
	}
	return false
}

// clientsByPolicy returns policy_id -> set of client IDs referencing it.
func clientsByPolicy(clients []policy.Client) map[string][]string {
	out := map[string][]string{}
	for _, c := range clients {
		out[c.PolicyID] = append(out[c.PolicyID], c.ID)
	}
	return out
}

// policyHasForeignClient reports whether a policy is referenced by any client
// outside the manager's scope, in either the draft or the active snapshot.
func policyHasForeignClient(draft, active []policy.Client, scope store.Scope, policyID string) bool {
	for _, c := range draft {
		if c.PolicyID == policyID && !inScope(scope, c.ID) {
			return true
		}
	}
	for _, c := range active {
		if c.PolicyID == policyID && !inScope(scope, c.ID) {
			return true
		}
	}
	return false
}

// overlapForeignClient reports whether a CIDR overlaps an enabled foreign
// client's network in the draft. Returns true without leaking which client.
func overlapForeignClient(draft []policy.Client, scope store.Scope, raw string) bool {
	p, e := netip.ParsePrefix(raw)
	if e != nil {
		if a, ae := netip.ParseAddr(raw); ae == nil {
			p = netip.PrefixFrom(a, a.BitLen())
		} else {
			return false
		}
	}
	p = p.Masked()
	for _, c := range draft {
		if !c.Enabled || inScope(scope, c.ID) {
			continue
		}
		for _, n := range c.Networks {
			np, e := netip.ParsePrefix(n)
			if e != nil {
				if a, ae := netip.ParseAddr(n); ae == nil {
					np = netip.PrefixFrom(a, a.BitLen())
				} else {
					continue
				}
			}
			np = np.Masked()
			if p.Overlaps(np) {
				return true
			}
		}
	}
	return false
}

// managerDraftView filters a snapshot to the manager's scope: his clients,
// policies referenced by his clients plus policies without clients, TLS
// profiles (so clients/policies stay valid), settings present but ignored, and
// no expert_config. Version stays the full draft version.
func managerDraftView(snap policy.Snapshot, scope store.Scope) policy.Snapshot {
	out := policy.Snapshot{Version: snap.Version, Mode: snap.Mode, Clients: []policy.Client{}, Policies: []policy.Policy{}, TLSProfiles: append([]policy.TLSProfile(nil), snap.TLSProfiles...)}
	if snap.Settings != nil {
		s := *snap.Settings
		out.Settings = &s
	}
	mine := map[string]bool{}
	for _, c := range snap.Clients {
		if inScope(scope, c.ID) {
			out.Clients = append(out.Clients, c)
			mine[c.ID] = true
		}
	}
	// Policies: keep those referenced by my clients, plus those with no clients.
	byPol := clientsByPolicy(snap.Clients)
	for _, p := range snap.Policies {
		refs := byPol[p.ID]
		keep := len(refs) == 0
		for _, id := range refs {
			if mine[id] {
				keep = true
				break
			}
		}
		if keep {
			out.Policies = append(out.Policies, p)
		}
	}
	sort.Slice(out.Clients, func(i, j int) bool { return out.Clients[i].ID < out.Clients[j].ID })
	sort.Slice(out.Policies, func(i, j int) bool { return out.Policies[i].ID < out.Policies[j].ID })
	return out
}

// mergeManagerDraft applies the manager's visible slice onto the full draft.
// Objects outside the scope, settings, expert_config and TLS profiles are
// taken from the current draft unchanged.
func mergeManagerDraft(current, input policy.Snapshot, scope store.Scope) policy.Snapshot {
	out := current
	if out.Clients == nil {
		out.Clients = []policy.Client{}
	}
	if out.Policies == nil {
		out.Policies = []policy.Policy{}
	}
	if out.TLSProfiles == nil {
		out.TLSProfiles = []policy.TLSProfile{}
	}
	// Clients: replace manager's clients (by ID) with those from input; keep foreign.
	inputByID := map[string]policy.Client{}
	for _, c := range input.Clients {
		inputByID[c.ID] = c
	}
	merged := []policy.Client{}
	seen := map[string]bool{}
	for _, c := range current.Clients {
		if inScope(scope, c.ID) {
			if replacement, ok := inputByID[c.ID]; ok {
				merged = append(merged, replacement)
				seen[c.ID] = true
			}
			// If a manager-deleted his own client, drop it.
			continue
		}
		merged = append(merged, c)
	}
	// Add manager's new clients (not in current draft). New clients are
	// created by the manager, so they bypass the scope check: the assignment
	// is persisted alongside the draft write.
	for _, c := range input.Clients {
		if !seen[c.ID] && !existsInCurrent(current.Clients, c.ID) {
			merged = append(merged, c)
		}
	}
	out.Clients = merged
	// Policies: replace manager-visible ones from input; keep policies that are
	// referenced by foreign clients in current draft.
	inputPolByID := map[string]policy.Policy{}
	for _, p := range input.Policies {
		inputPolByID[p.ID] = p
	}
	byPolCurrent := clientsByPolicy(current.Clients)
	byPolInput := clientsByPolicy(input.Clients)
	mergedP := []policy.Policy{}
	seenP := map[string]bool{}
	for _, p := range current.Policies {
		refsCur := byPolCurrent[p.ID]
		hasForeign := false
		for _, id := range refsCur {
			if !inScope(scope, id) {
				hasForeign = true
				break
			}
		}
		if hasForeign {
			mergedP = append(mergedP, p)
			seenP[p.ID] = true
			continue
		}
		// Manager may edit it. Use the input version if present.
		if replacement, ok := inputPolByID[p.ID]; ok {
			mergedP = append(mergedP, replacement)
			seenP[p.ID] = true
		}
		// Deleted by manager: drop.
	}
	// New policies created in input (not in current).
	for _, p := range input.Policies {
		if !seenP[p.ID] {
			hasForeign := false
			for _, id := range byPolInput[p.ID] {
				// A client is foreign only if it exists in the current draft
				// and is not in the manager's scope. New clients (not in
				// current) are the manager's own creations.
				if existsInCurrent(current.Clients, id) && !inScope(scope, id) {
					hasForeign = true
					break
				}
			}
			if !hasForeign {
				mergedP = append(mergedP, p)
			}
		}
	}
	out.Policies = mergedP
	// Settings, expert, mode, TLS: taken from current (manager edits ignored).
	return out
}

// validateManagerPolicyEdits checks that the manager did not modify a policy
// in his view that is referenced by a foreign client. Policies outside the
// manager's view (only referenced by foreign clients) are not checked: the
// merge takes them from the current draft unchanged. Returns the code+message
// for fail() when a shared policy was changed or deleted.
func validateManagerPolicyEdits(current, input policy.Snapshot, scope store.Scope) (string, string, bool) {
	// Determine which policies are in the manager's view: those referenced by
	// his clients, and those without any clients. Policies only referenced by
	// foreign clients are outside the view and handled by the merge.
	byPolCurrent := clientsByPolicy(current.Clients)
	viewPolicies := map[string]bool{}
	for _, p := range current.Policies {
		refs := byPolCurrent[p.ID]
		if len(refs) == 0 {
			viewPolicies[p.ID] = true
			continue
		}
		for _, id := range refs {
			if inScope(scope, id) {
				viewPolicies[p.ID] = true
				break
			}
		}
	}
	currentPol := map[string]policy.Policy{}
	for _, p := range current.Policies {
		currentPol[p.ID] = p
	}
	inputPol := map[string]policy.Policy{}
	for _, p := range input.Policies {
		inputPol[p.ID] = p
	}
	// Only check policies in the manager's view that have foreign clients.
	// A shared policy (visible to the manager, also referenced by a foreign
	// client) must not be changed or deleted.
	for id, cp := range currentPol {
		if !viewPolicies[id] {
			continue
		}
		if !policyHasForeignClient(current.Clients, current.Clients, scope, id) {
			continue
		}
		ip, ok := inputPol[id]
		if !ok {
			return "policy_shared_out_of_scope", "Политика используется чужими клиентами", false
		}
		cb, _ := json.Marshal(cp)
		ib, _ := json.Marshal(ip)
		if string(cb) != string(ib) {
			return "policy_shared_out_of_scope", "Политика используется чужими клиентами", false
		}
	}
	return "", "", true
}

// validateManagerDraft checks scope invariants the server must enforce before a
// merged draft is accepted: source networks inside the manager's networks, and
// no overlap with a foreign client. Returns the code+message pair for fail().
func validateManagerDraft(merged policy.Snapshot, scope store.Scope) (string, string, bool) {
	for _, c := range merged.Clients {
		if !inScope(scope, c.ID) {
			continue
		}
		for _, raw := range c.Networks {
			if !networkInScope(scope, raw) {
				return "source_outside_scope", "Адрес клиента вне ваших сетей", false
			}
			if overlapForeignClient(merged.Clients, scope, raw) {
				return "source_overlap", "Адрес пересекается с клиентом вне вашей области", false
			}
		}
	}
	return "", "", true
}

// draftChangesOutOfScope reports whether the draft differs from the active
// snapshot in objects outside the manager's scope. Used to gate POST
// /revisions (and apply/reset) for managers.
func draftChangesOutOfScope(draft, active policy.Snapshot, scope store.Scope) bool {
	draftClients := map[string]policy.Client{}
	for _, c := range draft.Clients {
		draftClients[c.ID] = c
	}
	activeClients := map[string]policy.Client{}
	for _, c := range active.Clients {
		activeClients[c.ID] = c
	}
	// Clients changed outside scope.
	for id, c := range draftClients {
		if !inScope(scope, id) {
			old, ok := activeClients[id]
			if !ok {
				return true
			}
			d, _ := json.Marshal(c)
			a, _ := json.Marshal(old)
			if string(d) != string(a) {
				return true
			}
		}
	}
	for id := range activeClients {
		if !inScope(scope, id) {
			if _, ok := draftClients[id]; !ok {
				return true
			}
		}
	}
	// Policies changed that a foreign client references.
	draftPol := map[string]policy.Policy{}
	for _, p := range draft.Policies {
		draftPol[p.ID] = p
	}
	activePol := map[string]policy.Policy{}
	for _, p := range active.Policies {
		activePol[p.ID] = p
	}
	for id, p := range draftPol {
		if policyHasForeignClient(draft.Clients, active.Clients, scope, id) {
			old, ok := activePol[id]
			if !ok {
				return true
			}
			d, _ := json.Marshal(p)
			a, _ := json.Marshal(old)
			if string(d) != string(a) {
				return true
			}
		}
	}
	for id := range activePol {
		if policyHasForeignClient(draft.Clients, active.Clients, scope, id) {
			if _, ok := draftPol[id]; !ok {
				return true
			}
		}
	}
	// Settings / expert / mode / TLS changes are admin-only.
	ds, _ := json.Marshal(draft.Settings)
	as, _ := json.Marshal(active.Settings)
	if string(ds) != string(as) {
		return true
	}
	if draft.Mode != active.Mode || draft.Expert != active.Expert {
		return true
	}
	dt, _ := json.Marshal(draft.TLSProfiles)
	at, _ := json.Marshal(active.TLSProfiles)
	if string(dt) != string(at) {
		return true
	}
	return false
}

// loadActiveManifestOrDraft is a convenience that never fails fatally: a
// missing manifest falls back to the draft snapshot (no apply yet).
func (s *Server) loadActiveManifestOrDraft() policy.Snapshot {
	snap, _ := s.activeRevisionSnapshot()
	return snap
}

// PruneManagerClients removes manager-client assignments for clients that no
// longer exist in either the draft or the active revision. Called from the
// periodic maintenance tick alongside store.Prune.
func (s *Server) PruneManagerClients() error {
	draft, _, dErr := s.draftSnapshot()
	active, aErr := s.activeRevisionSnapshot()
	if dErr != nil && aErr != nil {
		return nil
	}
	draftIDs := make([]string, len(draft.Clients))
	for i, c := range draft.Clients {
		draftIDs[i] = c.ID
	}
	activeIDs := make([]string, len(active.Clients))
	for i, c := range active.Clients {
		activeIDs[i] = c.ID
	}
	return s.Store.PruneManagerClients(draftIDs, activeIDs)
}

var errNotFound = errors.New("not found")

// ensure sql.ErrNoRows alias kept for handler use.
var _ = sql.ErrNoRows
