import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import DevicesPage from '../pages/DevicesPage'
import {
  REQUEST_ID,
  deviceListFixture,
  emptyDeviceListFixture,
  installFetch,
  jsonResponse,
  requestedUrls,
  renderWithProviders,
  serviceUnavailableBody,
  siteListFixture,
} from './utils'

/** Route the stub by endpoint so one mock serves both queries of this page. */
function apiStub(routes: {
  devices: (url: string) => ReturnType<typeof jsonResponse>
  sites?: (url: string) => ReturnType<typeof jsonResponse>
}) {
  return installFetch((url) => {
    if (url.startsWith('/api/v1/devices')) return routes.devices(url)
    if (url.startsWith('/api/v1/sites')) {
      return (routes.sites ?? (() => jsonResponse(siteListFixture)))(url)
    }
    throw new Error(`unexpected request: ${url}`)
  })
}

describe('DevicesPage', () => {
  it('renders the devices the API returned, server-side filtered and paginated', async () => {
    const fetchMock = apiStub({ devices: () => jsonResponse(deviceListFixture) })

    renderWithProviders(<DevicesPage />, { route: '/devices' })

    expect(await screen.findByRole('rowheader', { name: 'AP-Lobby' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'aa:bb:cc:dd:ee:02' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'U6 Pro' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'ap' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'ONLINE' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'OFFLINE' })).toBeInTheDocument()
    expect(screen.getByText('Page 1 of 1 · 2 devices · 25 per page')).toBeInTheDocument()

    expect(requestedUrls(fetchMock)).toEqual([
      '/api/v1/devices?page=1&page_size=25',
      '/api/v1/sites?page=1&page_size=50',
    ])
  })

  it('shows an empty state instead of rows when the API returns none', async () => {
    apiStub({ devices: () => jsonResponse(emptyDeviceListFixture) })

    renderWithProviders(<DevicesPage />, { route: '/devices' })

    expect(await screen.findByText('No devices in the cache')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('says the filters matched nothing when the API returns an empty filtered page', async () => {
    apiStub({ devices: () => jsonResponse(emptyDeviceListFixture) })

    renderWithProviders(<DevicesPage />, { route: '/devices?site_id=1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c' })

    expect(await screen.findByText('No devices match these filters')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('renders a 503 service_unavailable with its code, message and request_id', async () => {
    apiStub({ devices: () => jsonResponse(serviceUnavailableBody, 503) })

    renderWithProviders(<DevicesPage />, { route: '/devices' })

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('service_unavailable')
    expect(alert).toHaveTextContent('HTTP 503')
    expect(alert).toHaveTextContent('database is not reachable')
    expect(alert).toHaveTextContent(`request_id: ${REQUEST_ID}`)
    // The structured body is shown as-is, so the fallback explanation is absent.
    expect(alert).not.toHaveTextContent('did not carry the contract error shape')
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('sends the filters to the API with the contract parameter names', async () => {
    const user = userEvent.setup()
    const fetchMock = apiStub({ devices: () => jsonResponse(deviceListFixture) })

    renderWithProviders(<DevicesPage />, { route: '/devices' })
    await screen.findByRole('rowheader', { name: 'AP-Lobby' })

    await user.type(screen.getByLabelText('Site id'), '1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c')
    await user.type(screen.getByLabelText('Status'), 'ONLINE')
    await user.selectOptions(screen.getByLabelText('Rows per page'), '50')
    await user.click(screen.getByRole('button', { name: 'Apply filters' }))

    await waitFor(() =>
      expect(requestedUrls(fetchMock)).toContain(
        '/api/v1/devices?page=1&page_size=50&site_id=1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c&status=ONLINE',
      ),
    )
  })

  it('requests the next page when the operator pages forward', async () => {
    const user = userEvent.setup()
    const fetchMock = apiStub({
      devices: (url) =>
        url.includes('page=2')
          ? jsonResponse({
              data: [
                {
                  ...deviceListFixture.data[0],
                  id: '4a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6f',
                  name: 'AP-Floor2',
                },
              ],
              pagination: { page: 2, page_size: 25, total: 60, total_pages: 3 },
            })
          : jsonResponse({
              data: deviceListFixture.data,
              pagination: { page: 1, page_size: 25, total: 60, total_pages: 3 },
            }),
    })

    renderWithProviders(<DevicesPage />, { route: '/devices' })
    await screen.findByRole('rowheader', { name: 'AP-Lobby' })

    await user.click(screen.getByRole('button', { name: 'Next' }))

    expect(await screen.findByRole('rowheader', { name: 'AP-Floor2' })).toBeInTheDocument()
    expect(screen.getByText('Page 2 of 3 · 60 devices · 25 per page')).toBeInTheDocument()
    expect(requestedUrls(fetchMock)).toContain('/api/v1/devices?page=2&page_size=25')
    expect(screen.getByRole('button', { name: 'Previous' })).toBeEnabled()
  })
})
