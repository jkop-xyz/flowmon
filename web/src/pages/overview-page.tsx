import { queryOptions, useQuery } from "@tanstack/react-query"
import { Building2, ChartArea, Earth, Globe, Layers, Monitor, Pencil, Plug, type LucideIcon } from "lucide-react"
import { Fragment, lazy, Suspense, useState, type ReactNode } from "react"
import { Link, useLocation, useNavigate } from "react-router"

import { AreaChip } from "@/components/area-chip"
import { OverviewEditor, type EditorSection } from "@/components/overview-editor"
import { Button } from "@/components/ui/button"
import { DomainsCard } from "@/components/domains-card"
import { useDomainsShown } from "@/lib/domains"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  fetchBandwidth,
  fetchOverviewLayout,
  fetchTop,
  type BandwidthResult,
  type TopDimension,
  type TopItem,
  type WANLoad,
} from "@/lib/api"
import {
  DeviceHealthCard,
  DNSOverviewCard,
  InterfaceProblemsCard,
  UsageNowCard,
} from "@/components/overview-network"
import { WANLoadCard } from "@/components/wan-load-card"
import { dnsQuery, lineShare, useNetworkOverview } from "@/lib/overview"
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
import { formatBitsPerSecond, formatBytes, formatNumber, protocolName } from "@/lib/format"
import { useExporterFilter } from "@/lib/exporter-filter"
import { resolveRange, useTimeRange, type TimeRangePreset } from "@/lib/time-range"
import type { Area } from "@/lib/navigation"
import { cn } from "@/lib/utils"
import { useDeviceLabels } from "@/lib/devices"
import { overviewLayoutKey, resolveLayout, type OverviewSectionId } from "@/lib/overview-layout"

// The map and its shapes are a separate chunk, loaded when the Overview first needs them.
const WorldMap = lazy(() => import("@/components/world-map"))

type Range = { preset: TimeRangePreset; exporter?: string }

/** The chart's query; the figures row reads the same one. */
const bandwidthQuery = ({ preset, exporter }: Range) =>
  queryOptions({
    queryKey: ["bandwidth", preset.value, exporter],
    queryFn: ({ signal }) => fetchBandwidth({ ...resolveRange(preset), exporter }, signal),
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  })

/** A top list's query; the figures row reads the Local Hosts and Remote IPs ones for their counts. */
const topQuery = (dimension: TopDimension, { preset, exporter }: Range) =>
  queryOptions({
    queryKey: ["top", dimension, preset.value, exporter],
    queryFn: ({ signal }) => fetchTop(dimension, { ...resolveRange(preset), exporter }, signal),
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  })

const bandwidthSeries: TrafficSeries<"inboundBps" | "outboundBps">[] = [
  { key: "inboundBps", label: "Inbound", color: "var(--chart-1)" },
  { key: "outboundBps", label: "Outbound", color: "var(--chart-2)" },
]

/** What a section's renderer reads from the page. */
type OverviewContext = {
  network: ReturnType<typeof useNetworkOverview>
  showDomains: boolean
  search: string
}

type OverviewSection = {
  /** The name in the layout editor. */
  name: string
  /** The cards it holds, for the layout editor. */
  cards: string
  /** Column proportions of its cards, for the layout editor's sketch. */
  sketch: number[]
  /** Whether it has anything to show; a section that isn't available is skipped. */
  available: (ctx: OverviewContext & { dnsOn: boolean }) => boolean
  /** The layout editor's note when it isn't available. */
  unavailable?: string
  render: (ctx: OverviewContext) => ReactNode
}

