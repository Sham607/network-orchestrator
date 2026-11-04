import { useState } from 'react'
import {
  Copy,
  Check,
  X,
  Radio,
  Server,
  Shield,
  Laptop,
  RotateCw,
  AlertTriangle,
  Sliders,
  CheckCircle2,
} from 'lucide-react'
import type { Device } from '../api/types'
import { useDeviceAction } from '../api/queries'
import { StatusBadge } from './StatusBadge'

interface DeviceDrawerProps {
  device: Device | null
  onClose: () => void
}

export function DeviceDrawer({ device, onClose }: DeviceDrawerProps) {
  const [copied, setCopied] = useState(false)
  const [activeTab, setActiveTab] = useState<'details' | 'tune' | 'json'>('details')
  const [confirmRestart, setConfirmRestart] = useState(false)
  const [actionSuccess, setActionSuccess] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  // Channel Tuning state
  const [targetBand, setTargetBand] = useState<'5g' | '2.4g'>('5g')
  const [targetChannel, setTargetChannel] = useState<number>(36)
  const [targetWidth, setTargetWidth] = useState<number>(40)

  const actionMutation = useDeviceAction()

  if (!device) return null

  function copyJson() {
    if (!device) return
    void navigator.clipboard.writeText(JSON.stringify(device, null, 2))
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  function getDeviceIcon(type: string) {
    switch (type) {
      case 'ap':
        return <Radio className="h-5 w-5 text-primary" />
      case 'switch':
        return <Server className="h-5 w-5 text-emerald-500" />
      case 'gateway':
        return <Shield className="h-5 w-5 text-indigo-500" />
      default:
        return <Laptop className="h-5 w-5 text-muted-foreground" />
    }
  }

  async function handleRestart() {
    if (!device) return
    setActionError(null)
    setActionSuccess(null)
    try {
      const res = await actionMutation.mutateAsync({
        deviceId: device.id,
        action: { action: 'RESTART' },
      })
      setActionSuccess(`Restart queued successfully! Audit record #${res.audit_id}`)
      setConfirmRestart(false)
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleTuneRadio() {
    if (!device) return
    setActionError(null)
    setActionSuccess(null)
    try {
      const res = await actionMutation.mutateAsync({
        deviceId: device.id,
        action: {
          action: 'TUNE_RADIO',
          band: targetBand,
          channel: targetChannel,
          width: targetWidth,
        },
      })
      setActionSuccess(
        `Channel migration to Ch ${targetChannel} (${targetWidth}MHz) accepted! Audit #${res.audit_id}`,
      )
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="fixed inset-0 z-50 overflow-hidden bg-black/40 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="fixed inset-y-0 right-0 flex max-w-full pl-10">
        <div className="w-screen max-w-md transform bg-card shadow-2xl transition-all border-l border-border flex flex-col">
          {/* Header */}
          <div className="flex items-center justify-between border-b border-border px-6 py-4">
            <div className="flex items-center gap-3">
              <div className="rounded-lg bg-muted p-2">
                {getDeviceIcon(device.device_type)}
              </div>
              <div>
                <h3 className="text-base font-semibold text-foreground">
                  {device.name ?? 'Unnamed Device'}
                </h3>
                <p className="text-xs font-mono text-muted-foreground">{device.mac}</p>
              </div>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground transition-colors"
              aria-label="Close panel"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {/* Action Success / Error Banners */}
          {actionSuccess && (
            <div className="mx-6 mt-4 p-3 rounded-lg bg-emerald-50 text-emerald-800 dark:bg-emerald-950/60 dark:text-emerald-300 border border-emerald-200 dark:border-emerald-800 text-xs flex items-center gap-2">
              <CheckCircle2 className="h-4 w-4 text-emerald-500 shrink-0" />
              <span>{actionSuccess}</span>
            </div>
          )}

          {actionError && (
            <div className="mx-6 mt-4 p-3 rounded-lg bg-rose-50 text-rose-800 dark:bg-rose-950/60 dark:text-rose-300 border border-rose-200 dark:border-rose-800 text-xs flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 text-rose-500 shrink-0" />
              <span>{actionError}</span>
            </div>
          )}

          {/* Navigation Tabs */}
          <div className="flex border-b border-border px-6">
            <button
              type="button"
              onClick={() => setActiveTab('details')}
              className={`py-3 text-xs font-semibold border-b-2 transition-all mr-6 ${
                activeTab === 'details'
                  ? 'border-primary text-primary'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              }`}
            >
              Specifications
            </button>
            {device.device_type === 'ap' && (
              <button
                type="button"
                onClick={() => setActiveTab('tune')}
                className={`py-3 text-xs font-semibold border-b-2 transition-all mr-6 ${
                  activeTab === 'tune'
                    ? 'border-primary text-primary'
                    : 'border-transparent text-muted-foreground hover:text-foreground'
                }`}
              >
                Radio Tuning
              </button>
            )}
            <button
              type="button"
              onClick={() => setActiveTab('json')}
              className={`py-3 text-xs font-semibold border-b-2 transition-all ${
                activeTab === 'json'
                  ? 'border-primary text-primary'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              }`}
            >
              Raw Payload
            </button>
          </div>

          {/* Body */}
          <div className="flex-1 overflow-y-auto p-6 space-y-6">
            {activeTab === 'details' && (
              <>
                <div className="rounded-lg border border-border bg-muted/30 p-4 space-y-3">
                  <div className="flex justify-between items-center text-xs">
                    <span className="text-muted-foreground">Status</span>
                    <StatusBadge status={device.status} />
                  </div>
                  <div className="flex justify-between items-center text-xs">
                    <span className="text-muted-foreground">Model</span>
                    <span className="font-medium text-foreground">{device.model ?? 'unknown'}</span>
                  </div>
                  <div className="flex justify-between items-center text-xs">
                    <span className="text-muted-foreground">Device Type</span>
                    <span className="font-semibold uppercase tracking-wider text-xs text-primary">
                      {device.device_type}
                    </span>
                  </div>
                  <div className="flex justify-between items-center text-xs">
                    <span className="text-muted-foreground">IP Address</span>
                    <span className="font-mono text-foreground font-medium">
                      {device.ip_address ?? 'unknown'}
                    </span>
                  </div>
                  <div className="flex justify-between items-center text-xs">
                    <span className="text-muted-foreground">Firmware Version</span>
                    <span className="font-mono text-foreground">{device.firmware_version ?? 'unknown'}</span>
                  </div>
                  <div className="flex justify-between items-center text-xs">
                    <span className="text-muted-foreground">Site ID</span>
                    <span className="font-mono text-xs text-muted-foreground truncate max-w-[180px]">
                      {device.site_id}
                    </span>
                  </div>
                  <div className="flex justify-between items-center text-xs">
                    <span className="text-muted-foreground">Last Cache Sync</span>
                    <span className="text-muted-foreground">{device.synced_at}</span>
                  </div>
                </div>

                {/* Maintenance Actions Section */}
                {device.device_type === 'ap' && (
                  <div className="rounded-lg border border-border p-4 bg-card space-y-3">
                    <h4 className="text-xs font-semibold uppercase tracking-wider text-foreground flex items-center gap-2">
                      <RotateCw className="h-3.5 w-3.5 text-primary" />
                      Maintenance Operations
                    </h4>
                    <p className="text-xs text-muted-foreground">
                      Safely reboot hung APs (e.g. zero-client radio freeze) without disturbing network routing.
                    </p>

                    {!confirmRestart ? (
                      <button
                        type="button"
                        onClick={() => setConfirmRestart(true)}
                        disabled={actionMutation.isPending}
                        className="w-full inline-flex items-center justify-center gap-2 py-2 px-3 text-xs font-medium rounded-md border border-border bg-muted/60 hover:bg-muted text-foreground transition-colors"
                      >
                        <RotateCw className="h-3.5 w-3.5" />
                        <span>Reboot Access Point</span>
                      </button>
                    ) : (
                      <div className="p-3 rounded-lg border border-amber-500/30 bg-amber-500/10 space-y-2">
                        <p className="text-xs font-medium text-amber-700 dark:text-amber-400">
                          Confirm rebooting this Access Point?
                        </p>
                        <div className="flex gap-2">
                          <button
                            type="button"
                            onClick={handleRestart}
                            disabled={actionMutation.isPending}
                            className="flex-1 py-1.5 text-xs font-semibold rounded bg-amber-600 hover:bg-amber-700 text-white transition-colors"
                          >
                            {actionMutation.isPending ? 'Sending…' : 'Yes, Reboot'}
                          </button>
                          <button
                            type="button"
                            onClick={() => setConfirmRestart(false)}
                            disabled={actionMutation.isPending}
                            className="py-1.5 px-3 text-xs font-medium rounded border border-border bg-card text-muted-foreground"
                          >
                            Cancel
                          </button>
                        </div>
                      </div>
                    )}
                  </div>
                )}
              </>
            )}

            {activeTab === 'tune' && (
              <div className="space-y-4">
                <div className="p-3 rounded-lg border border-border bg-muted/20 text-xs space-y-1">
                  <span className="font-semibold text-foreground flex items-center gap-1.5">
                    <Sliders className="h-3.5 w-3.5 text-primary" />
                    SOP Non-DFS Spectrum Tuning
                  </span>
                  <p className="text-muted-foreground">
                    Per guidelines, 5 GHz channels strictly enforce <strong>HT40</strong> width and cleanest Non-DFS frequencies (UNII-1 &amp; UNII-3).
                  </p>
                </div>

                <div className="space-y-3">
                  <div className="field">
                    <label className="text-xs font-semibold">Radio Band</label>
                    <select
                      value={targetBand}
                      onChange={(e) => {
                        const val = e.target.value as '5g' | '2.4g'
                        setTargetBand(val)
                        setTargetChannel(val === '5g' ? 36 : 1)
                        setTargetWidth(val === '5g' ? 40 : 20)
                      }}
                      className="text-xs"
                    >
                      <option value="5g">5 GHz (High Throughput)</option>
                      <option value="2.4g">2.4 GHz (Legacy IoT)</option>
                    </select>
                  </div>

                  <div className="field">
                    <label className="text-xs font-semibold">Target Clean Channel</label>
                    <select
                      value={targetChannel}
                      onChange={(e) => setTargetChannel(Number(e.target.value))}
                      className="text-xs"
                    >
                      {targetBand === '5g' ? (
                        <>
                          <option value={36}>Channel 36 (UNII-1 Non-DFS - Clean)</option>
                          <option value={44}>Channel 44 (UNII-1 Non-DFS - Clean)</option>
                          <option value={149}>Channel 149 (UNII-3 Non-DFS)</option>
                          <option value={157}>Channel 157 (UNII-3 Non-DFS)</option>
                          <option value={161}>Channel 161 (UNII-3 Non-DFS)</option>
                        </>
                      ) : (
                        <>
                          <option value={1}>Channel 1 (Non-overlapping)</option>
                          <option value={6}>Channel 6 (Non-overlapping)</option>
                          <option value={11}>Channel 11 (Non-overlapping)</option>
                        </>
                      )}
                    </select>
                  </div>

                  <div className="field">
                    <label className="text-xs font-semibold">Channel Width</label>
                    <input
                      type="text"
                      readOnly
                      value={targetBand === '5g' ? 'HT40 (Mandated Policy)' : 'HT20 (Standard)'}
                      className="text-xs bg-muted text-muted-foreground cursor-not-allowed"
                    />
                  </div>

                  <button
                    type="button"
                    onClick={handleTuneRadio}
                    disabled={actionMutation.isPending}
                    className="w-full mt-4 py-2 px-4 text-xs font-semibold rounded-lg bg-primary text-primary-foreground hover:opacity-90 transition-opacity"
                  >
                    {actionMutation.isPending ? 'Migrating Channel…' : `Apply Channel ${targetChannel} (${targetWidth}MHz)`}
                  </button>
                </div>
              </div>
            )}

            {activeTab === 'json' && (
              <div className="relative">
                <div className="flex justify-between items-center mb-2">
                  <span className="text-xs font-semibold text-muted-foreground uppercase">Cached JSON</span>
                  <button
                    type="button"
                    onClick={copyJson}
                    className="inline-flex items-center gap-1.5 text-xs text-primary hover:underline"
                  >
                    {copied ? <Check className="h-3 w-3 text-emerald-500" /> : <Copy className="h-3 w-3" />}
                    {copied ? 'Copied' : 'Copy JSON'}
                  </button>
                </div>
                <pre className="rounded-lg border border-border bg-muted/60 p-4 text-xs font-mono text-foreground overflow-x-auto max-h-[420px]">
                  {JSON.stringify(device, null, 2)}
                </pre>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
