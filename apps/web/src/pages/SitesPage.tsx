import { useSites } from '../api/queries'
import { ErrorNotice } from '../components/ErrorNotice'
import { EmptyNotice, LoadingNotice } from '../components/Notices'

/** Sites list, read from `GET /api/v1/sites`. Read-only by design (phase 1). */
export default function SitesPage() {
  const { data, error, isPending, isFetching, refetch } = useSites()

  return (
    <section aria-labelledby="sites-heading">
      <div className="page-header">
        <h2 id="sites-heading">Sites</h2>
        <button
          type="button"
          className="button"
          onClick={() => void refetch()}
          disabled={isFetching}
        >
          {isFetching ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      <p className="source">
        Source: <code>GET /api/v1/sites</code>
      </p>

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
                  <tr key={site.id}>
                    <th scope="row">{site.name}</th>
                    <td>{site.description ?? <Unknown />}</td>
                    <td>{site.timezone ?? <Unknown />}</td>
                    <td>{site.gateway_mac ?? <Unknown />}</td>
                    <td>{site.synced_at}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {data.pagination.total > data.data.length && (
            <p className="source" role="status">
              Showing {data.data.length} of {data.pagination.total} sites — this view does not page
              yet.
            </p>
          )}
        </>
      )}
    </section>
  )
}

function Unknown() {
  return <span className="unknown">unknown</span>
}
