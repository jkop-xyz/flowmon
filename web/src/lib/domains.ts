import { useQuery } from "@tanstack/react-query"

import { fetchDomains } from "@/lib/api"
import { useExporterFilter } from "@/lib/exporter-filter"
import { resolveRange, useTimeRange, type TimeRangePreset } from "@/lib/time-range"

export const domainsQuery = (preset: TimeRangePreset, exporter: string, host?: string) => ({
  queryKey: ["domains", preset.value, exporter, host],
  queryFn: ({ signal }: { signal: AbortSignal }) => fetchDomains({ ...resolveRange(preset), exporter }, { host }, signal),
  refetchInterval: 60_000,
  placeholderData: (previous: Awaited<ReturnType<typeof fetchDomains>> | undefined) => previous,
})

/** Whether domains are worth a card: a DNS source exists, or traffic was named before. */
export function useDomainsShown() {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const query = useQuery(domainsQuery(preset, exporter))
  return query.data ? query.data.dnsSources > 0 || query.data.result.items.length > 0 : false
}

