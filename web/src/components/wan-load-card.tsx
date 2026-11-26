import { ChartArea } from "lucide-react"
import { useState } from "react"
import { Link } from "react-router"

import { AreaChip } from "@/components/area-chip"
import { TrafficChart } from "@/components/traffic-chart"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { UsageChart } from "@/components/usage-chart"
import { type WANLoad } from "@/lib/api"
import { formatBitsPerSecond, formatBytes } from "@/lib/format"
import { useTimeRange } from "@/lib/time-range"
import { bucketLabel, usageBuckets, usageUnit, type UsageBucket, type UsageUnit } from "@/lib/wan-usage"

type View = "speed" | "data"

const views: { value: View; label: string }[] = [
  { value: "speed", label: "Speed" },
  { value: "data", label: "Data used" },
]

const storageKey = "flowmon-wan-view"

function readStoredView(): View {
  try {
    const stored = localStorage.getItem(storageKey)
    if (stored === "speed" || stored === "data") return stored
  } catch {
    // Storage can be unavailable (private mode, blocked cookies); fall back to Speed.
  }
  return "speed"
}

/** The line's speed as dashed levels: one when both directions match, else each label on the far side of its own line. */
function lineLimits(down: number, up: number) {
  if (down === up) return up > 0 ? [{ value: down, label: `Line ${formatBitsPerSecond(down)}` }] : []
  const levels = [
    { value: down, label: `Line down ${formatBitsPerSecond(down)}` },
    { value: up, label: `Line up ${formatBitsPerSecond(up)}` },
  ].filter((l) => l.value > 0)
  const lowest = Math.min(...levels.map((l) => l.value))
  return levels.map((l) => (levels.length > 1 && l.value === lowest ? { ...l, side: "below" as const } : l))
}

type Direction = "in" | "out"

/** Download or upload over the range: the bytes moved, and either the average rate and the highest step or the busiest bucket. */
function UsageTile({
  direction,
  wan,
  view,
  buckets,
  unit,
  rangeMs,
}: {
  direction: Direction
  wan: WANLoad
  view: View
  buckets: UsageBucket[]
  unit: UsageUnit
  rangeMs: number
}) {
  const { total, line } = wan
  const down = direction === "in"
  const bytes = down ? total.inBytes : total.outBytes
  const bytesOf = (b: { inBytes: number; outBytes: number }) => (down ? b.inBytes : b.outBytes)
  const lineBps = down ? line.downBps : line.upBps
  const peak = Math.max(0, ...wan.points.map((p) => (down ? p.inBps : p.outBps)))
  const busiest = buckets.reduce<UsageBucket | undefined>((a, b) => (a && bytesOf(a) >= bytesOf(b) ? a : b), undefined)
  const meta =
    view === "speed" ? (
      total.seconds > 0 ? (
        <>
          Average <b className="font-medium text-foreground">{formatBitsPerSecond((bytes * 8) / total.seconds)}</b> ·
          peak {wan.stepSeconds === 3600 ? "hour" : "minute"}{" "}
          <b className="font-medium text-foreground">{formatBitsPerSecond(peak)}</b>
          {line.fromSetting && lineBps > 0 && ` · ${Math.round((100 * peak) / lineBps)} % of line`}
        </>
      ) : (
        "No data"
      )
    ) : busiest && bytesOf(busiest) > 0 ? (
      <>
        Busiest {unit.name} <b className="font-medium text-foreground">{bucketLabel(busiest, unit, rangeMs)}</b>,{" "}
        <b className="font-medium text-foreground">{formatBytes(bytesOf(busiest))}</b>
      </>
    ) : (
      "No data"
    )
  return (
    <div className="rounded-md border px-3 py-2.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="size-2 rounded-full" style={{ background: down ? "var(--chart-1)" : "var(--chart-2)" }} />
          {down ? "Downloaded" : "Uploaded"}
        </span>
        <span className="text-xl font-semibold tabular-nums">{formatBytes(bytes)}</span>
      </div>
      <p className="mt-0.5 text-xs text-muted-foreground tabular-nums">{meta}</p>
    </div>
  )
}

/** The WAN's load over the range, from the router's own counters: its speed against the line, or the data used per hour or day. */
export function WANLoadCard({ wan, fetched }: { wan: WANLoad; fetched: number }) {
  const { preset } = useTimeRange()
  const [view, setViewState] = useState<View>(readStoredView)
  const setView = (next: View) => {
    try {
      localStorage.setItem(storageKey, next)
    } catch {
      // Not remembered; the choice still applies until the page reloads.
    }
    setViewState(next)
  }
  const unit = usageUnit(preset.ms)
  const buckets = usageBuckets(wan.points, fetched - preset.ms, fetched, unit)
  const names = wan.interfaces.map((i) => `${i.name} on ${i.sourceName}`).join(", ")
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area="overview" icon={ChartArea} />
          WAN load
        </CardTitle>
        <CardDescription>
          {names}, from its counters each minute, {preset.label.toLowerCase()}
          {view === "data" && `, by ${unit.name}`}.{" "}
          {view === "data" ? null : wan.line.fromSetting ? (
            "Dashed: your line's speed."
          ) : (
            <>
              <Link to="/settings/internet-line" className="underline underline-offset-4">
                Set your line's speed
              </Link>{" "}
              to see how full it is.
            </>
          )}
        </CardDescription>
        <CardAction>
          <div className="flex rounded-md border p-0.5" role="group" aria-label="Show">
            {views.map((v) => (
              <Button
                key={v.value}
                size="sm"
                variant={v.value === view ? "secondary" : "ghost"}
                className="h-7"
                aria-pressed={v.value === view}
                onClick={() => setView(v.value)}
              >
                {v.label}
              </Button>
            ))}
          </div>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="grid gap-2.5 sm:grid-cols-2">
          {(["in", "out"] as const).map((direction) => (
            <UsageTile
              key={direction}
              direction={direction}
              wan={wan}
              view={view}
              buckets={buckets}
              unit={unit}
              rangeMs={preset.ms}
            />
          ))}
        </div>
        <div className="flex min-w-0 flex-col gap-2">
          {view === "speed" ? (
            <TrafficChart
              points={wan.points.map((p) => ({ t: p.t, down: p.inBps, up: p.outBps }))}
              series={[
                { key: "down", label: "Download", color: "var(--chart-1)" },
                { key: "up", label: "Upload", color: "var(--chart-2)" },
              ]}
              stepSeconds={wan.stepSeconds}
              rangeMs={preset.ms}
              incompleteFrom={fetched - 60_000}
              noun="counters"
              limits={wan.line.fromSetting ? lineLimits(wan.line.downBps, wan.line.upBps) : []}
            />
          ) : (
            <>
              <UsageChart buckets={buckets} unit={unit} rangeMs={preset.ms} />
              <p className="text-xs text-muted-foreground">
                Each bar is the bytes the router counted in that {unit.name}.
              </p>
            </>
          )}
          <p className="text-xs text-muted-foreground">
            The router's exact byte counts, so this includes traffic Flows miss, such as hardware-offloaded connections.
          </p>
        </div>
      </CardContent>
    </Card>
  )
}
