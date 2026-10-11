import { expect, test, type Page } from '@playwright/test'

/**
 * The client-presence slice in a real browser: the fixtures come from the Go
 * harness (`tests/e2e`), which syncs a mocked UniFi console into Postgres
 * before this suite runs. The page reads them through our own API — never
 * through the controller.
 */
const WIRELESS = 'laptop'
const WIRELESS_IP = '10.0.0.50'
const WIRELESS_MAC = 'aa:bb:cc:00:00:50'
const WIRED = 'workstation'
const WIRED_IP = '10.0.0.51'

/** Records the API calls and hosts a page touches, so assertions are about the
 * real traffic and not only about the rendered result. */
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

test('the clients view renders the cache the worker wrote', async ({ page }) => {
  const { apiCalls, hosts } = watchRequests(page)

  await page.goto('/clients')
  await expect(page).toHaveURL(/\/clients$/)
  await expect(page.getByRole('heading', { name: 'Clients', exact: true })).toBeVisible()

  const clientsTable = page.getByRole('table', { name: /Clients known to the control plane/ })

  const wirelessRow = clientsTable.getByRole('row', { name: new RegExp(WIRELESS) })
  await expect(wirelessRow).toBeVisible()
  await expect(wirelessRow).toContainText(WIRELESS_IP)
  await expect(wirelessRow).toContainText(WIRELESS_MAC)
  await expect(wirelessRow).toContainText('Connected')
  // SSID is null in phase 1 (Integration API v1 does not expose it); the view
  // must say "unknown" rather than invent a value.
  await expect(wirelessRow).toContainText('unknown')

  const wiredRow = clientsTable.getByRole('row', { name: new RegExp(WIRED) })
  await expect(wiredRow).toBeVisible()
  await expect(wiredRow).toContainText(WIRED_IP)

  // The pager is the API's own pagination, not a client-side slice.
  const pager = page.getByRole('navigation', { name: 'clients pagination' })
  await expect(pager).toContainText('Page 1 of 1')
  await expect(pager).toContainText('2 clients')

  expect(apiCalls.some((call) => call.startsWith('/api/v1/clients'))).toBe(true)

  // The page reaches only its own origin: the controller is the API's problem,
  // never the browser's.
  expect([...hosts]).toEqual([new URL(page.url()).host])
})

test('client filters are applied by the API, not in the browser', async ({ page }) => {
  const queries: string[] = []
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.pathname === '/api/v1/clients') {
      queries.push(url.search)
    }
  })

  await page.goto('/clients')
  const clientsTable = page.getByRole('table', { name: /Clients known to the control plane/ })
  await expect(clientsTable.getByRole('row', { name: new RegExp(WIRED) })).toBeVisible()

  await page.getByLabel('Type').selectOption('WIRELESS')
  await page.getByRole('button', { name: 'Apply filters' }).click()

  // The filter lives in the URL, so the view is shareable and survives a reload.
  await expect(page).toHaveURL(/client_type=WIRELESS/)
  await expect(clientsTable.getByRole('row', { name: new RegExp(WIRELESS) })).toBeVisible()
  await expect(clientsTable.getByRole('row', { name: new RegExp(WIRED) })).toHaveCount(0)
  expect(queries.some((query) => query.includes('client_type=WIRELESS'))).toBe(true)

  // Connection state is a server filter too. No fixture client is disconnected,
  // so the API answers zero rows and the page says exactly that.
  await page.getByLabel('Connection state').selectOption('false')
  await page.getByRole('button', { name: 'Apply filters' }).click()

  await expect(page).toHaveURL(/connected=false/)
  await expect(page.getByText('No clients match these filters')).toBeVisible()
  expect(queries.some((query) => query.includes('connected=false'))).toBe(true)
})
