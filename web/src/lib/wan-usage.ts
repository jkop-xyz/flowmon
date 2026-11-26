import type { WANLoad } from "@/lib/api"

export type UsageUnit = { name: "minute" | "5 minutes" | "30 minutes" | "hour" | "day" | "week"; ms?: number }

const minute = 60_000
const hour = 60 * minute
const day = 24 * hour

const units = {
  minute: { name: "minute", ms: minute },
  fiveMinutes: { name: "5 minutes", ms: 5 * minute },
  thirtyMinutes: { name: "30 minutes", ms: 30 * minute },
  hour: { name: "hour", ms: hour },
  day: { name: "day" },
  week: { name: "week" },
} satisfies Record<string, UsageUnit>

/** The longest range each bar size serves, matching the range presets (15m, 1h, 6h, 24h, 7d, 30d, 90d). */
const unitByRange: [maxRangeMs: number, unit: UsageUnit][] = [
  [15 * minute, units.minute],
  [hour, units.fiveMinutes],
  [6 * hour, units.thirtyMinutes],
  [day, units.hour],
  [7 * day, units.day],
  [30 * day, units.day],
  [Infinity, units.week],
]

/** The bar size for a range, from one table keyed by the range preset's length. */
export function usageUnit(rangeMs: number): UsageUnit {
  return unitByRange.find(([max]) => rangeMs <= max)![1]
}

export type UsageBucket = { start: number; end: number; inBytes: number; outBytes: number; partial: boolean }

/** The start of the bucket holding `t`, on the local clock: days at midnight, weeks on Monday. */
function bucketStart(t: number, unit: UsageUnit): number {
  const d = new Date(t)
  if (unit.ms !== undefined) {
    d.setSeconds(0, 0)
    if (unit.ms < hour) d.setMinutes(Math.floor(d.getMinutes() / (unit.ms / minute)) * (unit.ms / minute))
    else d.setMinutes(0)
    return d.getTime()
  }
  d.setHours(0, 0, 0, 0)
  if (unit.name === "week") d.setDate(d.getDate() - ((d.getDay() + 6) % 7))
  return d.getTime()
}

/** The start of the bucket after the one starting at `start`; days and weeks follow the calendar, so they survive clock changes. */
function nextStart(start: number, unit: UsageUnit): number {
  if (unit.ms !== undefined) return start + unit.ms
  const d = new Date(start)
  d.setDate(d.getDate() + (unit.name === "week" ? 7 : 1))
  return d.getTime()
}

/** Sums points into buckets of `unit` in local time, one per unit from the one holding `from` to the one holding `to`, empty ones included with zero bytes. partial: the bucket reaches past `from` or `to`. */
export function usageBuckets(points: WANLoad["points"], from: number, to: number, unit: UsageUnit): UsageBucket[] {
  const buckets: UsageBucket[] = []
  const byStart = new Map<number, UsageBucket>()
  for (let start = bucketStart(from, unit); start <= to;) {
    const end = nextStart(start, unit)
    const b = { start, end, inBytes: 0, outBytes: 0, partial: start < from || end > to }
    buckets.push(b)
    byStart.set(start, b)
    start = end
  }
  for (const p of points) {
    const b = byStart.get(bucketStart(new Date(p.t).getTime(), unit))
    if (!b) continue
    b.inBytes += p.inBytes
    b.outBytes += p.outBytes
  }
  return buckets
}

const clock = (t: number) => new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
const date = (t: number) => new Date(t).toLocaleDateString([], { day: "numeric", month: "short" })
const weekday = (t: number) => new Date(t).toLocaleDateString([], { weekday: "short" })

/** A short name for a bucket: "20:00", "Sat", "Sat 22 Nov" or "Week of 17 Nov", by bar size and range. */
export function bucketLabel(b: UsageBucket, unit: UsageUnit, rangeMs: number): string {
  if (unit.ms !== undefined) return clock(b.start)
  if (unit.name === "week") return `Week of ${date(b.start)}`
  return rangeMs <= 7 * day ? weekday(b.start) : `${weekday(b.start)} ${date(b.start)}`
}

/** A bucket's name for a tooltip: bars under a day give the date and the whole span; days and weeks always give the date. */
export function bucketTitle(b: UsageBucket, unit: UsageUnit): string {
  if (unit.ms !== undefined) return `${weekday(b.start)} ${date(b.start)}, ${clock(b.start)}–${clock(b.end)}`
  return unit.name === "week" ? `Week of ${date(b.start)}` : `${weekday(b.start)} ${date(b.start)}`
}
