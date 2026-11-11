import { useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Network as NetworkIcon, RefreshCw, Shield, Layers } from 'lucide-react'

import { useNetworks, useSites, useSyncStatus } from '../api/queries'
import type { Network, NetworkListQuery, SyncStatus } from '../api/types'
import { ErrorNotice } from '../components/ErrorNotice'
import { EmptyNotice, LoadingNotice } from '../components/Notices'
import { Pagination } from '../components/Pagination'
import { StatCard } from '../components/StatCard'
import { delay } from '../lib/utils'
import { pageSizeFrom, positiveInt } from './listParams'

const PAGE_SIZES = [25, 50, 100, 200] as const
const DEFAULT_PAGE_SIZE = 25
const UNKNOWN = <span className="unknown">unknown</span>

function parseVlanId(raw: string | null): number | undefined {
  if (!raw) return undefined
  const trimmed = raw.trim()
  if (!trimmed) return undefined
  const n = Number(trimmed)
  if (!Number.isInteger(n) || n < 0 || n > 4094) return undefined
  return n
}

function matchesQuery(network: Network, q: string): boolean {
  if (!q) return true
  const needle = q.toLowerCase()
  const vlanStr = network.vlan_id !== null && network.vlan_id !== undefined ? String(network.vlan_id) : ''
  return [network.name, network.management, vlanStr].some(
    (value) => typeof value === 'string' && value.toLowerCase().includes(needle),
  )
}

function emptyCacheHint(status: SyncStatus | undefined): string {
  if (status?.unifi?.surface === 'cloud') {
    return 'The cache is filled from the UniFi cloud surface (UNIFI_MODE=cloud), which does not serve networks (ADR-001). Network rows need the console Integration API: run with UNIFI_MODE=local from the office LAN or VPN.'
  }
  return 'The API answered successfully with zero networks. Networks appear once the sync worker has written them.'
}

export default function NetworksPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [isRefreshing, setIsRefreshing] = useState(false)
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

  async function handleRefresh() {
    setIsRefreshing(true)
    const start = Date.now()
    try {
      await Promise.all([networks.refetch(), sync.refetch()])
    } finally {
      const elapsed = Date.now() - start
      if (elapsed < 500) {
        await delay(500 - elapsed)
      }
      setIsRefreshing(false)
    }
  }

  const hasServerFilters = siteId !== '' || vlanId !== '' || management !== '' || enabled !== ''
  const hasLocalFilter = q !== ''
  const formKey = `${siteId}|${vlanId}|${management}|${enabled}|${pageSize}`

  const rows = networks.data?.data ?? []
  const visibleRows = rows.filter((network) => matchesQuery(network, q))
  const totalNetworks = networks.data?.pagination.total ?? 0
  const enabledCount = rows.filter((n) => n.enabled).length

  return (
    <section aria-labelledby="networks-heading" className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 pb-2">
        <div>
          <h2 id="networks-heading" className="text-2xl font-bold tracking-tight text-foreground">
            Networks
          </h2>
          <p className="source text-sm text-muted-foreground mt-1">
            Source: <code>GET /api/v1/networks</code> — site, VLAN id, management and enabled state
            are filtered and paginated by the API; search below only narrows rows already on this
            page.
          </p>
        </div>
        <div className="flex items-center gap-3">
          <button
            type="button"
            className="button inline-flex items-center gap-2"
            onClick={handleRefresh}
            disabled={isRefreshing || networks.isFetching}
            title={
              sync.data?.last_successful_sync_at
                ? `Last UniFi sync: ${new Date(sync.data.last_successful_sync_at).toLocaleTimeString()}`
                : 'Refresh cache'
            }
          >
            <RefreshCw className={`h-4 w-4 ${isRefreshing || networks.isFetching ? 'animate-spin' : ''}`} />
            <span>{isRefreshing || networks.isFetching ? 'Refreshing…' : 'Refresh'}</span>
          </button>
        </div>
      </div>

      {networks.data && rows.length > 0 && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <StatCard
            title="Total Networks"
            value={totalNetworks}
            description="VLAN configurations"
            icon={NetworkIcon}
          />
          <StatCard
            title="Enabled Status"
            value={`${enabledCount} / ${rows.length}`}
            description="Active subnets on current page"
            icon={Shield}
            badge={{ text: 'Configured', variant: 'info' }}
          />
          <StatCard
            title="Isolated VLANs"
            value={rows.filter((n) => n.vlan_id && n.vlan_id > 1).length}
            description="VLAN ID tagged segments"
            icon={Layers}
          />
        </div>
      )}

      <form
        className="filters"
        onSubmit={applyFilters}
        key={formKey}
        aria-label="Network filters"
      >
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
            spellCheck="false"
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
            spellCheck="false"
          />
          <p className="field-hint">Integer 0--4094. Values outside the range are kept out of the request.</p>
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
            spellCheck="false"
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
            type="search"
            defaultValue={q}
            placeholder="name, management, or VLAN id"
            autoComplete="off"
            spellCheck="false"
          />
          <p className="field-hint">
            Checks name, management and VLAN id of the rows already loaded for this page. The API
            has no free-text network search yet, so this cannot reach rows on other pages — narrow
            with site, VLAN id or management first.
          </p>
        </div>

        <div className="field">
          <label htmlFor="filter-page-size">Rows per page</label>
          <select id="filter-page-size" name="page_size" defaultValue={pageSize}>
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
            disabled={!hasServerFilters && !hasLocalFilter && pageSize === DEFAULT_PAGE_SIZE}
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
                  <tr key={network.id} className="hover:bg-muted/40 transition-colors">
                    <th scope="row" className="font-semibold text-foreground">{network.name}</th>
                    <td>{network.vlan_id ?? UNKNOWN}</td>
                    <td>{network.management ?? UNKNOWN}</td>
                    <td>
                      {network.enabled === null || network.enabled === undefined ? (
                        UNKNOWN
                      ) : network.enabled ? (
                        <span className="inline-flex items-center gap-1.5 text-xs font-medium text-emerald-600 dark:text-emerald-400">
                          <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                          Enabled
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
                          <span className="h-1.5 w-1.5 rounded-full bg-muted-foreground" />
                          Disabled
                        </span>
                      )}
                    </td>
                    <td>
                      {network.is_default === null || network.is_default === undefined ? (
                        UNKNOWN
                      ) : network.is_default ? (
                        <span className="text-xs font-semibold text-primary">Yes</span>
                      ) : (
                        <span className="text-xs text-muted-foreground">No</span>
                      )}
                    </td>
                    <td className="text-xs text-muted-foreground">{network.synced_at}</td>
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
