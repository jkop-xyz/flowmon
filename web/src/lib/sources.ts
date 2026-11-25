import { useQuery } from "@tanstack/react-query"

import { fetchSources, type SourceKind } from "@/lib/api"

export const sourcesKey = ["sources"]

export function useSources() {
  return useQuery({ queryKey: sourcesKey, queryFn: ({ signal }) => fetchSources(signal), refetchInterval: 15_000 })
}

/** What a kind of source is called in lists: "SNMP devices" rather than "SNMP device". */
export function kindPlural(k: SourceKind) {
  return k.name === "snmp" ? "SNMP devices" : k.title
}
