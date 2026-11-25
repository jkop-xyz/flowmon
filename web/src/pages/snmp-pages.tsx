import { useQuery } from "@tanstack/react-query"
import { useState } from "react"
import { Link, useLocation, useParams } from "react-router"

import { Page, QueryError, SectionSkeleton, TimeAgo } from "@/components/page"
import { TrafficChart, type TrafficSeries } from "@/components/traffic-chart"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  fetchDeviceHealth,
  fetchInterfaceSeries,
  fetchSNMPDevice,
  type HealthMetrics,
  type SNMPDevice,
  type SNMPInterface,
} from "@/lib/api"
import { formatBitsPerSecond, formatBytes, formatDateTime, formatNumber } from "@/lib/format"
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
      <HealthCard device={d} />
      {d.wifi.length > 0 && <WifiCard device={d} now={query.dataUpdatedAt} />}
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
            restarts={query.data.restarts}
          />
        )}
      </CardContent>
    </Card>
  )
}

/** How long a device has been up: "23 days", "5 h", "12 min". */
function formatUptime(seconds: number) {
  if (seconds >= 2 * 86400) return `${Math.floor(seconds / 86400)} days`
  if (seconds >= 2 * 3600) return `${Math.floor(seconds / 3600)} h`
  return `${Math.max(1, Math.floor(seconds / 60))} min`
}

const sensorUnits: Record<string, { unit: string; digits: number }> = {
  temperature: { unit: "°C", digits: 0 },
  voltage: { unit: "V", digits: 1 },
  current: { unit: "A", digits: 1 },
  power: { unit: "W", digits: 1 },
  fan: { unit: "rpm", digits: 0 },
}

const kindOrder = Object.keys(sensorUnits)

