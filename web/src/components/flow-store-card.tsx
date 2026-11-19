import { useQuery } from "@tanstack/react-query"
import { AlertTriangle, Download, HardDrive } from "lucide-react"

import { QueryError, Section, SectionHeading, SectionSkeleton, TimeAgo } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { fetchFlowStore, fetchSettings, flowStoreBackupURL, type FlowStoreInfo } from "@/lib/api"
import { formatBytes } from "@/lib/format"

/** How big the Flow Store is, what the size cap did, and backups. */
export function FlowStoreCard() {
  const query = useQuery({ queryKey: ["flow-store"], queryFn: ({ signal }) => fetchFlowStore(signal), refetchInterval: 60_000 })
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <HardDrive className="size-4" />
          Flow Store
        </CardTitle>
        <CardDescription>Raw Flows, Rollups, Exporters, Interfaces and the settings above, in one DuckDB file.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {query.isPending ? (
          <SectionSkeleton rows={3} />
        ) : query.isError ? (
          <QueryError title="Couldn't load the Flow Store's status" error={query.error} />
        ) : (
          <FlowStoreDetails info={query.data} now={query.dataUpdatedAt} />
        )}
      </CardContent>
    </Card>
  )
}

const dayFormat = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", timeZone: "UTC" })

/** "flows_20261115" → "raw Flows of Nov 15". */
function tableLabel(name: string): string {
  const m = /^(flows|rollup_minute)_(\d{4})(\d{2})(\d{2})$/.exec(name)
  if (!m) return name
  const day = dayFormat.format(Date.UTC(Number(m[2]), Number(m[3]) - 1, Number(m[4])))
  return `${m[1] === "flows" ? "raw Flows" : "minute Rollups"} of ${day}`
}

const day = 86_400_000

