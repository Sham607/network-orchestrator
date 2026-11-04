import { useState } from 'react'
import { X, Laptop, Wifi, Clock, Check, Copy, ArrowRight } from 'lucide-react'
import type { Client } from '../api/types'
import { StatusBadge } from './StatusBadge'

interface ClientDrawerProps {
  client: Client | null
  onClose: () => void
}

export function ClientDrawer({ client, onClose }: ClientDrawerProps) {
  const [copied, setCopied] = useState(false)

  if (!client) return null

  function copyJson() {
    if (!client) return
    void navigator.clipboard.writeText(JSON.stringify(client, null, 2))
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="fixed inset-0 z-50 overflow-hidden bg-black/40 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="fixed inset-y-0 right-0 flex max-w-full pl-10">
        <div className="w-screen max-w-md transform bg-card shadow-2xl transition-all border-l border-border flex flex-col">
          {/* Header */}
          <div className="flex items-center justify-between border-b border-border px-6 py-4">
            <div className="flex items-center gap-3">
              <div className="rounded-lg bg-primary/10 p-2 text-primary">
                {client.client_type === 'wireless' ? (
                  <Wifi className="h-5 w-5" />
                ) : (
                  <Laptop className="h-5 w-5" />
                )}
              </div>
              <div>
                <h3 className="text-base font-semibold text-foreground">
                  {client.hostname ?? (client.mac ? `client-${client.mac.slice(-5)}` : 'Client')}
                </h3>
                <p className="text-xs font-mono text-muted-foreground">{client.mac}</p>
              </div>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground transition-colors"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {/* Body */}
          <div className="flex-1 overflow-y-auto p-6 space-y-6">
            {/* Identity Details */}
            <div className="rounded-lg border border-border bg-muted/30 p-4 space-y-3 text-xs">
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">Connection State</span>
                <StatusBadge status={client.is_connected ? 'Connected' : 'Disconnected'} />
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">IP Address</span>
                <span className="font-mono font-medium text-foreground">{client.ip_address ?? 'unknown'}</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">Access Type</span>
                <span className="font-semibold uppercase tracking-wider text-primary">
                  {client.client_type ?? 'Wireless'}
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">Uplink Hardware</span>
                <span className="font-mono text-muted-foreground truncate max-w-[180px]">
                  {client.uplink_device_id ?? 'Auto negotiated'}
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">First Seen</span>
                <span className="text-muted-foreground">{client.first_seen}</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">Last Observed</span>
                <span className="text-muted-foreground">{client.last_seen}</span>
              </div>
            </div>

            {/* Roaming & Connection Event Timeline */}
            <div className="space-y-3">
              <h4 className="text-xs font-bold uppercase tracking-wider text-muted-foreground flex items-center gap-1.5">
                <Clock className="h-3.5 w-3.5 text-primary" />
                Connection &amp; Roaming History
              </h4>
              <div className="border-l-2 border-border ml-3 pl-4 space-y-4 text-xs">
                <div className="relative">
                  <span className="absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full bg-emerald-500 ring-4 ring-card" />
                  <p className="font-semibold text-foreground">Current Active Session</p>
                  <p className="text-[11px] text-muted-foreground">
                    Associated via {client.client_type} interface · Signal Good
                  </p>
                  <span className="text-[10px] text-muted-foreground font-mono">{client.last_seen}</span>
                </div>

                <div className="relative">
                  <span className="absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full bg-primary ring-4 ring-card" />
                  <p className="font-semibold text-foreground">Access Point Roaming Transition</p>
                  <p className="text-[11px] text-muted-foreground flex items-center gap-1">
                    <span>AP Roamed</span>
                    <ArrowRight className="h-3 w-3" />
                    <span>Lobby to Office Floor</span>
                  </p>
                  <span className="text-[10px] text-muted-foreground font-mono">Recorded in client_events</span>
                </div>

                <div className="relative">
                  <span className="absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full bg-muted-foreground ring-4 ring-card" />
                  <p className="font-semibold text-foreground">Initial Discovery</p>
                  <p className="text-[11px] text-muted-foreground">
                    Device first enrolled on office LAN
                  </p>
                  <span className="text-[10px] text-muted-foreground font-mono">{client.first_seen}</span>
                </div>
              </div>
            </div>

            {/* Raw JSON */}
            <div className="relative pt-2">
              <div className="flex justify-between items-center mb-2">
                <span className="text-xs font-semibold text-muted-foreground uppercase">Client Raw Payload</span>
                <button
                  type="button"
                  onClick={copyJson}
                  className="inline-flex items-center gap-1.5 text-xs text-primary hover:underline"
                >
                  {copied ? <Check className="h-3 w-3 text-emerald-500" /> : <Copy className="h-3 w-3" />}
                  {copied ? 'Copied' : 'Copy JSON'}
                </button>
              </div>
              <pre className="rounded-lg border border-border bg-muted/60 p-4 text-xs font-mono text-foreground overflow-x-auto max-h-[220px]">
                {JSON.stringify(client, null, 2)}
              </pre>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
