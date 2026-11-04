interface StatusBadgeProps {
  status: string | null | undefined
}

export function StatusBadge({ status }: StatusBadgeProps) {
  if (!status) {
    return <span className="text-xs text-muted-foreground italic">unknown</span>
  }

  const s = status.toUpperCase()
  const isOnline = s === 'ONLINE' || s === 'CONNECTED'
  const isOffline = s === 'OFFLINE' || s === 'DISCONNECTED'
  const isPending = s === 'ADOPTING' || s === 'UPGRADING'

  if (isOnline) {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-500/20 bg-emerald-50/80 px-2.5 py-0.5 text-xs font-semibold text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-950/40 dark:text-emerald-400">
        <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
        {status}
      </span>
    )
  }

  if (isOffline) {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-rose-500/20 bg-rose-50/80 px-2.5 py-0.5 text-xs font-semibold text-rose-700 dark:border-rose-500/30 dark:bg-rose-950/40 dark:text-rose-400">
        <span className="h-1.5 w-1.5 rounded-full bg-rose-500" />
        {status}
      </span>
    )
  }

  if (isPending) {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-sky-500/20 bg-sky-50/80 px-2.5 py-0.5 text-xs font-semibold text-sky-700 dark:border-sky-500/30 dark:bg-sky-950/40 dark:text-sky-400">
        <span className="h-1.5 w-1.5 rounded-full bg-sky-500 animate-ping" />
        {status}
      </span>
    )
  }

  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-muted/50 px-2.5 py-0.5 text-xs font-medium text-foreground">
      <span className="h-1.5 w-1.5 rounded-full bg-muted-foreground" />
      {status}
    </span>
  )
}
