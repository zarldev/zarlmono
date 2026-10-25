import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  retries: 0,
  use: {
    baseURL: "http://127.0.0.1:4173",
    viewport: { width: 1480, height: 960 },
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
  webServer: {
    command: "npx vite build --mode test && npx vite preview --mode test --port 4173",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: false,
  },
});
