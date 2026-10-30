import { defineConfig, devices } from '@playwright/test';

const port = Number(process.env.LOOM_E2E_PORT ?? 5700);

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  timeout: 30_000,
  expect: { timeout: 8_000 },
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  webServer: {
    command: `go run ./cmd/loom serve -addr 127.0.0.1:${port} -data .e2e-data -sim`,
    cwd: '..',
    url: `http://127.0.0.1:${port}/healthz`,
    reuseExistingServer: process.env.LOOM_E2E_REUSE === '1',
    timeout: 120_000,
    stdout: 'ignore',
    stderr: 'pipe',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
      testIgnore: /mobile\.spec\.ts/,
    },
    {
      name: 'mobile-chromium',
      use: { ...devices['Pixel 5'] },
      testMatch: /mobile\.spec\.ts/,
    },
  ],
});
