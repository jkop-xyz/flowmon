import { useQuery } from "@tanstack/react-query"

import { lookupServiceNames, type ServiceName } from "@/lib/api"

/** The key a Service Port is looked up by. */
export const serviceKey = (protocol: number, port: number) => `${protocol}/${port}`

/** Service Names for the Service Ports on screen, keyed by serviceKey. */
export function useServiceNames(ports: { protocol: number; port: number }[]): Record<string, ServiceName> {
  const keys = [...new Set(ports.filter((p) => p.port > 0).map((p) => serviceKey(p.protocol, p.port)))].sort().slice(0, 200)
  const query = useQuery({
    queryKey: ["service-names", keys.join(",")],
    queryFn: ({ signal }) => lookupServiceNames(keys, signal),
    enabled: keys.length > 0,
    staleTime: 5 * 60_000,
    placeholderData: (previous) => previous,
  })
  return query.data?.names ?? {}
}