function FlowStoreDetails({ info, now }: { info: FlowStoreInfo; now: number }) {
  const settings = useQuery({ queryKey: ["settings"], queryFn: ({ signal }) => fetchSettings(signal) })
  const rawRetentionDays = settings.data ? Math.round(settings.data.retention.raw.seconds / 86400) : undefined
  const cap = info.cap
  // The cap drops a day at a time, so while it limits history it drops about daily.
  const capLimiting = cap?.lastDrop !== undefined && now - new Date(cap.lastDrop).getTime() < 1.1 * day
  const share = info.maxBytes > 0 ? Math.min(1, info.usedBytes / info.maxBytes) : 0

  return (
    <>
      {cap?.over && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Over the size cap, with nothing left that may be dropped</AlertTitle>
          <AlertDescription>
            The size cap only drops raw Flows and minute Rollups older than a day. What's left (today's raw Flows, the last
            day of minute Rollups and the hour Rollups) takes more than{" "}
            <code className="font-mono">FLOWMON_FLOW_STORE_MAX_GB</code> allows, so the Flow Store keeps growing. Raise the
            cap, or shorten the minute and hour Rollup retention.
          </AlertDescription>
        </Alert>
      )}
      {!cap?.over && capLimiting && (
        <Alert variant="warning">
          <AlertTriangle />
          <AlertTitle>The size cap is dropping raw Flows before their retention ends</AlertTitle>
          <AlertDescription>
            <p>
              Raw Flows now reach back {info.rawDays} {info.rawDays === 1 ? "day" : "days"}
              {rawRetentionDays !== undefined && <> instead of {rawRetentionDays}</>}. Charts and top lists still cover the
              whole Rollup retention; the Flow Explorer and exact Top Remote IPs don't. Raise{" "}
              <code className="font-mono">FLOWMON_FLOW_STORE_MAX_GB</code> or shorten raw retention to stop this warning.
            </p>
            {cap.lastDrop && (
              <p className="mt-1">
                Last dropped <TimeAgo iso={cap.lastDrop} now={now} />: {cap.dropped.map(tableLabel).join(", ")}.
              </p>
            )}
          </AlertDescription>
        </Alert>
      )}

      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 text-sm sm:grid-cols-4">
        <Stat label="Data">
          {formatBytes(info.usedBytes)}
          {info.maxBytes > 0 && <span className="text-muted-foreground"> of {formatBytes(info.maxBytes)}</span>}
          {info.maxBytes > 0 && (
            <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted" aria-hidden>
              <div
                className={`h-full rounded-full ${share >= 1 ? "bg-destructive" : share > 0.9 ? "bg-warning" : "bg-primary"}`}
                style={{ width: `${share * 100}%` }}
              />
            </div>
          )}
        </Stat>
        <Stat
          label="Files on disk"
          title="Space freed by dropped data is reused before the file grows, so the file can be larger than the data."
        >
          {formatBytes(info.fileBytes)}
        </Stat>
        <Stat label="Raw Flows">
          {info.oldestRawDay ? (
            <>
              {info.rawDays} {info.rawDays === 1 ? "day" : "days"}
              <span className="text-muted-foreground"> since {dayFormat.format(new Date(info.oldestRawDay))}</span>
            </>
          ) : (
            "none yet"
          )}
        </Stat>
        <Stat
          label="Memory"
          title="DuckDB's memory and threads: FLOWMON_FLOW_STORE_MEMORY_LIMIT and FLOWMON_FLOW_STORE_THREADS."
        >
          {formatBytes(info.memoryBytes)}
          <span className="text-muted-foreground">
            {" "}
            of {info.memoryLimit}, {info.threads} {info.threads === 1 ? "thread" : "threads"}
          </span>
        </Stat>
      </dl>
      <p className="text-xs text-muted-foreground">
        {info.maxBytes > 0 ? (
          <>
            Capped by <code className="font-mono">FLOWMON_FLOW_STORE_MAX_GB</code>: past the cap, the oldest raw days are
            dropped first, then minute Rollups older than a day. Hour Rollups are never dropped.
          </>
        ) : (
          <>
            No size cap. Set <code className="font-mono">FLOWMON_FLOW_STORE_MAX_GB</code> to keep the Flow Store under a
            size by dropping the oldest raw days early.
          </>
        )}{" "}
        Memory and threads are set by <code className="font-mono">FLOWMON_FLOW_STORE_MEMORY_LIMIT</code> and{" "}
        <code className="font-mono">FLOWMON_FLOW_STORE_THREADS</code>.
      </p>

      <Section>
        <SectionHeading
          title="Backup"
          aside={
            <Button asChild size="sm" variant="outline">
              <a href={flowStoreBackupURL} download>
                <Download />
                Download a backup
              </a>
            </Button>
          }
        />
        <p className="text-sm text-muted-foreground">
          A consistent copy of the Flow Store, taken while collection keeps running. It needs about{" "}
          {formatBytes(info.usedBytes)} free next to the Flow Store while it's taken, and can take a while to start
          downloading. To restore, stop Flowmon, replace <code className="font-mono text-xs break-all">{info.path}</code> with the
          downloaded file, delete <code className="font-mono text-xs break-all">{info.path}.wal</code> if it exists, and start
          Flowmon. Accounts, Host Labels and Service Names live in the App Store and are backed up separately.
        </p>
        <p className="text-sm text-muted-foreground">
          {info.backupToken ? (
            <>
              Scripts can download it with the token from <code className="font-mono text-xs">FLOWMON_BACKUP_TOKEN</code>:{" "}
              <code className="font-mono text-xs break-all">
                curl -fH "Authorization: Bearer $TOKEN" -o flows.duckdb {window.location.origin}
                {flowStoreBackupURL}
              </code>
            </>
          ) : (
            <>
              To back up from a script, set <code className="font-mono text-xs">FLOWMON_BACKUP_TOKEN</code> and send it as a
              bearer token.
            </>
          )}
        </p>
      </Section>
    </>
  )
}

function Stat({ label, title, children }: { label: string; title?: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col" title={title}>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="tabular-nums">{children}</dd>
    </div>
  )
}
