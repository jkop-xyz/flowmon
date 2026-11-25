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
  device,
  className,
}: {
  host: string
  name?: HostNameInfo
  exporterHost?: boolean
  /** How many addresses a Host Label's prefix, or a Device, merges. */
  addresses?: number
  /** The Device behind it, when listed by Device: named by its vendor when nothing better exists. */
  device?: { vendor?: string; privateMac?: boolean; mac?: string }
  className?: string
}) {
  const { search } = useLocation()
  const named = name?.name ?? (device?.vendor && name?.status !== "pending" ? `${device.vendor} device` : undefined)
  return (
    <span className={cn("flex min-w-0 items-center gap-2", className)}>
      <Link
        to={{ pathname: device?.mac ? `/devices/${device.mac}` : hostPath(host), search }}
        className="flex min-w-0 flex-col underline-offset-4 hover:underline"
      >
        {named ? (
          <>
            <span className="truncate text-sm" title={named}>
              {named}
            </span>
            <span className="truncate font-mono text-ident text-muted-foreground" title={device?.mac ? `${host} · ${device.mac}` : host}>
              {host}
              {addresses ? ` · ${addresses} ${addresses === 1 ? "address" : "addresses"}` : ""}
            </span>
          </>
        ) : (
          <>
            <span className="truncate font-mono text-ident" title={host}>
              {host}
            </span>
            {addresses ? (
              <span className="truncate text-xs text-muted-foreground">
                {addresses} {addresses === 1 ? "address" : "addresses"}
              </span>
            ) : null}
            {name?.status === "pending" && (
              <span className="inline-block h-3 w-24 animate-pulse rounded bg-muted" aria-label="Resolving" />
            )}
          </>
        )}
      </Link>
      {exporterHost && <Badge variant="outline">Exporter Host</Badge>}
      {device?.privateMac && (
        <Badge variant="outline" title="A randomized MAC address: it may change, so this Device may show up again under another one">
          Private address
        </Badge>
      )}
    </span>
  )
}
