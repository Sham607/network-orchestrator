import { ApiError, requestIdFrom } from './errors'
import type { ErrorBody } from './types'

/** Value accepted in a query string; `undefined`/`null`/`''` are dropped. */
export type QueryValue = string | number | boolean | undefined | null
export type QueryParams = Record<string, QueryValue>

/** Same-origin prefix: the SPA is served behind Caddy, Vite proxies `/api` in dev. */
const API_BASE = '/api/v1'

/**
 * Build the request URL for an endpoint. Only the relative form is returned so
 * the browser (and the dev proxy) keep the request same-origin — there is no
 * API host baked into the bundle.
 */
export function buildUrl(path: string, query?: QueryParams): string {
  const url = new URL(`${API_BASE}${path}`, window.location.origin)
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value === undefined || value === null || value === '') continue
    url.searchParams.set(key, String(value))
  }
  return `${url.pathname}${url.search}`
}

function parseErrorBody(text: string): ErrorBody | undefined {
  if (text.trim() === '') return undefined
  try {
    const parsed: unknown = JSON.parse(text)
    if (
      typeof parsed === 'object' &&
      parsed !== null &&
      typeof (parsed as ErrorBody).code === 'string' &&
      typeof (parsed as ErrorBody).message === 'string'
    ) {
      return parsed as ErrorBody
    }
  } catch {
    return undefined
  }
  return undefined
}

async function errorFromResponse(res: Response): Promise<ApiError> {
  const headerRequestId = res.headers.get('x-request-id') ?? undefined
  const text = await res.text().catch(() => '')
  const body = parseErrorBody(text)

  if (body) {
    return new ApiError({
      status: res.status,
      code: body.code,
      message: body.message,
      details: body.details,
      requestId: requestIdFrom(body.details) ?? headerRequestId,
      structured: true,
      body: text,
    })
  }

  // The API did not answer with the contract's error shape (an unimplemented
  // route answers with a plain-text 404 today). Report exactly that instead of
  // flattening it into a generic "something went wrong".
  return new ApiError({
    status: res.status,
    code: `http_${res.status}`,
    message: text.trim() || res.statusText || 'the API returned no response body',
    requestId: headerRequestId,
    structured: false,
    body: text,
  })
}

/**
 * GET a typed resource. Throws an {@link ApiError} for any non-2xx response or
 * transport failure; never returns fabricated data.
 */
export async function apiGet<T>(
  path: string,
  query?: QueryParams,
  signal?: AbortSignal,
): Promise<T> {
  const url = buildUrl(path, query)

  let res: Response
  try {
    res = await fetch(url, { headers: { Accept: 'application/json' }, signal })
  } catch (cause) {
    if (signal?.aborted) throw cause
    throw new ApiError({
      status: 0,
      code: 'network_error',
      message: 'the request to our own API failed before a response arrived',
      cause,
    })
  }

  if (!res.ok) throw await errorFromResponse(res)
  return (await res.json()) as T
}
