import type { ServiceName } from "@/lib/api"
import { protocolName } from "@/lib/format"
import { cn } from "@/lib/utils"

/** A Service Port as "443/tcp https", with the registry's description on hover. */
export function ServicePort({
  protocol,
  port,
  name,
  className,
}: {
  protocol: number
  port: number
  name?: ServiceName
  className?: string
}) {
  if (port === 0) return <span className={cn("truncate", className)}>{protocolName(protocol)} (no port)</span>
  const title = [name?.description, name?.source === "user" ? "Your name for this port" : undefined].filter(Boolean).join(" · ")
  return (
    <span className={cn("flex min-w-0 items-baseline gap-1.5", className)} title={title || undefined}>
      <span className="shrink-0 font-mono text-ident">
        {port}/{protocolName(protocol).toLowerCase()}
      </span>
      {name?.name && <span className="truncate text-sm">{name.name}</span>}
    </span>
  )
}
