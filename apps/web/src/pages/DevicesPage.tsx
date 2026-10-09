import { type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'

import { useDevices, useSites } from '../api/queries'
import type { DeviceListQuery } from '../api/types'
import { ErrorNotice } from '../components/ErrorNotice'
import { EmptyNotice, LoadingNotice } from '../components/Notices'
import { Pagination } from '../components/Pagination'

const PAGE_SIZES = [25, 50, 100, 200] as const
const DEFAULT_PAGE_SIZE = 25

function positiveInt(raw: string | null, fallback: number): number {
  const parsed = Number(raw)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback
}

/** Only page sizes the contract allows; anything else falls back to the default. */
function pageSizeFrom(raw: string | null): number {
  const parsed = Number(raw)
  return (PAGE_SIZES as readonly number[]).includes(parsed) ? parsed : DEFAULT_PAGE_SIZE
}

/**
 * Devices list, read from `GET /api/v1/devices`.
 *
 * Filtering and pagination are server-side (`site_id`, `status`, `page`,
 * `page_size`) — the browser never downloads the whole inventory to filter it
 * itself. Read-only: phase 1 has no write UI.
 */
export default function DevicesPage() {
  const [searchParams, setSearchParams] = useSearchParams()

  const page = positiveInt(searchParams.get('page'), 1)
  const pageSize = pageSizeFrom(searchParams.get('page_size'))
  const siteId = (searchParams.get('site_id') ?? '').trim()
  const status = (searchParams.get('status') ?? '').trim()

  const query: DeviceListQuery = { page, page_size: pageSize }
  if (siteId) query.site_id = siteId
  if (status) query.status = status

  const devices = useDevices(query)
  // Best-effort: suggestions for the site filter. A failure here must not break
  // the device list, so the error is not surfaced as a page-level failure.
  const sites = useSites()
  const siteOptions = sites.data?.data ?? []

  function applyFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const next = new URLSearchParams()

    const nextSite = String(form.get('site_id') ?? '').trim()
    const nextStatus = String(form.get('status') ?? '').trim()
    const nextPageSize = String(form.get('page_size') ?? '').trim()

    if (nextSite) next.set('site_id', nextSite)
    if (nextStatus) next.set('status', nextStatus)
    if (nextPageSize) next.set('page_size', nextPageSize)
    // A filter change restarts at page 1; the old offset is meaningless.
    next.set('page', '1')

    setSearchParams(next)
  }

  function goToPage(nextPage: number) {
    const next = new URLSearchParams(searchParams)
    next.set('page', String(nextPage))
    setSearchParams(next)
  }

  const hasFilters = siteId !== '' || status !== ''
  const formKey = `${siteId}|${status}|${pageSize}`

  return (
    <section aria-labelledby="devices-heading">
      <div className="page-header">
        <h2 id="devices-heading">Devices</h2>
        <button
          type="button"
          className="button"
          onClick={() => void devices.refetch()}
          disabled={devices.isFetching}
        >
          {devices.isFetching ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      <p className="source">
        Source: <code>GET /api/v1/devices</code> — filtered and paginated by the API, not in the
        browser.
      </p>

      <form className="filters" onSubmit={applyFilters} key={formKey} aria-label="Device filters">
        <div className="field">
          <label htmlFor="filter-site-id">Site id</label>
          <input
            id="filter-site-id"
            name="site_id"
            type="text"
            defaultValue={siteId}
            placeholder="1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c"
            list="filter-site-id-options"
            autoComplete="off"
          />
          <datalist id="filter-site-id-options">
            {siteOptions.map((site) => (
              <option key={site.id} value={site.id}>
                {site.name}
              </option>
            ))}
          </datalist>
          <p className="field-hint">Local control-plane site id (UUID).</p>
        </div>

        <div className="field">
          <label htmlFor="filter-status">Status</label>
          <input
            id="filter-status"
            name="status"
            type="text"
            defaultValue={status}
            placeholder="ONLINE"
            autoComplete="off"
          />
          <p className="field-hint">
            The UniFi device state, upper-case (<code>ONLINE</code>, <code>OFFLINE</code>,{' '}
            <code>ADOPTING</code>…). The set is open, so this is free text.
          </p>
        </div>

        <div className="field">
          <label htmlFor="filter-page-size">Rows per page</label>
          <select id="filter-page-size" name="page_size" defaultValue={String(pageSize)}>
            {PAGE_SIZES.map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </div>

        <div className="field field-actions">
          <button type="submit" className="button button-primary">
            Apply filters
          </button>
          <button
            type="button"
            className="button"
            onClick={() => setSearchParams(new URLSearchParams())}
            disabled={!hasFilters && pageSize === DEFAULT_PAGE_SIZE && page === 1}
          >
            Clear
          </button>
        </div>
      </form>

      {devices.isPending && <LoadingNotice label="Loading devices" />}

      {!devices.isPending && devices.error && (
        <ErrorNotice error={devices.error} onRetry={() => void devices.refetch()} />
      )}

      {devices.data && devices.data.data.length === 0 && (
        <EmptyNotice
          title={hasFilters ? 'No devices match these filters' : 'No devices in the cache'}
          hint={
            hasFilters
              ? 'The API answered successfully with zero rows for the current filters. Widen or clear them to see more.'
              : 'The API answered successfully with zero devices. Devices appear once the sync worker has written them.'
          }
        />
      )}

      {devices.data && devices.data.data.length > 0 && (
        <>
          <div className="table-scroll">
            <table>
              <caption className="sr-only">Devices known to the control plane</caption>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">MAC</th>
                  <th scope="col">Model</th>
                  <th scope="col">Type</th>
                  <th scope="col">Status</th>
                  <th scope="col">IP address</th>
                  <th scope="col">Firmware</th>
                  <th scope="col">Synced at</th>
                </tr>
              </thead>
              <tbody>
                {devices.data.data.map((device) => (
                  <tr key={device.id}>
                    <th scope="row">{device.name ?? <Unknown />}</th>
                    <td>{device.mac ?? <Unknown />}</td>
                    <td>{device.model ?? <Unknown />}</td>
                    <td>{device.device_type}</td>
                    <td>{device.status ?? <Unknown />}</td>
                    <td>{device.ip_address ?? <Unknown />}</td>
                    <td>{device.firmware_version ?? <Unknown />}</td>
                    <td>{device.synced_at}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pagination
            pagination={devices.data.pagination}
            itemLabel="devices"
            busy={devices.isFetching}
            onPageChange={goToPage}
          />
        </>
      )}
    </section>
  )
}

function Unknown() {
  return <span className="unknown">unknown</span>
}
