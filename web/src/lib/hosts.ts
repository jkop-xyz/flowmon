import { useQuery } from "@tanstack/react-query"

import { fetchHostLabels, fetchHostNames, type HostName } from "@/lib/api"

/**
 * Host Names for the Local Hosts on screen (addresses or Host Label prefixes). Labels
 * answer at once; reverse DNS resolves in the background, so this asks again while any
 * name is pending.
 */
export function useHostNames(hosts: string[]): Record<string, HostName> {
  const unique = [...new Set(hosts)].filter(Boolean).sort().slice(0, 200)
  const query = useQuery({
    queryKey: ["host-names", unique.join(",")],
    queryFn: ({ signal }) => fetchHostNames(unique, signal),
    enabled: unique.length > 0,
    staleTime: 60_000,
    placeholderData: (previous) => previous,
    refetchInterval: (q) =>
      Object.values(q.state.data?.names ?? {}).some((n) => n.status === "pending") ? 1500 : false,
  })
  return query.data?.names ?? {}
}

export function useHostLabels() {
  return useQuery({ queryKey: ["host-labels"], queryFn: ({ signal }) => fetchHostLabels(signal) })
}

/** The Local Host page of an address or Host Label prefix. */
export const hostPath = (host: string) => `/hosts/${host}`
