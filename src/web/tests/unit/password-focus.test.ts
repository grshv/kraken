import { describe, expect, it, vi } from "vitest";
import { releasePasswordFocus } from "../../src/shell/passwordFocus";

describe("releasePasswordFocus", () => {
  it("blurs a focused password field", () => {
    const blur = vi.fn();
    releasePasswordFocus({
      activeElement: { tagName: "INPUT", type: "password", blur },
    });
    expect(blur).toHaveBeenCalledOnce();
  });

  it("leaves other focused elements alone", () => {
    for (const el of [
      { tagName: "INPUT", type: "text" },
      { tagName: "BUTTON", type: "submit" },
      { tagName: "BODY" },
    ]) {
      const blur = vi.fn();
      releasePasswordFocus({ activeElement: { ...el, blur } });
      expect(blur).not.toHaveBeenCalled();
    }
    expect(() => releasePasswordFocus({ activeElement: null })).not.toThrow();
  });
});
