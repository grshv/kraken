import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  timeout: 30000,
  workers: 1,
  use: {
    baseURL: process.env.SC_TEST_URL || "https://localhost:18443",
    ignoreHTTPSErrors: true,
    launchOptions: {
      executablePath: process.env.SC_CHROMIUM_PATH || undefined,
    },
  },
  outputDir: "/tmp/squid-control-browser-results",
  reporter: "list",
});
