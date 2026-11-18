import { useQuery } from "@tanstack/react-query"
import { Cable, Info } from "lucide-react"

import { Button } from "@/components/ui/button"
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
    <div className="flex min-w-0 flex-1 items-center gap-1 sm:flex-none">
      <Select value={exporter || ALL} onValueChange={(v) => setExporter(v === ALL ? "" : v)}>
        <SelectTrigger size="sm" className="min-w-0 flex-1 *:data-[slot=select-value]:flex-1 *:data-[slot=select-value]:text-left sm:w-52 sm:flex-none" aria-label="Exporter">
          <Cable className="size-4 text-muted-foreground" />
          <SelectValue />
        </SelectTrigger>
        <SelectContent align="end">
          <SelectItem value={ALL}>All Exporters</SelectItem>
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
          <Button variant="ghost" size="icon-xs" className="text-muted-foreground" aria-label="About the Exporter filter">
            <Info />
          </Button>
        </TooltipTrigger>
        <TooltipContent className="max-w-64">
          "All Exporters" adds up the traffic of every approved Exporter. Traffic seen by two Exporters counts twice
          until Internal Exporters arrive.
        </TooltipContent>
      </Tooltip>
    </div>
  )
}
