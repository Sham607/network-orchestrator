import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { apiGet, apiPost } from './client'
import type {
  ActionAccepted,
  ClientList,
  ClientListQuery,
  DeviceActionRequest,
  DeviceList,
  DeviceListQuery,
  NetworkList,
  NetworkListQuery,
  RFScanResponse,
  SiteList,
  SyncStatus,
  WANHealthResponse,
} from './types'

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

export function networksQueryKey(query: NetworkListQuery) {
  return ['networks', query] as const
}

export function useNetworks(query: NetworkListQuery) {
  return useQuery({
    queryKey: networksQueryKey(query),
    queryFn: ({ signal }) => apiGet<NetworkList>('/networks', query, signal),
  })
}

export const syncStatusQueryKey = ['sync-status'] as const

/**
 * Cache freshness and the surface that filled it (`GET /sync/status`). The
 * clients view uses it to explain an empty cache honestly: the UniFi cloud
 * surface serves no clients, so "wait for the sync worker" would be wrong
 * there.
 */
export function useSyncStatus() {
  return useQuery({
    queryKey: syncStatusQueryKey,
    queryFn: ({ signal }) => apiGet<SyncStatus>('/sync/status', undefined, signal),
  })
}

export function useDeviceAction() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ deviceId, action }: { deviceId: string; action: DeviceActionRequest }) =>
      apiPost<ActionAccepted, DeviceActionRequest>(`/devices/${deviceId}/actions`, action),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['devices'] })
    },
  })
}

export function useSiteRFScan(siteId: string | null) {
  return useQuery({
    queryKey: ['site-rf-scan', siteId],
    queryFn: ({ signal }) => apiGet<RFScanResponse>(`/sites/${siteId}/rf-scan`, undefined, signal),
    enabled: Boolean(siteId),
  })
}

export function useSiteWANHealth(siteId: string | null) {
  return useQuery({
    queryKey: ['site-wan-health', siteId],
    queryFn: ({ signal }) => apiGet<WANHealthResponse>(`/sites/${siteId}/wan-health`, undefined, signal),
    enabled: Boolean(siteId),
  })
}
