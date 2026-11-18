import { useQuery } from "@tanstack/react-query"

import { fetchReverseDNS, type ReverseDNSName } from "@/lib/api"

/**
 * Reverse DNS names for the addresses on screen. The server answers from its cache and
 * resolves the rest in the background, so this asks again while any are pending.
 */
export function useReverseDNS(ips: string[]): Record<string, ReverseDNSName> {
  const unique = [...new Set(ips)].sort().slice(0, 200)
  const query = useQuery({
    queryKey: ["reverse-dns", unique.join(",")],
    queryFn: ({ signal }) => fetchReverseDNS(unique, signal),
    enabled: unique.length > 0,
    staleTime: 5 * 60_000,
    placeholderData: (previous) => previous,
    refetchInterval: (q) =>
      Object.values(q.state.data?.names ?? {}).some((n) => n.status === "pending") ? 1500 : false,
  })
  return query.data?.names ?? {}
}
