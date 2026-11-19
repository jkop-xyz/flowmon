import { useQuery } from "@tanstack/react-query"
import { Database } from "lucide-react"

import { QueryError, SectionSkeleton, TimeAgo } from "@/components/page"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { appStoreNames, fetchStatus } from "@/lib/api"

/** Which database the App Store is in, and whether it answers. */
export function AppStoreCard() {
  const query = useQuery({ queryKey: ["status"], queryFn: ({ signal }) => fetchStatus(signal), refetchInterval: 15_000 })
  const s = query.data?.appStore
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Database className="size-4" />
          App Store
        </CardTitle>
        <CardDescription>
          Accounts, sessions, Host Labels and Service Names. Flows and the settings above are in the Flow Store, below.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        {query.isPending ? (
          <SectionSkeleton rows={2} />
        ) : query.isError ? (
          <QueryError title="Couldn't load the App Store's status" error={query.error} />
        ) : !s?.configured || !s.driver ? (
          <p className="text-muted-foreground">Not set up yet.</p>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{appStoreNames[s.driver]}</span>
              {s.available ? (
                <Badge variant="secondary">Answering</Badge>
              ) : (
                <Badge variant="destructive">
                  Not answering{s.since && <> since <TimeAgo iso={s.since} now={query.dataUpdatedAt} /></>}
                </Badge>
              )}
            </div>
            {s.location && <code className="font-mono text-ident break-all text-muted-foreground">{s.location}</code>}
            <p className="text-xs text-muted-foreground">
              {s.driver === "sqlite"
                ? "Kept in the data directory next to the Flow Store, and backed up with it."
                : "Back it up with your PostgreSQL backups: the Flow Store backup doesn't include it."}{" "}
              It was chosen at first-run setup or by <code className="font-mono">FLOWMON_DATABASE_URL</code>.
            </p>
          </>
        )}
      </CardContent>
    </Card>
  )
}
