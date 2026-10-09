import { describeCode, isApiError } from '../api/errors'

interface ErrorNoticeProps {
  error: unknown
  onRetry?: () => void
}

/**
 * Renders a failure exactly as the API reported it: the contract's
 * `{ code, message, details }` — including `request_id` — or, when the response
 * carried no structured error body, the raw HTTP status and body. Nothing is
 * collapsed into a generic "something went wrong".
 */
export function ErrorNotice({ error, onRetry }: ErrorNoticeProps) {
  if (!isApiError(error)) {
    return (
      <div className="notice notice-error" role="alert">
        <p className="notice-title">The request failed</p>
        <p>{String(error)}</p>
        {onRetry && <RetryButton onRetry={onRetry} />}
      </div>
    )
  }

  const meaning = describeCode(error.code)
  const requestId = error.requestId
  const details = error.details

  return (
    <div className="notice notice-error" role="alert">
      <p className="notice-title">
        The API returned an error: <code>{error.code}</code>
        {error.status > 0 && <span className="notice-status"> (HTTP {error.status})</span>}
      </p>
      <p>{error.message}</p>
      {meaning && <p className="notice-hint">{meaning}</p>}

      {!error.structured && (
        <p className="notice-hint">
          The response did not carry the contract error shape{' '}
          <code>{'{ code, message, details }'}</code>, so the code above is derived from the HTTP
          status and the body is shown verbatim.
        </p>
      )}

      {error.status === 404 && !error.structured && (
        <p className="notice-hint">
          This endpoint is contract-only: it exists in <code>openapi/openapi.yaml</code> but the API
          does not serve it yet.
        </p>
      )}

      {requestId && (
        <p>
          request_id: <code>{requestId}</code>
        </p>
      )}

      {details && Object.keys(details).length > 0 && (
        <details>
          <summary>details</summary>
          <pre>{JSON.stringify(details, null, 2)}</pre>
        </details>
      )}

      {onRetry && <RetryButton onRetry={onRetry} />}
    </div>
  )
}

function RetryButton({ onRetry }: { onRetry: () => void }) {
  return (
    <button type="button" className="button" onClick={onRetry}>
      Try again
    </button>
  )
}
