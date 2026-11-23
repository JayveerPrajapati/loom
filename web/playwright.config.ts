import { mkdtempSync } from 'node:fs';
import { randomBytes } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { defineConfig, devices } from '@playwright/test';

// The suite runs in two passes. The main pass covers everything except
// tests tagged `@isolated` — tests that intentionally require an empty
// workspace and would otherwise wipe shared state. The isolated pass
// (LOOM_E2E_ISOLATED=1) runs only those tags against its own server with a
// fresh data directory and a distinct port, so they never see or destroy
// another test's resources. Run both via `npm run e2e:only`.
// E2E server controls (P2-9). Every knob defaults to the deterministic
// values the timing tests rely on, so a plain `playwright test` needs no
// environment at all:
//
//   LOOM_E2E_PORT          server port (default 5700; isolated pass 5701)
//   LOOM_E2E_DATA          data directory (default: throwaway mkdtemp)
//   LOOM_E2E_WAIT_SWEEP     --wait-sweep-interval (default 1s; 100ms floor
//                          is enforced by the server). Durable-wait resumes
//                          are exact-deadline scheduled; the sweep is only
//                          the fallback, kept short so sweep-phase latency
//                          cannot mask a scheduler regression.
//   LOOM_E2E_RETENTION_DAYS --retention-days (default 0 = off: nothing is
//                          pruned mid-suite)
//   LOOM_E2E_SUBPROCESS=1   pass --enable-subprocess-nodes for
//                          publish-time env building (the -sim host still
//                          cannot execute python, so healthz keeps
//                          reporting subprocess disabled and the gate
//                          tests run unchanged)
//   LOOM_E2E_REUSE=1        reuse an already-running server (debugging)
//
// The cron scheduler itself has no fixed scan interval to configure: jobs
// fire at their own cron schedules and reload on job mutations.
const isolated = process.env.LOOM_E2E_ISOLATED === '1';
const port = Number(process.env.LOOM_E2E_PORT ?? (isolated ? 5701 : 5700));
const dataDir = process.env.LOOM_E2E_DATA ?? mkdtempSync(join(tmpdir(), 'loom-e2e-'));
const waitSweep = process.env.LOOM_E2E_WAIT_SWEEP ?? '1s';
const retentionDays = process.env.LOOM_E2E_RETENTION_DAYS ?? '0';
const bootUser = process.env.LOOM_E2E_USER ?? `admin-${randomBytes(4).toString('hex')}@e2e.local`;
const bootCode = process.env.LOOM_E2E_CODE ?? randomBytes(18).toString('base64url');
const csrf = process.env.LOOM_E2E_CSRF ?? randomBytes(16).toString('hex');

const serverArgs = [
  'go run ./cmd/loom serve',
  `-addr 127.0.0.1:${port}`,
  `-data ${dataDir}`,
  '-sim',
  `--wait-sweep-interval ${waitSweep}`,
  `--retention-days ${retentionDays}`,
];
if (process.env.LOOM_E2E_SUBPROCESS === '1') serverArgs.push('--enable-subprocess-nodes');
const serverCommand = serverArgs.join(' ');

process.env.LOOM_E2E_USER = bootUser;
process.env.LOOM_E2E_CODE = bootCode;
process.env.LOOM_E2E_CSRF = csrf;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  // Deterministic timing tests (see concurrency.spec.ts) opt out of retries
  // per-file; isolated tests never retry either.
  ...(isolated ? { retries: 0 } : {}),
  timeout: 30_000,
  expect: { timeout: 8_000 },
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  grep: isolated ? /@isolated/ : undefined,
  grepInvert: isolated ? undefined : /@isolated/,
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    extraHTTPHeaders: { 'X-CSRF-Token': csrf },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  webServer: {
    command: serverCommand,
    cwd: '..',
    url: `http://127.0.0.1:${port}/healthz`,
    env: {
      LOOM_BOOTSTRAP_ADMIN_EMAIL: bootUser,
      LOOM_BOOTSTRAP_ADMIN_PASSWORD: bootCode,
      LOOM_E2E_CSRF: csrf,
    },
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
