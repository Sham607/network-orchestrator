// API types derived from the generated contract (src/api/schema.d.ts).
//
// Nothing here re-declares a field: every type is an alias into the generated
// schema, so a change in openapi/openapi.yaml flows through `npm run openapi:gen`
// and `npm run typecheck` instead of being maintained twice.
import type { components, paths } from './schema'

export type Site = components['schemas']['Site']
export type Device = components['schemas']['Device']
export type Radio = components['schemas']['Radio']
export type Client = components['schemas']['Client']
export type Pagination = components['schemas']['Pagination']
export type SiteList = components['schemas']['SiteList']
export type DeviceList = components['schemas']['DeviceList']
export type ClientList = components['schemas']['ClientList']
export type Network = components['schemas']['Network']
export type NetworkList = components['schemas']['NetworkList']
export type SyncStatus = components['schemas']['SyncStatus']
export type User = components['schemas']['User']
export type LoginRequest = components['schemas']['LoginRequest']
export type LoginResponse = components['schemas']['LoginResponse']

export type DeviceActionRequest = components['schemas']['DeviceActionRequest']
export type ActionAccepted = components['schemas']['ActionAccepted']
export type ErrorBody = components['schemas']['Error']
/** Stable, machine-readable codes from the contract's `Error` enum. */
export type ErrorCode = ErrorBody['code']

// Query parameter names come from the contract too — `status` (not `state`) and
// `page_size` (not `limit`) are enforced by the type, not by convention.
export type SiteListQuery = NonNullable<paths['/sites']['get']['parameters']['query']>
export type DeviceListQuery = NonNullable<paths['/devices']['get']['parameters']['query']>
export type ClientListQuery = NonNullable<paths['/clients']['get']['parameters']['query']>
export type NetworkListQuery = NonNullable<paths['/networks']['get']['parameters']['query']>

export interface ChannelMetric {
  channel: number
  band: string
  count: number
  status: 'clean' | 'moderate' | 'congested'
}

export interface RogueAPItem {
  bssid: string
  essid: string
  channel: number
  band: string
  rssi: number
  signal: number
}

export interface RFScanResponse {
  site_id: string
  site_name: string
  total_rogue_aps: number
  cleanest_5g: number
  cleanest_24g: number
  channels_5g: ChannelMetric[]
  channels_24g: ChannelMetric[]
  strongest_interferers: RogueAPItem[]
}

export interface WANHealthResponse {
  site_id: string
  site_name: string
  gateway_mac: string
  status: 'healthy' | 'flapping_warning' | 'degraded'
  primary_wan: string
  latency_ms: number
  packet_loss_pct: number
  drops_last_24h: number
  uptime_pct: number
  isp_name?: string
  wan_ip?: string
  cpu_usage?: string
  mem_usage?: string
}
