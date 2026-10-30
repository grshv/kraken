import { describe, expect, it } from "vitest";
import {
  formatDuration,
  parseDuration,
  parsePortList,
} from "../../src/settings/units";
import { canDemote, canDisable } from "../../src/accounts/rules";

describe("parsePortList", () => {
  it("parses, dedupes and keeps order", () => {
    expect(parsePortList("80, 443")).toEqual([80, 443]);
    expect(parsePortList(" 443 ,80,443 ")).toEqual([443, 80]);
    expect(parsePortList("8080")).toEqual([8080]);
  });
  it("rejects empty, out of range and garbage", () => {
    for (const v of ["", " , ", "0", "65536", "80,abc", "-1", "80.5"])
      expect(parsePortList(v), v).toBeNull();
  });
});

describe("durations", () => {
  it("parses seconds, minutes and hours in russian units", () => {
    expect(parseDuration("60 с")).toBe(60);
    expect(parseDuration("15 мин")).toBe(900);
    expect(parseDuration("6 ч")).toBe(21600);
    expect(parseDuration("45")).toBe(45);
  });
  it("rejects garbage and negatives", () => {
    for (const v of ["", "abc", "-5 с", "5 лет", "1.5 ч"])
      expect(parseDuration(v), v).toBeNull();
  });
  it("formats with the largest whole unit", () => {
    expect(formatDuration(900)).toBe("15 мин");
    expect(formatDuration(21600)).toBe("6 ч");
    expect(formatDuration(90)).toBe("90 с");
    expect(formatDuration(0)).toBe("0 с");
  });
  it("round-trips", () => {
    for (const s of [1, 59, 60, 61, 3600, 5400, 86400])
      expect(parseDuration(formatDuration(s))).toBe(s);
  });
});

describe("last admin protection", () => {
  const users = [
    { username: "root", role: "admin" as const, enabled: true },
    { username: "off", role: "admin" as const, enabled: false },
    { username: "mgr", role: "manager" as const, enabled: true },
  ];
  it("the only enabled admin cannot be demoted or disabled", () => {
    expect(canDemote(users, "root")).toBe(false);
    expect(canDisable(users, "root")).toBe(false);
  });
  it("managers and disabled admins are not protected", () => {
    expect(canDisable(users, "mgr")).toBe(true);
    expect(canDemote(users, "off")).toBe(true);
  });
  it("a second enabled admin lifts the protection", () => {
    const two = [
      ...users,
      { username: "ops", role: "admin" as const, enabled: true },
    ];
    expect(canDemote(two, "root")).toBe(true);
    expect(canDisable(two, "root")).toBe(true);
  });
});
