//go:build integration

package integration

import (
	"path/filepath"
	"testing"

	"kraken.local/kraken/internal/policy"
)

// TestFlows exercises four key product flows through the HTTP API and real
// client traffic on a single disposable instance: deny→explain→allow→apply,
// unknown source promotion, manager scope boundaries, and apply failure with
// rollback. Each step is a numbered t.Run sharing one instance and subnet.
func TestFlows(t *testing.T) {
	t.Parallel()
	fixtures := t.TempDir()
	r := resources{}
	r.track(t)
	const subnet, base = "11.250.18.0/24", "11.250.18"
	inst := launchInstance(t, &r, fixtures, subnet, base)
	genOriginCert(t, fixtures)
	_, clientA := startOriginClient(t, &r, inst.network, fixtures, randName("flows"), base)

	// Additional client containers with distinct source IPs.
	clientB := r.addContainer(randName("flows-b"))
	docker(t, "run", "-d", "--name", clientB, "--network", inst.network, "--ip", base+".30",
		"-v", fixtures+":/fixtures:ro", pythonImage, "python", "-c", "import time;time.sleep(3600)")
	clientU := r.addContainer(randName("flows-u"))
	docker(t, "run", "-d", "--name", clientU, "--network", inst.network, "--ip", base+".40",
		"-v", fixtures+":/fixtures:ro", pythonImage, "python", "-c", "import time;time.sleep(3600)")

	flFetch := func(container, target string, tlsProxy, connect bool) int {
		return fetchStatus(t, container, target, tlsProxy, connect, "/fixtures/proxy.crt", "/fixtures/origin.crt")
	}

	// Initial policy: A and B have allowlist with denied.test (not allowed.test).
	snap := policySnap(t, inst.panel)
	profile := snap.TLSProfiles[0].ID
	polA, polB := randID(), randID()
	clientAID, clientBID := randID(), randID()
	snap.Policies = []policy.Policy{
		{ID: polA, Name: "A allowlist", Mode: "allowlist", Destinations: []policy.Destination{{Domain: "denied.test"}}, TLSProfileID: profile},
		{ID: polB, Name: "B allowlist", Mode: "allowlist", Destinations: []policy.Destination{{Domain: "denied.test"}}, TLSProfileID: profile},
	}
	snap.Clients = []policy.Client{
		{ID: clientAID, Name: "client A", Networks: []string{base + ".20"}, Enabled: true, PolicyID: polA},
		{ID: clientBID, Name: "client B", Networks: []string{base + ".30"}, Enabled: true, PolicyID: polB},
	}
	inst.panel.apply(t, snap, inst.panel.draftVersion())
	// U becomes a client in flow 2.
	var clientUID string

	// Wait for the initial policy to take effect.
	waitFor(t, 30, func() error {
		if flFetch(clientA, "http://denied.test/", false, false) != 200 {
			return errf("A denied.test not 200")
		}
		if flFetch(clientB, "http://denied.test/", false, false) != 200 {
			return errf("B denied.test not 200")
		}
		return nil
	})

	t.Run("1. Deny → explain → allow → apply", func(t *testing.T) {
		// A requests allowed.test → 403 (not in allowlist).
		if st := flFetch(clientA, "http://allowed.test/", false, false); st != 403 {
			t.Fatalf("A allowed.test: expected 403, got %d", st)
		}

		// Wait for the deny event and explain it.
		ev := inst.panel.waitForEvent(t, 30, "client_id="+clientAID+"&failures_only=true&destination=allowed.test")
		explain := inst.panel.apiOK(t, "GET", "/inspector/events/"+ev["id"].(string)+"/explain", nil, "")
		if explain["decision"] != "denied" || explain["reason"] != "not_in_allowlist" {
			t.Fatalf("explain: %+v", explain)
		}
		if explain["client_id"] != clientAID {
			t.Fatalf("explain client_id: %v, want %s", explain["client_id"], clientAID)
		}

		// Add allowed.test to A's allowlist in the draft (no apply yet).
		draft := deepCopySnap(policySnap(t, inst.panel))
		for i, p := range draft.Policies {
			if p.ID == polA {
				draft.Policies[i].Destinations = append(draft.Policies[i].Destinations, policy.Destination{Domain: "allowed.test"})
			}
		}
		inst.panel.putDraft(t, draft, inst.panel.draftVersion())

		// Draft-impact: newly_allowed ≥ 1 and A in clients.
		impact := inst.panel.apiOK(t, "GET", "/inspector/draft-impact", nil, "")
		if na, _ := impact["newly_allowed"].(float64); na < 1 {
			t.Fatalf("draft-impact newly_allowed: %+v", impact)
		}
		foundA := false
		for _, c := range impact["clients"].([]any) {
			if c.(map[string]any)["client_id"] == clientAID {
				foundA = true
			}
		}
		if !foundA {
			t.Fatalf("draft-impact clients missing A: %+v", impact["clients"])
		}

		// Before apply: draft does not change traffic.
		if st := flFetch(clientA, "http://allowed.test/", false, false); st != 403 {
			t.Fatalf("before apply: expected 403, got %d", st)
		}

		// Apply → A can access allowed.test (HTTP and CONNECT).
		inst.panel.apply(t, draft, inst.panel.draftVersion())
		waitFor(t, 30, func() error {
			if flFetch(clientA, "http://allowed.test/", false, false) != 200 {
				return errf("A allowed.test not 200 after apply")
			}
			return nil
		})
		if st := flFetch(clientA, "http://allowed.test/", false, true); st != 200 {
			t.Fatalf("A CONNECT: expected 200, got %d", st)
		}
	})

	t.Run("2. Unknown source", func(t *testing.T) {
		uIP := base + ".40"

		// U (not a client) requests allowed.test → 403.
		if st := flFetch(clientU, "http://allowed.test/", false, false); st != 403 {
			t.Fatalf("U: expected 403, got %d", st)
		}

		// Unknown-sources contains U with denied ≥ 1.
		src := waitForResult(t, 30, func() (map[string]any, error) {
			r := inst.panel.apiOK(t, "GET", "/inspector/unknown-sources", nil, "")
			for _, s := range r["sources"].([]any) {
				if s.(map[string]any)["source_ip"] == uIP {
					return s.(map[string]any), nil
				}
			}
			return nil, errf("U not in unknown-sources")
		})
		if denied, _ := src["denied"].(float64); denied < 1 {
			t.Fatalf("U denied count: %v", src)
		}

		// Explain U's event → unknown_source, client_id=null.
		ev := inst.panel.waitForEvent(t, 30, "source_ip="+uIP+"&failures_only=true")
		explain := inst.panel.apiOK(t, "GET", "/inspector/events/"+ev["id"].(string)+"/explain", nil, "")
		if explain["reason"] != "unknown_source" {
			t.Fatalf("explain: %+v", explain)
		}
		if explain["client_id"] != nil {
			t.Fatalf("explain client_id: %v, want null", explain["client_id"])
		}

		// Create client U (all_web) via draft, apply → U request 200.
		draft := deepCopySnap(policySnap(t, inst.panel))
		allWebID := randID()
		clientUID = randID()
		draft.Policies = append(draft.Policies, policy.Policy{ID: allWebID, Name: "U all_web", Mode: "all_web", TLSProfileID: draft.TLSProfiles[0].ID})
		draft.Clients = append(draft.Clients, policy.Client{ID: clientUID, Name: "client U", Networks: []string{uIP}, Enabled: true, PolicyID: allWebID})
		inst.panel.apply(t, draft, inst.panel.draftVersion())
		waitFor(t, 30, func() error {
			if flFetch(clientU, "http://allowed.test/", false, false) != 200 {
				return errf("U not 200 after apply")
			}
			return nil
		})
	})

	t.Run("3. Manager and scope", func(t *testing.T) {
		// Generate traffic from A, B and U to create events for each.
		flFetch(clientA, "http://allowed.test/", false, false) // 200
		flFetch(clientB, "http://allowed.test/", false, false) // 403
		flFetch(clientU, "http://allowed.test/", false, false) // 200
		// Wait for at least one A event to be ingested.
		inst.panel.waitForEvent(t, 30, "client_id="+clientAID+"&limit=1")

		// Admin creates a manager user with a password (API only, never printed).
		managerPassword := randPassword(24)
		inst.panel.apiOK(t, "POST", "/users", map[string]string{"username": "flowmgr", "password": managerPassword, "role": "manager"}, "")
		// Assign client A to the manager (not B, not U).
		inst.panel.apiOK(t, "PUT", "/users/flowmgr/scope", map[string]any{"networks": []string{base + ".20/32"}, "client_ids": []string{clientAID}}, "")

		// Manager logs in with a separate session.
		managerPanel := newPanel(inst.panel.origin, filepath.Join(fixtures, "panel.crt"), managerPassword)
		managerPanel.username = "flowmgr"
		managerPanel.login(t, managerPassword)

		// Manager /events: contains A, not B, not U.
		mgrPage := managerPanel.apiOK(t, "GET", "/events?limit=200", nil, "")
		mgrIDs := map[string]bool{}
		for _, v := range mgrPage["items"].([]any) {
			if cid, _ := v.(map[string]any)["client_id"].(string); cid != "" {
				mgrIDs[cid] = true
			}
		}
		if !mgrIDs[clientAID] {
			t.Errorf("manager events missing A")
		}
		if mgrIDs[clientBID] {
			t.Errorf("manager events leaked B")
		}
		if clientUID != "" && mgrIDs[clientUID] {
			t.Errorf("manager events leaked U")
		}

		// Admin stages a foreign (B) change; manager POST /revisions → 403.
		activeRev, _ := inst.panel.status()["applied_revision"].(string)
		adminDraft := deepCopySnap(policySnap(t, inst.panel))
		for i, c := range adminDraft.Clients {
			if c.ID == clientBID {
				adminDraft.Clients[i].Networks = []string{base + ".99"}
			}
		}
		inst.panel.putDraft(t, adminDraft, inst.panel.draftVersion())
		resp, st := managerPanel.api("POST", "/revisions", map[string]any{"expected_draft_version": managerPanel.draftVersion()}, "")
		if st != 403 || resp["code"] != "draft_out_of_scope" {
			t.Fatalf("manager foreign revision: %d %v", st, resp)
		}
		if rev, _ := inst.panel.status()["applied_revision"].(string); rev != activeRev {
			t.Fatalf("active revision changed: %s → %s", activeRev, rev)
		}

		// Admin discards the foreign draft change.
		inst.panel.apiOK(t, "DELETE", "/configuration/draft", map[string]any{"expected_version": inst.panel.draftVersion()}, "")

		// Manager edits A (remove denied.test from A's policy) and applies.
		mgrSnap := policySnap(t, managerPanel)
		for i, p := range mgrSnap.Policies {
			if p.ID == polA {
				mgrSnap.Policies[i].Destinations = []policy.Destination{{Domain: "allowed.test"}}
			}
		}
		managerPanel.applyAsManager(t, mgrSnap, managerPanel.draftVersion(), activeRev)
		// A → denied.test = 403 (manager's edit changed A's traffic).
		waitFor(t, 30, func() error {
			if flFetch(clientA, "http://denied.test/", false, false) != 403 {
				return errf("A denied.test not 403 after manager edit")
			}
			return nil
		})

		// Manager explaining B's event → 404 (no foreign leak).
		bEvent := inst.panel.waitForEvent(t, 30, "client_id="+clientBID+"&limit=1")
		resp, st = managerPanel.api("GET", "/inspector/events/"+bEvent["id"].(string)+"/explain", nil, "")
		if st != 404 {
			t.Fatalf("manager explain B event: expected 404, got %d", st)
		}
	})

	t.Run("4. Apply failure and rollback", func(t *testing.T) {
		// Baseline: A and U can access allowed.test.
		if st := flFetch(clientA, "http://allowed.test/", false, false); st != 200 {
			t.Fatalf("A baseline: expected 200, got %d", st)
		}
		if st := flFetch(clientU, "http://allowed.test/", false, false); st != 200 {
			t.Fatalf("U baseline: expected 200, got %d", st)
		}
		activeRev, _ := inst.panel.status()["applied_revision"].(string)
		configHash, _ := inst.panel.status()["observed_config_hash"].(string)

		// Draft that passes server validation but fails real squid -k parse.
		broken := deepCopySnap(policySnap(t, inst.panel))
		broken.Mode = "expert"
		broken.Expert = "acl fixture_bad src not..an..address"
		brokenDraft := inst.panel.putDraft(t, broken, inst.panel.draftVersion())
		brokenRev := inst.panel.apiOK(t, "POST", "/revisions", map[string]any{"expected_draft_version": brokenDraft.Version}, "")

		// Validate first: squid -k parse rejects the broken expert section.
		badOp := inst.panel.apiOK(t, "POST", "/operations", map[string]any{"action": "validate", "revision_id": brokenRev["id"], "expected_applied_revision": activeRev}, randKey())
		badResult := waitForResult(t, 60, func() (map[string]any, error) {
			cur, st := inst.panel.api("GET", "/operations/"+badOp["id"].(string), nil, "")
			if st < 200 || st >= 300 {
				return nil, errf("op status %d", st)
			}
			s, _ := cur["status"].(string)
			if s == "pending" || s == "validating" {
				return nil, errf("pending")
			}
			return cur, nil
		})
		if badResult["status"] != "rejected" {
			t.Fatalf("validate op: %v", badResult)
		}

		// Attempt apply on the broken revision. The server rejects it because
		// validation did not reach "validated" — no operation is created.
		baselineApplyAudit := inst.panel.apiOK(t, "GET", "/audit?action=apply&limit=200", nil, "")["items"].([]any)
		applyResp, applySt := inst.panel.api("POST", "/operations", map[string]any{
			"action":                    "apply",
			"revision_id":               brokenRev["id"],
			"expected_applied_revision": activeRev,
			"validation_operation_id":   badOp["id"],
		}, randKey())
		if applySt == 202 && applyResp != nil && applyResp["status"] == "succeeded" {
			t.Fatalf("broken revision apply unexpectedly succeeded: %v", applyResp)
		}
		// Actual: 422 validation_required (apply gated on a validated operation).
		if applySt != 422 || applyResp["code"] != "validation_required" {
			t.Fatalf("apply on broken revision: status %d code %v, want 422 validation_required", applySt, applyResp["code"])
		}

		// Active revision, fingerprint and traffic unchanged.
		if rev, _ := inst.panel.status()["applied_revision"].(string); rev != activeRev {
			t.Fatalf("active revision changed: %s → %s", activeRev, rev)
		}
		if hash, _ := inst.panel.status()["observed_config_hash"].(string); hash != configHash {
			t.Fatalf("config hash changed: %s → %s", configHash, hash)
		}
		if st := flFetch(clientA, "http://allowed.test/", false, false); st != 200 {
			t.Fatalf("A after failed apply: expected 200, got %d", st)
		}
		if st := flFetch(clientU, "http://allowed.test/", false, false); st != 200 {
			t.Fatalf("U after failed apply: expected 200, got %d", st)
		}

		// No successful apply of the broken revision in /audit.
		afterApplyAudit := inst.panel.apiOK(t, "GET", "/audit?action=apply&limit=200", nil, "")["items"].([]any)
		if len(afterApplyAudit) != len(baselineApplyAudit) {
			t.Fatalf("apply audit count changed after failed apply: %d → %d", len(baselineApplyAudit), len(afterApplyAudit))
		}

		// Discard the broken expert draft to reset to the active snapshot.
		inst.panel.apiOK(t, "DELETE", "/configuration/draft", map[string]any{"expected_version": inst.panel.draftVersion()}, "")

		// Apply a valid change: remove allowed.test from A → 403.
		valid := deepCopySnap(policySnap(t, inst.panel))
		for i, p := range valid.Policies {
			if p.ID == polA {
				valid.Policies[i].Destinations = []policy.Destination{}
			}
		}
		inst.panel.apply(t, valid, inst.panel.draftVersion())
		waitFor(t, 30, func() error {
			if flFetch(clientA, "http://allowed.test/", false, false) != 403 {
				return errf("A allowed.test not 403 after removal")
			}
			return nil
		})

		// Rollback to the previous revision → A can access allowed.test again.
		currentRev, _ := inst.panel.status()["applied_revision"].(string)
		rb := inst.panel.op(t, "rollback", activeRev, currentRev, "")
		if rb["status"] != "succeeded" {
			t.Fatalf("rollback: %v", rb)
		}
		waitFor(t, 30, func() error {
			if flFetch(clientA, "http://allowed.test/", false, false) != 200 {
				return errf("A allowed.test not 200 after rollback")
			}
			return nil
		})

		// /audit has records for both apply and rollback.
		if items := inst.panel.apiOK(t, "GET", "/audit?action=apply&limit=50", nil, "")["items"].([]any); len(items) == 0 {
			t.Fatal("no apply audit records")
		}
		if items := inst.panel.apiOK(t, "GET", "/audit?action=rollback&limit=50", nil, "")["items"].([]any); len(items) == 0 {
			t.Fatal("no rollback audit records")
		}
	})
}