const overviewSections: Record<OverviewSectionId, OverviewSection> = {
  figures: {
    name: "Figures",
    cards: "Download now, Upload now, Received and sent, DNS lookups",
    sketch: [1, 1, 1, 1],
    available: () => true,
    render: ({ network }) => <Figures wan={network.data?.wan ?? undefined} />,
  },
  load: {
    name: "Load now",
    cards: "WAN load (Bandwidth without SNMP), Who is using it now",
    sketch: [1.75, 1],
    available: () => true,
    // The load now and who causes it; without SNMP, the Flows' bandwidth.
    render: ({ network }) => (
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.75fr)_minmax(0,1fr)]">
        {network.data?.wan ? (
          <WANLoadCard wan={network.data.wan} fetched={network.dataUpdatedAt} />
        ) : (
          <BandwidthCard />
        )}
        <UsageNowCard />
      </div>
    ),
  },
  dns: {
    name: "DNS",
    cards: "Lookups and blocks, Most queried or most blocked",
    sketch: [1],
    available: ({ dnsOn }) => dnsOn,
    unavailable: "Needs AdGuard Home, which isn't set up. Shown once it is.",
    render: () => <DNSOverviewCard />,
  },
  top: {
    name: "Top lists",
    cards: "Devices, Domains, Remote IPs",
    sketch: [1, 1, 1],
    available: () => true,
    render: ({ showDomains, search }) => (
      <div className={cn("grid gap-6", showDomains ? "lg:grid-cols-3" : "lg:grid-cols-2")}>
        <TopCard
          dimension="devices"
          area="hosts"
          icon={Monitor}
          title="Devices"
          what="Devices"
          description="The busiest Devices by bytes sent and received, including Internal traffic. Traffic without a known MAC counts by address, or by Host Label prefix."
          action={
            <Link to={{ pathname: "/hosts", search }} className="text-sm underline-offset-4 hover:underline">
              All Devices
            </Link>
          }
        />
        {showDomains && <DomainsCard />}
        <TopCard
          dimension="remotes"
          area="remote"
          icon={Globe}
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
    ),
  },
  internet: {
    name: "The internet side",
    cards: "Countries map, ASN Owners, Service Ports, Protocols",
    sketch: [2, 1, 1, 1],
    available: () => true,
    render: () => (
      <div className="grid gap-6 lg:grid-cols-3 3xl:grid-cols-5">
        <CountriesCard className="lg:col-span-3 3xl:col-span-2" />
        <TopCard
          dimension="asns"
          area="remote"
          icon={Building2}
          title="ASN Owners"
          what="ASN Owners"
          description="The organisations announcing the Remote IPs."
          mono={false}
          footer={<GeoIPNote />}
        />
        <TopCard
          dimension="ports"
          area="explorer"
          icon={Plug}
          title="Service Ports"
          what="Service Ports"
          description="The busiest Service Ports, as Network Totals."
        />
        <TopCard
          dimension="protocols"
          area="explorer"
          icon={Layers}
          title="Protocols"
          what="protocols"
          description="The busiest IP protocols, as Network Totals."
        />
      </div>
    ),
  },
  health: {
    name: "Health",
    cards: "Device health, Interface problems",
    sketch: [1, 1],
    available: ({ network }) => (network.data?.devices.length ?? 0) > 0,
    unavailable: "Needs an SNMP source that reports devices. Shown once there is one.",
    render: ({ network }) => (
      <div className="grid gap-6 lg:grid-cols-2">
        <DeviceHealthCard devices={network.data?.devices ?? []} fetched={network.dataUpdatedAt} />
        <InterfaceProblemsCard problems={network.data?.problems ?? []} />
      </div>
    ),
  },
}

/** The saved layout; an error, or a 404 from a server without the endpoint, leaves the default order. */
const overviewLayoutQuery = queryOptions({
  queryKey: overviewLayoutKey,
  queryFn: ({ signal }) => fetchOverviewLayout(signal),
  retry: false,
  staleTime: Infinity,
})

