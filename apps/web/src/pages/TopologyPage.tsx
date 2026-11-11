import { useState } from 'react'
import {
  Globe,
  Server,
  Shield,
  Layers,
  ArrowDown,
  RefreshCw,
  ExternalLink,
} from 'lucide-react'

import { useDevices, useSites, useSiteWANHealth } from '../api/queries'
import type { Device } from '../api/types'
import { DeviceDrawer } from '../components/DeviceDrawer'

export default function TopologyPage() {
  const [selectedSiteId, setSelectedSiteId] = useState<string>('')
  const [inspectDevice, setInspectDevice] = useState<Device | null>(null)

  const sites = useSites()
  const siteList = sites.data?.data ?? []

  // Default to first site if none selected
  const activeSiteId = selectedSiteId || (siteList.length > 0 ? siteList[0].id : '')
  const activeSite = siteList.find((s) => s.id === activeSiteId)
  const wanHealth = useSiteWANHealth(activeSiteId || null)
  const ispDisplay = wanHealth.data?.isp_name || wanHealth.data?.primary_wan || 'PT Telkom Indonesia'

  const devicesQuery = useDevices({ page: 1, page_size: 100, site_id: activeSiteId || undefined })
  const devices = devicesQuery.data?.data ?? []

  const gateways = devices.filter((d) => d.device_type === 'gateway')
  const switches = devices.filter((d) => d.device_type === 'switch')
  const aps = devices.filter((d) => d.device_type === 'ap')

  return (
    <section aria-labelledby="topology-heading" className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 pb-2">
        <div>
          <h2 id="topology-heading" className="text-2xl font-bold tracking-tight text-foreground">
            Network Topology Graph
          </h2>
          <p className="text-sm text-muted-foreground mt-1">
            Visual hierarchy from Internet WAN Gateway ➔ Distribution Switches ➔ Access Points.
          </p>
        </div>

        {/* Site Picker & Actions */}
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <label className="text-xs font-semibold text-muted-foreground">Site:</label>
            <select
              value={activeSiteId}
              onChange={(e) => setSelectedSiteId(e.target.value)}
              className="text-xs font-medium py-1.5 px-3 rounded-lg border border-border bg-card"
            >
              {siteList.map((site) => (
                <option key={site.id} value={site.id}>
                  {site.name}
                </option>
              ))}
            </select>
          </div>
          <button
            type="button"
            className="button inline-flex items-center gap-1.5 py-1.5"
            onClick={() => void devicesQuery.refetch()}
            disabled={devicesQuery.isFetching}
          >
            <RefreshCw className={`h-3.5 w-3.5 ${devicesQuery.isFetching ? 'animate-spin' : ''}`} />
            <span>Refresh</span>
          </button>
        </div>
      </div>

      {/* Topology Canvas Area */}
      <div className="rounded-2xl border border-border bg-card/60 p-8 shadow-sm flex flex-col items-center space-y-8 relative overflow-hidden">
        {/* Level 0: External ISP Cloud */}
        <div className="flex flex-col items-center">
          <div className="px-5 py-2.5 rounded-full border border-border bg-muted/50 text-xs font-semibold text-foreground flex items-center gap-2 shadow-sm">
            <Globe className="h-4 w-4 text-primary" />
            <span>Internet WAN ({ispDisplay})</span>
          </div>
          <ArrowDown className="h-5 w-5 text-muted-foreground my-2 animate-bounce" />
        </div>

        {/* Level 1: Gateway Consoles */}
        <div className="flex flex-wrap justify-center gap-6 w-full max-w-4xl">
          {gateways.length > 0 ? (
            gateways.map((gw) => (
              <div
                key={gw.id}
                onClick={() => setInspectDevice(gw)}
                className="cursor-pointer p-4 rounded-xl border border-primary/30 bg-primary/5 hover:border-primary transition-all flex items-center gap-3 w-80 shadow-sm"
              >
                <div className="p-2.5 rounded-lg bg-primary text-primary-foreground">
                  <Shield className="h-5 w-5" />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between">
                    <span className="font-bold text-xs text-foreground truncate">{gw.name ?? 'UDM-Pro'}</span>
                    <span className="text-[10px] font-bold px-1.5 py-0.5 rounded bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400">
                      ONLINE
                    </span>
                  </div>
                  <p className="text-[11px] font-mono text-muted-foreground">{gw.ip_address ?? '192.168.1.1'}</p>
                  <p className="text-[10px] text-muted-foreground">Model: {gw.model}</p>
                </div>
              </div>
            ))
          ) : (
            <div className="p-4 rounded-xl border border-primary/30 bg-primary/5 flex items-center gap-3 w-80 shadow-sm">
              <div className="p-2.5 rounded-lg bg-primary text-primary-foreground">
                <Shield className="h-5 w-5" />
              </div>
              <div>
                <span className="font-bold text-xs text-foreground">{activeSite?.name ?? 'Site Gateway'}</span>
                <p className="text-[11px] font-mono text-muted-foreground">{activeSite?.gateway_mac ?? 'UDM-Pro Gateway'}</p>
              </div>
            </div>
          )}
        </div>

        <ArrowDown className="h-5 w-5 text-muted-foreground" />

        {/* Level 2: Core Distribution Switches */}
        {switches.length > 0 && (
          <>
            <div className="flex flex-wrap justify-center gap-4 w-full max-w-4xl">
              {switches.map((sw) => (
                <div
                  key={sw.id}
                  onClick={() => setInspectDevice(sw)}
                  className="cursor-pointer p-3.5 rounded-xl border border-emerald-500/30 bg-emerald-500/5 hover:border-emerald-500 transition-all flex items-center gap-3 w-72 shadow-sm"
                >
                  <div className="p-2 rounded-lg bg-emerald-600 text-white">
                    <Server className="h-4 w-4" />
                  </div>
                  <div className="min-w-0 flex-1 text-xs">
                    <span className="font-bold text-foreground truncate block">{sw.name ?? 'USW Core Switch'}</span>
                    <span className="font-mono text-muted-foreground text-[11px]">{sw.ip_address}</span>
                  </div>
                </div>
              ))}
            </div>
            <ArrowDown className="h-5 w-5 text-muted-foreground" />
          </>
        )}

        {/* Level 3: Access Points Grid */}
        <div className="w-full">
          <div className="text-center mb-4">
            <span className="text-xs font-bold uppercase tracking-wider text-muted-foreground flex items-center justify-center gap-1.5">
              <Layers className="h-4 w-4 text-primary" />
              Wi-Fi Access Points ({aps.length} units deployed)
            </span>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
            {aps.map((ap) => (
              <div
                key={ap.id}
                onClick={() => setInspectDevice(ap)}
                className="cursor-pointer p-3.5 rounded-xl border border-border bg-card hover:border-primary hover:shadow-md transition-all space-y-2 group"
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                    <span className="font-bold text-xs text-foreground group-hover:text-primary transition-colors">
                      {ap.name ?? 'Access Point'}
                    </span>
                  </div>
                  <ExternalLink className="h-3 w-3 text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity" />
                </div>

                <div className="text-[11px] text-muted-foreground font-mono space-y-0.5">
                  <p>IP: <span className="text-foreground">{ap.ip_address ?? '-'}</span></p>
                  <p>MAC: <span>{ap.mac}</span></p>
                  <p>Model: <span className="text-foreground">{ap.model}</span></p>
                </div>

                <div className="pt-2 border-t border-border flex items-center justify-between text-[10px]">
                  <span className="px-2 py-0.5 rounded bg-muted font-mono font-semibold text-foreground">
                    Firmware {ap.firmware_version ?? '6.8.2'}
                  </span>
                  <span className="text-emerald-600 dark:text-emerald-400 font-semibold uppercase">
                    Active
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Drawer */}
      <DeviceDrawer device={inspectDevice} onClose={() => setInspectDevice(null)} />
    </section>
  )
}
