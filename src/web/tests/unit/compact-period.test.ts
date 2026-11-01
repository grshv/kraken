import { describe, expect, it } from "vitest";
import { compactPeriod } from "../../src/model";

describe("compact journal period", () => {
  it("keeps one date inside a day", () => {
    expect(
      compactPeriod("2026-10-30T13:00:00+03:00", "2026-10-30T14:05:00+03:00"),
    ).toBe("30 окт., 13:00–14:05");
  });
  it("shows both dates across days in Moscow time", () => {
    expect(compactPeriod("2026-10-29T11:05:00Z", "2026-10-30T11:05:00Z")).toBe(
      "29 окт., 14:05 — 30 окт., 14:05",
    );
  });
  it("adds years only across a year boundary", () => {
    expect(
      compactPeriod("2026-12-31T23:00:00+03:00", "2027-01-01T01:00:00+03:00"),
    ).toBe("31 дек. 2026, 23:00 — 1 янв. 2027, 01:00");
  });
  it("returns a dash for an invalid time", () => {
    expect(compactPeriod("garbage", "2026-10-30T14:05:00Z")).toBe("—");
  });
});
