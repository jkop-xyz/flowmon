import { useQuery } from "@tanstack/react-query"
import { AlertTriangle } from "lucide-react"
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"
import { Skeleton } from "@/components/ui/skeleton"
import { fetchBandwidth } from "@/lib/api"
import { formatBitsPerSecond, formatTick } from "@/lib/format"
import { resolveRange, useTimeRange } from "@/lib/time-range"

const chartConfig = {
  inboundBps: { label: "Inbound", color: "var(--chart-1)" },
  outboundBps: { label: "Outbound", color: "var(--chart-2)" },
} satisfies ChartConfig

export function OverviewPage() {
  const { preset } = useTimeRange()
  const query = useQuery({
    queryKey: ["bandwidth", preset.value],
    queryFn: ({ signal }) => {
      const { from, to } = resolveRange(preset)
      return fetchBandwidth({ from, to }, signal)
    },
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
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertTitle>Couldn't load bandwidth</AlertTitle>
              <AlertDescription>{query.error.message}</AlertDescription>
            </Alert>
          ) : (
            <BandwidthChart points={query.data.points} stepSeconds={query.data.stepSeconds} rangeMs={preset.ms} />
          )}
        </CardContent>
      </Card>
    </div>
  )
}

type Point = { t: string; inboundBps: number; outboundBps: number }

function BandwidthChart({ points, stepSeconds, rangeMs }: { points: Point[]; stepSeconds: number; rangeMs: number }) {
  const data = points.map((p) => ({ ...p, time: new Date(p.t).getTime() }))
  const empty = points.every((p) => p.inboundBps === 0 && p.outboundBps === 0)

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
          <YAxis
            tickLine={false}
            axisLine={false}
            width={88}
            tickFormatter={(v: number) => formatBitsPerSecond(v)}
          />
          <ChartTooltip
            content={
              <ChartTooltipContent
                indicator="dot"
                labelFormatter={(_, payload) => {
                  const time = payload?.[0]?.payload?.time as number | undefined
                  return time === undefined ? "" : new Date(time).toLocaleString()
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
          <Area
            dataKey="inboundBps"
            type="monotone"
            stroke="var(--color-inboundBps)"
            fill="url(#fill-inboundBps)"
            strokeWidth={1.5}
            isAnimationActive={false}
          />
          <Area
            dataKey="outboundBps"
            type="monotone"
            stroke="var(--color-outboundBps)"
            fill="url(#fill-outboundBps)"
            strokeWidth={1.5}
            isAnimationActive={false}
          />
          <ChartLegend content={<ChartLegendContent />} />
        </AreaChart>
      </ChartContainer>
      {empty && (
        <p className="pointer-events-none absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">
          No Flows in this time range yet.
        </p>
      )}
      <p className="mt-2 text-xs text-muted-foreground">Each point averages {formatStep(stepSeconds)}.</p>
    </div>
  )
}

function formatStep(seconds: number) {
  if (seconds < 60) return `${seconds} seconds`
  if (seconds < 3600) return `${seconds / 60} minute${seconds === 60 ? "" : "s"}`
  return `${seconds / 3600} hour${seconds === 3600 ? "" : "s"}`
}
