import { useQuery } from "@tanstack/react-query"
import { DatabaseZap, Unplug } from "lucide-react"
import { Link } from "react-router"

import { TimeAgo } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { fetchStatus, type AppStoreStatus, type CollectionStatus } from "@/lib/api"
import { formatSeconds } from "@/lib/format"

/** Warnings above every page: the App Store is down, or Flows stopped arriving. */
export function StatusBanners() {
  const query = useQuery({
    queryKey: ["status"],
    queryFn: ({ signal }) => fetchStatus(signal),
    refetchInterval: 15_000,
  })
  const { appStore, collection } = query.data ?? {}
  const now = query.dataUpdatedAt
  return (
    <>
      {collection?.quiet && <QuietBanner c={collection} now={now} />}
      {appStore?.configured && !appStore.available && <AppStoreBanner store={appStore} now={now} />}
    </>
  )
}

function QuietBanner({ c, now }: { c: CollectionStatus; now: number }) {
  return (
    <Alert variant="warning" className="mx-auto mb-6 max-w-6xl">
      <Unplug />
      <AlertTitle>No Flows are arriving</AlertTitle>
      <AlertDescription>
        <p>
          {c.lastFlow ? (
            <>
              The last Flows arrived <TimeAgo iso={c.lastFlow} now={now} />
              {c.lastFlowFrom && <> from {c.lastFlowFrom}</>}.
            </>
          ) : (
            <>No Flows have arrived since Flowmon started.</>
          )}{" "}
          Check that the router still exports to this host and that no firewall blocks the port. The{" "}
          <Link to="/exporters" className="underline underline-offset-4">
            Exporters
          </Link>{" "}
          page shows what arrives. This warning appears after {formatSeconds(c.warnAfterSeconds)} without Flows
          (FLOWMON_NO_FLOWS_WARNING).
        </p>
      </AlertDescription>
    </Alert>
  )
}

function AppStoreBanner({ store, now }: { store: AppStoreStatus; now: number }) {
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
              <TimeAgo iso={store.since} now={now} />
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
