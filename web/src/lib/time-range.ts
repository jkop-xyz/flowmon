import { useSearchParams } from "react-router"

export type TimeRangePreset = {
  value: string
  label: string
  ms: number
}

const minute = 60_000
const hour = 60 * minute

export const timeRangePresets: TimeRangePreset[] = [
  { value: "15m", label: "Last 15 minutes", ms: 15 * minute },
  { value: "1h", label: "Last hour", ms: hour },
  { value: "6h", label: "Last 6 hours", ms: 6 * hour },
  { value: "24h", label: "Last 24 hours", ms: 24 * hour },
  { value: "7d", label: "Last 7 days", ms: 7 * 24 * hour },
  { value: "30d", label: "Last 30 days", ms: 30 * 24 * hour },
  { value: "90d", label: "Last 90 days", ms: 90 * 24 * hour },
]

export const defaultTimeRange = "1h"

/**
 * The global time range, kept in the URL (?range=1h) so it survives reloads, is shared
 * by every page and can be linked to. Pages resolve it to absolute times when they
 * fetch, so polling moves the window forward.
 */
export function useTimeRange() {
  const [params, setParams] = useSearchParams()
  const value = params.get("range") ?? defaultTimeRange
  const preset = timeRangePresets.find((p) => p.value === value) ?? timeRangePresets[1]

  const setRange = (next: string) => {
    setParams(
      (prev) => {
        const updated = new URLSearchParams(prev)
        if (next === defaultTimeRange) updated.delete("range")
        else updated.set("range", next)
        return updated
      },
      { replace: true },
    )
  }

  return { preset, setRange }
}

export function resolveRange(preset: TimeRangePreset, now: number = Date.now()) {
  return { from: new Date(now - preset.ms), to: new Date(now) }
}
