import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  timeout: 60000,
  workers: 1,
  use: {
    baseURL: process.env.KRAKEN_TEST_URL || "https://localhost:18443",
    ignoreHTTPSErrors: true,
    launchOptions: {
      executablePath: process.env.KRAKEN_CHROMIUM_PATH || undefined,
    },
  },
  outputDir: "/tmp/kraken-browser-results",
  reporter: "list",
});
