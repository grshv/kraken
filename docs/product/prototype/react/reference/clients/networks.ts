// Pure source-network parsing and overlap helpers. No DOM, no fetch.
//
// Mirrors the Go policy.Validate source-network rules: a bare address becomes a
// /32 (IPv4) or /128 (IPv6) host route; a CIDR with host bits set, an
// out-of-range prefix length, or anything that is not a valid IPv4/IPv6 prefix
// is rejected. IPv6 output is canonical (RFC 5952): lower-case hex, longest run
// of zero groups (>= 2) compressed to "::".

export function parseIPv4(s: string): number | null {
  const parts = s.split(".");
  if (parts.length !== 4) return null;
  let n = 0;
  for (const p of parts) {
    if (!/^\d+$/.test(p)) return null;
    if (p.length > 1 && p[0] === "0") return null; // no octal / leading zeros
    const v = Number(p);
    if (!Number.isInteger(v) || v < 0 || v > 255) return null;
    n = n * 256 + v;
  }
  return n >>> 0;
}

export function fmtIPv4(n: number): string {
  return [(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join(
    ".",
  );
}

// parseIPv6 returns 8 16-bit groups or null. Pure hex form only; IPv4-in-IPv6
// dotted tails are rejected (matching Go's source-network Is4In6 rejection).
export function parseIPv6(s: string): number[] | null {
  if (s === "") return null;
  const dbl = s.indexOf("::");
  if (dbl !== -1 && s.indexOf("::", dbl + 1) !== -1) return null; // only one "::"
  let head: string[];
  let tail: string[];
  if (dbl === -1) {
    head = s.split(":");
    tail = [];
  } else {
    const before = s.slice(0, dbl);
    const after = s.slice(dbl + 2);
    head = before === "" ? [] : before.split(":");
    tail = after === "" ? [] : after.split(":");
  }
  const groups: number[] = [];
  for (const g of [...head, ...tail]) {
    if (!/^[0-9a-fA-F]{1,4}$/.test(g)) return null;
    groups.push(parseInt(g, 16));
  }
  if (dbl !== -1) {
    if (groups.length > 8) return null;
    while (groups.length < 8) groups.splice(head.length, 0, 0);
  } else if (groups.length !== 8) {
    return null;
  }
  return groups;
}

// fmtIPv6 renders 8 groups in canonical compressed form.
export function fmtIPv6(groups: number[]): string {
  let bestStart = -1;
  let bestLen = 0;
  let curStart = -1;
  let curLen = 0;
  for (let i = 0; i < groups.length; i++) {
    if (groups[i] === 0) {
      if (curStart === -1) curStart = i;
      curLen++;
      if (curLen > bestLen) {
        bestLen = curLen;
        bestStart = curStart;
      }
    } else {
      curStart = -1;
      curLen = 0;
    }
  }
  if (bestLen < 2) {
    return groups.map((g) => g.toString(16)).join(":");
  }
  const left = groups
    .slice(0, bestStart)
    .map((g) => g.toString(16))
    .join(":");
  const right = groups
    .slice(bestStart + bestLen)
    .map((g) => g.toString(16))
    .join(":");
  return left + "::" + right;
}

function maskIPv4(n: number, bits: number): number | null {
  const mask = bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0;
  if ((n & (~mask >>> 0)) !== 0) return null; // host bits set
  return (n & mask) >>> 0;
}

function maskIPv6(groups: number[], bits: number): number[] | null {
  const out = groups.slice();
  for (let k = 0; k < 8; k++) {
    const within = Math.max(0, Math.min(16, bits - 16 * k));
    const mask = within === 0 ? 0 : (0xffff << (16 - within)) & 0xffff;
    if ((groups[k] & (~mask & 0xffff)) !== 0) return null; // host bits set
    out[k] = groups[k] & mask;
  }
  return out;
}

// parseNetwork trims and accepts a bare address or a CIDR; returns the
// canonical "addr/prefix" or null for garbage, host bits, or bad length.
export function parseNetwork(input: string): string | null {
  const s = input.trim();
  if (s === "") return null;
  let addr = s;
  let bits: number | null = null;
  const slash = s.indexOf("/");
  if (slash !== -1) {
    addr = s.slice(0, slash);
    const b = s.slice(slash + 1);
    if (!/^\d+$/.test(b)) return null;
    bits = Number(b);
  }
  const v4 = parseIPv4(addr);
  if (v4 !== null) {
    const b = bits === null ? 32 : bits;
    if (!Number.isInteger(b) || b < 0 || b > 32) return null;
    const masked = maskIPv4(v4, b);
    if (masked === null) return null;
    return fmtIPv4(masked) + "/" + b;
  }
  const v6 = parseIPv6(addr);
  if (v6 !== null) {
    const b = bits === null ? 128 : bits;
    if (!Number.isInteger(b) || b < 0 || b > 128) return null;
    const masked = maskIPv6(v6, b);
    if (masked === null) return null;
    return fmtIPv6(masked) + "/" + b;
  }
  return null;
}

// networksOverlap reports whether two CIDRs share any address. Different
// families never overlap. Invalid input never overlaps.
export function networksOverlap(a: string, b: string): boolean {
  const pa = parseNetwork(a);
  const pb = parseNetwork(b);
  if (!pa || !pb) return false;
  const [aa, ab] = pa.split("/");
  const [ba, bb] = pb.split("/");
  const la = Number(ab);
  const lb = Number(bb);
  const ga = parseIPv4(aa);
  const gb = parseIPv4(ba);
  if (ga !== null && gb !== null) {
    const m = Math.min(la, lb);
    const mask = m === 0 ? 0 : (0xffffffff << (32 - m)) >>> 0;
    return (ga & mask) === (gb & mask);
  }
  const g6a = parseIPv6(aa);
  const g6b = parseIPv6(ba);
  if (g6a !== null && g6b !== null) {
    const m = Math.min(la, lb);
    for (let k = 0; k < 8; k++) {
      const within = Math.max(0, Math.min(16, m - 16 * k));
      if (within === 0) break;
      const mask = within === 16 ? 0xffff : (0xffff << (16 - within)) & 0xffff;
      if ((g6a[k] & mask) !== (g6b[k] & mask)) return false;
    }
    return true;
  }
  return false;
}

// networkWithin reports whether inner is fully contained in outer. A network is
// within itself. Different families and invalid input return false.
export function networkWithin(inner: string, outer: string): boolean {
  const pi = parseNetwork(inner);
  const po = parseNetwork(outer);
  if (!pi || !po) return false;
  const [ia, ib] = pi.split("/");
  const [oa, ob] = po.split("/");
  const li = Number(ib);
  const lo = Number(ob);
  if (lo > li) return false;
  const gia = parseIPv4(ia);
  const goa = parseIPv4(oa);
  if (gia !== null && goa !== null) {
    const mask = lo === 0 ? 0 : (0xffffffff << (32 - lo)) >>> 0;
    return (gia & mask) === (goa & mask);
  }
  const g6i = parseIPv6(ia);
  const g6o = parseIPv6(oa);
  if (g6i !== null && g6o !== null) {
    for (let k = 0; k < 8; k++) {
      const within = Math.max(0, Math.min(16, lo - 16 * k));
      if (within === 0) break;
      const mask = within === 16 ? 0xffff : (0xffff << (16 - within)) & 0xffff;
      if ((g6i[k] & mask) !== (g6o[k] & mask)) return false;
    }
    return true;
  }
  return false;
}
