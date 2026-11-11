import { useEffect, useState, useRef, useCallback } from 'react'

export interface ClientSpikeMetric {
  mac: string
  hostname: string
  ip_address: string
  ap_name: string
  download_mbps: number
  upload_mbps: number
  is_spike: boolean
}

export interface SiteLossMetric {
  site_id: string
  site_name: string
  latency_ms: number
  loss_pct: number
  status: string
  primary_wan: string
}

export interface TelemetryPayload {
  timestamp: string
  global_loss_pct: number
  avg_latency_ms: number
  active_sites: number
  total_clients: number
  sites: SiteLossMetric[]
  top_talkers: ClientSpikeMetric[]
  has_spike: boolean
  has_loss_warning: boolean
}

export interface TelemetryMessage {
  type: 'telemetry_tick' | 'loss_alert' | 'traffic_spike_alert'
  data: TelemetryPayload
}

export function useTelemetryWebSocket() {
  const [isConnected, setIsConnected] = useState(false)
  const [payload, setPayload] = useState<TelemetryPayload | null>(null)
  const [latencyHistory, setLatencyHistory] = useState<number[]>([12, 13, 11, 14, 12, 12, 13])
  const [lossHistory, setLossHistory] = useState<number[]>([0, 0, 0, 0, 0, 0, 0])
  const [activeAlert, setActiveAlert] = useState<{ message: string; type: 'loss' | 'spike' } | null>(null)

  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimeoutRef = useRef<number | null>(null)

  const clearAlert = useCallback(() => {
    setActiveAlert(null)
  }, [])

  useEffect(() => {
    let unmounted = false

    function connect() {
      if (unmounted) return

      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const wsUrl = `${protocol}//${window.location.host}/api/v1/ws/telemetry`

      const ws = new WebSocket(wsUrl)
      wsRef.current = ws

      ws.onopen = () => {
        if (!unmounted) {
          setIsConnected(true)
        }
      }

      ws.onmessage = (event) => {
        if (unmounted) return
        try {
          const msg = JSON.parse(event.data) as TelemetryMessage
          if (msg && msg.data) {
            setPayload(msg.data)
            setLatencyHistory((prev) => [...prev.slice(-29), msg.data.avg_latency_ms])
            setLossHistory((prev) => [...prev.slice(-29), msg.data.global_loss_pct])

            if (msg.type === 'loss_alert' || msg.data.has_loss_warning) {
              setActiveAlert({
                message: `High WAN packet loss detected (${msg.data.global_loss_pct}%)`,
                type: 'loss',
              })
            } else if (msg.type === 'traffic_spike_alert' || msg.data.has_spike) {
              const topHog = msg.data.top_talkers.find((t) => t.is_spike)
              if (topHog) {
                const speed = Math.max(topHog.download_mbps, topHog.upload_mbps)
                const dir = topHog.download_mbps >= topHog.upload_mbps ? 'Download' : 'Upload'
                setActiveAlert({
                  message: `Traffic Spike: ${topHog.hostname || topHog.mac} consuming ${speed} Mbps (${dir}) on ${topHog.ap_name}!`,
                  type: 'spike',
                })
              }
            } else {
              setActiveAlert((prev) => (prev?.type === 'spike' ? null : prev))
            }
          }
        } catch {
          // ignore parsing error
        }
      }

      ws.onclose = () => {
        if (!unmounted) {
          setIsConnected(false)
          reconnectTimeoutRef.current = window.setTimeout(connect, 3000)
        }
      }

      ws.onerror = () => {
        ws.close()
      }
    }

    connect()

    return () => {
      unmounted = true
      clearTimeout(reconnectTimeoutRef.current!)
      if (wsRef.current) {
        wsRef.current.close()
      }
    }
  }, [])

  return {
    isConnected,
    payload,
    latencyHistory,
    lossHistory,
    activeAlert,
    clearAlert,
  }
}
