const numberFormat = new Intl.NumberFormat()

export function formatNumber(n: number): string {
  return numberFormat.format(n)
}

export const byteUnits = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"]

/** Volumes are shown in bytes (rates use bits/s elsewhere). */
export function formatBytes(n: number): string {
  let value = n
  let unit = 0
  while (value >= 1024 && unit < byteUnits.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(unit === 0 || value >= 100 ? 0 : 1)} ${byteUnits[unit]}`
}

const relative = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" })

export function formatAgo(iso: string, now: number): string {
  const seconds = Math.round((new Date(iso).getTime() - now) / 1000)
  const abs = Math.abs(seconds)
  if (abs < 60) return relative.format(seconds, "second")
  if (abs < 3600) return relative.format(Math.round(seconds / 60), "minute")
  if (abs < 86400) return relative.format(Math.round(seconds / 3600), "hour")
  return relative.format(Math.round(seconds / 86400), "day")
}

/** Full date and time in the browser's time zone, for tooltips. */
export function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString()
}

const bitUnits = ["bit/s", "kbit/s", "Mbit/s", "Gbit/s", "Tbit/s"]

/** Rates are shown in bits per second (decimal units, like link speeds). */
export function formatBitsPerSecond(bps: number): string {
  let value = bps
  let unit = 0
  while (value >= 1000 && unit < bitUnits.length - 1) {
    value /= 1000
    unit++
  }
  const digits = value === 0 || value >= 100 || unit === 0 ? 0 : value >= 10 ? 1 : 2
  return `${value.toFixed(digits)} ${bitUnits[unit]}`
}

/** Axis ticks: times for short ranges, dates and times for longer ones. */
export function formatTick(ms: number, rangeMs: number): string {
  const d = new Date(ms)
  if (rangeMs <= 24 * 3600_000) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
  return d.toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" })
}

/** Durations in one short style everywhere: "0.5 s", "45 s", "2.3 min", "5 min", "1 h". */
export function formatSeconds(s: number): string {
  const abs = Math.abs(s)
  const trim = (v: number, digits: number) => String(Number(v.toFixed(digits)))
  if (abs < 10) return `${trim(s, 1)} s`
  if (abs < 120) return `${Math.round(s)} s`
  if (abs < 7200) return `${trim(s / 60, 1)} min`
  return `${trim(s / 3600, 1)} h`
}

/** An hour bucket, e.g. "Wed 14:00". */
export function formatHour(iso: string): string {
  return new Date(iso).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" })
}

const tickSteps = [60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400].map((s) => s * 1000)

/** Evenly spaced axis ticks on round times (every 2 min, 1 h, 6 h …), about `count` of them. */
export function timeTicks(start: number, end: number, count = 7): number[] {
  const step = tickSteps.find((s) => (end - start) / s <= count) ?? tickSteps[tickSteps.length - 1]
  // Align to local wall-clock time, so ticks land on :00, :05 and so on.
  const offset = new Date(start).getTimezoneOffset() * 60_000
  const first = Math.ceil((start - offset) / step) * step + offset
  const out: number[] = []
  for (let t = first; t <= end; t += step) out.push(t)
  return out
}

const protocolNames: Record<number, string> = {
  1: "ICMP",
  2: "IGMP",
  6: "TCP",
  17: "UDP",
  41: "IPv6-in-IPv4",
  47: "GRE",
  50: "ESP",
  58: "ICMPv6",
  89: "OSPF",
  132: "SCTP",
}

export function protocolName(n: number): string {
  return protocolNames[n] ?? `IP protocol ${n}`
}

const percent = new Intl.NumberFormat(undefined, { style: "percent", maximumFractionDigits: 0 })

/** How much of a total is estimated: "about 40%", "under 1%" rather than "0%", "nearly all" or "all". */
export function estimateShare(estimated: number, total: number): string {
  const share = total > 0 ? estimated / total : 0
  if (share >= 0.9999) return "all"
  if (share >= 0.995) return "nearly all"
  if (share < 0.01) return "under 1%"
  return `about ${percent.format(share)}`
}
