import { describe, expect, it } from 'vitest'

import { apiGet, buildUrl } from '../api/client'
import { ApiError } from '../api/errors'
import {
  REQUEST_ID,
  installFetch,
  jsonResponse,
  serviceUnavailableBody,
  textResponse,
} from './utils'

describe('buildUrl', () => {
  it('uses the contract parameter names and keeps the request same-origin', () => {
    expect(buildUrl('/devices', { page: 2, page_size: 50, status: 'ONLINE' })).toBe(
      '/api/v1/devices?page=2&page_size=50&status=ONLINE',
    )
  })

  it('drops empty values so an unset filter is not sent as an empty string', () => {
    expect(
      buildUrl('/devices', { page: 1, page_size: 25, status: '', site_id: undefined, mac: null }),
    ).toBe('/api/v1/devices?page=1&page_size=25')
  })
})

describe('apiGet', () => {
  it('returns the parsed body on success', async () => {
    installFetch(() => jsonResponse({ status: 'ok' }))

    await expect(apiGet<{ status: string }>('/health')).resolves.toEqual({ status: 'ok' })
  })

  it('throws a structured ApiError carrying the contract code, message and request_id', async () => {
    installFetch(() => jsonResponse(serviceUnavailableBody, 503))

    const error = await apiGet('/devices').catch((caught: unknown) => caught)

    expect(error).toBeInstanceOf(ApiError)
    const apiError = error as ApiError
    expect(apiError.status).toBe(503)
    expect(apiError.code).toBe('service_unavailable')
    expect(apiError.message).toBe('database is not reachable')
    expect(apiError.structured).toBe(true)
    expect(apiError.requestId).toBe(REQUEST_ID)
    expect(apiError.details).toEqual(serviceUnavailableBody.details)
  })

  it('keeps the raw body when the response is not the contract error shape', async () => {
    // This is what an unimplemented route answers with today: chi's plain-text 404.
    installFetch(() => textResponse('404 page not found\n', 404))

    const error = (await apiGet('/sites').catch((caught: unknown) => caught)) as ApiError

    expect(error).toBeInstanceOf(ApiError)
    expect(error.status).toBe(404)
    expect(error.code).toBe('http_404')
    expect(error.structured).toBe(false)
    expect(error.message).toBe('404 page not found')
    expect(error.body).toBe('404 page not found\n')
  })

  it('falls back to the X-Request-Id header when the body has no details', async () => {
    installFetch(() => jsonResponse({ code: 'internal_error', message: 'boom' }, 500, {
      'X-Request-Id': REQUEST_ID,
    }))

    const error = (await apiGet('/sites').catch((caught: unknown) => caught)) as ApiError

    expect(error.requestId).toBe(REQUEST_ID)
  })

  it('reports a transport failure as a network error rather than an API code', async () => {
    installFetch(() => {
      throw new TypeError('Failed to fetch')
    })

    const error = (await apiGet('/sites').catch((caught: unknown) => caught)) as ApiError

    expect(error).toBeInstanceOf(ApiError)
    expect(error.code).toBe('network_error')
    expect(error.status).toBe(0)
    expect(error.structured).toBe(false)
  })
})
