import { useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Users, RefreshCw, Wifi, Cable } from 'lucide-react'

import { useClients, useSites, useSyncStatus } from '../api/queries'
import type { Client, ClientListQuery, SyncStatus } from '../api/types'
import { ClientDrawer } from '../components/ClientDrawer'
import { TopTalkersWidget } from '../components/TopTalkersWidget'
import { useTelemetryWebSocket } from '../hooks/useTelemetryWebSocket'
import { ErrorNotice } from '../components/ErrorNotice'
import { EmptyNotice, LoadingNotice } from '../components/Notices'
import { Pagination } from '../components/Pagination'
import { StatCard } from '../components/StatCard'
import { StatusBadge } from '../components/StatusBadge'
import { delay } from '../lib/utils'
import { pageSizeFrom, positiveInt } from './listParams'

const PAGE_SIZES = [25, 50, 100, 200] as const
const DEFAULT_PAGE_SIZE = 25
const UNKNOWN = <span className="unknown">unknown</span>

function matchesQuery(client: Client, q: string): boolean {
  if (!q) return true
  const needle = q.toLowerCase()
  return [client.hostname, client.mac, client.ip_address].some(
    (value) => typeof value === 'string' && value.toLowerCase().includes(needle),
  )
}

function emptyCacheHint(status: SyncStatus | undefined): string {
  if (status?.unifi?.surface === 'cloud') {
    return 'The cache is filled from the UniFi cloud surface (UNIFI_MODE=cloud), which does not serve clients (ADR-001). Client rows need the console Integration API: run with UNIFI_MODE=local from the office LAN or VPN.'
  }
  return 'The API answered successfully with zero clients. Clients appear once the sync worker has written them.'
}

export default function ClientsPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [selectedClient, setSelectedClient] = useState<Client | null>(null)
  const [isRefreshing, setIsRefreshing] = useState(false)
  const { isConnected: isWsConnected, payload: telemetryData } = useTelemetryWebSocket()
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
  const sites = useSites()
  const siteOptions = sites.data?.data ?? []
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
      await Promise.all([clients.refetch(), sync.refetch()])
    } finally {
      const elapsed = Date.now() - start
      if (elapsed < 500) {
        await delay(500 - elapsed)
      }
      setIsRefreshing(false)
    }
  }
  const hasServerFilters = siteId !== '' || connected !== '' || clientType !== ''
  const hasLocalFilter = q !== ''
  const formKey = `${siteId}|${connected}|${clientType}|${pageSize}`

  const rows = clients.data?.data ?? []
  const visibleRows = rows.filter((client) => matchesQuery(client, q))
  const totalClients = clients.data?.pagination.total ?? 0
  const wirelessCount = rows.filter((c) => c.client_type === 'wireless').length
  const wiredCount = rows.filter((c) => c.client_type === 'wired').length

  return (
    <section aria-labelledby="clients-heading" className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 pb-2">
        <div>
          <h2 id="clients-heading" className="text-2xl font-bold tracking-tight text-foreground">
            Clients
          </h2>
          <p className="source text-sm text-muted-foreground mt-1">
            Source: <code>GET /api/v1/clients</code> — site, connection state and type are filtered
            and paginated by the API; search below only narrows rows already on this page.
          </p>
        </div>
        <div className="flex items-center gap-3">
          <button
            type="button"
            className="button inline-flex items-center gap-2"
            onClick={handleRefresh}
            disabled={isRefreshing || clients.isFetching}
            title={
              sync.data?.last_successful_sync_at
                ? `Last UniFi sync: ${new Date(sync.data.last_successful_sync_at).toLocaleTimeString()}`
                : 'Refresh cache'
            }
          >
            <RefreshCw className={`h-4 w-4 ${isRefreshing || clients.isFetching ? 'animate-spin' : ''}`} />
            <span>{isRefreshing || clients.isFetching ? 'Refreshing…' : 'Refresh'}</span>
          </button>
        </div>
      </div>
      {clients.data && rows.length > 0 && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <StatCard
            title="Total Clients"
            value={totalClients}
            description="Cached active & historical clients"
            icon={Users}
          />
          <StatCard
            title="Wireless Wi-Fi"
            value={wirelessCount}
            description="Radio connected devices"
            icon={Wifi}
            badge={{ text: 'Wireless', variant: 'info' }}
          />
          <StatCard
            title="Wired LAN"
            value={wiredCount}
            description="Ethernet port connections"
            icon={Cable}
          />
        </div>
      )}

      {/* Real-Time Bandwidth & Traffic Spike Monitor */}
      <TopTalkersWidget
        talkers={telemetryData?.top_talkers ?? []}
        isConnected={isWsConnected}
      />

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
            type="search"
            defaultValue={q}
            placeholder="hostname, MAC, or IP"
            autoComplete="off"
            spellCheck="false"
          />
          <p className="field-hint">
            Checks hostname, MAC and IP of the rows already loaded for this page. The API has no
            free-text client search yet, so this cannot reach rows on other pages — narrow with
            site, connection state or type first.
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
                  <tr
                    key={client.id}
                    onClick={() => setSelectedClient(client)}
                    className="cursor-pointer hover:bg-muted/40 transition-colors"
                  >
                    <th scope="row" className="font-semibold text-foreground">
                      {client.hostname ?? <UnknownHost client={client} />}
                    </th>
                    <td>
                      {client.ip_address ? (
                        <span className="font-mono text-xs">{client.ip_address}</span>
                      ) : (
                        UNKNOWN
                      )}
                    </td>
                    <td>
                      {client.mac ? (
                        <span className="font-mono text-xs text-muted-foreground bg-muted/60 px-1.5 py-0.5 rounded">
                          {client.mac}
                        </span>
                      ) : (
                        UNKNOWN
                      )}
                    </td>
                    <td>
                      <StatusBadge status={client.is_connected ? 'Connected' : 'Disconnected'} />
                    </td>
                    <td>{client.ssid ?? UNKNOWN}</td>
                    <td>
                      {client.uplink_device_id ? (
                        <span className="font-mono text-xs text-muted-foreground truncate max-w-[120px] block">
                          {client.uplink_device_id}
                        </span>
                      ) : (
                        UNKNOWN
                      )}
                    </td>
                    <td className="text-xs text-muted-foreground">{client.last_seen}</td>
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

      {/* Client Inspection Drawer with Roaming Timeline */}
      <ClientDrawer client={selectedClient} onClose={() => setSelectedClient(null)} />
        </>
      )}
    </section>
  )
}

function UnknownHost({ client }: { client: Client }) {
  if (client.mac) return <span className="unknown">client-{client.mac.slice(-5)}</span>
  return UNKNOWN
}
