import { type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'

import { useNetworks, useSites, useSyncStatus } from '../api/queries'
import type { Network, NetworkListQuery, SyncStatus } from '../api/types'
import { ErrorNotice } from '../components/ErrorNotice'
import { EmptyNotice, LoadingNotice } from '../components/Notices'
import { Pagination } from '../components/Pagination'
import { pageSizeFrom, positiveInt } from './listParams'

const PAGE_SIZES = [25, 50, 100, 200] as const
const DEFAULT_PAGE_SIZE = 25

/** Parses a VLAN id string, restricting it strictly to the contract bounds 0..4094. */
function parseVlanId(raw: string | null): number | undefined {
  if (!raw) return undefined
  const trimmed = raw.trim()
  if (!trimmed) return undefined
  const n = Number(trimmed)
  if (!Number.isInteger(n) || n < 0 || n > 4094) return undefined
  return n
}

/** True when any of name, management or VLAN id contains `q` (case-insensitive). */
function matchesQuery(network: Network, q: string): boolean {
  if (!q) return true
  const needle = q.toLowerCase()
  const vlanStr = network.vlan_id !== null && network.vlan_id !== undefined ? String(network.vlan_id) : ''
  return [network.name, network.management, vlanStr].some(
    (value) => typeof value === 'string' && value.toLowerCase().includes(needle),
  )
}

/**
 * Why the cache is empty, as far as `/sync/status` knows.
 *
 * The UniFi cloud surface does not serve networks at all (ADR-001), so on a
 * cloud deployment the rows this view reads can only ever come from
 * `UNIFI_MODE=local`. Telling the operator to wait for the sync worker there
 * would send them looking for rows that will never arrive.
 */
function emptyCacheHint(status: SyncStatus | undefined): string {
  if (status?.unifi?.surface === 'cloud') {
    return 'The cache is filled from the UniFi cloud surface (UNIFI_MODE=cloud), which does not serve networks (ADR-001). Network rows need the console Integration API: run with UNIFI_MODE=local from the office LAN or VPN.'
  }
  return 'The API answered successfully with zero networks. Networks appear once the sync worker has written them.'
}

/**
 * Networks list, read from `GET /api/v1/networks`.
 *
 * `site_id`, `vlan_id`, `management` and `enabled` are server-side filters.
 * The free-text search is local to the current page.
 */
export default function NetworksPage() {
  const [searchParams, setSearchParams] = useSearchParams()

  const page = positiveInt(searchParams.get('page'), 1)
  const pageSize = pageSizeFrom(searchParams.get('page_size'), PAGE_SIZES, DEFAULT_PAGE_SIZE)
  const siteId = (searchParams.get('site_id') ?? '').trim()
  const vlanId = (searchParams.get('vlan_id') ?? '').trim()
  const management = (searchParams.get('management') ?? '').trim()
  const enabled = (searchParams.get('enabled') ?? '').trim()
  const q = (searchParams.get('q') ?? '').trim()

  const query: NetworkListQuery = { page, page_size: pageSize }
  if (siteId) query.site_id = siteId
  const parsedVlan = parseVlanId(vlanId)
  if (parsedVlan !== undefined) query.vlan_id = parsedVlan
  if (management) query.management = management
  if (enabled === 'true') query.enabled = true
  if (enabled === 'false') query.enabled = false

  const networks = useNetworks(query)
  const sites = useSites()
  const siteOptions = sites.data?.data ?? []
  const sync = useSyncStatus()

  function applyFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const next = new URLSearchParams()

    const nextSite = String(form.get('site_id') ?? '').trim()
    const nextVlan = String(form.get('vlan_id') ?? '').trim()
    const nextManagement = String(form.get('management') ?? '').trim()
    const nextEnabled = String(form.get('enabled') ?? '').trim()
    const nextQ = String(form.get('q') ?? '').trim()
    const nextPageSize = String(form.get('page_size') ?? '').trim()

    if (nextSite) next.set('site_id', nextSite)
    if (nextVlan) next.set('vlan_id', nextVlan)
    if (nextManagement) next.set('management', nextManagement)
    if (nextEnabled) next.set('enabled', nextEnabled)
    if (nextQ) next.set('q', nextQ)
    if (nextPageSize) next.set('page_size', nextPageSize)
    next.set('page', '1')

    setSearchParams(next)
  }

  function goToPage(nextPage: number) {
    const next = new URLSearchParams(searchParams)
    next.set('page', String(nextPage))
    setSearchParams(next)
  }

  const hasServerFilters = siteId !== '' || vlanId !== '' || management !== '' || enabled !== ''
  const hasAnyFilter = hasServerFilters || q !== ''
  const formKey = `${siteId}|${vlanId}|${management}|${enabled}|${q}|${pageSize}`

  const rows = networks.data?.data ?? []
  const visibleRows = rows.filter((network) => matchesQuery(network, q))

  return (
    <section aria-labelledby="networks-heading">
      <div className="page-header">
        <h2 id="networks-heading">Networks</h2>
        <button
          type="button"
          className="button"
          onClick={() => void networks.refetch()}
          disabled={networks.isFetching}
        >
          {networks.isFetching ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      <p className="source">
        Source: <code>GET /api/v1/networks</code> — site, VLAN id, management and enabled state are filtered
        and paginated by the API; search below only narrows rows already on this page.
      </p>

      <form className="filters" onSubmit={applyFilters} key={formKey} aria-label="Network filters">
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
          <label htmlFor="filter-vlan-id">VLAN id</label>
          <input
            id="filter-vlan-id"
            name="vlan_id"
            type="text"
            defaultValue={vlanId}
            placeholder="e.g. 1, 10, 20"
            autoComplete="off"
          />
          <p className="field-hint">Integer 0–4094. Values outside the range are kept out of the request.</p>
        </div>

        <div className="field">
          <label htmlFor="filter-management">Management</label>
          <input
            id="filter-management"
            name="management"
            type="text"
            defaultValue={management}
            placeholder="e.g. GATEWAY, SWITCH"
            autoComplete="off"
          />
        </div>

        <div className="field">
          <label htmlFor="filter-enabled">Enabled state</label>
          <select id="filter-enabled" name="enabled" defaultValue={enabled}>
            <option value="">All</option>
            <option value="true">Enabled</option>
            <option value="false">Disabled</option>
          </select>
        </div>

        <div className="field">
          <label htmlFor="filter-q">Search</label>
          <input
            id="filter-q"
            name="q"
            type="text"
            defaultValue={q}
            placeholder="name, management, or VLAN id"
            autoComplete="off"
          />
          <p className="field-hint">
            Checks name, management and VLAN id of the rows already loaded for this page. The API has no
            free-text network search yet, so this cannot reach rows on other pages — narrow with
            site, VLAN id or management first.
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

      {networks.isPending && <LoadingNotice label="Loading networks" />}

      {!networks.isPending && networks.error && (
        <ErrorNotice error={networks.error} onRetry={() => void networks.refetch()} />
      )}

      {networks.data && rows.length === 0 && (
        <EmptyNotice
          title={hasServerFilters ? 'No networks match these filters' : 'No networks in the cache'}
          hint={
            hasServerFilters
              ? 'The API answered successfully with zero rows for the current filters. Widen or clear them to see more.'
              : emptyCacheHint(sync.data)
          }
        />
      )}

      {networks.data && rows.length > 0 && visibleRows.length === 0 && (
        <EmptyNotice
          title={`No rows on this page match "${q}"`}
          hint="Search only checks the rows already loaded for this page — clear it, or narrow with site, VLAN id or management and try again."
        />
      )}

      {networks.data && visibleRows.length > 0 && (
        <>
          <div className="table-scroll">
            <table>
              <caption className="sr-only">Networks known to the control plane</caption>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">VLAN</th>
                  <th scope="col">Management</th>
                  <th scope="col">Status</th>
                  <th scope="col">Default</th>
                  <th scope="col">Synced at</th>
                </tr>
              </thead>
              <tbody>
                {visibleRows.map((network) => (
                  <tr key={network.id}>
                    <th scope="row">{network.name}</th>
                    <td>{network.vlan_id ?? <Unknown />}</td>
                    <td>{network.management ?? <Unknown />}</td>
                    <td>
                      {network.enabled === null || network.enabled === undefined ? (
                        <Unknown />
                      ) : network.enabled ? (
                        'Enabled'
                      ) : (
                        'Disabled'
                      )}
                    </td>
                    <td>
                      {network.is_default === null || network.is_default === undefined ? (
                        <Unknown />
                      ) : network.is_default ? (
                        'Yes'
                      ) : (
                        'No'
                      )}
                    </td>
                    <td>{network.synced_at}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pagination
            pagination={networks.data.pagination}
            itemLabel="networks"
            busy={networks.isFetching}
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
