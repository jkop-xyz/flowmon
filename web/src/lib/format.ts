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
