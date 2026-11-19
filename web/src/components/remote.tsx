import { Link, useLocation } from "react-router"

import type { Coverage } from "@/lib/api"
import { formatDateTime } from "@/lib/format"

/** A link to a Remote IP's page that keeps the time range and Exporter filter. */
export function RemoteIPLink({ ip, className }: { ip: string; className?: string }) {
  const { search } = useLocation()
  return (
    <Link to={{ pathname: `/remote/${ip}`, search }} className={className ?? "font-mono text-ident underline-offset-4 hover:underline"}>
      {ip}
    </Link>
  )
}

/**
 * Says plainly what an answer about Remote IPs can't include: lists instead of raw Flows,
 * or a time range that starts before any data was kept.
 */
export function CoverageNote({ coverage, rangeFrom }: { coverage: Coverage; rangeFrom: Date }) {
  const notes: string[] = []
  if (coverage.approximate) {
    const unit = coverage.source === "day" ? "day" : "hour"
    notes.push(
      `Approximate: from the busiest ${50} Remote IPs per Local Host and ${unit}. A Remote IP outside a host's busiest in some ${unit} counts as other traffic there.`,
    )
    const start = coverage.listsFrom ?? coverage.rawFrom
    if (start && new Date(start) > rangeFrom) {
      notes.push(`There is no Remote IP data before ${formatDateTime(start)}.`)
    } else if (!start) {
      notes.push("There is no Remote IP data for this time range yet.")
    }
  }
  if (notes.length === 0) return null
  return <p className="text-xs text-muted-foreground">{notes.join(" ")}</p>
}
