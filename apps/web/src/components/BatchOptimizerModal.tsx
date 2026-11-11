import { useState, useEffect, useRef } from 'react'
import { X, Play, Sliders, CheckCircle2, AlertTriangle, Loader2 } from 'lucide-react'
import type { Device } from '../api/types'
import { useDeviceAction } from '../api/queries'
import { useAuth } from './AuthGate'
interface BatchOptimizerModalProps {
  devices: Device[]
  isOpen: boolean
  onClose: () => void
}

export function BatchOptimizerModal({ devices, isOpen, onClose }: BatchOptimizerModalProps) {
  const { canOperate } = useAuth()
  const aps = devices.filter((d) => d.device_type === 'ap')
  const [isRunning, setIsRunning] = useState(false)
  const [currentIndex, setCurrentIndex] = useState(0)
  const [countdown, setCountdown] = useState(0)
  const [logs, setLogs] = useState<string[]>([])
  const [isCompleted, setIsCompleted] = useState(false)

  const actionMutation = useDeviceAction()
  const timerRef = useRef<number | null>(null)
  const isCancelledRef = useRef<boolean>(false)
  const channelPool = [36, 44, 149, 157, 161] as const

  useEffect(() => {
    if (!isOpen) {
      isCancelledRef.current = true
      if (timerRef.current !== null) {
        clearInterval(timerRef.current)
        timerRef.current = null
      }
      setIsRunning(false)
      setCountdown(0)
    } else {
      isCancelledRef.current = false
    }
    return () => {
      isCancelledRef.current = true
      if (timerRef.current !== null) {
        clearInterval(timerRef.current)
        timerRef.current = null
      }
    }
  }, [isOpen])

  if (!isOpen) return null

  async function startOptimization() {
    isCancelledRef.current = false
    setIsRunning(true)
    setIsCompleted(false)
    setLogs([`[${new Date().toLocaleTimeString()}] Starting sequential optimization across ${aps.length} Access Points...`])
    setCurrentIndex(0)
    await runStep(0)
  }
  async function runStep(index: number) {
    if (isCancelledRef.current) {
      setIsRunning(false)
      return
    }
    if (index >= aps.length) {
      setIsRunning(false)
      setIsCompleted(true)
      setLogs((prev) => [...prev, `[${new Date().toLocaleTimeString()}] ✅ Optimization completed for all ${aps.length} APs.`])
      return
    }
    const ap = aps[index]
    const targetChannel = channelPool[index % channelPool.length]

    setLogs((prev) => [
      ...prev,
      `[${new Date().toLocaleTimeString()}] [${index + 1}/${aps.length}] Tuning ${ap.name ?? ap.mac} ➔ Channel ${targetChannel} (HT40)...`,
    ])

    try {
      await actionMutation.mutateAsync({
        deviceId: ap.id,
        action: {
          action: 'TUNE_RADIO',
          band: '5g',
          channel: targetChannel,
          width: 40,
        },
      })
      setLogs((prev) => [
        ...prev,
        `[${new Date().toLocaleTimeString()}] [+] ${ap.name ?? ap.mac} applied. Waiting 15s safe delay for client seamless handover...`,
      ])

      // 15 seconds countdown
      let remaining = 15
      setCountdown(remaining)
      timerRef.current = setInterval(() => {
        if (isCancelledRef.current) {
          clearInterval(timerRef.current!)
          timerRef.current = null
          return
        }
        remaining -= 1
        setCountdown(remaining)
        if (remaining <= 0) {
          clearInterval(timerRef.current!)
          timerRef.current = null
          if (!isCancelledRef.current) {
            setCurrentIndex(index + 1)
            void runStep(index + 1)
          }
        }
      }, 1000)
    } catch (err: unknown) {
      setLogs((prev) => [
        ...prev,
        `[${new Date().toLocaleTimeString()}] [-] Error tuning ${ap.name ?? ap.mac}: ${err instanceof Error ? err.message : String(err)}`,
      ])
      setIsRunning(false)
    }
  }

  const progressPct = aps.length > 0 ? Math.round(((currentIndex) / aps.length) * 100) : 0

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto bg-black/50 backdrop-blur-sm flex items-center justify-center p-4 animate-in fade-in duration-150">
      <div className="w-full max-w-2xl bg-card text-foreground rounded-xl shadow-2xl border border-border overflow-hidden flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-border bg-muted/30">
          <div className="flex items-center gap-3">
            <div className="rounded-lg bg-primary/10 p-2 text-primary">
              <Sliders className="h-5 w-5" />
            </div>
            <div>
              <h3 className="text-base font-bold text-foreground">
                Sequential Multi-AP Optimizer
              </h3>
              <p className="text-xs text-muted-foreground">
                Strictly 1-by-1 sequential execution with 15s client handover delays
              </p>
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={isRunning}
            className="p-1.5 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground disabled:opacity-50"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body */}
        <div className="p-6 space-y-5">
          {/* Information box */}
          <div className="rounded-lg border border-border bg-muted/20 p-4 text-xs space-y-2">
            <span className="font-semibold text-foreground flex items-center gap-1.5">
              <AlertTriangle className="h-4 w-4 text-amber-500" />
              SOP Zero-Disruption Safeguards
            </span>
            <p className="text-muted-foreground leading-relaxed">
              This routine distributes clean Non-DFS frequencies (UNII-1 &amp; UNII-3) across all{' '}
              <strong>{aps.length} Access Points</strong>. To prevent client drops, updates are
              applied sequentially one AP at a time with a mandatory 15-second association delay.
            </p>
          </div>

          {/* Progress Bar Area */}
          <div className="space-y-2">
            <div className="flex justify-between items-center text-xs">
              <span className="font-semibold text-foreground">
                Progress: {currentIndex} of {aps.length} APs
              </span>
              <span className="font-mono text-muted-foreground">
                {countdown > 0 ? `Waiting ${countdown}s delay…` : `${progressPct}%`}
              </span>
            </div>
            <div className="w-full bg-muted h-3 rounded-full overflow-hidden">
              <div
                className="bg-primary h-full transition-all duration-300 rounded-full"
                style={{ width: `${progressPct}%` }}
              />
            </div>
          </div>

          {/* Console Output Log */}
          <div className="rounded-lg border border-border bg-black/80 p-3 h-44 overflow-y-auto font-mono text-[11px] text-emerald-400 space-y-1">
            {logs.length === 0 ? (
              <span className="text-muted-foreground italic">Ready. Click start to begin sequential optimization.</span>
            ) : (
              logs.map((log, i) => <div key={i}>{log}</div>)
            )}
          </div>

          {/* Action CTAs */}
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={onClose}
              disabled={isRunning}
              className="px-4 py-2 text-xs font-semibold rounded-lg border border-border hover:bg-muted text-muted-foreground"
            >
              Close
            </button>
            {!isRunning && !isCompleted && (
              canOperate ? (
                <button
                  type="button"
                  onClick={startOptimization}
                  className="inline-flex items-center gap-2 px-5 py-2 text-xs font-semibold rounded-lg bg-primary text-primary-foreground hover:opacity-90 transition-opacity"
                >
                  <Play className="h-3.5 w-3.5" />
                  <span>Start Safe Multi-AP Optimization</span>
                </button>
              ) : (
                <span className="inline-flex items-center gap-1.5 px-3 py-2 text-xs text-muted-foreground border border-dashed border-border rounded-lg">
                  🔒 Operator or admin role required
                </span>
              )
            )}
            {isRunning && (
              <>
                <button
                  type="button"
                  disabled
                  className="inline-flex items-center gap-2 px-4 py-2 text-xs font-semibold rounded-lg bg-muted text-muted-foreground cursor-not-allowed"
                >
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  <span>Optimizing ({countdown}s)...</span>
                </button>
                <button
                  type="button"
                  onClick={() => {
                    isCancelledRef.current = true
                    if (timerRef.current !== null) {
                      clearInterval(timerRef.current)
                      timerRef.current = null
                    }
                    setIsRunning(false)
                    setCountdown(0)
                    setLogs((prev) => [...prev, `[${new Date().toLocaleTimeString()}] ⏹️ Optimization stopped by operator.`])
                  }}
                  className="inline-flex items-center gap-1.5 px-3 py-2 text-xs font-semibold rounded-lg bg-rose-600 text-white hover:bg-rose-700 transition-colors shadow-sm"
                >
                  <X className="h-3.5 w-3.5" />
                  <span>Stop</span>
                </button>
              </>
            )}
            {isCompleted && (
              <span className="inline-flex items-center gap-1.5 text-xs font-semibold text-emerald-600 dark:text-emerald-400 py-2">
                <CheckCircle2 className="h-4 w-4" />
                <span>All APs Successfully Optimized</span>
              </span>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
