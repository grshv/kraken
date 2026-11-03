import { describe, expect, it } from "vitest";
import {
  networksOverlap,
  networkWithin,
  parseNetwork,
} from "../../src/clients/networks";
import {
  mergeDestinations,
  parseDomainInput,
} from "../../src/policies/domains";
import { entityMarks } from "../../src/draft/marks";

describe("parseNetwork", () => {
  it("normalizes single addresses and prefixes", () => {
    expect(parseNetwork(" 10.0.0.5 ")).toBe("10.0.0.5/32");
    expect(parseNetwork("10.0.0.0/8")).toBe("10.0.0.0/8");
    expect(parseNetwork("2001:db8::1")).toBe("2001:db8::1/128");
    expect(parseNetwork("2001:db8::/32")).toBe("2001:db8::/32");
  });
  it("rejects garbage, host bits and bad lengths", () => {
    for (const v of [
      "",
      "10.0.0",
      "10.0.0.256",
      "10.1.2.3/8",
      "10.0.0.0/33",
      "2001:db8::/129",
      "host.example",
      "10.0.0.0/-1",
    ]) {
      expect(parseNetwork(v), v).toBeNull();
    }
  });
});

describe("network relations", () => {
  it("overlap is symmetric and family-aware", () => {
    expect(networksOverlap("10.0.0.0/8", "10.1.0.5/32")).toBe(true);
    expect(networksOverlap("10.1.0.5/32", "10.0.0.0/8")).toBe(true);
    expect(networksOverlap("10.0.0.0/16", "10.1.0.0/16")).toBe(false);
    expect(networksOverlap("10.0.0.0/8", "2001:db8::/32")).toBe(false);
    expect(networksOverlap("2001:db8::/32", "2001:db8:1::/48")).toBe(true);
  });
  it("within requires the inner prefix to be inside the outer one", () => {
    expect(networkWithin("10.1.0.5/32", "10.1.0.0/16")).toBe(true);
    expect(networkWithin("10.1.0.0/16", "10.1.0.0/16")).toBe(true);
    expect(networkWithin("10.0.0.0/8", "10.0.0.0/16")).toBe(false);
    expect(networkWithin("10.2.0.5/32", "10.1.0.0/16")).toBe(false);
  });
});

describe("parseDomainInput", () => {
  it("strips scheme, path, port and case", () => {
    expect(parseDomainInput("https://PyPI.org/simple/x")).toEqual({
      domain: "pypi.org",
      include_subdomains: false,
    });
    expect(parseDomainInput("files.pythonhosted.org:443")).toEqual({
      domain: "files.pythonhosted.org",
      include_subdomains: false,
    });
    expect(parseDomainInput("example.com.")).toEqual({
      domain: "example.com",
      include_subdomains: false,
    });
  });
  it("wildcard prefixes mean subdomains", () => {
    expect(parseDomainInput("*.github.com")).toEqual({
      domain: "github.com",
      include_subdomains: true,
    });
    expect(parseDomainInput(".github.com")).toEqual({
      domain: "github.com",
      include_subdomains: true,
    });
  });
  it("rejects IPs and invalid names", () => {
    for (const v of [
      "",
      "10.0.0.1",
      "[2001:db8::1]",
      "bad domain.com",
      "a..b",
      "*.",
      "exa_mple.com",
    ]) {
      expect(parseDomainInput(v), v).toBeNull();
    }
  });
});

describe("mergeDestinations", () => {
  it("dedupes by domain and widens subdomains", () => {
    const list = [{ domain: "a.com", include_subdomains: false }];
    expect(
      mergeDestinations(list, [{ domain: "a.com", include_subdomains: true }]),
    ).toEqual([{ domain: "a.com", include_subdomains: true }]);
    expect(
      mergeDestinations(list, [{ domain: "b.com", include_subdomains: false }]),
    ).toHaveLength(2);
    expect(list).toEqual([{ domain: "a.com", include_subdomains: false }]);
  });
});

describe("entityMarks", () => {
  it("marks new, changed and deleted, skips unchanged", () => {
    const active = [
      { id: "a", n: 1 },
      { id: "b", n: 1 },
      { id: "c", n: 1 },
    ];
    const draft = [
      { id: "a", n: 1 },
      { id: "b", n: 2 },
      { id: "d", n: 1 },
    ];
    expect(entityMarks(active, draft)).toEqual({
      b: "changed",
      c: "deleted",
      d: "new",
    });
  });
  it("ignores key order inside objects", () => {
    expect(
      entityMarks([{ id: "a", x: 1, y: 2 }], [{ y: 2, id: "a", x: 1 }]),
    ).toEqual({});
  });
});
