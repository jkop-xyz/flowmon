import { useQuery } from "@tanstack/react-query"
import { useState } from "react"
import { Link, useLocation, useParams } from "react-router"

import { Page, QueryError, SectionSkeleton, TimeAgo } from "@/components/page"
import { TrafficChart, type TrafficSeries } from "@/components/traffic-chart"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { fetchInterfaceSeries, fetchSNMPDevice, type SNMPInterface } from "@/lib/api"
import { formatBitsPerSecond, formatNumber } from "@/lib/format"
import { resolveRange, useTimeRange } from "@/lib/time-range"

const series: TrafficSeries<"inBps" | "outBps">[] = [
  { key: "inBps", label: "In", color: "var(--chart-1)" },
  { key: "outBps", label: "Out", color: "var(--chart-2)" },
]

/** How full an interface is: its busier direction against its speed. */
function utilisation(i: SNMPInterface) {
  if (!i.now || !i.speed) return undefined
  return Math.max(i.now.inBps, i.now.outBps) / i.speed
}

/** A device polled over SNMP: every interface's load, errors and status, and a chart per interface. */
export function SNMPDevicePage() {
  const { id = "" } = useParams()
  const { search } = useLocation()
  const query = useQuery({
    queryKey: ["snmp-device", id],
    queryFn: ({ signal }) => fetchSNMPDevice(id, signal),
    refetchInterval: 30_000,
  })
  const [picked, setPicked] = useState<number | null>(null)
  if (query.isPending) {
    return (
      <Page>
        <SectionSkeleton rows={6} />
      </Page>
    )
  }
  if (query.isError) {
    return (
      <Page>
        <QueryError title="Couldn't load the SNMP device" error={query.error} />
      </Page>
    )
  }
  const d = query.data
  const h = d.source.health
  // The busiest interface unless one was picked.
  const busiest = [...d.interfaces].sort(
    (a, b) => (b.now ? b.now.inBps + b.now.outBps : -1) - (a.now ? a.now.inBps + a.now.outBps : -1),
  )[0]
  const shown = d.interfaces.find((i) => i.ifIndex === picked) ?? busiest
  return (
    <Page>
      <Card>
        <CardHeader>
          <CardTitle className="flex flex-wrap items-center gap-2">
            {d.source.name}
            <Badge variant={h.state === "failing" ? "destructive" : h.state === "ok" ? "default" : "secondary"}>
              {!d.source.enabled
                ? "Off"
                : h.state === "ok"
                  ? "Polling"
                  : h.state === "failing"
                    ? "Failing"
                    : "Starting"}
            </Badge>
          </CardTitle>
          <CardDescription className="flex flex-wrap gap-x-3">
            <span className="font-mono text-ident">{d.address}</span>
            {d.exporter && (
              <span>
                The Exporter{" "}
                <Link to={{ pathname: "/exporters", search }} className="font-mono underline underline-offset-4">
                  {d.exporter}
                </Link>
              </span>
            )}
            {h.lastSuccess && (
              <span>
                Polled <TimeAgo iso={h.lastSuccess} now={query.dataUpdatedAt} />
              </span>
            )}
            {h.state === "failing" && h.error && <span className="text-destructive">{h.error}</span>}
          </CardDescription>
        </CardHeader>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Interfaces</CardTitle>
          <CardDescription>
            Each interface's latest minute, how full it is against its speed, and the last hour's errors and discards.
            Pick one to chart it.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {d.interfaces.length === 0 ? (
            <p className="text-sm text-muted-foreground">No interfaces yet: they appear after the first poll.</p>
          ) : (
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Interface</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="text-right">Speed</TableHead>
                    <TableHead className="text-right">In</TableHead>
                    <TableHead className="text-right">Out</TableHead>
                    <TableHead className="text-right">Used</TableHead>
                    <TableHead className="text-right">Errors · discards</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {d.interfaces.map((i) => {
                    const u = utilisation(i)
                    const problems = i.now ? i.now.hourErrors + i.now.hourDiscards : 0
                    return (
                      <TableRow
                        key={i.ifIndex}
                        onClick={() => setPicked(i.ifIndex)}
                        className="cursor-pointer"
                        data-state={shown?.ifIndex === i.ifIndex ? "selected" : undefined}
                      >
                        <TableCell className="max-w-64">
                          <span className="block truncate font-mono text-ident">{i.name || `#${i.ifIndex}`}</span>
                          {i.alias && <span className="block truncate text-xs text-muted-foreground">{i.alias}</span>}
                        </TableCell>
                        <TableCell>
                          <Badge variant={i.up ? "secondary" : "outline"}>{i.up ? "Up" : "Down"}</Badge>
                        </TableCell>
                        <TableCell className="text-right tabular-nums">
                          {i.speed ? formatBitsPerSecond(i.speed) : "–"}
                        </TableCell>
                        <TableCell className="text-right tabular-nums">
                          {i.now ? formatBitsPerSecond(i.now.inBps) : "–"}
                        </TableCell>
                        <TableCell className="text-right tabular-nums">
                          {i.now ? formatBitsPerSecond(i.now.outBps) : "–"}
                        </TableCell>
                        <TableCell
                          className={
                            u !== undefined && u > 0.8
                              ? "text-right text-warning-foreground tabular-nums"
                              : "text-right tabular-nums"
                          }
                        >
                          {u === undefined ? "–" : `${(u * 100).toFixed(u < 0.1 ? 1 : 0)} %`}
                        </TableCell>
                        <TableCell
                          className={
                            problems > 0 ? "text-right text-warning-foreground tabular-nums" : "text-right tabular-nums"
                          }
                        >
                          {i.now ? `${formatNumber(i.now.hourErrors)} · ${formatNumber(i.now.hourDiscards)}` : "–"}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
      {shown && <InterfaceChart id={d.source.id} iface={shown} />}
    </Page>
  )
}

function InterfaceChart({ id, iface }: { id: string; iface: SNMPInterface }) {
  const { preset } = useTimeRange()
  const query = useQuery({
    queryKey: ["interface-series", id, iface.ifIndex, preset.value],
    queryFn: ({ signal }) => fetchInterfaceSeries(id, iface.ifIndex, resolveRange(preset), signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <span className="font-mono text-ident">{iface.name || `#${iface.ifIndex}`}</span>
          {iface.alias && <span className="text-muted-foreground"> · {iface.alias}</span>}
        </CardTitle>
        <CardDescription>Traffic in and out, from its counters, {preset.label.toLowerCase()}.</CardDescription>
      </CardHeader>
      <CardContent>
        {query.isPending ? (
          <Skeleton className="h-72 w-full" />
        ) : query.isError ? (
          <QueryError title="Couldn't load its counters" error={query.error} />
        ) : (
          <TrafficChart
            points={query.data.points.map((p) => ({ t: p.t, inBps: p.inBps, outBps: p.outBps }))}
            series={series}
            stepSeconds={query.data.stepSeconds}
            rangeMs={preset.ms}
            incompleteFrom={query.dataUpdatedAt - 60_000}
            noun="counters"
          />
        )}
      </CardContent>
    </Card>
  )
}
