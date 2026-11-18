import { useQuery } from "@tanstack/react-query"
import { DatabaseZap } from "lucide-react"

import { TimeAgo } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { fetchStatus } from "@/lib/api"

/**
 * Says when the App Store isn't answering: collection carries on, but anything stored
 * there (logins, labels, names) waits. Shown above every page.
 */
export function AppStoreBanner() {
  const query = useQuery({
    queryKey: ["status"],
    queryFn: ({ signal }) => fetchStatus(signal),
    refetchInterval: 15_000,
  })
  const store = query.data?.appStore
  if (!store?.configured || store.available) return null
  const name = store.driver === "postgres" ? "Postgres" : "the App Store database"
  return (
    <Alert variant="warning" className="mx-auto mb-6 max-w-6xl">
      <DatabaseZap />
      <AlertTitle>The App Store isn't answering</AlertTitle>
      <AlertDescription>
        <p>
          Flowmon lost its connection to {name}
          {store.since && (
            <>
              {" "}
              <TimeAgo iso={store.since} now={query.dataUpdatedAt} />
            </>
          )}
          . Flows are still collected and every chart works. Host Labels, Service Names and account changes can't be saved
          until it's back, and new logins have to wait.
        </p>
        {store.error && <p className="mt-1 font-mono text-xs break-all">{store.error}</p>}
      </AlertDescription>
    </Alert>
  )
}
