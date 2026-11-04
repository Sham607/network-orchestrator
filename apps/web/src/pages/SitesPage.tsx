import { Globe, RefreshCw, Clock, Network } from 'lucide-react'

import { useSites } from '../api/queries'
import { ErrorNotice } from '../components/ErrorNotice'
import { EmptyNotice, LoadingNotice } from '../components/Notices'
import { StatCard } from '../components/StatCard'

const UNKNOWN = <span className="unknown">unknown</span>

export default function SitesPage() {
  const { data, error, isPending, isFetching, refetch } = useSites()

  const totalSites = data?.pagination.total ?? 0
  const items = data?.data ?? []
  const withGateway = items.filter((s) => Boolean(s.gateway_mac)).length
  const timezones = new Set(items.map((s) => s.timezone).filter(Boolean)).size

  return (
    <section aria-labelledby="sites-heading" className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 pb-2">
        <div>
          <h2 id="sites-heading" className="text-2xl font-bold tracking-tight text-foreground">
            Sites
          </h2>
          <p className="source text-sm text-muted-foreground mt-1">
            Source: <code>GET /api/v1/sites</code>
          </p>
        </div>
        <button
          type="button"
          className="button inline-flex items-center gap-2"
          onClick={() => void refetch()}
          disabled={isFetching}
        >
          <RefreshCw className={`h-4 w-4 ${isFetching ? 'animate-spin' : ''}`} />
          <span>{isFetching ? 'Refreshing…' : 'Refresh'}</span>
        </button>
      </div>

      {data && data.data.length > 0 && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <StatCard
            title="Total Sites"
            value={totalSites}
            description="UniFi managed controller sites"
            icon={Globe}
            badge={{ text: 'Active', variant: 'success' }}
          />
          <StatCard
            title="Gateways Configured"
            value={`${withGateway} / ${items.length}`}
            description="Sites with detected gateway MAC"
            icon={Network}
          />
          <StatCard
            title="Distinct Timezones"
            value={timezones || 1}
            description="Operating regions"
            icon={Clock}
          />
        </div>
      )}

      {isPending && <LoadingNotice label="Loading sites" />}

      {!isPending && error && <ErrorNotice error={error} onRetry={() => void refetch()} />}

      {data && data.data.length === 0 && (
        <EmptyNotice
          title="No sites in the cache"
          hint="The API answered successfully with zero sites. Nothing is fabricated here: sites appear once the sync worker has written them."
        />
      )}

      {data && data.data.length > 0 && (
        <>
          <div className="table-scroll">
            <table>
              <caption className="sr-only">Sites known to the control plane</caption>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Description</th>
                  <th scope="col">Timezone</th>
                  <th scope="col">Gateway MAC</th>
                  <th scope="col">Synced at</th>
                </tr>
              </thead>
              <tbody>
                {data.data.map((site) => (
                  <tr key={site.id} className="hover:bg-muted/40 transition-colors">
                    <th scope="row" className="font-semibold text-foreground">
                      {site.name}
                    </th>
                    <td>{site.description ?? UNKNOWN}</td>
                    <td>
                      {site.timezone ? (
                        <span className="inline-flex items-center gap-1.5 text-xs text-foreground">
                          <Clock className="h-3 w-3 text-muted-foreground" />
                          {site.timezone}
                        </span>
                      ) : (
                        UNKNOWN
                      )}
                    </td>
                    <td>
                      {site.gateway_mac ? (
                        <span className="font-mono text-xs text-muted-foreground bg-muted/60 px-1.5 py-0.5 rounded">
                          {site.gateway_mac}
                        </span>
                      ) : (
                        UNKNOWN
                      )}
                    </td>
                    <td className="text-xs text-muted-foreground">{site.synced_at}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {data.pagination.total > data.data.length && (
            <p className="source text-xs text-muted-foreground" role="status">
              Showing {data.data.length} of {data.pagination.total} sites — this view does not page
              yet.
            </p>
          )}
        </>
      )}
    </section>
  )
}
