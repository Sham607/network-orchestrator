interface EmptyNoticeProps {
  title: string
  hint?: string
}

/** Shown when the API answered successfully with an empty collection. */
export function EmptyNotice({ title, hint }: EmptyNoticeProps) {
  return (
    <div className="notice notice-empty" role="status">
      <p className="notice-title">{title}</p>
      {hint && <p className="notice-hint">{hint}</p>}
    </div>
  )
}

/** Shown while a request is in flight; always replaced by data, empty or error. */
export function LoadingNotice({ label }: { label: string }) {
  return (
    <div className="notice notice-loading" role="status" aria-busy="true">
      <p className="notice-title">{label}…</p>
      <div className="skeleton" aria-hidden="true">
        <span />
        <span />
        <span />
      </div>
    </div>
  )
}
