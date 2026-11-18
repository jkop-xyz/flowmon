import type { ReverseDNSName } from "@/lib/api"
import { cn } from "@/lib/utils"

/** An address's reverse DNS name, a placeholder while it resolves, nothing if it has none. */
export function ReverseDNS({ name, className }: { name?: ReverseDNSName; className?: string }) {
  if (!name || name.status === "pending") {
    return <span className={cn("inline-block h-3 w-24 animate-pulse rounded bg-muted align-middle", className)} aria-label="Resolving" />
  }
  if (name.status !== "resolved" || !name.name) return null
  return (
    <span className={cn("truncate text-xs text-muted-foreground", className)} title={name.name}>
      {name.name}
    </span>
  )
}
