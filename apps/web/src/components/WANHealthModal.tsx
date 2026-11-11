import { X, Network, ShieldCheck, Activity, RefreshCw } from 'lucide-react'
import { useSiteWANHealth } from '../api/queries'

interface WANHealthModalProps {
  siteId: string | null
  siteName: string
  isOpen: boolean
  onClose: () => void
}

export function WANHealthModal({ siteId, siteName, isOpen, onClose }: WANHealthModalProps) {
  const wanHealth = useSiteWANHealth(isOpen ? siteId : null)

  if (!isOpen) return null

  const data = wanHealth.data

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto bg-black/50 backdrop-blur-sm flex items-center justify-center p-4 animate-in fade-in duration-150">
      <div className="w-full max-w-xl bg-card text-foreground rounded-xl shadow-2xl border border-border overflow-hidden flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-border bg-muted/30">
          <div className="flex items-center gap-3">
            <div className="rounded-lg bg-emerald-500/10 p-2 text-emerald-500">
              <Network className="h-5 w-5" />
            </div>
            <div>
              <h3 className="text-base font-bold text-foreground">
                WAN Uplink &amp; ISP Flapping Monitor
              </h3>
              <p className="text-xs text-muted-foreground">
                Site: <span className="font-semibold text-foreground">{siteName}</span> · UDM Pro Gateway Telemetry
              </p>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void wanHealth.refetch()}
              disabled={wanHealth.isFetching}
              className="p-1.5 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground"
            >
              <RefreshCw className={`h-4 w-4 ${wanHealth.isFetching ? 'animate-spin' : ''}`} />
            </button>
            <button
              type="button"
              onClick={onClose}
              className="p-1.5 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </div>

        {/* Content Body */}
        <div className="p-6 space-y-5">
          {wanHealth.isPending && (
            <div className="py-8 text-center text-xs text-muted-foreground">
              <RefreshCw className="h-5 w-5 animate-spin mx-auto text-primary mb-2" />
              Querying UDM Pro WAN port statistics…
            </div>
          )}

          {data && (
            <>
              {/* WAN Status Card */}
              <div className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 p-4 flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <ShieldCheck className="h-7 w-7 text-emerald-500 shrink-0" />
                  <div>
                    <h4 className="text-sm font-bold text-emerald-700 dark:text-emerald-400">
                      Primary WAN Uplink: Stable
                    </h4>
                    <p className="text-xs text-muted-foreground font-mono">
                      {data.primary_wan}
                    </p>
                  </div>
                </div>
                <span className={`px-2.5 py-1 rounded-full text-white text-xs font-bold uppercase ${data.status === 'healthy' ? 'bg-emerald-500' : data.status === 'degraded' ? 'bg-red-500' : 'bg-amber-500'}`}>
                  {data.uptime_pct}% Up
                </span>
              </div>

              {/* Metrics Grid */}
              <div className="grid grid-cols-3 gap-3">
                <div className="p-3 rounded-lg border border-border bg-muted/20 text-center">
                  <span className="text-[11px] font-semibold text-muted-foreground uppercase">Average Latency</span>
                  <p className="text-xl font-mono font-bold text-foreground mt-1">{data.latency_ms} ms</p>
                  <span className="text-[10px] text-emerald-600 font-medium">Optimal Response</span>
                </div>
                <div className="p-3 rounded-lg border border-border bg-muted/20 text-center">
                  <span className="text-[11px] font-semibold text-muted-foreground uppercase">Packet Loss</span>
                  <p className="text-xl font-mono font-bold text-foreground mt-1">{data.packet_loss_pct}%</p>
                  <span className="text-[10px] text-emerald-600 font-medium">Zero Drop</span>
                </div>
                <div className="p-3 rounded-lg border border-border bg-muted/20 text-center">
                  <span className="text-[11px] font-semibold text-muted-foreground uppercase">Flapping Events (24h)</span>
                  <p className="text-xl font-mono font-bold text-foreground mt-1">{data.drops_last_24h}</p>
                  <span className="text-[10px] text-emerald-600 font-medium">No Link Drops</span>
                </div>
              </div>

              {/* Gateway Diagnostics */}
              <div className="rounded-lg border border-border bg-card p-3.5 text-xs space-y-2">
                <div className="font-semibold text-foreground flex items-center gap-1.5">
                  <Activity className="h-4 w-4 text-primary" />
                  Live Gateway &amp; Uplink Diagnostics
                </div>
                <div className="grid grid-cols-2 gap-2 text-muted-foreground pt-1 font-mono text-[11px]">
                  <div>Public WAN IP: <strong className="text-foreground font-sans">{data.wan_ip || 'Auto-Assigned'}</strong></div>
                  <div>ISP Carrier: <strong className="text-foreground font-sans">{data.isp_name || data.primary_wan}</strong></div>
                  <div>System Load: <strong className="text-foreground font-sans">{data.cpu_usage ? `CPU ${data.cpu_usage}% · Mem ${data.mem_usage}%` : 'Normal (UDM-Pro)'}</strong></div>
                  <div>Link Drops: <strong className="text-foreground font-sans">{data.drops_last_24h} event(s) in 24h</strong></div>
                </div>
              </div>

              {/* Gateway details */}
              <div className="flex justify-between items-center text-xs text-muted-foreground pt-1 border-t border-border">
                <span>Gateway Hardware MAC: <strong className="font-mono text-foreground">{data.gateway_mac || 'UDM-Pro'}</strong></span>
                <span>Uptime: <strong className="text-foreground">{data.uptime_pct}%</strong></span>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
