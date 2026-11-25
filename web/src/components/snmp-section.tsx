import { useQuery } from "@tanstack/react-query"
import { AlertTriangle } from "lucide-react"
import { Link } from "react-router"

import { QueryError, Section, SectionHeading, SectionSkeleton, TimeAgo } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { fetchReconciliation, fetchSNMP, type ReconDirection, type SNMPView } from "@/lib/api"
import { formatBytes, formatHour } from "@/lib/format"

/** Which SNMP Data Source polls an approved Exporter, and Reconciliation on its counters. */
export function SNMPSection({ address }: { address: string }) {
  const query = useQuery({ queryKey: ["snmp", address], queryFn: ({ signal }) => fetchSNMP(address, signal) })
  const src = query.data?.source
  return (
    <Section>
      <SectionHeading title="SNMP" aside={src && <SourceState source={src} now={query.dataUpdatedAt} />} />
      {query.isPending ? (
        <SectionSkeleton rows={1} />
      ) : query.isError ? (
        <QueryError title="Couldn't load its SNMP source" error={query.error} />
      ) : src ? (
        <p className="text-sm text-muted-foreground">
          Polled every minute by the SNMP Data Source{" "}
          <Link to={`/snmp/${src.id}`} className="underline underline-offset-4">
            {src.name}
          </Link>
          , which names its interfaces and keeps their counters: open it for every interface's load and errors. Change it
          under Settings → Data Sources.
        </p>
      ) : (
        <p className="text-sm text-muted-foreground">
          Not polled. To name its interfaces and check Flow totals against its own counters, add it as an SNMP device
          under{" "}
          <Link to="/settings" className="underline underline-offset-4">
            Settings → Data Sources
          </Link>
          , at this address. On RouterOS, allow SNMP only from Flowmon's address.
        </p>
      )}
      {src?.enabled && <ReconciliationTable address={address} />}
    </Section>
  )
}

function SourceState({ source, now }: { source: NonNullable<SNMPView["source"]>; now: number }) {
  const h = source.health
  if (!source.enabled) return <>Off</>
  if (h.state === "failing") return <>Polling fails: {h.error}</>
  if (!h.lastSuccess) return <>Waiting for the first poll</>
  return (
    <>
      Polled <TimeAgo iso={h.lastSuccess} now={now} />
      {h.detail ? `, ${h.detail}` : ""}
    </>
  )
}

function Cell({ d, threshold }: { d: ReconDirection; threshold: number }) {
  if (d.gap === null) return <span className="text-muted-foreground">Too little traffic</span>
  const pct = d.gap * 100
  return (
    <span className={Math.abs(d.gap) > threshold ? "text-warning-foreground" : undefined}>
      {formatBytes(d.interfaceBytes)} vs {formatBytes(d.flowBytes)}{" "}
      <span className="tabular-nums">
        ({pct > 0 ? "−" : "+"}
        {Math.abs(pct).toFixed(1)}%)
      </span>
    </span>
  )
}

function ReconciliationTable({ address }: { address: string }) {
  const query = useQuery({
    queryKey: ["reconciliation", address],
    queryFn: ({ signal }) => fetchReconciliation(address, signal),
    refetchInterval: 5 * 60_000,
  })
  const heading = <SectionHeading title="Reconciliation" />
  if (query.isPending) {
    return (
      <Section>
        {heading}
        <SectionSkeleton rows={2} />
      </Section>
    )
  }
  if (query.isError) {
    return (
      <Section>
        {heading}
        <QueryError title="Couldn't load the Reconciliation" error={query.error} />
      </Section>
    )
  }
  const data = query.data
  const gapPct = Math.round(data.thresholds.gap * 100)
  return (
    <Section>
      {heading}
      {data.interfaces.length === 0 ? (
        <p className="text-sm text-muted-foreground">Mark a WAN Interface above to reconcile its counters.</p>
      ) : (
        <>
          <p className="text-xs text-muted-foreground">
            Bytes on each WAN Interface from SNMP (minus {data.overheadBytes} B per packet) against bytes in Flows, per
            complete hour. A warning needs a gap above {gapPct}% for {data.thresholds.sustainedHours} hours in a row.
          </p>
          {data.interfaces.map((i) => (
            <div key={i.name} className="flex flex-col gap-2">
              {i.warning && (
                <Alert variant="warning">
                  <AlertTriangle />
                  <AlertTitle>{i.name}: Flows and interface counters disagree</AlertTitle>
                  <AlertDescription>
                    Traffic is missing from the Flows or counted twice. Common causes: hardware offload (FastTrack HW,
                    L3HW) that Traffic Flow can't see, a long active timeout, or the wrong overhead preset.
                  </AlertDescription>
                </Alert>
              )}
              <div className="rounded-lg border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{i.name} · hour</TableHead>
                      <TableHead>In: interface vs Flows</TableHead>
                      <TableHead>Out: interface vs Flows</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {i.hours
                      .filter((h) => h.complete)
                      .slice(-6)
                      .reverse()
                      .map((h) => (
                        <TableRow key={h.hour}>
                          <TableCell className="tabular-nums">{formatHour(h.hour)}</TableCell>
                          <TableCell>
                            <Cell d={h.in} threshold={data.thresholds.gap} />
                          </TableCell>
                          <TableCell>
                            <Cell d={h.out} threshold={data.thresholds.gap} />
                          </TableCell>
                        </TableRow>
                      ))}
                    {i.hours.every((h) => !h.complete) && (
                      <TableRow>
                        <TableCell colSpan={3} className="text-muted-foreground">
                          The first complete hour appears after an hour of polling.
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              </div>
            </div>
          ))}
        </>
      )}
    </Section>
  )
}
