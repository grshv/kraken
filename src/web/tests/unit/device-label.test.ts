import { describe, expect, it } from "vitest";
import { deviceLabel } from "../../src/accounts/device";

describe("deviceLabel", () => {
  it("names browser and OS from common agents", () => {
    expect(
      deviceLabel(
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36",
      ),
    ).toBe("Chrome · macOS");
    expect(
      deviceLabel(
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/141.0 Safari/537.36 Edg/141.0",
      ),
    ).toBe("Edge · Windows");
    expect(
      deviceLabel(
        "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0",
      ),
    ).toBe("Firefox · Linux");
    expect(
      deviceLabel(
        "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1",
      ),
    ).toBe("Safari · iOS");
  });
  it("never echoes unknown or empty agents", () => {
    expect(deviceLabel("<script>x</script>")).toBe("Другой клиент");
    expect(deviceLabel(null)).toBe("Неизвестное устройство");
    expect(deviceLabel("")).toBe("Неизвестное устройство");
  });
});
