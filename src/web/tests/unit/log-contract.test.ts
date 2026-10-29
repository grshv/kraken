import { describe, expect, it } from "vitest";
import { parseLogQuery } from "../../src/log/query";

// The log contract test covers the search-box parser, which is the only piece
// of log/query logic that is part of the UI contract. Facet counting and the
// histogram moved to the server (/events/facets) and are no longer unit-tested
// here; their Go-side contract is covered by telemetry and httpapi tests.

describe("parseLogQuery", () => {
  it("extracts known tokens and keeps free text", () => {
    expect(
      parseLogQuery(
        "client:ci-01 host:npmjs.org результат:отказ method:CONNECT ip:10.1.0.5 foo bar",
      ),
    ).toEqual({
      client: "ci-01",
      host: "npmjs.org",
      result: "denied",
      method: "CONNECT",
      source: "10.1.0.5",
      free: ["foo", "bar"],
    });
  });
  it("accepts english and russian result words, ignores empty values", () => {
    expect(parseLogQuery("result:allowed").result).toBe("allowed");
    expect(parseLogQuery("результат:разрешено").result).toBe("allowed");
    expect(parseLogQuery("result:denied").result).toBe("denied");
    expect(parseLogQuery("client:  ").client).toBeUndefined();
    expect(parseLogQuery("").free).toEqual([]);
  });
});
