import { useQuery } from "@tanstack/react-query"

import { fetchDNS, fetchNetworkOverview, fetchUsageNow } from "@/lib/api"
import { resolveRange, useTimeRange, type TimeRangePreset } from "@/lib/time-range"

/** The Overview's SNMP side: WAN load, each device's health and interface problems. */
export function useNetworkOverview() {
  const { preset } = useTimeRange()
  return useQuery({
    queryKey: ["overview-network", preset.value],
    queryFn: ({ signal }) => fetchNetworkOverview(resolveRange(preset), signal),
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  })
}

/** Who is using the internet now. */
export function useUsageNow() {
  return useQuery({ queryKey: ["usage-now"], queryFn: ({ signal }) => fetchUsageNow(signal), refetchInterval: 30_000 })
}

/** How full a line is, 0 to 1 and more; undefined without a line speed. */
export function lineShare(bps: number, line: number) {
  return line > 0 ? bps / line : undefined
}

/** The network's DNS lookups in the range; the DNS page and the Overview share it. */
export const dnsQuery = (preset: TimeRangePreset) => ({
  queryKey: ["dns", preset.value, undefined, undefined],
  queryFn: ({ signal }: { signal: AbortSignal }) => fetchDNS(resolveRange(preset), {}, signal),
  refetchInterval: 30_000,
  placeholderData: (previous: Awaited<ReturnType<typeof fetchDNS>> | undefined) => previous,
})
