import { defineConfig, devices } from '@playwright/test'

/**
 * Web E2E for the dashboard.
 *
 * The stack — disposable Postgres, the real API binary and a mocked UniFi
 * controller — is started by the Go harness in `tests/e2e` (`E2E_WEB=1`), which
 * also starts the Vite dev server and passes `PLAYWRIGHT_BASE_URL`. Keeping the
 * stack on the Go side means one set of fixtures and no second database
 * bootstrap, and it guarantees the browser path is exercised against the same
 * mocked controller as the API suite. Run it with `make e2e-web`.
 *
 * `retries` is deliberately 0: a flaky test is a failing test, so a retry would
 * only hide the defect this suite exists to find.
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: 'list',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:5173',
    trace: 'retain-on-failure',
    // Playwright's bundled Chromium needs glibc. On a host where it cannot run
    // (musl/Alpine), point the suite at the system Chromium instead of skipping
    // the tests. CI and normal Linux/macOS hosts leave this unset.
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] }
      : {},
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
