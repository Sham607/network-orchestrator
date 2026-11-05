import { Zap, WifiOff, AlertTriangle } from 'lucide-react'
import type { TelemetryPayload } from '../hooks/useTelemetryWebSocket'
import { LiveSparkline } from './LiveSparkline'

interface LiveTickerProps {
  isConnected: boolean
  payload: TelemetryPayload | null
  latencyHistory: number[]
  lossHistory: number[]
}

export function LiveTicker({
  isConnected,
  payload,
  latencyHistory,
  lossHistory,
}: LiveTickerProps) {
  const lossPct = payload?.global_loss_pct ?? 0.0
  const latencyMs = payload?.avg_latency_ms ?? 12.4
  const isLossAlert = lossPct >= 2.0

  return (
    <div className="flex items-center gap-4 text-xs">
      {/* Stream Status Badge */}
      <div className="hidden lg:flex items-center gap-1.5 px-2.5 py-1 rounded-full border border-border bg-card shadow-sm">
        {isConnected ? (
          <>
            <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
            <Zap className="h-3 w-3 text-primary" />
            <span className="font-semibold text-foreground text-[11px]">NOC Live Stream</span>
          </>
        ) : (
          <>
            <span className="h-2 w-2 rounded-full bg-amber-500" />
            <WifiOff className="h-3 w-3 text-muted-foreground" />
            <span className="text-muted-foreground text-[11px]">Connecting…</span>
          </>
        )}
      </div>

      {/* Latency Sparkline */}
      <div className="hidden sm:flex items-center gap-2 px-3 py-1 rounded-lg border border-border bg-card shadow-sm">
        <span className="text-[11px] text-muted-foreground uppercase font-semibold">RTT</span>
        <span className="font-mono font-bold text-foreground">{latencyMs} ms</span>
        <div className="w-16 h-5 flex items-center">
          <LiveSparkline data={latencyHistory} color="#3b82f6" height={18} width={64} />
        </div>
      </div>

      {/* Packet Loss Indicator */}
      <div
        className={`flex items-center gap-2 px-3 py-1 rounded-lg border shadow-sm transition-all ${
          isLossAlert
            ? 'border-rose-500/50 bg-rose-500/10 text-rose-700 dark:text-rose-400'
            : 'border-border bg-card text-foreground'
        }`}
      >
        <span className="text-[11px] uppercase font-semibold text-muted-foreground flex items-center gap-1">
          {isLossAlert && <AlertTriangle className="h-3 w-3 text-rose-500 animate-pulse" />}
          Loss
        </span>
        <span className={`font-mono font-bold ${isLossAlert ? 'text-rose-600' : 'text-emerald-600'}`}>
          {lossPct}%
        </span>
        <div className="hidden md:flex w-14 h-5 items-center">
          <LiveSparkline
            data={lossHistory}
            color={isLossAlert ? '#ef4444' : '#10b981'}
            height={18}
            width={56}
          />
        </div>
      </div>
    </div>
  )
}
