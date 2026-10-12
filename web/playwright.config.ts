import { defineConfig } from "@playwright/test";

// Сквозные тесты идут против запущенных `task serve` и `task web:dev`.
// Браузер — установленный в системе Chrome (или Edge через FF_E2E_CHANNEL=msedge).
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  fullyParallel: false,
  reporter: [["list"]],
  use: {
    baseURL: process.env.FF_E2E_BASE_URL ?? "http://localhost:5173",
    channel: process.env.FF_E2E_CHANNEL ?? "chrome",
    viewport: { width: 1440, height: 900 },
    locale: "ru-RU",
    trace: "retain-on-failure",
  },
});
