import { useQuery } from "@tanstack/react-query"
import { lazy, Suspense, type ReactNode } from "react"
import { Link, useLocation } from "react-router"
import { Area, AreaChart, CartesianGrid, ReferenceArea, ReferenceLine, XAxis, YAxis } from "recharts"

import { Badge } from "@/components/ui/badge"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import { Skeleton } from "@/components/ui/skeleton"
import {
  fetchBandwidth,
  fetchTop,
  type BandwidthPoint,
  type ClassificationChange,
  type TopDimension,
  type ReverseDNSName,
  type TopItem,
} from "@/lib/api"
import { DBIPAttribution } from "@/components/geoip"
import { useGeoIP, usesDBIP } from "@/lib/geoip"
import { Page, QueryError, SectionSkeleton } from "@/components/page"
import { RemoteIPLink } from "@/components/remote"
import { ReverseDNS } from "@/components/reverse-dns"
import { useReverseDNS } from "@/lib/reverse-dns"
import { countryName } from "@/lib/countries"
import {
  formatBitsPerSecond,
  formatBytes,
  formatDateTime,
  formatSeconds,
  formatTick,
  protocolName,
  timeTicks,
} from "@/lib/format"
import { useExporterFilter } from "@/lib/exporter-filter"
import { resolveRange, useTimeRange } from "@/lib/time-range"

// The map and its shapes are a separate chunk, loaded when the Overview first needs them.
const WorldMap = lazy(() => import("@/components/world-map"))

const chartConfig = {
  inboundBps: { label: "Inbound", color: "var(--chart-1)" },
  outboundBps: { label: "Outbound", color: "var(--chart-2)" },
} satisfies ChartConfig

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
            <BandwidthChart
              points={query.data.result.points}
              stepSeconds={query.data.result.stepSeconds}
              rangeMs={preset.ms}
              incompleteFrom={new Date(query.data.incompleteFrom).getTime()}
              changes={query.data.changes ?? []}
            />
          )}
        </CardContent>
      </Card>

      <div className="flex flex-col gap-2">
        <div className="flex flex-col gap-6">
          <CountriesCard />
          <div className="grid gap-6 lg:grid-cols-2">
            <TopCard
              dimension="hosts"
              title="Local Hosts"
              what="Local Hosts"
              description="The busiest Local Hosts by bytes sent and received, including Internal traffic."
            />
            <TopCard
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
          </div>
          <div className="grid gap-6 lg:grid-cols-3">
            <TopCard
              dimension="asns"
              title="ASN Owners"
              what="ASN Owners"
              description="The organisations announcing the Remote IPs."
              mono={false}
              footer={<GeoIPNote />}
            />
            <TopCard
              dimension="ports"
              title="Service Ports"
              what="Service Ports"
              description="The busiest Service Ports, as Network Totals."
            />
            <TopCard
              dimension="protocols"
              title="Protocols"
              what="protocols"
              description="The busiest IP protocols, as Network Totals."
            />
          </div>
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

