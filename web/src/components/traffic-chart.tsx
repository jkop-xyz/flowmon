import { Link } from "react-router"
import { Area, AreaChart, CartesianGrid, ReferenceArea, ReferenceLine, XAxis, YAxis } from "recharts"

import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import { classificationHistoryId } from "@/lib/anchors"
import type { ClassificationChange } from "@/lib/api"
import { formatBitsPerSecond, formatDateTime, formatSeconds, formatTick, timeTicks } from "@/lib/format"

/** One line on the chart: a field of each point, in bits per second. */
export type TrafficSeries<K extends string> = { key: K; label: string; color: string }

/**
 * Traffic over time as overlapping areas, one per series. The end of the range that may
 * still change is shaded, and classification changes are dashed lines.
 */
export function TrafficChart<K extends string>({
  points,
  series,
  stepSeconds,
  rangeMs,
  incompleteFrom,
  changes = [],
  format = formatBitsPerSecond,
  noun = "Flows",
}: {
  points: ({ t: string } & Record<K, number>)[]
  series: TrafficSeries<K>[]
  stepSeconds: number
  rangeMs: number
  incompleteFrom: number
  changes?: ClassificationChange[]
  /** How values read on the axis and in the tooltip; bits per second by default. */
  format?: (v: number) => string
  /** What the chart counts, for its notes: "Flows" by default. */
  noun?: string
}) {
  const config: ChartConfig = Object.fromEntries(series.map((s) => [s.key, { label: s.label, color: s.color }]))
  const data = points.map((p) => ({ ...p, time: new Date(p.t).getTime() }))
  const empty = points.every((p) => series.every((s) => p[s.key] === 0))
  const last = data.length > 0 ? data[data.length - 1].time : 0
  const end = last + stepSeconds * 1000
  const shadeFrom = Math.max(incompleteFrom - stepSeconds * 1000, data[0]?.time ?? 0)

  return (
    <div className="relative w-full min-w-0">
      <ChartContainer config={config} className="aspect-auto h-72 w-full min-w-0 [&_.recharts-responsive-container]:min-w-0">
        <AreaChart data={data} margin={{ left: 8, right: 8, top: 8 }}>
          <defs>
            {series.map((s) => (
              <linearGradient key={s.key} id={`fill-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor={`var(--color-${s.key})`} stopOpacity={0.5} />
                <stop offset="95%" stopColor={`var(--color-${s.key})`} stopOpacity={0.05} />
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
          <YAxis tickLine={false} axisLine={false} width={88} tickFormatter={(v: number) => format(v)} />
          {end > shadeFrom && (
            <ReferenceArea x1={shadeFrom} x2={end} fill="var(--muted-foreground)" fillOpacity={0.08} ifOverflow="hidden" />
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
                    <span className="text-muted-foreground">{config[String(name)]?.label ?? name}</span>
                    <span className="font-mono tabular-nums">{format(Number(value))}</span>
                  </div>
                )}
              />
            }
          />
          {series.map((s) => (
            <Area
              key={s.key}
              dataKey={s.key}
              type="monotone"
              stroke={`var(--color-${s.key})`}
              fill={`url(#fill-${s.key})`}
              strokeWidth={1.5}
              isAnimationActive={false}
            />
          ))}
          <ChartLegend content={<ChartLegendContent />} />
        </AreaChart>
      </ChartContainer>
      {empty && (
        <p className="pointer-events-none absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">
          No {noun} in this time range yet.
        </p>
      )}
      <p className="mt-2 text-xs text-muted-foreground">
        Each point averages {formatSeconds(stepSeconds)}. The shaded end is incomplete: {noun} for it may still arrive.
      </p>
      {changes.length > 0 && <ChangesNote changes={changes} />}
    </div>
  )
}

/** One line about the dashed lines, linking to where each change is listed and can be removed. */
function ChangesNote({ changes }: { changes: ClassificationChange[] }) {
  const exporters = [...new Set(changes.map((c) => c.exporter))]
  const to = exporters.length === 1 ? `/exporters#${classificationHistoryId(exporters[0])}` : "/exporters"
  const n = changes.length
  return (
    <p className="mt-1 text-xs text-muted-foreground">
      {n === 1 ? "The dashed line marks a classification change" : `The ${n} dashed lines mark classification changes`}
      {exporters.length === 1 ? ` of Exporter ${exporters[0]}` : ""}.{" "}
      <Link to={to} className="underline underline-offset-2 hover:text-foreground">
        See the classification history
      </Link>
    </p>
  )
}
