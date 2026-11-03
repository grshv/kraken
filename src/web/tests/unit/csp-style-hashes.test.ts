import { describe, expect, it } from "vitest";
// @ts-expect-error plain ESM build script without type declarations
import { hash, inlineStyles } from "../../scripts/csp-style-hashes.mjs";

// The build writes the CSP hashes of the components' inline styles; it must
// still find each of them in the installed packages.
describe("CSP style hashes from the build", () => {
  it("finds every inline style the standard components insert", async () => {
    const styles: Record<string, string> = await inlineStyles();
    expect(Object.keys(styles).sort()).toEqual([
      "chart-summary",
      "chart-traffic",
      "scroll-area",
      "sonner",
      "sonner-empty",
    ]);
    expect(styles.sonner).toContain("[data-sonner-toaster]");
    expect(styles["scroll-area"]).toContain(
      "[data-radix-scroll-area-viewport]",
    );
    expect(styles["chart-traffic"]).toContain("[data-chart=chart-traffic]");
    expect(styles["chart-summary"]).toContain("--color-denied: var(--s-deny);");
  });
  it("hashes as CSP sha256 sources", () => {
    expect(hash("")).toBe(
      "sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
    );
  });
});
