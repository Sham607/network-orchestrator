import { expect, test, type Page } from '@playwright/test'

/**
 * The network/VLAN slice in a real browser: the fixtures come from the Go
 * harness (`tests/e2e`), which syncs a mocked UniFi console into Postgres
 * before this suite runs. The page reads them through our own API — never
 * through the controller.
 */
const DEFAULT_NETWORK = 'Default'
const DEFAULT_VLAN = '1'
const DEFAULT_MANAGEMENT = 'GATEWAY'
const DEFAULT_STATUS = 'Enabled'
const DEFAULT_IS_DEFAULT = 'Yes'

const GUEST_NETWORK = 'Guest'
const GUEST_VLAN = '20'
const GUEST_MANAGEMENT = 'SWITCH'
const GUEST_STATUS = 'Disabled'
const GUEST_IS_DEFAULT = 'No'

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

test('the networks view renders the cache the worker wrote', async ({ page }) => {
  const { apiCalls, hosts } = watchRequests(page)

  await page.goto('/networks')
  await expect(page).toHaveURL(/\/networks$/)
  await expect(page.getByRole('heading', { name: 'Networks', exact: true })).toBeVisible()

  const networksTable = page.getByRole('table', { name: /Networks known to the control plane/ })

  const defaultRow = networksTable.getByRole('row', { name: new RegExp(DEFAULT_NETWORK) })
  await expect(defaultRow).toBeVisible()
  await expect(defaultRow).toContainText(DEFAULT_VLAN)
  await expect(defaultRow).toContainText(DEFAULT_MANAGEMENT)
  await expect(defaultRow).toContainText(DEFAULT_STATUS)
  await expect(defaultRow).toContainText(DEFAULT_IS_DEFAULT)

  const guestRow = networksTable.getByRole('row', { name: new RegExp(GUEST_NETWORK) })
  await expect(guestRow).toBeVisible()
  await expect(guestRow).toContainText(GUEST_VLAN)
  await expect(guestRow).toContainText(GUEST_MANAGEMENT)
  await expect(guestRow).toContainText(GUEST_STATUS)
  await expect(guestRow).toContainText(GUEST_IS_DEFAULT)

  // The pager is the API's own pagination, not a client-side slice.
  const pager = page.getByRole('navigation', { name: 'networks pagination' })
  await expect(pager).toContainText('Page 1 of 1')
  await expect(pager).toContainText('2 networks')

  expect(apiCalls.some((call) => call.startsWith('/api/v1/networks'))).toBe(true)

  // The page reaches only its own origin: the controller is the API's problem,
  // never the browser's.
  expect([...hosts]).toEqual([new URL(page.url()).host])
})

test('network filters are applied by the API, not in the browser', async ({ page }) => {
  const queries: string[] = []
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.pathname === '/api/v1/networks') {
      queries.push(url.search)
    }
  })

  await page.goto('/networks')
  const networksTable = page.getByRole('table', { name: /Networks known to the control plane/ })
  await expect(networksTable.getByRole('row', { name: new RegExp(DEFAULT_NETWORK) })).toBeVisible()
  await expect(networksTable.getByRole('row', { name: new RegExp(GUEST_NETWORK) })).toBeVisible()

  // Filter by enabled state: false -> only Guest
  await page.getByLabel('Enabled state').selectOption('false')
  await page.getByRole('button', { name: 'Apply filters' }).click()

  await expect(page).toHaveURL(/enabled=false/)
  await expect(networksTable.getByRole('row', { name: new RegExp(GUEST_NETWORK) })).toBeVisible()
  await expect(networksTable.getByRole('row', { name: new RegExp(DEFAULT_NETWORK) })).toHaveCount(0)
  expect(queries.some((query) => query.includes('enabled=false'))).toBe(true)

  // Filter by VLAN id: 1 -> only Default
  await page.getByLabel('Enabled state').selectOption('')
  await page.getByLabel('VLAN id').fill('1')
  await page.getByRole('button', { name: 'Apply filters' }).click()

  await expect(page).toHaveURL(/vlan_id=1/)
  await expect(networksTable.getByRole('row', { name: new RegExp(DEFAULT_NETWORK) })).toBeVisible()
  await expect(networksTable.getByRole('row', { name: new RegExp(GUEST_NETWORK) })).toHaveCount(0)
  expect(queries.some((query) => query.includes('vlan_id=1'))).toBe(true)

  // Filter by VLAN id with no matches
  await page.getByLabel('VLAN id').fill('99')
  await page.getByRole('button', { name: 'Apply filters' }).click()

  await expect(page).toHaveURL(/vlan_id=99/)
  await expect(page.getByText('No networks match these filters')).toBeVisible()
  expect(queries.some((query) => query.includes('vlan_id=99'))).toBe(true)
})
