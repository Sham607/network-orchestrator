import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import SitesPage from '../pages/SitesPage'
import {
  emptySiteListFixture,
  installFetch,
  jsonResponse,
  requestedUrls,
  renderWithProviders,
  siteListFixture,
  textResponse,
} from './utils'

describe('SitesPage', () => {
  it('renders the sites the API returned', async () => {
    const fetchMock = installFetch(() => jsonResponse(siteListFixture))

    renderWithProviders(<SitesPage />, { route: '/sites' })

    expect(await screen.findByRole('rowheader', { name: 'HQ' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Head office' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Asia/Jakarta' })).toBeInTheDocument()
    expect(screen.getByRole('rowheader', { name: 'Warehouse' })).toBeInTheDocument()

    // Absent fields are labelled, never invented.
    expect(screen.getAllByText('unknown')).toHaveLength(3)
    expect(requestedUrls(fetchMock)).toEqual(['/api/v1/sites?page=1&page_size=50'])
  })

  it('shows an explicit empty state when the API returns no sites', async () => {
    installFetch(() => jsonResponse(emptySiteListFixture))

    renderWithProviders(<SitesPage />, { route: '/sites' })

    expect(await screen.findByText('No sites in the cache')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('surfaces a plain-text 404 from the API without pretending it is data', async () => {
    installFetch(() => textResponse('404 page not found\n', 404))

    renderWithProviders(<SitesPage />, { route: '/sites' })

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('http_404')
    expect(alert).toHaveTextContent('HTTP 404')
    expect(alert).toHaveTextContent('404 page not found')
    // The honest reading of a 404 before the resource handlers land.
    expect(alert).toHaveTextContent('contract-only')
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.queryByText('No sites in the cache')).not.toBeInTheDocument()
  })

  it('renders the contract error shape, request_id included', async () => {
    installFetch(() =>
      jsonResponse(
        {
          code: 'not_found',
          message: 'sites are not served yet',
          details: { request_id: '01a111bd-15d0-7f21-aec2-20bd264a6040' },
        },
        404,
      ),
    )

    renderWithProviders(<SitesPage />, { route: '/sites' })

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('not_found')
    expect(alert).toHaveTextContent('sites are not served yet')
    expect(alert).toHaveTextContent('request_id: 01a111bd-15d0-7f21-aec2-20bd264a6040')
  })

  it('says so when it can only show the first page of sites', async () => {
    installFetch(() =>
      jsonResponse({
        data: siteListFixture.data,
        pagination: { page: 1, page_size: 50, total: 120, total_pages: 3 },
      }),
    )

    renderWithProviders(<SitesPage />, { route: '/sites' })

    expect(await screen.findByText(/Showing 2 of 120 sites/)).toBeInTheDocument()
  })

  it('retries the request when the operator asks it to', async () => {
    const user = userEvent.setup()
    let attempt = 0
    const fetchMock = installFetch(() => {
      attempt += 1
      return attempt === 1
        ? textResponse('404 page not found\n', 404)
        : jsonResponse(siteListFixture)
    })

    renderWithProviders(<SitesPage />, { route: '/sites' })

    await screen.findByRole('alert')
    await user.click(screen.getByRole('button', { name: 'Try again' }))

    expect(await screen.findByRole('rowheader', { name: 'HQ' })).toBeInTheDocument()
    await waitFor(() => expect(requestedUrls(fetchMock)).toHaveLength(2))
  })
})
