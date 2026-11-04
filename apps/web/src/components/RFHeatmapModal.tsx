import { X, Radio, ShieldCheck, AlertCircle, RefreshCw, Layers } from 'lucide-react'
import { useSiteRFScan } from '../api/queries'

interface RFHeatmapModalProps {
  siteId: string | null
  siteName: string
  isOpen: boolean
  onClose: () => void
}

export function RFHeatmapModal({ siteId, siteName, isOpen, onClose }: RFHeatmapModalProps) {
  const rfScan = useSiteRFScan(isOpen ? siteId : null)

  if (!isOpen) return null

  const data = rfScan.data

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto bg-black/50 backdrop-blur-sm flex items-center justify-center p-4 animate-in fade-in duration-150">
      <div className="w-full max-w-3xl bg-card text-foreground rounded-xl shadow-2xl border border-border overflow-hidden flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-border bg-muted/30">
          <div className="flex items-center gap-3">
            <div className="rounded-lg bg-primary/10 p-2 text-primary">
              <Radio className="h-5 w-5" />
            </div>
            <div>
              <h3 className="text-base font-bold text-foreground">
                RF Spectrum &amp; Interference Analyzer
              </h3>
              <p className="text-xs text-muted-foreground">
                Site: <span className="font-semibold text-foreground">{siteName}</span> · Environmental Rogue AP Scanner
              </p>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void rfScan.refetch()}
              disabled={rfScan.isFetching}
              className="p-1.5 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground"
              title="Rescan Spectrum"
            >
              <RefreshCw className={`h-4 w-4 ${rfScan.isFetching ? 'animate-spin' : ''}`} />
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
        <div className="p-6 overflow-y-auto space-y-6">
          {rfScan.isPending && (
            <div className="py-12 text-center text-sm text-muted-foreground space-y-2">
              <RefreshCw className="h-6 w-6 animate-spin mx-auto text-primary" />
              <p>Scanning surrounding RF spectrum via Cloud Connector Proxy…</p>
            </div>
          )}

          {data && (
            <>
              {/* Cleanest Channel Recommendations Banner */}
              <div className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 p-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                <div className="flex items-center gap-3">
                  <ShieldCheck className="h-6 w-6 text-emerald-500 shrink-0" />
                  <div>
                    <h4 className="text-sm font-bold text-emerald-700 dark:text-emerald-400">
                      Recommended Non-DFS Spectrum Allocation
                    </h4>
                    <p className="text-xs text-muted-foreground mt-0.5">
                      Based on {data.total_rogue_aps} detected neighboring BSSIDs with low airtime contention.
                    </p>
                  </div>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <span className="px-2.5 py-1 rounded-md bg-emerald-600 text-white font-mono text-xs font-bold">
                    5 GHz: Ch {data.cleanest_5g} (HT40)
                  </span>
                  <span className="px-2.5 py-1 rounded-md bg-muted font-mono text-xs font-semibold text-foreground">
                    2.4 GHz: Ch {data.cleanest_24g}
                  </span>
                </div>
              </div>

              {/* 5 GHz Non-DFS Spectrum Chart */}
              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <h4 className="text-xs font-bold uppercase tracking-wider text-muted-foreground flex items-center gap-1.5">
                    <Layers className="h-3.5 w-3.5 text-primary" />
                    5 GHz Spectrum (Non-DFS UNII-1 &amp; UNII-3)
                  </h4>
                  <span className="text-[11px] text-muted-foreground">Enforced Width: HT40</span>
                </div>

                <div className="space-y-2">
                  {data.channels_5g.map((ch) => {
                    const isOptimal = ch.channel === data.cleanest_5g
                    const maxVal = Math.max(...data.channels_5g.map((c) => c.count), 1)
                    const pct = Math.min(Math.round((ch.count / maxVal) * 100), 100)

                    return (
                      <div
                        key={ch.channel}
                        className={`p-3 rounded-lg border transition-all ${
                          isOptimal
                            ? 'border-emerald-500/50 bg-emerald-500/5 shadow-sm'
                            : 'border-border bg-card'
                        }`}
                      >
                        <div className="flex items-center justify-between text-xs mb-1.5">
                          <span className="font-semibold text-foreground flex items-center gap-2">
                            <span className="font-mono bg-muted px-1.5 py-0.5 rounded text-[11px]">
                              Channel {ch.channel}
                            </span>
                            {ch.channel <= 48 ? '(UNII-1 Clean)' : '(UNII-3)'}
                            {isOptimal && (
                              <span className="text-[10px] font-bold text-emerald-600 dark:text-emerald-400 uppercase tracking-wide">
                                ★ Cleanest
                              </span>
                            )}
                          </span>
                          <span className="text-muted-foreground font-mono">
                            {ch.count} interfering rogue BSSIDs
                          </span>
                        </div>
                        {/* Progress Bar */}
                        <div className="w-full bg-muted/60 h-2 rounded-full overflow-hidden">
                          <div
                            className={`h-full rounded-full transition-all duration-500 ${
                              ch.status === 'clean'
                                ? 'bg-emerald-500'
                                : ch.status === 'moderate'
                                ? 'bg-amber-500'
                                : 'bg-rose-500'
                            }`}
                            style={{ width: `${Math.max(pct, 4)}%` }}
                          />
                        </div>
                      </div>
                    )
                  })}
                </div>
              </div>

              {/* 2.4 GHz Legacy Spectrum */}
              <div className="space-y-3">
                <h4 className="text-xs font-bold uppercase tracking-wider text-muted-foreground flex items-center gap-1.5">
                  <Radio className="h-3.5 w-3.5 text-amber-500" />
                  2.4 GHz Non-Overlapping Channels
                </h4>
                <div className="grid grid-cols-3 gap-3">
                  {data.channels_24g.map((ch) => (
                    <div
                      key={ch.channel}
                      className="p-3 rounded-lg border border-border bg-card text-center space-y-1"
                    >
                      <span className="font-mono text-sm font-bold text-foreground">
                        Ch {ch.channel}
                      </span>
                      <p className="text-[11px] text-muted-foreground">{ch.count} detected</p>
                      <span
                        className={`inline-block px-2 py-0.5 rounded-full text-[10px] font-semibold uppercase ${
                          ch.status === 'clean'
                            ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400'
                            : 'bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-400'
                        }`}
                      >
                        {ch.status}
                      </span>
                    </div>
                  ))}
                </div>
              </div>

              {/* Strongest Rogue APs */}
              {data.strongest_interferers.length > 0 && (
                <div className="space-y-2">
                  <h4 className="text-xs font-bold uppercase tracking-wider text-muted-foreground flex items-center gap-1.5">
                    <AlertCircle className="h-3.5 w-3.5 text-rose-500" />
                    Strongest External Transmitters (High Signal RSSI)
                  </h4>
                  <div className="rounded-lg border border-border overflow-hidden">
                    <table className="w-full text-xs">
                      <thead className="bg-muted/50 text-muted-foreground uppercase text-[10px]">
                        <tr>
                          <th className="p-2 text-left">SSID / Network</th>
                          <th className="p-2 text-left">BSSID</th>
                          <th className="p-2 text-left">Channel</th>
                          <th className="p-2 text-right">Signal RSSI</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border">
                        {data.strongest_interferers.map((ap, i) => (
                          <tr key={i} className="hover:bg-muted/30">
                            <td className="p-2 font-medium text-foreground">{ap.essid || '<Hidden SSID>'}</td>
                            <td className="p-2 font-mono text-muted-foreground">{ap.bssid}</td>
                            <td className="p-2 font-mono text-foreground">Ch {ap.channel}</td>
                            <td className="p-2 font-mono text-right text-rose-600 dark:text-rose-400 font-bold">
                              {ap.rssi} dBm
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
