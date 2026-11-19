import { useQuery } from "@tanstack/react-query"
import { lazy, Suspense, type ReactNode } from "react"
import { Link, useLocation } from "react-router"

import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  fetchBandwidth,
  fetchTop,
  type BandwidthResult,
  type TopDimension,
  type TopItem,
} from "@/lib/api"
import { DBIPAttribution } from "@/components/geoip"
import { useGeoIP, usesDBIP } from "@/lib/geoip"
import { Page, QueryError, SectionSkeleton } from "@/components/page"
import { BarList, BarRow } from "@/components/bar-list"
import { EstimateNote } from "@/components/estimate"
import { HostLink } from "@/components/host"
import { RemoteIPLink } from "@/components/remote"
import { ServicePort } from "@/components/service-port"
import { TrafficChart, type TrafficSeries } from "@/components/traffic-chart"
import { serviceKey, useServiceNames } from "@/lib/services"
import { useHostNames } from "@/lib/hosts"
import { ReverseDNS } from "@/components/reverse-dns"
import { useReverseDNS } from "@/lib/reverse-dns"
import { countryName } from "@/lib/countries"
import { protocolName } from "@/lib/format"
import { useExporterFilter } from "@/lib/exporter-filter"
import { resolveRange, useTimeRange } from "@/lib/time-range"

// The map and its shapes are a separate chunk, loaded when the Overview first needs them.
const WorldMap = lazy(() => import("@/components/world-map"))

const bandwidthSeries: TrafficSeries<"inboundBps" | "outboundBps">[] = [
  { key: "inboundBps", label: "Inbound", color: "var(--chart-1)" },
  { key: "outboundBps", label: "Outbound", color: "var(--chart-2)" },
]

export function OverviewPage() {
  const { search } = useLocation()
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const query = useQuery({
    queryKey: ["bandwidth", preset.value, exporter],
    queryFn: ({ signal }) => fetchBandwidth({ ...resolveRange(preset), exporter }, signal),
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  })

  const incomplete = query.data && new Date(query.data.result.to) > new Date(query.data.incompleteFrom)

  return (
    <Page>
      {/* On wide screens: the chart and the map side by side, then every list in one row. */}
      <div className="grid gap-6 4xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Bandwidth</CardTitle>
            <CardDescription>
              Traffic crossing the boundary between your Local Networks and the internet, {preset.label.toLowerCase()}.
              Each Flow is spread over its duration.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {query.isPending ? (
              <Skeleton className="h-72 w-full" />
            ) : query.isError ? (
              <QueryError title="Couldn't load bandwidth" error={query.error} />
            ) : (
              <TrafficChart
                points={query.data.result.points}
                series={bandwidthSeries}
                stepSeconds={query.data.result.stepSeconds}
                rangeMs={preset.ms}
                incompleteFrom={new Date(query.data.incompleteFrom).getTime()}
                changes={query.data.changes ?? []}
              />
            )}
            {query.data && (
              <div className="mt-3">
                <EstimateNote estimated={query.data.result.estimatedBytes} total={bandwidthBytes(query.data.result)} />
              </div>
            )}
          </CardContent>
        </Card>
        <CountriesCard />
      </div>

      <div className="flex flex-col gap-2">
        <div className="grid items-start gap-6 lg:grid-cols-6 4xl:grid-cols-5">
          <TopCard
            className="lg:col-span-3 4xl:col-span-1"
            dimension="hosts"
            title="Local Hosts"
            what="Local Hosts"
            description="The busiest Local Hosts by bytes sent and received, including Internal traffic."
            action={
              <Link to={{ pathname: "/hosts", search }} className="text-sm underline-offset-4 hover:underline">
                All Local Hosts
              </Link>
            }
          />
          <TopCard
            className="lg:col-span-3 4xl:col-span-1"
            dimension="remotes"
            title="Remote IPs"
            what="Remote IPs"
            description="The busiest addresses on the internet side, by bytes sent and received."
            action={
              <Link to={{ pathname: "/remote", search }} className="text-sm underline-offset-4 hover:underline">
                All Remote IPs
              </Link>
            }
          />
          <TopCard
            className="lg:col-span-2 4xl:col-span-1"
            dimension="asns"
            title="ASN Owners"
            what="ASN Owners"
            description="The organisations announcing the Remote IPs."
            mono={false}
            footer={<GeoIPNote />}
          />
          <TopCard
            className="lg:col-span-2 4xl:col-span-1"
            dimension="ports"
            title="Service Ports"
            what="Service Ports"
            description="The busiest Service Ports, as Network Totals."
          />
          <TopCard
            className="lg:col-span-2 4xl:col-span-1"
            dimension="protocols"
            title="Protocols"
            what="protocols"
            description="The busiest IP protocols, as Network Totals."
          />
        </div>
        {incomplete && (
          <p className="text-xs text-muted-foreground">
            The chart and lists include the last few minutes, which are incomplete: Flows for them may still arrive.
          </p>
        )}
      </div>
    </Page>
  )
}

