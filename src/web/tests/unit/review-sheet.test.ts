import { describe, expect, it } from "vitest";
import { changeKind } from "../../src/draft/ChangeList";
import { diffHunks } from "../../src/draft/ReviewSheet";

describe("changeKind", () => {
  it("reads the marker from semanticDiff lines", () => {
    expect(changeKind("Добавлен клиент: build-02")).toBe("add");
    expect(changeKind("Удалён клиент: old")).toBe("del");
    expect(changeKind("Политика «web» удалена")).toBe("del");
    expect(changeKind("Изменён клиент: ci-01")).toBe("mod");
    expect(changeKind("Изменены настройки Squid")).toBe("mod");
  });
});

describe("diffHunks", () => {
  const c = (t: string) => ({ kind: "context", text: t });
  it("keeps changed lines with two lines of context and marks gaps", () => {
    const lines = [
      ...["a", "b", "c", "d", "e"].map(c),
      { kind: "added", text: "+1" },
      ...["f", "g", "h", "i", "j", "k"].map(c),
      { kind: "removed", text: "-1" },
      c("l"),
    ];
    expect(diffHunks(lines).map((l) => l?.text ?? null)).toEqual([
      "d",
      "e",
      "+1",
      "f",
      "g",
      null,
      "j",
      "k",
      "-1",
      "l",
    ]);
  });
  it("returns nothing when only context lines remain", () => {
    expect(diffHunks([c("a"), c("b")])).toEqual([]);
  });
});

describe("detailLines", () => {
  it("splits an audit summary into change lines", async () => {
    const { detailLines } = await import("../../src/audit/AuditSection");
    expect(detailLines("Добавлен клиент: a; Изменены настройки Squid")).toEqual(
      ["Добавлен клиент: a", "Изменены настройки Squid"],
    );
    expect(detailLines("")).toEqual([]);
  });
});
