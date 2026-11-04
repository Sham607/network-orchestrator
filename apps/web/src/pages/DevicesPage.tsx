import { useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Radio, Server, Shield, Activity, RefreshCw, Eye, FileText, Sliders } from 'lucide-react'
import { AuditReportModal } from '../components/AuditReportModal'
import { BatchOptimizerModal } from '../components/BatchOptimizerModal'
import { useDevices, useSites } from '../api/queries'
import type { Device, DeviceListQuery } from '../api/types'
import { DeviceDrawer } from '../components/DeviceDrawer'
import { ErrorNotice } from '../components/ErrorNotice'
import { EmptyNotice, LoadingNotice } from '../components/Notices'
import { Pagination } from '../components/Pagination'
import { StatCard } from '../components/StatCard'
import { StatusBadge } from '../components/StatusBadge'
import { pageSizeFrom, positiveInt } from './listParams'

const PAGE_SIZES = [25, 50, 100, 200] as const
const DEFAULT_PAGE_SIZE = 25
const UNKNOWN = <span className="unknown">unknown</span>

export default function DevicesPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [selectedDevice, setSelectedDevice] = useState<Device | null>(null)
  const [isReportOpen, setIsReportOpen] = useState(false)
  const [isBatchOpen, setIsBatchOpen] = useState(false)
  const page = positiveInt(searchParams.get('page'), 1)
  const pageSize = pageSizeFrom(searchParams.get('page_size'), PAGE_SIZES, DEFAULT_PAGE_SIZE)
  const siteId = (searchParams.get('site_id') ?? '').trim()
  const status = (searchParams.get('status') ?? '').trim()

  const query: DeviceListQuery = { page, page_size: pageSize }
  if (siteId) query.site_id = siteId
  if (status) query.status = status

  const devices = useDevices(query)
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

  // Metrics computation from current loaded dataset
  const totalInCache = devices.data?.pagination.total ?? 0
  const items = devices.data?.data ?? []
  const onlineCount = items.filter(d => (d.status ?? '').toUpperCase() === 'ONLINE').length
  const apCount = items.filter(d => d.device_type === 'ap').length
  const otherCount = items.filter(d => d.device_type !== 'ap').length

  return (
    <section aria-labelledby="devices-heading" className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 pb-2">
        <div>
          <h2 id="devices-heading" className="text-2xl font-bold tracking-tight text-foreground">
            Devices
          </h2>
          <p className="source text-sm text-muted-foreground mt-1">
            Source: <code>GET /api/v1/devices</code> — filtered and paginated by the API, not in the browser.
          </p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <button
            type="button"
            className="button inline-flex items-center gap-2"
            onClick={() => setIsBatchOpen(true)}
            disabled={!devices.data || apCount === 0}
            title="Sequentially optimize AP channels with 15s safe delays"
          >
            <Sliders className="h-4 w-4 text-primary" />
            <span>Auto-Optimize Channels</span>
          </button>
          <button
            type="button"
            className="button inline-flex items-center gap-2"
            onClick={() => setIsReportOpen(true)}
            disabled={!devices.data || devices.data.data.length === 0}
          >
            <FileText className="h-4 w-4" />
            <span>Export Audit Report</span>
          </button>
          <button
            type="button"
            className="button inline-flex items-center gap-2"
            onClick={() => void devices.refetch()}
            disabled={devices.isFetching}
          >
            <RefreshCw className={`h-4 w-4 ${devices.isFetching ? 'animate-spin' : ''}`} />
            <span>{devices.isFetching ? 'Refreshing…' : 'Refresh'}</span>
          </button>
        </div>
      </div>

      {/* KPI Metric Cards */}
      {devices.data && devices.data.data.length > 0 && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          <StatCard
            title="Total Devices"
            value={totalInCache}
            description="Cached control plane devices"
            icon={Activity}
          />
          <StatCard
            title="Online Ratio"
            value={`${onlineCount}/${items.length}`}
            description="Active in current page"
            icon={Shield}
            badge={{ text: 'Healthy', variant: 'success' }}
          />
          <StatCard
            title="Access Points"
            value={apCount}
            description="Wi-Fi AP hardware"
            icon={Radio}
          />
          <StatCard
            title="Switches & GW"
            value={otherCount}
            description="Gateways & switches"
            icon={Server}
          />
        </div>
      )}

      {/* Filter Form Card */}
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
          <label htmlFor="filter-status">Status</label>
          <input
            id="filter-status"
            name="status"
            type="text"
            defaultValue={status}
            placeholder="ONLINE"
            list="filter-status-suggestions"
            autoComplete="off"
            spellCheck="false"
          />
          <datalist id="filter-status-suggestions">
            <option value="ONLINE" />
            <option value="OFFLINE" />
            <option value="ADOPTING" />
            <option value="UPGRADING" />
          </datalist>
          <p className="field-hint">
            The UniFi device state, upper-case (<code>ONLINE</code>, <code>OFFLINE</code>,{' '}
            <code>ADOPTING</code> …). The set is open, so this is free text.
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
            disabled={!hasFilters && pageSize === DEFAULT_PAGE_SIZE}
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
                  <th scope="col" className="text-right">Action</th>
                </tr>
              </thead>
              <tbody>
                {devices.data.data.map((device) => (
                  <tr
                    key={device.id}
                    className="cursor-pointer hover:bg-muted/40 transition-colors"
                    onClick={() => setSelectedDevice(device)}
                  >
                    <th scope="row" className="font-semibold text-foreground">
                      {device.name ?? UNKNOWN}
                    </th>
                    <td>
                      {device.mac ? (
                        <span className="font-mono text-xs text-muted-foreground bg-muted/60 px-1.5 py-0.5 rounded">
                          {device.mac}
                        </span>
                      ) : (
                        UNKNOWN
                      )}
                    </td>
                    <td>{device.model ?? UNKNOWN}</td>
                    <td>
                      <span className="inline-flex items-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                        {device.device_type}
                      </span>
                    </td>
                    <td>
                      {device.status ? <StatusBadge status={device.status} /> : UNKNOWN}
                    </td>
                    <td>
                      {device.ip_address ? (
                        <span className="font-mono text-xs">{device.ip_address}</span>
                      ) : (
                        UNKNOWN
                      )}
                    </td>
                    <td>
                      {device.firmware_version ? (
                        <span className="text-xs text-muted-foreground">{device.firmware_version}</span>
                      ) : (
                        UNKNOWN
                      )}
                    </td>
                    <td className="text-xs text-muted-foreground">{device.synced_at}</td>
                    <td className="text-right" onClick={(e) => e.stopPropagation()}>
                      <button
                        type="button"
                        onClick={() => setSelectedDevice(device)}
                        className="inline-flex items-center gap-1 text-xs text-primary hover:text-primary/80 font-medium p-1 rounded hover:bg-muted transition-colors"
                        title="Inspect Device"
                      >
                        <Eye className="h-3.5 w-3.5" />
                        <span>Inspect</span>
                      </button>
                    </td>
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

      {/* Slide-over Inspection Sheet */}
      <DeviceDrawer device={selectedDevice} onClose={() => setSelectedDevice(null)} />

      {/* Audit Report Modal */}
      <AuditReportModal devices={items} isOpen={isReportOpen} onClose={() => setIsReportOpen(false)} />

      {/* Batch Sequential Optimizer Modal */}
      <BatchOptimizerModal devices={items} isOpen={isBatchOpen} onClose={() => setIsBatchOpen(false)} />
    </section>
  )
}
