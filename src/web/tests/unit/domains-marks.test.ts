import { describe, it, expect } from "vitest";
import {
  parseDomainInput,
  mergeDestinations,
} from "../../src/policies/domains";
import { entityMarks, deepEqual } from "../../src/draft/marks";

describe("parseDomainInput", () => {
  it("strips scheme, path, port and trailing dot", () => {
    expect(parseDomainInput("https://pypi.org/path?q=1")).toEqual({
      domain: "pypi.org",
      include_subdomains: false,
    });
    expect(parseDomainInput("pypi.org:443")).toEqual({
      domain: "pypi.org",
      include_subdomains: false,
    });
    expect(parseDomainInput("pypi.org.")).toEqual({
      domain: "pypi.org",
      include_subdomains: false,
    });
    expect(parseDomainInput("HTTPS://Files.PythonHosted.Org/")).toEqual({
      domain: "files.pythonhosted.org",
      include_subdomains: false,
    });
  });
  it("recognises *.x and .x as include_subdomains", () => {
    expect(parseDomainInput("*.pypi.org")).toEqual({
      domain: "pypi.org",
      include_subdomains: true,
    });
    expect(parseDomainInput(".pypi.org")).toEqual({
      domain: "pypi.org",
      include_subdomains: true,
    });
    expect(parseDomainInput("https://*.cdn.example.com:8443/x")).toEqual({
      domain: "cdn.example.com",
      include_subdomains: true,
    });
  });
  it("rejects IP literals, spaces, underscores, empty labels", () => {
    expect(parseDomainInput("1.2.3.4")).toBeNull();
    expect(parseDomainInput("::1")).toBeNull();
    expect(parseDomainInput("[::1]:443")).toBeNull();
    expect(parseDomainInput("pypi org")).toBeNull();
    expect(parseDomainInput("pypi_org.com")).toBeNull();
    expect(parseDomainInput("pypi..org")).toBeNull();
    expect(parseDomainInput("")).toBeNull();
    expect(parseDomainInput("   ")).toBeNull();
    expect(parseDomainInput("localhost")).toBeNull(); // single-label
  });
  it("strips userinfo", () => {
    expect(parseDomainInput("https://user:pass@pypi.org/p")).toEqual({
      domain: "pypi.org",
      include_subdomains: false,
    });
  });
});

describe("mergeDestinations", () => {
  it("dedups by domain and ORs include_subdomains, preserves order", () => {
    const list = [
      { domain: "a.example", include_subdomains: false },
      { domain: "b.example", include_subdomains: true },
    ];
    const out = mergeDestinations(list, [
      { domain: "a.example", include_subdomains: true },
      { domain: "c.example", include_subdomains: false },
    ]);
    expect(out).toEqual([
      { domain: "a.example", include_subdomains: true },
      { domain: "b.example", include_subdomains: true },
      { domain: "c.example", include_subdomains: false },
    ]);
  });
  it("does not mutate inputs", () => {
    const list = [{ domain: "a.example", include_subdomains: false }];
    const add = [{ domain: "a.example", include_subdomains: true }];
    mergeDestinations(list, add);
    expect(list[0].include_subdomains).toBe(false);
    expect(add[0].include_subdomains).toBe(true);
  });
});

describe("entityMarks and deepEqual", () => {
  type Item = { id: string; v: number; tag?: string };
  it("deepEqual ignores object key order, respects array order", () => {
    expect(deepEqual({ a: 1, b: [1, 2] }, { b: [1, 2], a: 1 })).toBe(true);
    expect(deepEqual({ a: [1, 2] }, { a: [2, 1] })).toBe(false);
    expect(deepEqual({ a: 1 }, { a: 1, b: 2 })).toBe(false);
  });
  it("marks new, changed, deleted and omits unchanged", () => {
    const active: Item[] = [
      { id: "a", v: 1 },
      { id: "b", v: 2 },
      { id: "c", v: 3 },
    ];
    const draft: Item[] = [
      { id: "a", v: 1 }, // unchanged -> omitted
      { id: "b", v: 9 }, // changed
      { id: "d", v: 4 }, // new
    ];
    expect(entityMarks(active, draft)).toEqual({
      b: "changed",
      d: "new",
      c: "deleted",
    });
  });
  it("treats same data with different key order as unchanged", () => {
    const active = [{ id: "a", v: 1, tag: "x" }];
    const draft = [{ id: "a", tag: "x", v: 1 }];
    expect(entityMarks(active, draft)).toEqual({});
  });
});
