import { useQuery } from "@tanstack/react-query"
import { Cable, Info } from "lucide-react"

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { fetchExporterOptions } from "@/lib/api"
import { useExporterFilter } from "@/lib/exporter-filter"

const ALL = "__all__"

export function ExporterPicker() {
  const { exporter, setExporter } = useExporterFilter()
  const options = useQuery({
    queryKey: ["exporter-options"],
    queryFn: ({ signal }) => fetchExporterOptions(signal),
    refetchInterval: 60_000,
  })

  return (
    <div className="flex items-center gap-1">
      <Select value={exporter || ALL} onValueChange={(v) => setExporter(v === ALL ? "" : v)}>
        <SelectTrigger size="sm" className="w-48" aria-label="Exporter">
          <Cable className="size-4 text-muted-foreground" />
          <SelectValue />
        </SelectTrigger>
        <SelectContent align="end">
          <SelectItem value={ALL}>All exporters</SelectItem>
          {(options.data ?? []).map((o) => (
            <SelectItem key={o.address} value={o.address}>
              {o.name ? `${o.name} (${o.address})` : o.address}
            </SelectItem>
          ))}
          {exporter && !(options.data ?? []).some((o) => o.address === exporter) && (
            <SelectItem value={exporter}>{exporter}</SelectItem>
          )}
        </SelectContent>
      </Select>
      <Tooltip>
        <TooltipTrigger asChild>
          <button type="button" className="text-muted-foreground" aria-label="About the Exporter filter">
            <Info className="size-4" />
          </button>
        </TooltipTrigger>
        <TooltipContent className="max-w-64">
          "All exporters" adds up the traffic of every approved Exporter. Traffic seen by two Exporters counts twice
          until Internal Exporters arrive.
        </TooltipContent>
      </Tooltip>
    </div>
  )
}