/** The bytes a bandwidth series shows: its rates times the step. */
function bandwidthBytes(r: BandwidthResult) {
  return r.points.reduce((n, p) => n + ((p.inboundBps + p.outboundBps + p.internalBps) * r.stepSeconds) / 8, 0)
}

/** Text labels; hosts, Remote IPs and ports are rendered as links and names instead. */
function itemLabel(dimension: TopDimension, item: TopItem) {
  switch (dimension) {
    case "protocols":
      return protocolName(item.protocol)
    case "countries":
      return item.country ? `${countryName(item.country)} (${item.country})` : "Unknown"
    case "asns":
      return item.asn ? `AS${item.asn}${item.asnOrg ? ` ${item.asnOrg}` : ""}` : "Unknown"
    default:
      return ""
  }
}

function itemKey(item: TopItem) {
  return `${item.host}/${item.protocol}/${item.servicePort}/${item.country}/${item.asn}`
}

/** Under GeoIP data: why it is missing, or the attribution DB-IP's licence asks for. */
function GeoIPNote() {
  const geoip = useGeoIP()
  if (geoip.data && geoip.data.databases.every((d) => !d.source)) {
    return (
      <>
        No GeoIP database is loaded yet, so countries and ASN Owners show as Unknown.{" "}
        <Link to="/settings" className="underline underline-offset-4">
          Settings
        </Link>{" "}
        says why.
      </>
    )
  }
  return usesDBIP(geoip.data) ? <DBIPAttribution /> : null
}

