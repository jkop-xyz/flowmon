import { Link, useLocation } from "react-router"

import { Badge } from "@/components/ui/badge"
import type { HostName as HostNameInfo } from "@/lib/api"
import { hostPath } from "@/lib/hosts"
import { cn } from "@/lib/utils"

/**
 * A Local Host as a link to its page: its Host Name with the address under it, or the
 * address alone when it has no name. A placeholder shows while reverse DNS resolves.
 */
export function HostLink({
  host,
  name,
  exporterHost,
  addresses,
  className,
}: {
  host: string
  name?: HostNameInfo
  exporterHost?: boolean
  /** How many addresses a Host Label's prefix merges. */
  addresses?: number
  className?: string
}) {
  const { search } = useLocation()
  const named = name?.name
  return (
    <span className={cn("flex min-w-0 items-center gap-2", className)}>
      <Link to={{ pathname: hostPath(host), search }} className="flex min-w-0 flex-col underline-offset-4 hover:underline">
        {named ? (
          <>
            <span className="truncate text-sm" title={named}>
              {named}
            </span>
            <span className="truncate font-mono text-ident text-muted-foreground" title={host}>
              {host}
              {addresses ? ` · ${addresses} ${addresses === 1 ? "address" : "addresses"}` : ""}
            </span>
          </>
        ) : (
          <>
            <span className="truncate font-mono text-ident" title={host}>
              {host}
            </span>
            {name?.status === "pending" && (
              <span className="inline-block h-3 w-24 animate-pulse rounded bg-muted" aria-label="Resolving" />
            )}
          </>
        )}
      </Link>
      {exporterHost && <Badge variant="outline">Exporter Host</Badge>}
    </span>
  )
}
