import { Activity, Flame, Wifi, ArrowDown, ArrowUp } from 'lucide-react'
import type { ClientSpikeMetric } from '../hooks/useTelemetryWebSocket'

interface TopTalkersWidgetProps {
  talkers: ClientSpikeMetric[]
  isConnected: boolean
}

export function TopTalkersWidget({ talkers, isConnected }: TopTalkersWidgetProps) {
  const maxSpeed = Math.max(...talkers.map((t) => t.download_mbps), 50)

  return (
    <div className="rounded-xl border border-border bg-card p-5 shadow-sm space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Activity className="h-4 w-4 text-primary" />
          <h3 className="text-sm font-bold text-foreground">
            Top Talkers &amp; Traffic Spikes
          </h3>
        </div>
        <span className="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
          <span
            className={`h-2 w-2 rounded-full ${
              isConnected ? 'bg-emerald-500 animate-pulse' : 'bg-amber-500'
            }`}
          />
          {isConnected ? 'Real-Time Stream' : 'Connecting…'}
        </span>
      </div>

      {talkers.length === 0 ? (
        <p className="text-xs text-muted-foreground py-4 text-center italic">
          Listening for client throughput delta…
        </p>
      ) : (
        <div className="space-y-3">
          {talkers.map((client) => {
            const pct = Math.min(Math.round((client.download_mbps / maxSpeed) * 100), 100)
            const isCriticalSpike = client.is_spike || client.download_mbps >= 50

            return (
              <div
                key={client.mac}
                className={`p-3 rounded-lg border transition-all ${
                  isCriticalSpike
                    ? 'border-rose-500/50 bg-rose-500/5'
                    : 'border-border/60 bg-muted/20'
                }`}
              >
                <div className="flex items-center justify-between text-xs mb-1.5">
                  <div className="flex items-center gap-2 min-w-0">
                    <Wifi className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                    <span className="font-bold text-foreground truncate max-w-[140px] sm:max-w-[200px]">
                      {client.hostname || client.mac}
                    </span>
                    <span className="text-[10px] text-muted-foreground font-mono truncate max-w-[120px]">
                      via {client.ap_name}
                    </span>
                  </div>

                  <div className="flex items-center gap-2 shrink-0">
                    {isCriticalSpike && (
                      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-bold bg-rose-500 text-white animate-pulse">
                        <Flame className="h-3 w-3" />
                        SPIKE
                      </span>
                    )}
                    <span className="font-mono font-bold text-foreground flex items-center gap-1">
                      <ArrowDown className="h-3 w-3 text-primary" />
                      {client.download_mbps} Mbps
                    </span>
                    <span className="font-mono text-[11px] text-muted-foreground flex items-center">
                      <ArrowUp className="h-3 w-3 text-muted-foreground" />
                      {client.upload_mbps}
                    </span>
                  </div>
                </div>

                {/* Throughput Bar */}
                <div className="w-full bg-muted/60 h-1.5 rounded-full overflow-hidden">
                  <div
                    className={`h-full rounded-full transition-all duration-300 ${
                      isCriticalSpike
                        ? 'bg-rose-500'
                        : client.download_mbps > 20
                        ? 'bg-amber-500'
                        : 'bg-primary'
                    }`}
                    style={{ width: `${Math.max(pct, 4)}%` }}
                  />
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
