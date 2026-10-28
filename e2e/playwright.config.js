// Browsertests: startet wolweb selbst (server.sh) und prüft Übersicht, Weckseiten, Tastatur, Touch und
// Barrierefreiheit (axe-core). Aufruf: npm ci && npx playwright install chromium && npx playwright test
const { defineConfig } = require("@playwright/test");

const port = process.env.PORT || "8399";

module.exports = defineConfig({
  testDir: "tests",
  workers: 1, // ein Server mit einer gemeinsamen Geräteliste
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 30000,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://127.0.0.1:${port}/`,
    locale: "de-DE",
    reducedMotion: "reduce", // keine Einblend-Animationen, die Kontraste und Größen verfälschen
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: {
    command: "sh server.sh",
    url: `http://127.0.0.1:${port}/health`,
    timeout: 180000, // inklusive go build
    reuseExistingServer: false,
    env: { PORT: port },
  },
});
