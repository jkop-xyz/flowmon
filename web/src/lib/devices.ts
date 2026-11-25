import { useQuery } from "@tanstack/react-query"

import { fetchDeviceLabels } from "@/lib/api"

export const deviceLabelsKey = ["device-labels"]

/** The names given to Devices, by MAC; empty until loaded. */
export function useDeviceLabels(): Record<string, string> {
  const query = useQuery({
    queryKey: deviceLabelsKey,
    queryFn: ({ signal }) => fetchDeviceLabels(signal),
    staleTime: 60_000,
  })
  return query.data?.labels ?? {}
}