/** A sensor's name as MikroTik gives it, readable: "cpu-temperature" is "CPU temperature". */
function sensorLabel(name: string) {
  const words = name.replace(/([a-z])(\d+)/gi, "$1 $2").split("-")
  const text = words.map((w) => (/^(cpu|psu|poe|sfp)$/i.test(w) ? w.toUpperCase() : w)).join(" ")
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/** The sensors among a device's readings, by kind then name. */
function sensors(metrics: HealthMetrics) {
  return Object.keys(metrics)
    .filter((k) => k.includes(":"))
    .map((k) => {
      const [kind, name] = [k.slice(0, k.indexOf(":")), k.slice(k.indexOf(":") + 1)]
      return { key: k, kind, name, value: metrics[k] }
    })
    .filter((s) => s.kind in sensorUnits)
    .sort((a, b) => kindOrder.indexOf(a.kind) - kindOrder.indexOf(b.kind) || a.name.localeCompare(b.name))
}

function Reading({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <div className="rounded-lg bg-muted px-3 py-2">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="text-lg font-semibold tabular-nums">{value}</div>
      {detail && <div className="text-xs text-muted-foreground">{detail}</div>}
    </div>
  )
}

const percentFormat = (v: number) => `${v.toFixed(0)} %`

/** Device Health: the latest readings, and CPU, memory and temperatures over the range with restarts marked. */
function HealthCard({ device }: { device: SNMPDevice }) {
  const { preset } = useTimeRange()
  const query = useQuery({
    queryKey: ["device-health", device.source.id, preset.value],
    queryFn: ({ signal }) => fetchDeviceHealth(device.source.id, resolveRange(preset), signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const m = device.health?.metrics
  const restarts = query.data?.restarts ?? []
  const temps = m ? sensors(m).filter((s) => s.kind === "temperature") : []
  const usage = (query.data?.points ?? [])
    .filter((p) => p.metrics.cpu !== undefined || p.metrics.memory_total)
    .map((p) => ({
      t: p.t,
      cpu: p.metrics.cpu ?? 0,
      memory: p.metrics.memory_total ? (100 * (p.metrics.memory_used ?? 0)) / p.metrics.memory_total : 0,
    }))
  const tempSeries = temps.map((s, i) => ({
    key: `t${i}`,
    label: sensorLabel(s.name),
    color: `var(--chart-${(i % 5) + 1})`,
  }))
  const tempPoints = (query.data?.points ?? [])
    .filter((p) => temps.every((s) => p.metrics[s.key] !== undefined))
    .map((p) => ({ t: p.t, ...Object.fromEntries(temps.map((s, i) => [`t${i}`, p.metrics[s.key]])) }))
  const rangeMs = preset.ms
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2">
          Health
          {restarts.length > 0 && (
            <Badge variant="warning">
              Restarted {restarts.length === 1 ? "" : `${restarts.length} times `}in the {preset.label.toLowerCase()}
            </Badge>
          )}
        </CardTitle>
        <CardDescription>
          What the device reports about itself each minute: uptime, CPU and memory from HOST-RESOURCES-MIB, and on
          MikroTik its sensors and Wi-Fi clients.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {!m ? (
          <p className="text-sm text-muted-foreground">No readings yet: they appear after the first poll.</p>
        ) : (
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
            {m.uptime !== undefined && (
              <Reading
                label="Up for"
                value={formatUptime(m.uptime)}
                detail={device.health?.boot ? `since ${formatDateTime(device.health.boot)}` : undefined}
              />
            )}
            {m.cpu !== undefined && <Reading label="CPU" value={percentFormat(m.cpu)} />}
            {m.memory_total ? (
              <Reading
                label="Memory"
                value={percentFormat((100 * (m.memory_used ?? 0)) / m.memory_total)}
                detail={`${formatBytes(m.memory_used ?? 0)} of ${formatBytes(m.memory_total)}`}
              />
            ) : null}
            {sensors(m).map((s) => (
              <Reading
                key={s.key}
                label={sensorLabel(s.name)}
                value={`${s.value.toFixed(sensorUnits[s.kind].digits)} ${sensorUnits[s.kind].unit}`}
              />
            ))}
            {m.wifi_clients !== undefined && <Reading label="Wi-Fi clients" value={formatNumber(m.wifi_clients)} />}
          </div>
        )}
        {m && m.cpu === undefined && !m.memory_total && (
          <p className="text-sm text-muted-foreground">
            This device doesn't report CPU or memory over SNMP (no HOST-RESOURCES-MIB), so only its uptime and restarts
            are shown.
          </p>
        )}
        {query.isError && <QueryError title="Couldn't load its health" error={query.error} />}
        {usage.length > 0 && (
          <div className="flex flex-col gap-2">
            <h3 className="text-sm font-medium">CPU and memory</h3>
            <TrafficChart
              points={usage}
              series={[
                { key: "cpu", label: "CPU", color: "var(--chart-1)" },
                { key: "memory", label: "Memory", color: "var(--chart-3)" },
              ]}
              stepSeconds={query.data!.stepSeconds}
              rangeMs={rangeMs}
              incompleteFrom={Infinity}
              format={percentFormat}
              noun="readings"
              note={false}
              restarts={restarts}
            />
          </div>
        )}
        {tempPoints.length > 0 && (
          <div className="flex flex-col gap-2">
            <h3 className="text-sm font-medium">Temperatures</h3>
            <TrafficChart
              points={tempPoints as ({ t: string } & Record<string, number>)[]}
              series={tempSeries}
              stepSeconds={query.data!.stepSeconds}
              rangeMs={rangeMs}
              incompleteFrom={Infinity}
              format={(v) => `${v.toFixed(0)} °C`}
              noun="readings"
              note={false}
              restarts={restarts}
            />
          </div>
        )}
      </CardContent>
    </Card>
  )
}

/** How good a Wi-Fi signal is, by the usual rule of thumb. */
function signalQuality(dbm: number) {
  if (dbm >= -60) return { text: "Good", className: "" }
  if (dbm >= -70) return { text: "Fair", className: "" }
  return { text: "Weak", className: "text-warning-foreground" }
}

/** The stations registered to the device's Wi-Fi interfaces, as last polled. */
function WifiCard({ device, now }: { device: SNMPDevice; now: number }) {
  const { search } = useLocation()
  return (
    <Card>
      <CardHeader>
        <CardTitle>Wi-Fi clients</CardTitle>
        <CardDescription>
          Stations registered to its wireless interfaces at the last poll, strongest signal first. Signal under −70 dBm
          is weak.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Device</TableHead>
                <TableHead>Interface</TableHead>
                <TableHead className="text-right">Signal</TableHead>
                <TableHead className="text-right">Rate to it</TableHead>
                <TableHead className="text-right">Rate from it</TableHead>
                <TableHead className="text-right">Connected</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {device.wifi.map((c) => {
                const q = signalQuality(c.signal)
                return (
                  <TableRow key={`${c.mac}/${c.ifIndex}`}>
                    <TableCell>
                      <Link
                        to={{ pathname: `/devices/${encodeURIComponent(c.mac)}`, search }}
                        className="font-mono text-ident underline-offset-4 hover:underline"
                      >
                        {c.mac}
                      </Link>
                    </TableCell>
                    <TableCell className="font-mono text-ident">{c.interface || `#${c.ifIndex}`}</TableCell>
                    <TableCell className={`text-right tabular-nums ${q.className}`}>
                      {c.signal} dBm · {q.text}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{formatBitsPerSecond(c.txRate)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatBitsPerSecond(c.rxRate)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatUptime(c.connectedSeconds)}</TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          Polled <TimeAgo iso={device.wifi[0].seen} now={now} />.
        </p>
      </CardContent>
    </Card>
  )
}
