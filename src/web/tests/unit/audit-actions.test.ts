import { describe, expect, it } from "vitest";
import {
  AUDIT_ACTIONS,
  AUDIT_FILTERS,
  actionCategory,
  actionLabel,
} from "../../src/audit/actions";

describe("audit action labels", () => {
  it("every action emitted by the Go server has a Russian label", () => {
    for (const a of AUDIT_ACTIONS) {
      const label = actionLabel(a);
      expect(label, `action ${a}`).toBeTruthy();
      // cyrillic presence: a real translation, not the raw code echoed back
      expect(/[А-Яа-я]/.test(label), `action ${a} label is Russian`).toBe(true);
    }
  });
  it("unknown action falls back to the raw code", () => {
    expect(actionLabel("unknown_action_xyz")).toBe("unknown_action_xyz");
  });
});

describe("audit categories", () => {
  it("create_revision is in the apply group", () => {
    expect(actionCategory("create_revision")).toBe("apply");
  });
  it("login family is in the login group", () => {
    for (const a of ["login", "login_failed", "logout", "csrf_failed", "rate_limited", "session_capacity_reached"]) {
      expect(actionCategory(a), a).toBe("login");
    }
  });
  it("account actions map to the account group", () => {
    for (const a of ["create_user", "update_user", "update_scope", "create_proxy_account", "update_proxy_account", "reset_proxy_password", "password_reset"]) {
      expect(actionCategory(a), a).toBe("account");
    }
  });
  it("draft actions map to the draft group", () => {
    expect(actionCategory("update_draft")).toBe("draft");
    expect(actionCategory("discard_draft")).toBe("draft");
  });
  it("unknown action is other so it still shows under All", () => {
    expect(actionCategory("unknown_action_xyz")).toBe("other");
  });
  it("audit filter list exposes exactly the four UI groups", () => {
    expect(AUDIT_FILTERS.map((f) => f.id)).toEqual([
      "all",
      "apply",
      "login",
      "account",
    ]);
    expect(AUDIT_FILTERS.every((f) => /[А-Яа-я]/.test(f.label))).toBe(true);
  });
});

describe("audit actions list is exhaustive against the Go source", () => {
  // Cross-check the strings written via Store.Audit / AuditTx in src/internal:
  // auth.go, users.go, store/users.go, store/proxy_accounts.go and
  // httpapi/server.go (ChangeDraft/DiscardDraft/CreateRevision).
  it("contains the known action strings", () => {
    const expected = [
      "csrf_failed",
      "rate_limited",
      "login_failed",
      "login",
      "logout",
      "session_capacity_reached",
      "password_reset",
      "create_user",
      "update_user",
      "update_scope",
      "create_proxy_account",
      "update_proxy_account",
      "reset_proxy_password",
      "update_draft",
      "discard_draft",
      "create_revision",
    ];
    for (const a of expected) {
      expect(AUDIT_ACTIONS, `missing ${a}`).toContain(a);
    }
  });
});
