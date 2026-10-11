import { useQuery } from '@tanstack/react-query'

import { apiGet } from './client'
import type { ClientList, ClientListQuery, DeviceList, DeviceListQuery, SiteList } from './types'

// Polling is the phase-1 realtime mechanism (ADR-002 §4): the QueryClient
// refetches on an interval while the tab is active. Server state lives in the
// query cache, so no component keeps its own copy.

export const sitesQueryKey = ['sites'] as const

export function useSites() {
  return useQuery({
    queryKey: sitesQueryKey,
    queryFn: ({ signal }) => apiGet<SiteList>('/sites', { page: 1, page_size: 50 }, signal),
  })
}

export function devicesQueryKey(query: DeviceListQuery) {
  return ['devices', query] as const
}

export function useDevices(query: DeviceListQuery) {
  return useQuery({
    queryKey: devicesQueryKey(query),
    queryFn: ({ signal }) => apiGet<DeviceList>('/devices', query, signal),
  })
}

export function clientsQueryKey(query: ClientListQuery) {
  return ['clients', query] as const
}

export function useClients(query: ClientListQuery) {
  return useQuery({
    queryKey: clientsQueryKey(query),
    queryFn: ({ signal }) => apiGet<ClientList>('/clients', query, signal),
  })
}
