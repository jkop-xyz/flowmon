const numberFormat = new Intl.NumberFormat()

export function formatNumber(n: number): string {
  return numberFormat.format(n)
}

const byteUnits = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"]

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

export function formatSeconds(s: number): string {
  const abs = Math.abs(s)
  if (abs < 1) return `${s.toFixed(2)} s`
  if (abs < 120) return `${s.toFixed(0)} s`
  return `${(s / 60).toFixed(1)} min`
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
