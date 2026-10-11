import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import ClientsPage from '../pages/ClientsPage'
import {
  REQUEST_ID,
  clientListFixture,
  cloudSyncStatusFixture,
  consoleSyncStatusFixture,
  emptyClientListFixture,
  installFetch,
  jsonResponse,
  requestedUrls,
  renderWithProviders,
  serviceUnavailableBody,
  siteListFixture,
} from './utils'

/**
 * Route the stub by endpoint so one mock serves every query of this page.
 * `/sync/status` defaults to the console surface, i.e. a cache that can hold
 * clients.
 */
function apiStub(routes: {
  clients: (url: string) => ReturnType<typeof jsonResponse>
  sites?: (url: string) => ReturnType<typeof jsonResponse>
  sync?: (url: string) => ReturnType<typeof jsonResponse>
}) {
  return installFetch((url) => {
    if (url.startsWith('/api/v1/clients')) return routes.clients(url)
    if (url.startsWith('/api/v1/sites')) {
      return (routes.sites ?? (() => jsonResponse(siteListFixture)))(url)
    }
    if (url.startsWith('/api/v1/sync/status')) {
      return (routes.sync ?? (() => jsonResponse(consoleSyncStatusFixture)))(url)
    }
    throw new Error(`unexpected request: ${url}`)
  })
}

describe('ClientsPage', () => {
  it('renders the clients the API returned, server-side filtered and paginated', async () => {
    const fetchMock = apiStub({ clients: () => jsonResponse(clientListFixture) })

    renderWithProviders(<ClientsPage />, { route: '/clients' })

    expect(await screen.findByRole('rowheader', { name: 'ivan-laptop' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: '10.20.0.42' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'aa:bb:cc:dd:ee:10' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Connected' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Disconnected' })).toBeInTheDocument()
    expect(screen.getByText('Page 1 of 1 · 2 clients · 25 per page')).toBeInTheDocument()

    expect(requestedUrls(fetchMock)).toEqual([
      '/api/v1/clients?page=1&page_size=25',
      '/api/v1/sites?page=1&page_size=50',
      '/api/v1/sync/status',
    ])
  })

  it('shows an empty state instead of rows when the API returns none', async () => {
    apiStub({ clients: () => jsonResponse(emptyClientListFixture) })

    renderWithProviders(<ClientsPage />, { route: '/clients' })

    expect(await screen.findByText('No clients in the cache')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('explains that the cloud surface serves no clients instead of promising rows', async () => {
    apiStub({
      clients: () => jsonResponse(emptyClientListFixture),
      sync: () => jsonResponse(cloudSyncStatusFixture),
    })

    renderWithProviders(<ClientsPage />, { route: '/clients' })

    expect(await screen.findByText('No clients in the cache')).toBeInTheDocument()
    expect(screen.getByText(/does not serve clients \(ADR-001\)/)).toHaveTextContent(
      'run with UNIFI_MODE=local',
    )
    expect(screen.queryByText(/once the sync worker has written them/)).not.toBeInTheDocument()
  })

  it('says the filters matched nothing when the API returns an empty filtered page', async () => {
    apiStub({ clients: () => jsonResponse(emptyClientListFixture) })

    renderWithProviders(<ClientsPage />, { route: '/clients?connected=true' })

    expect(await screen.findByText('No clients match these filters')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('renders a 503 service_unavailable with its code, message and request_id', async () => {
    apiStub({ clients: () => jsonResponse(serviceUnavailableBody, 503) })

    renderWithProviders(<ClientsPage />, { route: '/clients' })

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('service_unavailable')
    expect(alert).toHaveTextContent('HTTP 503')
    expect(alert).toHaveTextContent('database is not reachable')
    expect(alert).toHaveTextContent(`request_id: ${REQUEST_ID}`)
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('sends the filters to the API with the contract parameter names', async () => {
    const user = userEvent.setup()
    const fetchMock = apiStub({ clients: () => jsonResponse(clientListFixture) })

    renderWithProviders(<ClientsPage />, { route: '/clients' })
    await screen.findByRole('rowheader', { name: 'ivan-laptop' })

    await user.type(screen.getByLabelText('Site id'), '1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c')
    await user.selectOptions(screen.getByLabelText('Connection state'), 'Connected')
    await user.selectOptions(screen.getByLabelText('Type'), 'Wireless')
    await user.selectOptions(screen.getByLabelText('Rows per page'), '50')
    await user.click(screen.getByRole('button', { name: 'Apply filters' }))

    await waitFor(() =>
      expect(requestedUrls(fetchMock)).toContain(
        '/api/v1/clients?page=1&page_size=50&site_id=1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c&connected=true&client_type=WIRELESS',
      ),
    )
  })

  it('requests the next page when the operator pages forward', async () => {
    const user = userEvent.setup()
    const fetchMock = apiStub({
      clients: (url) =>
        url.includes('page=2')
          ? jsonResponse({
              data: [
                {
                  ...clientListFixture.data[0],
                  id: '7a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c72',
                  hostname: 'second-page-client',
                },
              ],
              pagination: { page: 2, page_size: 25, total: 60, total_pages: 3 },
            })
          : jsonResponse({
              data: clientListFixture.data,
              pagination: { page: 1, page_size: 25, total: 60, total_pages: 3 },
            }),
    })

    renderWithProviders(<ClientsPage />, { route: '/clients' })
    await screen.findByRole('rowheader', { name: 'ivan-laptop' })

    await user.click(screen.getByRole('button', { name: 'Next' }))

    expect(await screen.findByRole('rowheader', { name: 'second-page-client' })).toBeInTheDocument()
    expect(screen.getByText('Page 2 of 3 · 60 clients · 25 per page')).toBeInTheDocument()
    expect(requestedUrls(fetchMock)).toContain('/api/v1/clients?page=2&page_size=25')
  })

  it('narrows the loaded page to rows matching the search text, without a new request', async () => {
    const user = userEvent.setup()
    const fetchMock = apiStub({ clients: () => jsonResponse(clientListFixture) })

    renderWithProviders(<ClientsPage />, { route: '/clients' })
    await screen.findByRole('rowheader', { name: 'ivan-laptop' })

    await user.type(screen.getByLabelText('Search'), 'ivan')
    await user.click(screen.getByRole('button', { name: 'Apply filters' }))

    expect(await screen.findByRole('rowheader', { name: 'ivan-laptop' })).toBeInTheDocument()
    expect(screen.queryByRole('cell', { name: 'Disconnected' })).not.toBeInTheDocument()
    // The search is client-side only: no extra request beyond the unfiltered page fetch.
    expect(requestedUrls(fetchMock).filter((url) => url.startsWith('/api/v1/clients'))).toEqual([
      '/api/v1/clients?page=1&page_size=25',
    ])
  })

  it('shows a page-local empty state when the search text matches nothing on this page', async () => {
    const user = userEvent.setup()
    apiStub({ clients: () => jsonResponse(clientListFixture) })

    renderWithProviders(<ClientsPage />, { route: '/clients' })
    await screen.findByRole('rowheader', { name: 'ivan-laptop' })

    await user.type(screen.getByLabelText('Search'), 'no-such-client')
    await user.click(screen.getByRole('button', { name: 'Apply filters' }))

    expect(await screen.findByText('No rows on this page match "no-such-client"')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })
})
