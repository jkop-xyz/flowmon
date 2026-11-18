import { Clock } from "lucide-react"

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { timeRangePresets, useTimeRange } from "@/lib/time-range"

export function TimeRangePicker() {
  const { preset, setRange } = useTimeRange()

  return (
    <Select value={preset.value} onValueChange={setRange}>
      <SelectTrigger size="sm" className="w-44" aria-label="Time range">
        <Clock className="size-4 text-muted-foreground" />
        <SelectValue />
      </SelectTrigger>
      <SelectContent align="end">
        {timeRangePresets.map((p) => (
          <SelectItem key={p.value} value={p.value}>
            {p.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
