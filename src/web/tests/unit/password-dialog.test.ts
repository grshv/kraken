import { describe, expect, it } from "vitest";
import { passwordProblem } from "../../src/shell/PasswordDialog";

describe("passwordProblem", () => {
  it("mirrors the server rules for a new own password", () => {
    expect(passwordProblem("old-password-123", "short", "")).toBe(
      "Не короче 12 символов",
    );
    expect(passwordProblem("old-password-123", "old-password-123", "")).toBe(
      "Совпадает с текущим",
    );
    expect(passwordProblem("old", "new-password-123", "new-password-12")).toBe(
      "Пароли не совпадают",
    );
    expect(passwordProblem("old", "new-password-123", "new-password-123")).toBe(
      "",
    );
    // 12 bytes, not 12 characters: six Cyrillic letters are enough.
    expect(passwordProblem("old", "пароль", "")).toBe("");
    expect(passwordProblem("old", "x".repeat(1025), "")).toBe(
      "Слишком длинный пароль",
    );
  });
});
