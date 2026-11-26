import { Bar, BarChart, CartesianGrid, Cell, XAxis, YAxis } from "recharts"

import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import { formatBytes } from "@/lib/format"
import { bucketLabel, bucketTitle, type UsageBucket, type UsageUnit } from "@/lib/wan-usage"

const config = {
  inBytes: { label: "Downloaded", color: "var(--chart-1)" },
  outBytes: { label: "Uploaded", color: "var(--chart-2)" },
} satisfies ChartConfig

/** Bytes downloaded and uploaded per bucket as pairs of bars; a bucket the range cuts short is paler. */
export function UsageChart({ buckets, unit, rangeMs }: { buckets: UsageBucket[]; unit: UsageUnit; rangeMs: number }) {
  const empty = buckets.every((b) => b.inBytes === 0 && b.outBytes === 0)
  const last = buckets.length - 1
  return (
    <div className="relative w-full min-w-0">
      <ChartContainer
        config={config}
        className="aspect-auto h-72 w-full min-w-0 [&_.recharts-responsive-container]:min-w-0"
      >
        <BarChart data={buckets} margin={{ left: 8, right: 8, top: 8 }} barGap={2} barCategoryGap="20%" maxBarSize={18}>
          <CartesianGrid vertical={false} />
          <XAxis
            dataKey="start"
            tickLine={false}
            axisLine={false}
            minTickGap={24}
            tickFormatter={(v: number) =>
              bucketLabel(
                buckets.find((b) => b.start === v)!,
                unit,
                rangeMs,
              )
            }
          />
          <YAxis tickLine={false} axisLine={false} width={88} tickFormatter={(v: number) => formatBytes(v)} />
          <ChartTooltip
            cursor={{ fill: "var(--muted-foreground)", fillOpacity: 0.08 }}
            content={
              <ChartTooltipContent
                indicator="dot"
                labelFormatter={(_, payload) => {
                  const b = payload?.[0]?.payload as UsageBucket | undefined
                  if (!b) return ""
                  const note = !b.partial ? "" : b === buckets[last] ? " (so far)" : " (part of it)"
                  return bucketTitle(b, unit) + note
                }}
                formatter={(value, name) => (
                  <div className="flex w-full items-center justify-between gap-4">
                    <span className="text-muted-foreground">{config[name as keyof typeof config]?.label ?? name}</span>
                    <span className="font-mono tabular-nums">{formatBytes(Number(value))}</span>
                  </div>
                )}
              />
            }
          />
          {(["inBytes", "outBytes"] as const).map((key) => (
            <Bar key={key} dataKey={key} fill={`var(--color-${key})`} radius={[4, 4, 0, 0]} isAnimationActive={false}>
              {buckets.map((b) => (
                <Cell key={b.start} fillOpacity={b.partial ? 0.5 : 1} />
              ))}
            </Bar>
          ))}
          <ChartLegend content={<ChartLegendContent />} />
        </BarChart>
      </ChartContainer>
      {empty && (
        <p className="pointer-events-none absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">
          No counters in this time range yet.
        </p>
      )}
    </div>
  )
}
