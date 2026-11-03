import { describe, it, expect } from "vitest";
import {
  parseNetwork,
  networksOverlap,
  networkWithin,
} from "../../src/clients/networks";

describe("parseNetwork", () => {
  it("promotes a bare IPv4 to /32 and normalizes", () => {
    expect(parseNetwork("10.20.0.7")).toBe("10.20.0.7/32");
    expect(parseNetwork("  192.0.2.4 ")).toBe("192.0.2.4/32");
  });
  it("accepts a masked CIDR as-is", () => {
    expect(parseNetwork("10.20.0.0/24")).toBe("10.20.0.0/24");
    expect(parseNetwork("0.0.0.0/0")).toBe("0.0.0.0/0");
  });
  it("rejects host bits set", () => {
    expect(parseNetwork("10.20.0.1/24")).toBeNull();
    expect(parseNetwork("10.20.0.5/30")).toBeNull();
    expect(parseNetwork("10.0.0.1/0")).toBeNull();
  });
  it("rejects bad prefix length and garbage", () => {
    expect(parseNetwork("10.0.0.0/33")).toBeNull();
    expect(parseNetwork("10.0.0.0/-1")).toBeNull();
    expect(parseNetwork("10.0.0.0/abc")).toBeNull();
    expect(parseNetwork("not-a-network")).toBeNull();
    expect(parseNetwork("")).toBeNull();
    expect(parseNetwork("10.0.0.0/8/9")).toBeNull();
  });
  it("rejects leading-zero octets and out-of-range octets", () => {
    expect(parseNetwork("010.0.0.0/8")).toBeNull();
    expect(parseNetwork("10.256.0.0/16")).toBeNull();
  });
  it("canonicalizes IPv6: lower-case, longest zero-run compressed", () => {
    expect(parseNetwork("2001:DB8::1")).toBe("2001:db8::1/128");
    expect(parseNetwork("2001:0db8:0000:0000:0000:0000:0000:0001")).toBe(
      "2001:db8::1/128",
    );
    expect(parseNetwork("::1")).toBe("::1/128");
    expect(parseNetwork("::")).toBe("::/128");
    expect(parseNetwork("fe80::1")).toBe("fe80::1/128");
  });
  it("expands a bare IPv6 to /128 and masks host bits", () => {
    expect(parseNetwork("2001:db8::")).toBe("2001:db8::/128");
    expect(parseNetwork("2001:db8::/120")).toBe("2001:db8::/120");
    expect(parseNetwork("2001:db8::ff/120")).toBeNull(); // host bits set
    expect(parseNetwork("2001:db8::1/129")).toBeNull();
  });
  it("compresses the first longest run only", () => {
    // 0:0:0:1:0:0:0:1 -> two runs of 3 zeros; first run wins
    expect(parseNetwork("0:0:0:1:0:0:0:1")).toBe("::1:0:0:0:1/128");
  });
});

describe("networksOverlap", () => {
  it("overlaps within IPv4", () => {
    expect(networksOverlap("10.0.0.0/8", "10.1.0.0/16")).toBe(true);
    expect(networksOverlap("10.0.0.0/24", "10.0.0.128/25")).toBe(true);
    expect(networksOverlap("10.0.0.0/24", "10.0.1.0/24")).toBe(false);
  });
  it("different families never overlap", () => {
    expect(networksOverlap("10.0.0.0/8", "::1/128")).toBe(false);
    expect(networksOverlap("0.0.0.0/0", "2001:db8::/32")).toBe(false);
  });
  it("invalid input never overlaps", () => {
    expect(networksOverlap("garbage", "10.0.0.0/8")).toBe(false);
    expect(networksOverlap("10.0.0.0/8", "10.0.0.1/24")).toBe(false); // host bits -> null
  });
  it("overlaps within IPv6", () => {
    expect(networksOverlap("2001:db8::/32", "2001:db8:1::/48")).toBe(true);
    expect(networksOverlap("2001:db8::/32", "2001:db9::/32")).toBe(false);
  });
});

describe("networkWithin", () => {
  it("contains narrower networks and equals, not wider", () => {
    expect(networkWithin("10.1.0.0/16", "10.0.0.0/8")).toBe(true);
    expect(networkWithin("10.0.0.0/8", "10.1.0.0/16")).toBe(false);
    expect(networkWithin("10.0.0.0/24", "10.0.0.0/24")).toBe(true);
  });
  it("rejects cross-family containment", () => {
    expect(networkWithin("::1/128", "0.0.0.0/0")).toBe(false);
  });
  it("manager-style: a host inside a /22 is within", () => {
    // 10.20.0.0/22 covers 10.20.0.0–10.20.3.255.
    expect(networkWithin("10.20.0.77/32", "10.20.0.0/22")).toBe(true);
    expect(networkWithin("10.20.2.5/32", "10.20.0.0/22")).toBe(true);
    expect(networkWithin("10.20.4.1/32", "10.20.0.0/22")).toBe(false);
    expect(networkWithin("10.21.0.1/32", "10.20.0.0/22")).toBe(false);
  });
});
