import { useQuery } from "@tanstack/react-query"

import { fetchGeoIP, type GeoIPSource, type GeoIPStatus } from "@/lib/api"

export function useGeoIP() {
  return useQuery({
    queryKey: ["geoip"],
    queryFn: ({ signal }) => fetchGeoIP(signal),
    staleTime: 5 * 60_000,
    // Follow a download while it runs.
    refetchInterval: (q) => (q.state.data?.downloading ? 2_000 : false),
  })
}

export const sourceLabels: Record<GeoIPSource, string> = {
  file: "Your file",
  maxmind: "MaxMind GeoLite2",
  dbip: "DB-IP Lite",
}

/** True when any database in use is DB-IP Lite, whose licence (CC BY 4.0) requires attribution. */
export function usesDBIP(status: GeoIPStatus | undefined) {
  return status?.databases.some((d) => d.source === "dbip") ?? false
}
