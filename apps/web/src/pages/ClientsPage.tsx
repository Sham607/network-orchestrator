import { type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'

import { useClients, useSites, useSyncStatus } from '../api/queries'
import type { Client, ClientListQuery, SyncStatus } from '../api/types'
import { ErrorNotice } from '../components/ErrorNotice'
import { EmptyNotice, LoadingNotice } from '../components/Notices'
import { Pagination } from '../components/Pagination'
import { pageSizeFrom, positiveInt } from './listParams'

const PAGE_SIZES = [25, 50, 100, 200] as const
const DEFAULT_PAGE_SIZE = 25

/** True when any of hostname, MAC or IP contains `q` (case-insensitive). */
function matchesQuery(client: Client, q: string): boolean {
  if (!q) return true
  const needle = q.toLowerCase()
  return [client.hostname, client.mac, client.ip_address].some(
    (value) => typeof value === 'string' && value.toLowerCase().includes(needle),
  )
}

/**
 * Why the cache is empty, as far as `/sync/status` knows.
 *
 * The UniFi cloud surface does not serve clients at all (ADR-001), so on a
 * cloud deployment the rows this view reads can only ever come from
 * `UNIFI_MODE=local`. Telling the operator to wait for the sync worker there
 * would send them looking for rows that will never arrive.
 */
function emptyCacheHint(status: SyncStatus | undefined): string {
  if (status?.unifi?.surface === 'cloud') {
    return 'The cache is filled from the UniFi cloud surface (UNIFI_MODE=cloud), which does not serve clients (ADR-001). Client rows need the console Integration API: run with UNIFI_MODE=local from the office LAN or VPN.'
  }
  return 'The API answered successfully with zero clients. Clients appear once the sync worker has written them.'
}

/**
 * Clients list, read from `GET /api/v1/clients`.
 *
 * `site_id`, `connected` and `client_type` are server-side filters, like the
 * devices view — the browser never downloads the whole client table to filter
 * it itself. The free-text search is the exception: the contract has no
 * hostname/IP filter for clients (only an exact `mac` match), so it narrows
 * only the rows already loaded for the current page, and says so.
 */
export default function ClientsPage() {
  const [searchParams, setSearchParams] = useSearchParams()

  const page = positiveInt(searchParams.get('page'), 1)
  const pageSize = pageSizeFrom(searchParams.get('page_size'), PAGE_SIZES, DEFAULT_PAGE_SIZE)
  const siteId = (searchParams.get('site_id') ?? '').trim()
  const connected = (searchParams.get('connected') ?? '').trim()
  const clientType = (searchParams.get('client_type') ?? '').trim()
  const q = (searchParams.get('q') ?? '').trim()

  const query: ClientListQuery = { page, page_size: pageSize }
  if (siteId) query.site_id = siteId
  if (connected) query.connected = connected === 'true'
  if (clientType === 'WIRELESS' || clientType === 'WIRED') query.client_type = clientType

  const clients = useClients(query)
  // Best-effort: suggestions for the site filter. A failure here must not break
  // the client list, so the error is not surfaced as a page-level failure.
  const sites = useSites()
  const siteOptions = sites.data?.data ?? []
  // Best-effort too: the surface that filled the cache, used only to explain an
  // empty result. If it is unavailable, the page falls back to the generic hint.
  const sync = useSyncStatus()

  function applyFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const next = new URLSearchParams()

    const nextSite = String(form.get('site_id') ?? '').trim()
    const nextConnected = String(form.get('connected') ?? '').trim()
    const nextClientType = String(form.get('client_type') ?? '').trim()
    const nextQ = String(form.get('q') ?? '').trim()
    const nextPageSize = String(form.get('page_size') ?? '').trim()

    if (nextSite) next.set('site_id', nextSite)
    if (nextConnected) next.set('connected', nextConnected)
    if (nextClientType) next.set('client_type', nextClientType)
    if (nextQ) next.set('q', nextQ)
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

  const hasServerFilters = siteId !== '' || connected !== '' || clientType !== ''
  const hasAnyFilter = hasServerFilters || q !== ''
  const formKey = `${siteId}|${connected}|${clientType}|${q}|${pageSize}`

  const rows = clients.data?.data ?? []
  const visibleRows = rows.filter((client) => matchesQuery(client, q))

  return (
    <section aria-labelledby="clients-heading">
      <div className="page-header">
        <h2 id="clients-heading">Clients</h2>
        <button
          type="button"
          className="button"
          onClick={() => void clients.refetch()}
          disabled={clients.isFetching}
        >
          {clients.isFetching ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      <p className="source">
        Source: <code>GET /api/v1/clients</code> — site, connection state and type are filtered and
        paginated by the API; search below only narrows rows already on this page.
      </p>

      <form className="filters" onSubmit={applyFilters} key={formKey} aria-label="Client filters">
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
          <label htmlFor="filter-connected">Connection state</label>
          <select id="filter-connected" name="connected" defaultValue={connected}>
            <option value="">All</option>
            <option value="true">Connected</option>
            <option value="false">Offline</option>
          </select>
        </div>

        <div className="field">
          <label htmlFor="filter-client-type">Type</label>
          <select id="filter-client-type" name="client_type" defaultValue={clientType}>
            <option value="">All types</option>
            <option value="WIRELESS">Wireless</option>
            <option value="WIRED">Wired</option>
          </select>
        </div>

        <div className="field">
          <label htmlFor="filter-q">Search</label>
          <input
            id="filter-q"
            name="q"
            type="text"
            defaultValue={q}
            placeholder="hostname, MAC, or IP"
            autoComplete="off"
          />
          <p className="field-hint">
            Checks hostname, MAC and IP of the rows already loaded for this page. The API has no
            free-text client search yet, so this cannot reach rows on other pages — narrow with
            site, connection state or type first.
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
            disabled={!hasAnyFilter && pageSize === DEFAULT_PAGE_SIZE && page === 1}
          >
            Clear
          </button>
        </div>
      </form>

      {clients.isPending && <LoadingNotice label="Loading clients" />}

      {!clients.isPending && clients.error && (
        <ErrorNotice error={clients.error} onRetry={() => void clients.refetch()} />
      )}

      {clients.data && rows.length === 0 && (
        <EmptyNotice
          title={hasServerFilters ? 'No clients match these filters' : 'No clients in the cache'}
          hint={
            hasServerFilters
              ? 'The API answered successfully with zero rows for the current filters. Widen or clear them to see more.'
              : emptyCacheHint(sync.data)
          }
        />
      )}

      {clients.data && rows.length > 0 && visibleRows.length === 0 && (
        <EmptyNotice
          title={`No rows on this page match "${q}"`}
          hint="Search only checks the rows already loaded for this page — clear it, or narrow with site, connection state or type and try again."
        />
      )}

      {clients.data && visibleRows.length > 0 && (
        <>
          <div className="table-scroll">
            <table>
              <caption className="sr-only">Clients known to the control plane</caption>
              <thead>
                <tr>
                  <th scope="col">Hostname</th>
                  <th scope="col">IP address</th>
                  <th scope="col">MAC</th>
                  <th scope="col">Status</th>
                  <th scope="col">SSID</th>
                  <th scope="col">Uplink device</th>
                  <th scope="col">Last seen</th>
                </tr>
              </thead>
              <tbody>
                {visibleRows.map((client) => (
                  <tr key={client.id}>
                    <th scope="row">{client.hostname ?? <Unknown />}</th>
                    <td>{client.ip_address ?? <Unknown />}</td>
                    <td>{client.mac ?? <Unknown />}</td>
                    <td>{client.is_connected ? 'Connected' : 'Disconnected'}</td>
                    <td>{client.ssid ?? <Unknown />}</td>
                    <td>{client.uplink_device_id ?? <Unknown />}</td>
                    <td>{client.last_seen}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pagination
            pagination={clients.data.pagination}
            itemLabel="clients"
            busy={clients.isFetching}
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