function BandwidthChart({
  points,
  stepSeconds,
  rangeMs,
  incompleteFrom,
  changes,
}: {
  points: BandwidthPoint[]
  stepSeconds: number
  rangeMs: number
  incompleteFrom: number
  changes: ClassificationChange[]
}) {
  const data = points.map((p) => ({ ...p, time: new Date(p.t).getTime() }))
  const empty = points.every((p) => p.inboundBps === 0 && p.outboundBps === 0)
  const last = data.length > 0 ? data[data.length - 1].time : 0
  const end = last + stepSeconds * 1000
  const shadeFrom = Math.max(incompleteFrom - stepSeconds * 1000, data[0]?.time ?? 0)

  return (
    <div className="relative w-full min-w-0">
      <ChartContainer
        config={chartConfig}
        className="aspect-auto h-72 w-full min-w-0 [&_.recharts-responsive-container]:min-w-0"
      >
        <AreaChart data={data} margin={{ left: 8, right: 8, top: 8 }}>
          <defs>
            {(["inboundBps", "outboundBps"] as const).map((key) => (
              <linearGradient key={key} id={`fill-${key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor={`var(--color-${key})`} stopOpacity={0.5} />
                <stop offset="95%" stopColor={`var(--color-${key})`} stopOpacity={0.05} />
              </linearGradient>
            ))}
          </defs>
          <CartesianGrid vertical={false} />
          <XAxis
            dataKey="time"
            type="number"
            scale="time"
            domain={[data[0]?.time ?? 0, end]}
            tickLine={false}
            axisLine={false}
            ticks={data.length > 0 ? timeTicks(data[0].time, end) : []}
            tickFormatter={(v: number) => formatTick(v, rangeMs)}
          />
          <YAxis tickLine={false} axisLine={false} width={88} tickFormatter={(v: number) => formatBitsPerSecond(v)} />
          {end > shadeFrom && (
            <ReferenceArea
              x1={shadeFrom}
              x2={end}
              fill="var(--muted-foreground)"
              fillOpacity={0.08}
              ifOverflow="hidden"
            />
          )}
          {changes.map((c) => (
            <ReferenceLine
              key={`${c.exporter}/${c.t}`}
              x={new Date(c.t).getTime()}
              stroke="var(--muted-foreground)"
              strokeDasharray="4 4"
            />
          ))}
          <ChartTooltip
            content={
              <ChartTooltipContent
                indicator="dot"
                labelFormatter={(_, payload) => {
                  const time = payload?.[0]?.payload?.time as number | undefined
                  if (time === undefined) return ""
                  const label = formatDateTime(new Date(time).toISOString())
                  return time + stepSeconds * 1000 > incompleteFrom ? `${label} (incomplete)` : label
                }}
                formatter={(value, name) => (
                  <div className="flex w-full items-center justify-between gap-4">
                    <span className="text-muted-foreground">
                      {chartConfig[name as keyof typeof chartConfig]?.label ?? name}
                    </span>
                    <span className="font-mono tabular-nums">{formatBitsPerSecond(Number(value))}</span>
                  </div>
                )}
              />
            }
          />
          {(["inboundBps", "outboundBps"] as const).map((key) => (
            <Area
              key={key}
              dataKey={key}
              type="monotone"
              stroke={`var(--color-${key})`}
              fill={`url(#fill-${key})`}
              strokeWidth={1.5}
              isAnimationActive={false}
            />
          ))}
          <ChartLegend content={<ChartLegendContent />} />
        </AreaChart>
      </ChartContainer>
      {empty && (
        <p className="pointer-events-none absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">
          No Flows in this time range yet.
        </p>
      )}
      <p className="mt-2 text-xs text-muted-foreground">
        Each point averages {formatSeconds(stepSeconds)}. The shaded end is incomplete: Flows for it may still arrive.
      </p>
      {changes.length > 0 && (
        <ul className="mt-1 flex flex-col gap-0.5 text-xs text-muted-foreground">
          {changes.map((c) => (
            <li key={`${c.exporter}/${c.t}`}>
              Dashed line at {formatDateTime(c.t)}: {c.description} (Exporter {c.exporter}).
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function itemLabel(dimension: TopDimension, item: TopItem) {
  switch (dimension) {
    case "hosts":
      return item.host ?? ""
    case "remotes":
      return item.remoteIp ?? ""
    case "protocols":
      return protocolName(item.protocol)
    case "ports":
      return item.servicePort === 0
        ? `${protocolName(item.protocol)} (no port)`
        : `${item.servicePort}/${protocolName(item.protocol).toLowerCase()}`
    case "countries":
      return item.country ? `${countryName(item.country)} (${item.country})` : "Unknown"
    case "asns":
      return item.asn ? `AS${item.asn}${item.asnOrg ? ` ${item.asnOrg}` : ""}` : "Unknown"
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
  if (items.length === 0) return <p className="text-sm text-muted-foreground">No Flows in this time range yet.</p>
  return (
    <ol className="flex flex-col gap-3">
      {items.map((item) => (
        <TopRow
          key={itemKey(item)}
          label={itemLabel(dimension, item)}
          item={item}
          max={items[0].totalBytes}
          mono={mono}
          exporterHost={dimension === "hosts" && item.localIsExporterHost}
          remote={dimension === "remotes"}
          name={dimension === "remotes" ? names[item.remoteIp ?? ""] : undefined}
        />
      ))}
    </ol>
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
}: {
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
    <Card>
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

function TopRow({
  label,
  item,
  max,
  mono,
  exporterHost,
  remote,
  name,
}: {
  label: string
  item: TopItem
  max: number
  mono: boolean
  exporterHost?: boolean
  /** The label is a Remote IP: link to its page. */
  remote?: boolean
  /** Its reverse DNS name, for Remote IPs. */
  name?: ReverseDNSName
}) {
  const width = max > 0 ? Math.max(2, (item.totalBytes / max) * 100) : 0
  const received = item.receivedBytes
  const sent = item.sentBytes + item.internalBytes
  return (
    <li className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-2 text-sm">
        <span className="flex min-w-0 items-center gap-2">
          {remote ? (
            <>
              <RemoteIPLink ip={label} className="shrink-0 font-mono text-xs underline-offset-4 hover:underline" />
              <ReverseDNS name={name} className="min-w-0" />
            </>
          ) : (
            <span className={mono ? "truncate font-mono text-xs" : "truncate"} title={label}>
              {label}
            </span>
          )}
          {exporterHost && <Badge variant="outline">Exporter Host</Badge>}
        </span>
        <span className="shrink-0 tabular-nums">{formatBytes(item.totalBytes)}</span>
      </div>
      <div className="flex h-1.5 overflow-hidden rounded-full bg-muted" style={{ width: `${width}%` }}>
        <div className="h-full bg-chart-1" style={{ flexGrow: received }} title={`Received ${formatBytes(received)}`} />
        <div className="h-full bg-chart-2" style={{ flexGrow: sent }} title={`Sent ${formatBytes(sent)}`} />
      </div>
    </li>
  )
}
