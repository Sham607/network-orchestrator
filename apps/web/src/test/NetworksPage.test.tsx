import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import NetworksPage from '../pages/NetworksPage'
import {
  REQUEST_ID,
  cloudSyncStatusFixture,
  consoleSyncStatusFixture,
  emptyNetworkListFixture,
  installFetch,
  jsonResponse,
  networkListFixture,
  requestedUrls,
  renderWithProviders,
  serviceUnavailableBody,
  siteListFixture,
} from './utils'

/**
 * Route the stub by endpoint so one mock serves every query of this page.
 * `/sync/status` defaults to the console surface, i.e. a cache that can hold networks.
 */
function apiStub(routes: {
  networks: (url: string) => ReturnType<typeof jsonResponse>
  sites?: (url: string) => ReturnType<typeof jsonResponse>
  sync?: (url: string) => ReturnType<typeof jsonResponse>
}) {
  return installFetch((url) => {
    if (url.startsWith('/api/v1/networks')) return routes.networks(url)
    if (url.startsWith('/api/v1/sites')) {
      return (routes.sites ?? (() => jsonResponse(siteListFixture)))(url)
    }
    if (url.startsWith('/api/v1/sync/status')) {
      return (routes.sync ?? (() => jsonResponse(consoleSyncStatusFixture)))(url)
    }
    throw new Error(`unexpected request: ${url}`)
  })
}

