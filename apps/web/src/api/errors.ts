import type { ErrorBody, ErrorCode } from './types'

/**
 * Meaning of every code in the contract's `Error` enum. Typing this as a
 * `Record<ErrorCode, string>` makes the compiler fail when the contract adds a
 * code the UI does not explain yet.
 */
const CODE_MEANING: Record<ErrorCode, string> = {
  unifi_unreachable: 'The UniFi controller is not reachable; the API is serving stale cache.',
  unifi_unauthorized: 'Our UniFi API key is invalid or expired.',
  unifi_forbidden: 'Our UniFi API key lacks the required scope.',
  unifi_rate_limited: 'The UniFi controller rate-limited us.',
  unifi_schema_drift: 'The UniFi response did not match the expected shape.',
  not_found: 'The resource is not in the cache.',
  validation_failed: 'The request failed input validation.',
  unauthorized: 'No valid operator session.',
  forbidden: 'This session is not allowed to perform the request.',
  rate_limited: 'Too many requests; retry after the interval the API reports.',
  service_unavailable: 'A dependency the API needs is unavailable.',
  internal_error: 'Unexpected server-side failure.',
}

/** Human-readable meaning of a contract error code, or undefined for a non-contract code. */
export function describeCode(code: string): string | undefined {
  return (CODE_MEANING as Record<string, string | undefined>)[code]
}

/** Body of a non-2xx response that was not the contract's error shape. */
export interface ApiErrorInit {
  status: number
  code: string
  message: string
  details?: ErrorBody['details']
  requestId?: string
  /** True when the response body matched `{ code, message, details }`. */
  structured?: boolean
  /** Raw body text, kept for unstructured failures so nothing is swallowed. */
  body?: string
  cause?: unknown
}

/**
 * A failed API call. `code` is the contract's stable branch point; for responses
 * that did not carry the contract shape it is synthesized as `http_<status>` and
 * `structured` is false, so the UI can say so instead of inventing a code.
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details?: ErrorBody['details']
  readonly requestId?: string
  readonly structured: boolean
  readonly body?: string

  constructor(init: ApiErrorInit) {
    super(init.message, { cause: init.cause })
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.details = init.details
    this.requestId = init.requestId
    this.structured = init.structured ?? false
    this.body = init.body
  }
}

/** Narrow an unknown thrown value to an ApiError (query errors are not typed). */
export function isApiError(value: unknown): value is ApiError {
  return value instanceof ApiError
}

/** `details.request_id` is untyped in the contract; read it defensively. */
export function requestIdFrom(details: ErrorBody['details']): string | undefined {
  const value = details?.request_id
  return typeof value === 'string' ? value : undefined
}