/** Where Remote IPs are: a world map and the top countries. */
function CountriesCard() {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  // One query feeds both the map (every country) and the list (the top few).
  const countries = useQuery({
    queryKey: ["top", "countries", preset.value, exporter],
    queryFn: ({ signal }) => fetchTop("countries", { ...resolveRange(preset), exporter }, signal, 300),
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>Countries</CardTitle>
        <CardDescription>
          Where Remote IPs are, by bytes sent and received. Shades are on a log scale; hover a country for its numbers.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {countries.isPending ? (
          <Skeleton className="aspect-[2.2] w-full" />
        ) : countries.isError ? (
          <QueryError title="Couldn't load the busiest countries" error={countries.error} />
        ) : (
          <div className="grid items-start gap-6 lg:grid-cols-[3fr_2fr]">
            <Suspense fallback={<Skeleton className="aspect-[2.2] w-full" />}>
              <WorldMap items={countries.data.result.items} total={countries.data.result.totalBytes} />
            </Suspense>
            <TopList dimension="countries" items={countries.data.result.items.slice(0, 8)} mono={false} />
          </div>
        )}
        {countries.data && (
          <EstimateNote estimated={countries.data.result.estimatedBytes} total={countries.data.result.totalBytes} />
        )}
        <CardFooterNote>
          <GeoIPNote />
        </CardFooterNote>
      </CardContent>
    </Card>
  )
}

function CardFooterNote({ children }: { children: ReactNode }) {
  return <p className="text-xs text-muted-foreground empty:hidden">{children}</p>
}

function TopList({ dimension, items, mono = true }: { dimension: TopDimension; items: TopItem[]; mono?: boolean }) {
  const names = useReverseDNS(dimension === "remotes" ? items.map((i) => i.remoteIp ?? "") : [])
  const hostNames = useHostNames(dimension === "hosts" ? items.map((i) => i.host ?? "") : [])
  const serviceNames = useServiceNames(
    dimension === "ports" ? items.map((i) => ({ protocol: i.protocol, port: i.servicePort })) : [],
  )
  if (items.length === 0) return <p className="text-sm text-muted-foreground">No Flows in this time range yet.</p>

  const label = (item: TopItem) => {
    switch (dimension) {
      case "hosts":
        return (
          <HostLink
            host={item.host ?? ""}
            name={hostNames[item.host ?? ""]}
            exporterHost={item.localIsExporterHost}
            addresses={item.addresses}
          />
        )
      case "remotes":
        return (
          <>
            <RemoteIPLink ip={item.remoteIp ?? ""} className="shrink-0 font-mono text-xs underline-offset-4 hover:underline" />
            <ReverseDNS name={names[item.remoteIp ?? ""]} className="min-w-0" />
          </>
        )
      case "ports":
        return (
          <ServicePort
            protocol={item.protocol}
            port={item.servicePort}
            name={serviceNames[serviceKey(item.protocol, item.servicePort)]}
          />
        )
    }
    const text = itemLabel(dimension, item)
    return (
      <span className={mono ? "truncate font-mono text-xs" : "truncate"} title={text}>
        {text}
      </span>
    )
  }

  return (
    <BarList>
      {items.map((item) => (
        <BarRow
          key={itemKey(item)}
          label={label(item)}
          total={item.totalBytes}
          received={item.receivedBytes}
          sent={item.sentBytes + item.internalBytes}
          max={items[0].totalBytes}
          estimated={item.estimatedBytes}
        />
      ))}
    </BarList>
  )
}

function TopCard({
  dimension,
  title,
  what,
  description,
  mono = true,
  footer,
  action,
  className,
}: {
  className?: string
  dimension: TopDimension
  title: string
  /** How errors name the list, e.g. "Service Ports". */
  what: string
  description: string
  /** Addresses and ports read best in a monospace font; names don't. */
  mono?: boolean
  footer?: ReactNode
  /** A link in the header, e.g. to the full list. */
  action?: ReactNode
}) {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const { search } = useLocation()
  const query = useQuery({
    queryKey: ["top", dimension, preset.value, exporter],
    queryFn: ({ signal }) => fetchTop(dimension, { ...resolveRange(preset), exporter }, signal),
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  })

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
        {action && <CardAction>{action}</CardAction>}
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {query.isPending ? (
          <SectionSkeleton rows={5} />
        ) : query.isError ? (
          <QueryError title={`Couldn't load the busiest ${what}`} error={query.error} />
        ) : (
          <TopList dimension={dimension} items={query.data.result.items} mono={mono} />
        )}
        {query.data && (
          <EstimateNote estimated={query.data.result.estimatedBytes} total={query.data.result.totalBytes} />
        )}
        {query.data?.result.approximate && (
          <CardFooterNote>
            Approximate: from hourly lists of the busiest Remote IPs per Local Host.{" "}
            <Link to={{ pathname: "/remote", search }} className="underline underline-offset-4">
              Details
            </Link>
          </CardFooterNote>
        )}
        {footer && <CardFooterNote>{footer}</CardFooterNote>}
      </CardContent>
    </Card>
  )
}