describe('NetworksPage', () => {
  it('renders the networks the API returned, server-side filtered and paginated', async () => {
    const fetchMock = apiStub({ networks: () => jsonResponse(networkListFixture) })

    renderWithProviders(<NetworksPage />, { route: '/networks' })

    expect(await screen.findByRole('rowheader', { name: 'Default' })).toBeInTheDocument()
    expect(screen.getByRole('rowheader', { name: 'Guest' })).toBeInTheDocument()
    expect(screen.getByRole('rowheader', { name: 'Untagged' })).toBeInTheDocument()

    expect(screen.getByRole('cell', { name: '1' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: '20' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'GATEWAY' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'SWITCH' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Enabled' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Disabled' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Yes' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'No' })).toBeInTheDocument()

    // Untagged network has null fields which should render as unknown
    const unknowns = screen.getAllByText('unknown')
    expect(unknowns.length).toBeGreaterThanOrEqual(4)

    expect(screen.getByText('Page 1 of 1 · 3 networks · 25 per page')).toBeInTheDocument()

    expect(requestedUrls(fetchMock)).toEqual([
      '/api/v1/networks?page=1&page_size=25',
      '/api/v1/sites?page=1&page_size=50',
      '/api/v1/sync/status',
    ])
  })

  it('shows an empty state instead of rows when the API returns none', async () => {
    apiStub({ networks: () => jsonResponse(emptyNetworkListFixture) })

    renderWithProviders(<NetworksPage />, { route: '/networks' })

    expect(await screen.findByText('No networks in the cache')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('explains that the cloud surface serves no networks instead of promising rows', async () => {
    apiStub({
      networks: () => jsonResponse(emptyNetworkListFixture),
      sync: () => jsonResponse(cloudSyncStatusFixture),
    })

    renderWithProviders(<NetworksPage />, { route: '/networks' })

    expect(await screen.findByText('No networks in the cache')).toBeInTheDocument()
    expect(screen.getByText(/does not serve networks \(ADR-001\)/)).toHaveTextContent(
      'run with UNIFI_MODE=local',
    )
    expect(screen.queryByText(/once the sync worker has written them/)).not.toBeInTheDocument()
  })

  it('says the filters matched nothing when the API returns an empty filtered page', async () => {
    apiStub({ networks: () => jsonResponse(emptyNetworkListFixture) })

    renderWithProviders(<NetworksPage />, { route: '/networks?enabled=true' })

    expect(await screen.findByText('No networks match these filters')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('renders a 503 service_unavailable with its code, message and request_id', async () => {
    apiStub({ networks: () => jsonResponse(serviceUnavailableBody, 503) })

    renderWithProviders(<NetworksPage />, { route: '/networks' })

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('service_unavailable')
    expect(alert).toHaveTextContent('HTTP 503')
    expect(alert).toHaveTextContent('database is not reachable')
    expect(alert).toHaveTextContent(`request_id: ${REQUEST_ID}`)
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('sends the filters to the API with the contract parameter names', async () => {
    const user = userEvent.setup()
    const fetchMock = apiStub({ networks: () => jsonResponse(networkListFixture) })

    renderWithProviders(<NetworksPage />, { route: '/networks' })
    await screen.findByRole('rowheader', { name: 'Default' })

    await user.type(screen.getByLabelText('Site id'), '1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c')
    await user.type(screen.getByLabelText('VLAN id'), '20')
    await user.type(screen.getByLabelText('Management'), 'SWITCH')
    await user.selectOptions(screen.getByLabelText('Enabled state'), 'Enabled')
    await user.selectOptions(screen.getByLabelText('Rows per page'), '50')
    await user.click(screen.getByRole('button', { name: 'Apply filters' }))

    await waitFor(() =>
      expect(requestedUrls(fetchMock)).toContain(
        '/api/v1/networks?page=1&page_size=50&site_id=1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c&vlan_id=20&management=SWITCH&enabled=true',
      ),
    )
  })

  it('keeps a VLAN id the contract does not accept out of the request', async () => {
    const fetchMock = apiStub({ networks: () => jsonResponse(networkListFixture) })

    renderWithProviders(<NetworksPage />, { route: '/networks?vlan_id=4095' })
    await screen.findByRole('rowheader', { name: 'Default' })

    expect(screen.getByLabelText('VLAN id')).toHaveValue('4095')
    const networkCalls = requestedUrls(fetchMock).filter((url) => url.startsWith('/api/v1/networks'))
    expect(networkCalls).toEqual(['/api/v1/networks?page=1&page_size=25'])
  })

  it('sends a valid VLAN id when within 0..4094', async () => {
    const fetchMock = apiStub({ networks: () => jsonResponse(networkListFixture) })

    renderWithProviders(<NetworksPage />, { route: '/networks?vlan_id=40' })
    await screen.findByRole('rowheader', { name: 'Default' })

    expect(screen.getByLabelText('VLAN id')).toHaveValue('40')
    const networkCalls = requestedUrls(fetchMock).filter((url) => url.startsWith('/api/v1/networks'))
    expect(networkCalls).toEqual(['/api/v1/networks?page=1&page_size=25&vlan_id=40'])
  })

  it('requests the next page when the operator pages forward', async () => {
    const user = userEvent.setup()
    const fetchMock = apiStub({
      networks: (url) =>
        url.includes('page=2')
          ? jsonResponse({
              data: [
                {
                  ...networkListFixture.data[0],
                  id: '8d6e7f8a-9b0c-4d1e-2f3a-4b5c6d7e8f9d',
                  name: 'second-page-network',
                },
              ],
              pagination: { page: 2, page_size: 25, total: 30, total_pages: 2 },
            })
          : jsonResponse({
              data: networkListFixture.data,
              pagination: { page: 1, page_size: 25, total: 30, total_pages: 2 },
            }),
    })

    renderWithProviders(<NetworksPage />, { route: '/networks' })
    await screen.findByRole('rowheader', { name: 'Default' })

    await user.click(screen.getByRole('button', { name: 'Next' }))

    expect(await screen.findByRole('rowheader', { name: 'second-page-network' })).toBeInTheDocument()
    expect(screen.getByText('Page 2 of 2 · 30 networks · 25 per page')).toBeInTheDocument()
    expect(requestedUrls(fetchMock)).toContain('/api/v1/networks?page=2&page_size=25')
  })

  it('narrows the loaded page to rows matching the search text, without a new request', async () => {
    const user = userEvent.setup()
    const fetchMock = apiStub({ networks: () => jsonResponse(networkListFixture) })

    renderWithProviders(<NetworksPage />, { route: '/networks' })
    await screen.findByRole('rowheader', { name: 'Default' })

    await user.type(screen.getByLabelText('Search'), 'Guest')
    await user.click(screen.getByRole('button', { name: 'Apply filters' }))

    expect(await screen.findByRole('rowheader', { name: 'Guest' })).toBeInTheDocument()
    expect(screen.queryByRole('rowheader', { name: 'Default' })).not.toBeInTheDocument()
    expect(screen.queryByRole('rowheader', { name: 'Untagged' })).not.toBeInTheDocument()

    expect(requestedUrls(fetchMock).filter((url) => url.startsWith('/api/v1/networks'))).toEqual([
      '/api/v1/networks?page=1&page_size=25',
    ])
  })

  it('searches by VLAN id locally', async () => {
    const user = userEvent.setup()
    apiStub({ networks: () => jsonResponse(networkListFixture) })

    renderWithProviders(<NetworksPage />, { route: '/networks' })
    await screen.findByRole('rowheader', { name: 'Default' })

    await user.type(screen.getByLabelText('Search'), '20')
    await user.click(screen.getByRole('button', { name: 'Apply filters' }))

    expect(await screen.findByRole('rowheader', { name: 'Guest' })).toBeInTheDocument()
    expect(screen.queryByRole('rowheader', { name: 'Default' })).not.toBeInTheDocument()
  })

  it('shows a page-local empty state when the search text matches nothing on this page', async () => {
    const user = userEvent.setup()
    apiStub({ networks: () => jsonResponse(networkListFixture) })

    renderWithProviders(<NetworksPage />, { route: '/networks' })
    await screen.findByRole('rowheader', { name: 'Default' })

    await user.type(screen.getByLabelText('Search'), 'no-such-network')
    await user.click(screen.getByRole('button', { name: 'Apply filters' }))

    expect(await screen.findByText('No rows on this page match "no-such-network"')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })
})
