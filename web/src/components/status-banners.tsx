import { useQuery } from "@tanstack/react-query"
import { DatabaseZap, HardDrive, Unplug } from "lucide-react"
import { Link } from "react-router"

import { pageWidth, TimeAgo } from "@/components/page"
import { cn } from "@/lib/utils"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { fetchStatus, type AppStoreStatus, type CollectionStatus, type FlowStoreCapStatus } from "@/lib/api"
import { formatBytes, formatSeconds } from "@/lib/format"

/** Warnings above every page: the App Store is down, Flows stopped arriving, or the Flow Store outgrew its cap. */
export function StatusBanners() {
  const query = useQuery({
    queryKey: ["status"],
    queryFn: ({ signal }) => fetchStatus(signal),
    refetchInterval: 15_000,
  })
  const { appStore, collection, flowStore } = query.data ?? {}
  const now = query.dataUpdatedAt
  return (
    <>
      {collection?.quiet && <QuietBanner c={collection} now={now} />}
      {appStore?.configured && !appStore.available && <AppStoreBanner store={appStore} now={now} />}
      {flowStore?.overCap && <OverCapBanner s={flowStore} />}
    </>
  )
}

function QuietBanner({ c, now }: { c: CollectionStatus; now: number }) {
  return (
    <Alert variant="warning" className={cn("mx-auto mb-6", pageWidth)}>
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
    <Alert variant="warning" className={cn("mx-auto mb-6", pageWidth)}>
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
        {store.error && <p className="mt-1 font-mono text-ident break-all">{store.error}</p>}
      </AlertDescription>
    </Alert>
  )
}

function OverCapBanner({ s }: { s: FlowStoreCapStatus }) {
  return (
    <Alert variant="warning" className={cn("mx-auto mb-6", pageWidth)}>
      <HardDrive />
      <AlertTitle>The Flow Store is over its size cap</AlertTitle>
      <AlertDescription>
        <p>
          It takes {formatBytes(s.usedBytes)} of the {formatBytes(s.maxBytes)} FLOWMON_FLOW_STORE_MAX_GB allows, and
          everything the cap may drop is gone, so it keeps growing. See the{" "}
          <Link to="/settings/databases" className="underline underline-offset-4">
            Settings → Databases
          </Link>{" "}
          page.
        </p>
      </AlertDescription>
    </Alert>
  )
}
