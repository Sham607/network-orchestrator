import { expect, test, type Page } from '@playwright/test'

/**
 * The user-visible vertical slice: the browser reads the cache through our own
 * API, never through the controller.
 *
 * The fixtures come from the Go harness (`tests/e2e`), which syncs a mocked
 * UniFi console into Postgres before this suite runs: one site, `E2E HQ`, and
 * three devices, one of them OFFLINE.
 */
const SITE = 'E2E HQ'
const ONLINE_DEVICES = ['AP Lobby', 'UDM HQ']
const OFFLINE_DEVICE = 'Switch Branch'

/**
 * Records every API call the page makes and every host it contacts, so the
 * assertions can be made about the real network traffic rather than about the
 * rendered result alone.
 */
function watchRequests(page: Page) {
  const apiCalls: string[] = []
  const hosts = new Set<string>()

  page.on('request', (request) => {
    const url = new URL(request.url())
    hosts.add(url.host)
    if (url.pathname.startsWith('/api/')) {
      apiCalls.push(url.pathname + url.search)
    }
  })

  return { apiCalls, hosts }
}

test('the site and device views render what the API cache holds', async ({ page }) => {
  const { apiCalls, hosts } = watchRequests(page)

  await page.goto('/')

  // The root redirects to the first read-only view.
  await expect(page).toHaveURL(/\/sites$/)
  await expect(page.getByRole('heading', { name: 'Sites', exact: true })).toBeVisible()

  const sitesTable = page.getByRole('table', { name: /Sites known to the control plane/ })
  await expect(sitesTable.getByRole('row', { name: new RegExp(SITE) })).toBeVisible()

  await page.getByRole('link', { name: 'Devices' }).click()
  await expect(page).toHaveURL(/\/devices$/)
  await expect(page.getByRole('heading', { name: 'Devices', exact: true })).toBeVisible()

  const devicesTable = page.getByRole('table', { name: /Devices known to the control plane/ })
  for (const name of [...ONLINE_DEVICES, OFFLINE_DEVICE]) {
    await expect(devicesTable.getByRole('row', { name: new RegExp(name) })).toBeVisible()
  }
  await expect(devicesTable.getByRole('row', { name: new RegExp(OFFLINE_DEVICE) })).toContainText(
    'OFFLINE',
  )

  // The API's own pagination drives the pager; the browser does not page locally.
  await expect(page.getByRole('navigation', { name: 'devices pagination' })).toContainText(
    'Page 1 of 1',
  )
  await expect(page.getByRole('navigation', { name: 'devices pagination' })).toContainText(
    '3 devices',
  )

  expect(apiCalls.some((call) => call.startsWith('/api/v1/sites'))).toBe(true)
  expect(apiCalls.some((call) => call.startsWith('/api/v1/devices'))).toBe(true)

  // Acceptance criterion 5: the web path touches no UniFi endpoint. The browser
  // only ever talks to the app's own origin, which is the only origin it can
  // reach; the controller is reached by the API's sync worker, not by the page.
  const appHost = new URL(page.url()).host
  expect([...hosts]).toEqual([appHost])
})

test('the status filter is applied by the API, not in the browser', async ({ page }) => {
  const deviceQueries: string[] = []
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.pathname === '/api/v1/devices') {
      deviceQueries.push(url.search)
    }
  })

  await page.goto('/devices')
  await expect(page.getByRole('row', { name: new RegExp(ONLINE_DEVICES[0]) })).toBeVisible()

  await page.getByLabel('Status').fill('OFFLINE')
  await page.getByRole('button', { name: 'Apply filters' }).click()

  // The filter lives in the URL, so the view is shareable and survives a reload.
  await expect(page).toHaveURL(/status=OFFLINE/)

  const devicesTable = page.getByRole('table', { name: /Devices known to the control plane/ })
  await expect(devicesTable.getByRole('row', { name: new RegExp(OFFLINE_DEVICE) })).toBeVisible()
  await expect(devicesTable.getByRole('row', { name: new RegExp(ONLINE_DEVICES[0]) })).toHaveCount(0)

  expect(deviceQueries.some((query) => query.includes('status=OFFLINE'))).toBe(true)
})

test('a failing API is reported with its contract code, not a generic message', async ({ page }) => {
  // The same 503 the API returns when the cache is stale and the controller is
  // unreachable (ADR-004). The UI must surface the code and the reason.
  await page.route('**/api/v1/sites*', (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 'unifi_unreachable',
        message: 'the UniFi cache is stale',
        details: { reason: 'cache_stale', request_id: 'req-e2e-web' },
      }),
    }),
  )

  await page.goto('/sites')

  const alert = page.getByRole('alert')
  await expect(alert).toContainText('unifi_unreachable')
  await expect(alert).toContainText('HTTP 503')
  await expect(alert).toContainText('cache_stale')
})

test('an unknown path explains itself instead of rendering nothing', async ({ page }) => {
  await page.goto('/does-not-exist')
  await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible()
})