export function OverviewPage() {
  const { search } = useLocation()
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const query = useQuery(bandwidthQuery({ preset, exporter }))
  const network = useNetworkOverview()
  const dns = useQuery(dnsQuery(preset))
  const layout = useQuery(overviewLayoutQuery)
  const [editing, setEditing] = useState(false)
  const showDomains = useDomainsShown()
  const incomplete = query.data && new Date(query.data.result.to) > new Date(query.data.incompleteFrom)
  const dnsOn = (dns.data?.dnsSources ?? 0) > 0

  // Draw once the saved layout has settled, so the page doesn't show the default order and then jump.
  if (layout.isPending) {
    return (
      <Page>
        <SectionSkeleton rows={5} />
      </Page>
    )
  }

  const ctx = { network, showDomains, search }
  const resolved = resolveLayout(layout.data ?? null)

  if (editing) {
    const sections = Object.fromEntries(
      Object.entries(overviewSections).map(([id, { name, cards, sketch, available, unavailable }]) => [
        id,
        { name, cards, sketch, unavailable: available({ ...ctx, dnsOn }) ? undefined : unavailable },
      ]),
    ) as Record<OverviewSectionId, EditorSection>
    return (
      <Page>
        <OverviewEditor layout={resolved} sections={sections} onClose={() => setEditing(false)} />
      </Page>
    )
  }

  const shown = resolved.filter((entry) => !entry.hidden && overviewSections[entry.id].available({ ...ctx, dnsOn }))

  return (
    <Page
      actions={
        <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
          <Pencil data-icon="inline-start" />
          Edit layout
        </Button>
      }
    >
      {shown.map(({ id }) => (
        <Fragment key={id}>{overviewSections[id].render(ctx)}</Fragment>
      ))}
      {incomplete && shown.some(({ id }) => id === "top" || id === "internet") && (
        <p className="text-xs text-muted-foreground">
          The chart and lists include the last few minutes, which are incomplete: Flows for them may still arrive.
        </p>
      )}
    </Page>
  )
}

