/** Parses a URL search param as a positive integer, falling back when absent or invalid. */
export function positiveInt(raw: string | null, fallback: number): number {
  const parsed = Number(raw)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback
}

/** Restricts a page-size param to an allowed set, falling back to the default otherwise. */
export function pageSizeFrom(raw: string | null, allowed: readonly number[], fallback: number): number {
  const parsed = Number(raw)
  return allowed.includes(parsed) ? parsed : fallback
}
