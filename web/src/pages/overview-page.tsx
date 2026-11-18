import { useQuery } from "@tanstack/react-query"
import { AlertTriangle } from "lucide-react"
import { Area, AreaChart, CartesianGrid, ReferenceArea, XAxis, YAxis } from "recharts"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import { Skeleton } from "@/components/ui/skeleton"
import { fetchBandwidth, fetchTop, type BandwidthPoint, type TopDimension, type TopItem } from "@/lib/api"
import { formatBitsPerSecond, formatBytes, formatTick, protocolName } from "@/lib/format"
import { useExporterFilter } from "@/lib/exporter-filter"
import { resolveRange, useTimeRange } from "@/lib/time-range"

const chartConfig = {
  inboundBps: { label: "Inbound", color: "var(--chart-1)" },
  outboundBps: { label: "Outbound", color: "var(--chart-2)" },
} satisfies ChartConfig

export function OverviewPage() {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const query = useQuery({
    queryKey: ["bandwidth", preset.value, exporter],
    queryFn: ({ signal }) => fetchBandwidth({ ...resolveRange(preset), exporter }, signal),
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  })

  return (
    <div className="flex flex-col gap-6">
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
            />
          )}
        </CardContent>
      </Card>

      <div className="grid gap-6 lg:grid-cols-3">
        <TopCard
          dimension="hosts"
          title="Top Local Hosts"
          description="Sent and received by each Local Host, including Internal traffic."
        />
        <TopCard dimension="protocols" title="Top protocols" description="Network Totals by IP protocol." />
        <TopCard
          dimension="ports"
          title="Top Service Ports"
          description="Network Totals by protocol and Service Port."
        />
      </div>
    </div>
  )
}

function QueryError({ title, error }: { title: string; error: Error }) {
  return (
    <Alert variant="destructive">
      <AlertTriangle />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{error.message}</AlertDescription>
    </Alert>
  )
}

function BandwidthChart({
  points,
  stepSeconds,
  rangeMs,
  incompleteFrom,
}: {
  points: BandwidthPoint[]
  stepSeconds: number
  rangeMs: number
  incompleteFrom: number
}) {
  const data = points.map((p) => ({ ...p, time: new Date(p.t).getTime() }))
  const empty = points.every((p) => p.inboundBps === 0 && p.outboundBps === 0)
  const last = data.length > 0 ? data[data.length - 1].time : 0
  const shadeFrom = Math.max(incompleteFrom - stepSeconds * 1000, data[0]?.time ?? 0)

  return (
    <div className="relative">
      <ChartContainer config={chartConfig} className="aspect-auto h-72 w-full">
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
            domain={["dataMin", "dataMax"]}
            tickLine={false}
            axisLine={false}
            minTickGap={48}
            tickFormatter={(v: number) => formatTick(v, rangeMs)}
          />
          <YAxis tickLine={false} axisLine={false} width={88} tickFormatter={(v: number) => formatBitsPerSecond(v)} />
          {last > shadeFrom && (
            <ReferenceArea
              x1={shadeFrom}
              x2={last}
              fill="var(--muted-foreground)"
              fillOpacity={0.08}
              ifOverflow="hidden"
            />
          )}
          <ChartTooltip
            content={
              <ChartTooltipContent
                indicator="dot"
                labelFormatter={(_, payload) => {
                  const time = payload?.[0]?.payload?.time as number | undefined
                  if (time === undefined) return ""
                  const label = new Date(time).toLocaleString()
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
        Each point averages {formatStep(stepSeconds)}. The shaded end is incomplete: Flows for it may still arrive.
      </p>
    </div>
  )
}

function formatStep(seconds: number) {
  if (seconds < 60) return `${seconds} seconds`
  if (seconds < 3600) return `${seconds / 60} minute${seconds === 60 ? "" : "s"}`
  return `${seconds / 3600} hour${seconds === 3600 ? "" : "s"}`
}

function itemLabel(dimension: TopDimension, item: TopItem) {
  switch (dimension) {
    case "hosts":
      return item.host ?? ""
    case "protocols":
      return protocolName(item.protocol)
    case "ports":
      return item.servicePort === 0
        ? `${protocolName(item.protocol)} (no port)`
        : `${item.servicePort}/${protocolName(item.protocol).toLowerCase()}`
  }
}

function TopCard({ dimension, title, description }: { dimension: TopDimension; title: string; description: string }) {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const query = useQuery({
    queryKey: ["top", dimension, preset.value, exporter],
    queryFn: ({ signal }) => fetchTop(dimension, { ...resolveRange(preset), exporter }, signal),
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        {query.isPending ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 5 }, (_, i) => (
              <Skeleton key={i} className="h-8 w-full" />
            ))}
          </div>
        ) : query.isError ? (
          <QueryError title={`Couldn't load ${title.toLowerCase()}`} error={query.error} />
        ) : query.data.result.items.length === 0 ? (
          <p className="text-sm text-muted-foreground">No traffic in this time range yet.</p>
        ) : (
          <>
            <ol className="flex flex-col gap-3">
              {query.data.result.items.map((item) => (
                <TopRow
                  key={`${item.host}/${item.protocol}/${item.servicePort}`}
                  label={itemLabel(dimension, item)}
                  item={item}
                  max={query.data.result.items[0].totalBytes}
                  exporterHost={dimension === "hosts" && item.localIsExporterHost}
                />
              ))}
            </ol>
            {new Date(query.data.result.to) > new Date(query.data.incompleteFrom) && (
              <p className="mt-4 text-xs text-muted-foreground">
                Includes the last few minutes, which are incomplete: Flows for them may still arrive.
              </p>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

function TopRow({
  label,
  item,
  max,
  exporterHost,
}: {
  label: string
  item: TopItem
  max: number
  exporterHost?: boolean
}) {
  const width = max > 0 ? Math.max(2, (item.totalBytes / max) * 100) : 0
  const received = item.receivedBytes
  const sent = item.sentBytes + item.internalBytes
  return (
    <li className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-2 text-sm">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate font-mono text-xs">{label}</span>
          {exporterHost && <Badge variant="outline">router</Badge>}
        </span>
        <span className="shrink-0 tabular-nums">{formatBytes(item.totalBytes)}</span>
      </div>
      <div className="flex h-1.5 overflow-hidden rounded-full bg-muted" style={{ width: `${width}%` }}>
        <div
          className="h-full bg-[var(--chart-1)]"
          style={{ flexGrow: received }}
          title={`Received ${formatBytes(received)}`}
        />
        <div className="h-full bg-[var(--chart-2)]" style={{ flexGrow: sent }} title={`Sent ${formatBytes(sent)}`} />
      </div>
    </li>
  )
}