/** Bandwidth from Flows: what the Overview charts when no SNMP source polls a WAN Interface. */
function BandwidthCard() {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const query = useQuery(bandwidthQuery({ preset, exporter }))
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area="overview" icon={ChartArea} />
          Bandwidth
        </CardTitle>
        <CardDescription>
          Traffic crossing the boundary between your Local Networks and the internet, {preset.label.toLowerCase()}. Each
          Flow is spread over its duration. With an SNMP source on the router and a WAN Interface marked, this shows
          the router's own counters instead.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col">
        {query.isPending ? (
          <Skeleton className="h-72 w-full" />
        ) : query.isError ? (
          <QueryError title="Couldn't load bandwidth" error={query.error} />
        ) : (
          <TrafficChart
            fill
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

/** A row's React key, from every field a list can be by. A field missing here gives that list's rows one key. */
function itemKey(item: TopItem) {
  return `${item.mac}/${item.host}/${item.remoteIp}/${item.protocol}/${item.servicePort}/${item.country}/${item.asn}`
}

/** Under GeoIP data: why it is missing, or the attribution DB-IP's licence asks for. */
function GeoIPNote() {
  const geoip = useGeoIP()
  if (geoip.data && geoip.data.databases.every((d) => !d.source)) {
    return (
      <>
        No GeoIP database is loaded yet, so countries and ASN Owners show as Unknown.{" "}
        <Link to="/settings/geoip" className="underline underline-offset-4">
          Settings → GeoIP
        </Link>{" "}
        says why.
      </>
    )
  }
  return usesDBIP(geoip.data) ? <DBIPAttribution /> : null
}

/** Where Remote IPs are: a world map and the top countries. */
/** The Flow Explorer with a filter, keeping the page's time range and Exporter. */
function explorerFor(search: string, filters: Record<string, string>) {
  const params = new URLSearchParams(search)
  for (const [k, v] of Object.entries(filters)) params.set(k, v)
  return { pathname: "/flows", search: `?${params}` }
}

function CountriesCard({ className }: { className?: string }) {
  const navigate = useNavigate()
  const { search } = useLocation()
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
    <Card className={className}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area="remote" icon={Earth} />
          Countries
        </CardTitle>
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
              <WorldMap
                items={countries.data.result.items}
                total={countries.data.result.totalBytes}
                onSelect={(country) => navigate(explorerFor(search, { country }))}
              />
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
  const { search } = useLocation()
  const names = useReverseDNS(dimension === "remotes" ? items.map((i) => i.remoteIp ?? "") : [])
  const hostNames = useHostNames(dimension === "hosts" || dimension === "devices" ? items.map((i) => i.host ?? "") : [])
  const deviceLabels = useDeviceLabels()
  const serviceNames = useServiceNames(
    dimension === "ports" ? items.map((i) => ({ protocol: i.protocol, port: i.servicePort })) : [],
  )
  if (items.length === 0) return <p className="text-sm text-muted-foreground">No Flows in this time range yet.</p>

  const label = (item: TopItem) => {
    switch (dimension) {
      case "hosts":
      case "devices":
        return (
          <HostLink
            host={item.host ?? ""}
            name={hostNames[item.host ?? ""]}
            exporterHost={item.localIsExporterHost}
            addresses={item.addresses}
            device={item.mac ? { mac: item.mac, vendor: item.vendor, privateMac: item.privateMac, label: deviceLabels[item.mac] } : undefined}
          />
        )
      case "remotes":
        return (
          <>
            <RemoteIPLink ip={item.remoteIp ?? ""} className="shrink-0 font-mono text-ident underline-offset-4 hover:underline" />
            <ReverseDNS name={names[item.remoteIp ?? ""]} className="min-w-0" />
          </>
        )
      case "countries":
        // Unknown ("") has no filter to open.
        if (!item.country) break
        return (
          <Link
            to={explorerFor(search, { country: item.country })}
            className="truncate underline-offset-4 hover:underline"
            title={`Flows with ${itemLabel(dimension, item)}`}
          >
            {itemLabel(dimension, item)}
          </Link>
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
      <span className={mono ? "truncate font-mono text-ident" : "truncate"} title={text}>
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
  area,
  icon,
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
  /** The area its rows lead to, which colours the title's chip. */
  area: Area
  icon: LucideIcon
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
  const query = useQuery(topQuery(dimension, { preset, exporter }))

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area={area} icon={icon} />
          {title}
        </CardTitle>
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

/**
 * The headline numbers: the WAN's load now against the line (with SNMP), the range's traffic, Local Hosts and
 * Remote IPs, and DNS lookups when a DNS source exists. Each comes from a request the cards make anyway.
 */
function Figures({ wan }: { wan?: WANLoad }) {
  const { search } = useLocation()
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const bandwidth = useQuery(bandwidthQuery({ preset, exporter }))
  const hosts = useQuery(topQuery("devices", { preset, exporter }))
  const remotes = useQuery(topQuery("remotes", { preset, exporter }))
  const dns = useQuery(dnsQuery(preset))
  const bytes = (key: "inboundBps" | "outboundBps") =>
    bandwidth.data &&
    formatBytes(bandwidth.data.result.points.reduce((n, p) => n + (p[key] * bandwidth.data.result.stepSeconds) / 8, 0))
  const remoteCount = remotes.data && formatNumber(remotes.data.result.count)
  const d = dns.data?.dnsSources ? dns.data.result : undefined
  const devices = hosts.data && formatNumber(hosts.data.result.count)
  const remoteLabel = remotes.data?.result.approximate ? `at least ${remoteCount}` : remoteCount
  const figures: ReactNode[] = []
  if (wan) {
    // The load now leads; the range's traffic shares one tile, as do its counts.
    figures.push(
      <LoadFigure key="down" label="Download now" color="var(--chart-1)" bps={wan.now?.inBps} line={wan.line.downBps} lineSet={wan.line.fromSetting} />,
      <LoadFigure key="up" label="Upload now" color="var(--chart-2)" bps={wan.now?.outBps} line={wan.line.upBps} lineSet={wan.line.fromSetting} />,
      <Figure
        key="traffic"
        label={`Received · sent, ${preset.label.toLowerCase()}`}
        value={bytes("inboundBps")}
        detail={bytes("outboundBps")}
        footer={
          <span className="flex flex-wrap gap-x-3">
            <AreaLink area="hosts" to={{ pathname: "/hosts", search }}>
              {devices ?? "…"} Devices
            </AreaLink>
            <AreaLink area="remote" to={{ pathname: "/remote", search }}>
              {remoteLabel ?? "…"} Remote IPs
            </AreaLink>
          </span>
        }
      />,
    )
  } else {
    figures.push(
      <Figure key="rx" label={<Swatch color="var(--chart-1)">Received</Swatch>} value={bytes("inboundBps")} />,
      <Figure key="tx" label={<Swatch color="var(--chart-2)">Sent</Swatch>} value={bytes("outboundBps")} />,
      <Figure key="hosts" label={<AreaLink area="hosts" to={{ pathname: "/hosts", search }}>Devices</AreaLink>} value={devices} />,
      <Figure key="remotes" label={<AreaLink area="remote" to={{ pathname: "/remote", search }}>Remote IPs</AreaLink>} value={remoteLabel} />,
    )
  }
  if (d) {
    figures.push(
      <Figure
        key="dns"
        label={<AreaLink area="dns" to={{ pathname: "/dns", search }}>DNS lookups</AreaLink>}
        value={formatNumber(d.queries)}
        detail={d.queries > 0 ? `${((100 * d.blocked) / d.queries).toFixed(1)} % blocked` : undefined}
      />,
    )
  }
  return (
    <div
      className={cn(
        "grid grid-cols-2 gap-px overflow-hidden rounded-xl border bg-border",
        figures.length === 3 ? "lg:grid-cols-3" : figures.length === 4 ? "lg:grid-cols-4" : "lg:grid-cols-5",
      )}
    >
      {figures}
    </div>
  )
}

/** A rate now against the line's speed, with a meter that warns above 80 %. */
function LoadFigure({ label, color, bps, line, lineSet }: { label: string; color: string; bps?: number; line: number; lineSet: boolean }) {
  const share = bps !== undefined ? lineShare(bps, line) : undefined
  return (
    <div className="flex flex-col gap-1 bg-card px-5 py-4">
      <span className="text-xs text-muted-foreground">
        <Swatch color={color}>{label}</Swatch>
      </span>
      {bps === undefined ? (
        <span className="text-sm text-muted-foreground">No recent counters</span>
      ) : (
        <span className="text-2xl font-semibold tracking-tight whitespace-nowrap tabular-nums">{formatBitsPerSecond(bps)}</span>
      )}
      {share !== undefined && (
        <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-muted">
          <div
            className={cn("h-full rounded-full", share > 0.8 ? "bg-warning" : "")}
            style={{ width: `${Math.min(100, share * 100)}%`, background: share > 0.8 ? undefined : color }}
          />
        </div>
      )}
      {share !== undefined && (
        <span className={cn("text-xs text-muted-foreground tabular-nums", share > 0.8 && "font-medium text-warning-foreground")}>
          {(share * 100).toFixed(0)} % of the {formatBitsPerSecond(line)} {lineSet ? "line" : "port"}
        </span>
      )}
    </div>
  )
}

function Figure({
  label,
  value,
  detail,
  footer,
}: {
  label: ReactNode
  value: string | undefined
  detail?: string
  footer?: ReactNode
}) {
  return (
    <div className="flex flex-col gap-1 bg-card px-5 py-4">
      <span className="text-xs text-muted-foreground">{label}</span>
      {value === undefined ? (
        <Skeleton className="h-8 w-24" />
      ) : (
        <span className="text-2xl font-semibold tracking-tight tabular-nums">
          {value}
          {detail && <span className="text-sm font-normal text-muted-foreground"> · {detail}</span>}
        </span>
      )}
      {footer && <span className="text-xs">{footer}</span>}
    </div>
  )
}

function Swatch({ color, children }: { color: string; children: ReactNode }) {
  return (
    <span className="flex items-center gap-1.5">
      <span className="size-2 rounded-sm" style={{ background: color }} />
      {children}
    </span>
  )
}

function AreaLink({ area, to, children }: { area: Area; to: { pathname: string; search: string }; children: ReactNode }) {
  return (
    <Link data-area={area} to={to} className="font-medium text-area-ink underline-offset-4 hover:underline">
      {children} →
    </Link>
  )
}
