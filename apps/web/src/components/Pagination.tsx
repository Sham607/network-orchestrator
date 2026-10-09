import type { Pagination as PaginationData } from '../api/types'

interface PaginationProps {
  pagination: PaginationData
  itemLabel: string
  busy?: boolean
  onPageChange: (page: number) => void
}

/** Prev/next pager driven entirely by the API's `pagination` object. */
export function Pagination({ pagination, itemLabel, busy, onPageChange }: PaginationProps) {
  const { page, page_size: pageSize, total, total_pages: totalPages } = pagination
  const lastPage = Math.max(totalPages, 1)

  return (
    <nav className="pagination" aria-label={`${itemLabel} pagination`}>
      <button
        type="button"
        className="button"
        onClick={() => onPageChange(page - 1)}
        disabled={busy || page <= 1}
      >
        Previous
      </button>
      <p aria-live="polite">
        Page {page} of {lastPage} · {total} {itemLabel} · {pageSize} per page
      </p>
      <button
        type="button"
        className="button"
        onClick={() => onPageChange(page + 1)}
        disabled={busy || page >= lastPage}
      >
        Next
      </button>
    </nav>
  )
}
