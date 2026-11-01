// Pure domain-input parsing and destination-list merge. No DOM, no fetch.
//
// parseDomainInput accepts a pasted URL or host, strips scheme/path/port and a
// trailing dot, lower-cases, and recognises "*.x" / ".x" as include_subdomains.
// IP literals, whitespace, underscores, empty labels and other invalid hosts
// return null. The surviving label validation mirrors the Go domainPattern so
// anything accepted here is also accepted by policy.Validate.

export type DomainEntry = { domain: string; include_subdomains: boolean };

function isIPLiteral(s: string): boolean {
  if (s.includes(":")) return true; // IPv6 (pure hex form)
  const parts = s.split(".");
  return (
    parts.length === 4 &&
    parts.every(
      (p) => /^\d+$/.test(p) && Number(p) >= 0 && Number(p) <= 255,
    )
  );
}

const LABEL = "[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?";
const DOMAIN_RE = new RegExp(`^${LABEL}(?:\\.${LABEL})+$`);

export function parseDomainInput(
  input: string,
): DomainEntry | null {
  let s = input.trim().toLowerCase();
  if (s === "") return null;
  s = s.replace(/^[a-z][a-z0-9+.-]*:\/\/+/, ""); // scheme
  const cut = s.search(/[/?#]/);
  if (cut !== -1) s = s.slice(0, cut); // path / query / fragment
  const at = s.lastIndexOf("@");
  if (at !== -1) s = s.slice(at + 1); // userinfo
  if (s.startsWith("[")) return null; // bracketed IPv6 literal
  s = s.replace(/:([0-9]{1,5})$/, ""); // trailing port
  if (s.endsWith(".")) s = s.slice(0, -1); // trailing dot
  if (s === "" || /[\s_]/.test(s) || s.includes("..")) return null;
  let includeSubdomains = false;
  if (s.startsWith("*.")) {
    includeSubdomains = true;
    s = s.slice(2);
  } else if (s.startsWith(".")) {
    includeSubdomains = true;
    s = s.slice(1);
  }
  if (s === "" || isIPLiteral(s)) return null;
  if (!DOMAIN_RE.test(s)) return null;
  return { domain: s, include_subdomains: includeSubdomains };
}

// mergeDestinations returns a new array deduplicated by domain, with
// include_subdomains OR'd across duplicates. Original order is preserved and
// new entries from `add` are appended. The inputs are not mutated.
export function mergeDestinations(
  list: DomainEntry[],
  add: DomainEntry[],
): DomainEntry[] {
  const incl = new Map<string, boolean>();
  for (const d of list) incl.set(d.domain, d.include_subdomains);
  for (const d of add) {
    incl.set(d.domain, (incl.get(d.domain) ?? false) || d.include_subdomains);
  }
  const out: DomainEntry[] = [];
  const seen = new Set<string>();
  for (const d of list) {
    if (!seen.has(d.domain)) {
      out.push({ domain: d.domain, include_subdomains: !!incl.get(d.domain) });
      seen.add(d.domain);
    }
  }
  for (const d of add) {
    if (!seen.has(d.domain)) {
      out.push({ domain: d.domain, include_subdomains: !!incl.get(d.domain) });
      seen.add(d.domain);
    }
  }
  return out;
}
