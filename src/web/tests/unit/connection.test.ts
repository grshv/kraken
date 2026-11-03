import { describe, it, expect } from "vitest";
import { tlsUnavailable } from "../../src/clients/connection";

describe("tlsUnavailable", () => {
  it("closes TLS without the proxy certificate, before checking logins", () => {
    expect(tlsUnavailable(false, 3)).toMatch(/Сертификат прокси не установлен/);
    expect(tlsUnavailable(false, 0)).toMatch(/Сертификат прокси не установлен/);
  });
  it("needs a proxy login", () => {
    expect(tlsUnavailable(true, 0)).toMatch(/создайте логин прокси/);
    expect(tlsUnavailable(null, 0)).toMatch(/создайте логин прокси/);
  });
  it("is open with a certificate (or while unknown) and a login", () => {
    expect(tlsUnavailable(true, 1)).toBe("");
    expect(tlsUnavailable(null, 1)).toBe("");
  });
});
