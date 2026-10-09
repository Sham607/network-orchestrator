// API types derived from the generated contract (src/api/schema.d.ts).
//
// Nothing here re-declares a field: every type is an alias into the generated
// schema, so a change in openapi/openapi.yaml flows through `npm run openapi:gen`
// and `npm run typecheck` instead of being maintained twice.
import type { components, paths } from './schema'

export type Site = components['schemas']['Site']
export type Device = components['schemas']['Device']
export type Radio = components['schemas']['Radio']
export type Pagination = components['schemas']['Pagination']
export type SiteList = components['schemas']['SiteList']
export type DeviceList = components['schemas']['DeviceList']

/** The single error body shape every endpoint returns: `{ code, message, details }`. */
export type ErrorBody = components['schemas']['Error']
/** Stable, machine-readable codes from the contract's `Error` enum. */
export type ErrorCode = ErrorBody['code']

// Query parameter names come from the contract too — `status` (not `state`) and
// `page_size` (not `limit`) are enforced by the type, not by convention.
export type SiteListQuery = NonNullable<paths['/sites']['get']['parameters']['query']>
export type DeviceListQuery = NonNullable<paths['/devices']['get']['parameters']['query']>
