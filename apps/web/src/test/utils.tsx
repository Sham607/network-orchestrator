import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, type RenderResult } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'

import type { ClientList, DeviceList, ErrorBody, SiteList } from '../api/types'

/** Render a component inside the providers the app supplies at runtime. */
export function renderWithProviders(
  ui: ReactElement,
  options: { route?: string } = {},
): RenderResult {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0, refetchInterval: false },
    },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[options.route ?? '/']}>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

/** Minimal stand-in for `Response`, so tests need no fetch implementation. */
export interface FakeResponse {
  ok: boolean
  status: number
  statusText: string
  headers: { get(name: string): string | null }
  json(): Promise<unknown>
  text(): Promise<string>
}

export function jsonResponse(
  body: unknown,
  status = 200,
  headers: Record<string, string> = {},
): FakeResponse {
  const all: Record<string, string> = {}
  for (const [key, value] of Object.entries({ 'content-type': 'application/json', ...headers })) {
    all[key.toLowerCase()] = value
  }
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: '',
    headers: { get: (name) => all[name.toLowerCase()] ?? null },
    json: async () => body,
    text: async () => JSON.stringify(body),
  }
}

export function textResponse(text: string, status: number): FakeResponse {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: '',
    headers: { get: () => null },
    json: async () => {
      throw new Error('body is not JSON')
    },
    text: async () => text,
  }
}

/** Install a fetch stub that answers from `handler(url)`, and return the mock. */
export function installFetch(handler: (url: string) => FakeResponse) {
  const mock = vi.fn(async (input: RequestInfo | URL) => handler(String(input)) as Response)
  vi.stubGlobal('fetch', mock)
  return mock
}

/** URLs every fetch call asked for, in order. */
export function requestedUrls(mock: ReturnType<typeof vi.fn>): string[] {
  return mock.mock.calls.map((call) => String(call[0]))
}

export const siteListFixture: SiteList = {
  data: [
    {
      id: '1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c',
      name: 'HQ',
      description: 'Head office',
      timezone: 'Asia/Jakarta',
      gateway_mac: 'aa:bb:cc:dd:ee:01',
      synced_at: '2026-10-06T15:39:10Z',
    },
    {
      id: '2f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1d',
      name: 'Warehouse',
      description: null,
      timezone: null,
      gateway_mac: null,
      synced_at: '2026-10-06T15:39:10Z',
    },
  ],
  pagination: { page: 1, page_size: 50, total: 2, total_pages: 1 },
}

export const emptySiteListFixture: SiteList = {
  data: [],
  pagination: { page: 1, page_size: 50, total: 0, total_pages: 0 },
}

export const deviceListFixture: DeviceList = {
  data: [
    {
      id: '2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d',
      site_id: '1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c',
      name: 'AP-Lobby',
      mac: 'aa:bb:cc:dd:ee:02',
      model: 'U6 Pro',
      device_type: 'ap',
      status: 'ONLINE',
      firmware_version: '6.8.2',
      firmware_updatable: false,
      ip_address: '10.20.0.31',
      adopted_at: '2025-06-02T07:02:25Z',
      provisioned_at: '2026-08-05T02:06:38Z',
      uplink_device_id: '3b4c5d6e-7f8a-4b9c-0d1e-2f3a4b5c6d7e',
      supported: true,
      radios: [],
      uptime_sec: null,
      synced_at: '2026-10-06T15:39:10Z',
    },
    {
      id: '3a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6e',
      site_id: '1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c',
      name: null,
      mac: 'aa:bb:cc:dd:ee:03',
      model: null,
      device_type: 'switch',
      status: 'OFFLINE',
      firmware_version: null,
      firmware_updatable: null,
      ip_address: null,
      adopted_at: null,
      provisioned_at: null,
      uplink_device_id: null,
      supported: null,
      radios: [],
      uptime_sec: null,
      synced_at: '2026-10-06T15:39:10Z',
    },
  ],
  pagination: { page: 1, page_size: 25, total: 2, total_pages: 1 },
}

export const emptyDeviceListFixture: DeviceList = {
  data: [],
  pagination: { page: 1, page_size: 25, total: 0, total_pages: 0 },
}

export const clientListFixture: ClientList = {
  data: [
    {
      id: '5a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c70',
      site_id: '1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c',
      mac: 'aa:bb:cc:dd:ee:10',
      hostname: 'ivan-laptop',
      ip_address: '10.20.0.42',
      client_type: 'WIRELESS',
      access_type: 'DEFAULT',
      uplink_device_id: '2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d',
      ssid: null,
      signal_dbm: null,
      is_connected: true,
      connected_at: '2026-10-06T14:00:00Z',
      disconnected_at: null,
      first_seen: '2026-09-01T08:00:00Z',
      last_seen: '2026-10-06T15:39:10Z',
      synced_at: '2026-10-06T15:39:10Z',
    },
    {
      id: '6a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c71',
      site_id: '1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c',
      mac: null,
      hostname: null,
      ip_address: null,
      client_type: 'WIRED',
      access_type: null,
      uplink_device_id: null,
      ssid: null,
      signal_dbm: null,
      is_connected: false,
      connected_at: null,
      disconnected_at: '2026-10-05T11:00:00Z',
      first_seen: '2026-08-01T08:00:00Z',
      last_seen: '2026-10-05T11:00:00Z',
      synced_at: '2026-10-06T15:39:10Z',
    },
  ],
  pagination: { page: 1, page_size: 25, total: 2, total_pages: 1 },
}

export const emptyClientListFixture: ClientList = {
  data: [],
  pagination: { page: 1, page_size: 25, total: 0, total_pages: 0 },
}

export const REQUEST_ID = '01a111bd-15d0-7f21-aec2-20bd264a6040'

/** `503 service_unavailable` exactly as `openapi/openapi.yaml` documents it. */
export const serviceUnavailableBody: ErrorBody = {
  code: 'service_unavailable',
  message: 'database is not reachable',
  details: { request_id: REQUEST_ID, reason: 'database_unreachable' },
}

/** `404 not_found` as the contract documents it. */
export const notFoundBody: ErrorBody = {
  code: 'not_found',
  message: 'device not found',
  details: { request_id: REQUEST_ID, resource: 'device' },
}
