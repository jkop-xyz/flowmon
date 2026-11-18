import type { ReactNode } from "react"

import { formatBytes } from "@/lib/format"

/** A ranked list of BarRows. */
export function BarList({ children }: { children: ReactNode }) {
  return <ol className="flex flex-col gap-3">{children}</ol>
}

/**
 * One ranked value: its label and total, over a bar as long as its share of the largest
 * and split into received and sent.
 */
export function BarRow({
  label,
  total,
  received,
  sent,
  max,
}: {
  label: ReactNode
  total: number
  received: number
  sent: number
  max: number
}) {
  const width = max > 0 ? Math.max(2, (total / max) * 100) : 0
  return (
    <li className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-2 text-sm">
        <span className="flex min-w-0 items-center gap-2">{label}</span>
        <span className="shrink-0 tabular-nums">{formatBytes(total)}</span>
      </div>
      <div className="flex h-1.5 overflow-hidden rounded-full bg-muted" style={{ width: `${width}%` }}>
        <div className="h-full bg-chart-1" style={{ flexGrow: received }} title={`Received ${formatBytes(received)}`} />
        <div className="h-full bg-chart-2" style={{ flexGrow: sent }} title={`Sent ${formatBytes(sent)}`} />
      </div>
    </li>
  )
}
