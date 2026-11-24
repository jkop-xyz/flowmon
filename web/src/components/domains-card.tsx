import { useQuery } from "@tanstack/react-query"
import { AtSign } from "lucide-react"
import { Link, useLocation } from "react-router"

import { AreaChip } from "@/components/area-chip"
import { BarList, BarRow } from "@/components/bar-list"
import { QueryError, SectionSkeleton } from "@/components/page"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { type DomainItem, type DomainResult } from "@/lib/api"
import { domainsQuery } from "@/lib/domains"
import { useExporterFilter } from "@/lib/exporter-filter"
import { formatDateTime } from "@/lib/format"
import { useTimeRange } from "@/lib/time-range"
import { cn } from "@/lib/utils"

/** The busiest Registrable Domains, for the network or one Local Host (host). */
export function DomainsCard({ host, className }: { host?: string; className?: string }) {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const query = useQuery(domainsQuery(preset, exporter, host))
  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area="explorer" icon={AtSign} />
          Domains
        </CardTitle>
        <CardDescription>
          {host ? "The sites it talked to" : "The busiest sites"}, by the names looked up in DNS, grouped by Registrable Domain.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {query.isPending ? (
          <SectionSkeleton rows={5} />
        ) : query.isError ? (
          <QueryError title="Couldn't load the busiest domains" error={query.error} />
        ) : query.data.dnsSources === 0 && query.data.result.items.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            DNS collection is off. Add AdGuard Home as a Data Source to see which domains traffic goes to; the deploy
            guide's "DNS from AdGuard Home" says how.
          </p>
        ) : (
          <DomainList result={query.data.result} host={host} />
        )}
        {query.data && <DomainNote result={query.data.result} />}
      </CardContent>
    </Card>
  )
}

function DomainList({ result, host }: { result: DomainResult; host?: string }) {
  const { search } = useLocation()
  const items = result.items
  if (items.length === 0) return <p className="text-sm text-muted-foreground">No traffic counted in this time range yet.</p>
  const explorer = (domain: string) => {
    const params = new URLSearchParams(search)
    params.set("domain", domain)
    if (host) params.set("host", host)
    return { pathname: "/flows", search: `?${params}` }
  }
  const label = (item: DomainItem) =>
    item.registrable ? (
      <Link
        to={explorer(item.registrable)}
        className="truncate font-mono text-ident underline-offset-4 hover:underline"
        title={`Flows to ${item.registrable} and its subdomains`}
      >
        {item.registrable}
      </Link>
    ) : (
      <Link
        to={explorer("-")}
        className="truncate text-muted-foreground underline-offset-4 hover:underline"
        title="Traffic to addresses no Local Host looked up: DNS over HTTPS, hard-coded addresses, or lookups from before DNS collection"
      >
        No DNS lookup
      </Link>
    )
  return (
    <BarList>
      {items.map((item) => (
        <BarRow
          key={item.registrable || "-"}
          label={label(item)}
          total={item.totalBytes}
          received={item.receivedBytes}
          sent={item.sentBytes}
          max={items[0].totalBytes}
        />
      ))}
    </BarList>
  )
}

/** How far domain totals reach: a few minutes behind, and not before DNS collection began. */
function DomainNote({ result }: { result: DomainResult }) {
  const notes: string[] = []
  if (result.countedThrough) {
    notes.push(
      `Counted up to ${new Date(result.countedThrough).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}: domains are counted five minutes after traffic arrives, once its DNS lookup is in.`,
    )
  }
  if (result.since && new Date(result.since) > new Date(result.from)) {
    notes.push(`Domains start on ${formatDateTime(result.since)}; earlier traffic has none.`)
  }
  if (notes.length === 0) return null
  return <p className={cn("text-xs text-muted-foreground")}>{notes.join(" ")}</p>
}
