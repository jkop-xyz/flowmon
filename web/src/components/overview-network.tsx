import { useQuery } from "@tanstack/react-query"
import { AlertTriangle, ChartArea, Cpu, Signpost, Users } from "lucide-react"
import { Link, useLocation } from "react-router"

import { AreaChip } from "@/components/area-chip"
import { BarList, BarRow } from "@/components/bar-list"
import { HostLink } from "@/components/host"
import { QueryError, SectionSkeleton, TimeAgo } from "@/components/page"
import { TrafficChart } from "@/components/traffic-chart"
import { Badge } from "@/components/ui/badge"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { type DeviceHealthSummary, type InterfaceProblem, type WANLoad } from "@/lib/api"
import { useDeviceLabels } from "@/lib/devices"
import { formatBitsPerSecond, formatNumber } from "@/lib/format"
import { useHostNames } from "@/lib/hosts"
import { dnsQuery, useUsageNow } from "@/lib/overview"
import { useTimeRange } from "@/lib/time-range"

/** The WAN's load over the range, from the router's own counters, against the line. */
export function WANLoadCard({ wan, fetched }: { wan: WANLoad; fetched: number }) {
  const { preset } = useTimeRange()
  const names = wan.interfaces.map((i) => `${i.name} on ${i.sourceName}`).join(", ")
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area="overview" icon={ChartArea} />
          WAN load
        </CardTitle>
        <CardDescription>
          {names}, from its counters each minute, {preset.label.toLowerCase()}.{" "}
          {wan.line.fromSetting ? (
            "Dashed: your line's speed."
          ) : (
            <>
              <Link to="/settings/internet-line" className="underline underline-offset-4">
                Set your line's speed
              </Link>{" "}
              to see how full it is.
            </>
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <TrafficChart
          points={wan.points.map((p) => ({ t: p.t, down: p.inBps, up: p.outBps }))}
          series={[
            { key: "down", label: "Download", color: "var(--chart-1)" },
            { key: "up", label: "Upload", color: "var(--chart-2)" },
          ]}
          stepSeconds={wan.stepSeconds}
          rangeMs={preset.ms}
          incompleteFrom={fetched - 60_000}
          noun="counters"
          limits={
            wan.line.fromSetting
              ? [
                  { value: wan.line.downBps, label: `Line down ${formatBitsPerSecond(wan.line.downBps)}` },
                  { value: wan.line.upBps, label: `Line up ${formatBitsPerSecond(wan.line.upBps)}` },
                ].filter((l) => l.value > 0)
              : []
          }
        />
        <p className="text-xs text-muted-foreground">
          The router's exact byte counts, so this includes traffic Flows miss, such as hardware-offloaded connections.
        </p>
      </CardContent>
    </Card>
  )
}

/** The Devices and domains behind the last few minutes' traffic with the internet. */
export function UsageNowCard() {
  const query = useUsageNow()
  const devices = query.data?.devices ?? []
  const domains = query.data?.domains ?? []
  const names = useHostNames([...devices.map((d) => d.host), ...domains.map((d) => d.topHost)])
  const labels = useDeviceLabels()
  const who = (mac: string | undefined, host: string) => (mac && labels[mac]) || names[host]?.name || host
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area="hosts" icon={Users} />
          Who is using it now
        </CardTitle>
        <CardDescription>The Devices and domains behind the last 5 minutes' traffic with the internet.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        {query.isPending ? (
          <SectionSkeleton rows={5} />
        ) : query.isError ? (
          <QueryError title="Couldn't load who is using it" error={query.error} />
        ) : devices.length === 0 ? (
          <p className="text-sm text-muted-foreground">No traffic with the internet in the last 5 minutes.</p>
        ) : (
          <>
            <BarList>
              {devices.map((d) => (
                <BarRow
                  key={d.mac ?? d.host}
                  label={
                    <HostLink
                      host={d.host}
                      name={names[d.host]}
                      device={
                        d.mac
                          ? { mac: d.mac, vendor: d.vendor, privateMac: d.privateMac, label: labels[d.mac] }
                          : undefined
                      }
                    />
                  }
                  total={d.inBps + d.outBps}
                  received={d.inBps}
                  sent={d.outBps}
                  max={devices[0].inBps + devices[0].outBps}
                  format={formatBitsPerSecond}
                />
              ))}
            </BarList>
            {domains.length > 0 && (
              <div className="flex flex-col gap-3 border-t pt-4">
                <BarList>
                  {domains.map((d) => (
                    <BarRow
                      key={d.domain}
                      label={
                        <span className="flex min-w-0 items-baseline gap-2">
                          <Link
                            to={`/dns/${encodeURIComponent(d.domain)}`}
                            className="truncate font-mono text-ident underline-offset-4 hover:underline"
                          >
                            {d.domain}
                          </Link>
                          <span className="truncate text-xs text-muted-foreground">{who(d.topMac, d.topHost)}</span>
                        </span>
                      }
                      total={d.inBps + d.outBps}
                      received={d.inBps}
                      sent={d.outBps}
                      max={domains[0].inBps + domains[0].outBps}
                      format={formatBitsPerSecond}
                    />
                  ))}
                </BarList>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

const percent = (v: number) => `${v.toFixed(0)} %`

function Reading({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-lg bg-muted px-3 py-2">
      <div className="truncate text-xs text-muted-foreground">{label}</div>
      <div className="text-lg font-semibold tabular-nums">{value}</div>
    </div>
  )
}

/** The hottest temperature sensor a device reports, if any. */
function hottest(metrics: Record<string, number>) {
  const temps = Object.entries(metrics).filter(([k]) => k.startsWith("temperature:"))
  return temps.length > 0 ? Math.max(...temps.map(([, v]) => v)) : undefined
}

function uptime(seconds: number) {
  if (seconds >= 2 * 86400) return `${Math.floor(seconds / 86400)} days`
  if (seconds >= 2 * 3600) return `${Math.floor(seconds / 3600)} h`
  return `${Math.max(1, Math.floor(seconds / 60))} min`
}

/** Each SNMP device's CPU, memory and temperature, and restarts in the last day; the first in full. */
export function DeviceHealthCard({ devices, fetched }: { devices: DeviceHealthSummary[]; fetched: number }) {
  const { search } = useLocation()
  // The device that reports the most leads: usually the router.
  const ranked = [...devices].sort((a, b) => Object.keys(b.metrics).length - Object.keys(a.metrics).length)
  const [main, ...rest] = ranked
  const m = main.metrics
  const temp = hottest(m)
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area="exporters" icon={Cpu} />
          Device health
        </CardTitle>
        <CardDescription>What each SNMP device reports about itself, each minute.</CardDescription>
        <CardAction>
          <Link
            to={{ pathname: `/snmp/${encodeURIComponent(main.source)}`, search }}
            className="text-sm underline-offset-4 hover:underline"
          >
            {main.name}
          </Link>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {Object.keys(m).length === 0 ? (
          <p className="text-sm text-muted-foreground">No readings from {main.name} yet.</p>
        ) : (
          <div className="grid grid-cols-3 gap-2">
            {m.cpu !== undefined && <Reading label="CPU" value={percent(m.cpu)} />}
            {m.memory_total ? (
              <Reading label="Memory" value={percent((100 * (m.memory_used ?? 0)) / m.memory_total)} />
            ) : null}
            {temp !== undefined && <Reading label="Temperature" value={`${temp.toFixed(0)} °C`} />}
            {m.uptime !== undefined && m.cpu === undefined && <Reading label="Up for" value={uptime(m.uptime)} />}
          </div>
        )}
        <DeviceLine d={main} fetched={fetched} />
        {rest.map((d) => (
          <div key={d.source} className="flex flex-wrap items-baseline justify-between gap-x-3 border-t pt-2 text-sm">
            <Link
              to={{ pathname: `/snmp/${encodeURIComponent(d.source)}`, search }}
              className="underline-offset-4 hover:underline"
            >
              {d.name}
            </Link>
            <DeviceLine d={d} fetched={fetched} />
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

/** A device's state in a line: polling problems, a recent restart, uptime and readings. */
function DeviceLine({ d, fetched }: { d: DeviceHealthSummary; fetched: number }) {
  const m = d.metrics
  const temp = hottest(m)
  const parts = [
    m.cpu !== undefined ? `CPU ${percent(m.cpu)}` : undefined,
    temp !== undefined ? `${temp.toFixed(0)} °C` : undefined,
    m.uptime !== undefined ? `up ${uptime(m.uptime)}` : undefined,
    m.wifi_clients !== undefined ? `${formatNumber(m.wifi_clients)} Wi-Fi clients` : undefined,
  ].filter(Boolean)
  return (
    <span className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      {d.state === "failing" && <Badge variant="destructive">Not answering</Badge>}
      {d.restarts.length > 0 && (
        <Badge variant="warning">
          Restarted <TimeAgo iso={d.restarts[0]} now={fetched} />
        </Badge>
      )}
      {parts.join(" · ")}
    </span>
  )
}

/** Interfaces that are down, or had errors or discards in the last hour, on any SNMP device. */
export function InterfaceProblemsCard({ problems }: { problems: InterfaceProblem[] }) {
  const { search } = useLocation()
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area="exporters" icon={AlertTriangle} />
          Interface problems
        </CardTitle>
        <CardDescription>
          Named interfaces that are down, and errors and discards in the last hour, on every SNMP device.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {problems.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            None: every named interface is up, without errors or discards.
          </p>
        ) : (
          <ul className="flex flex-col divide-y text-sm">
            {problems.slice(0, 8).map((p) => (
              <li key={`${p.source}/${p.ifIndex}`} className="flex items-start justify-between gap-3 py-2 first:pt-0">
                <span className="min-w-0">
                  <Link
                    to={{ pathname: `/snmp/${encodeURIComponent(p.source)}`, search }}
                    className="underline-offset-4 hover:underline"
                  >
                    {p.sourceName}
                  </Link>{" "}
                  · <span className="font-mono text-ident">{p.name}</span>
                  {p.alias && <span className="block truncate text-xs text-muted-foreground">{p.alias}</span>}
                </span>
                <span className="flex shrink-0 flex-wrap justify-end gap-1">
                  {p.down && <Badge variant="destructive">Down</Badge>}
                  {p.errors > 0 && <Badge variant="warning">{formatNumber(p.errors)} errors</Badge>}
                  {p.discards > 0 && <Badge variant="warning">{formatNumber(p.discards)} discards</Badge>}
                </span>
              </li>
            ))}
          </ul>
        )}
        {problems.length > 8 && <p className="mt-2 text-xs text-muted-foreground">And {problems.length - 8} more.</p>}
      </CardContent>
    </Card>
  )
}

/** Lookups and blocks in the range, and the most blocked names. */
export function DNSOverviewCard() {
  const { preset } = useTimeRange()
  const query = useQuery(dnsQuery(preset))
  const s = query.data?.result
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <AreaChip area="dns" icon={Signpost} />
          DNS
        </CardTitle>
        <CardDescription>Lookups and blocks, {preset.label.toLowerCase()}.</CardDescription>
        <CardAction>
          <Link to="/dns" className="text-sm underline-offset-4 hover:underline">
            All DNS
          </Link>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {query.isPending ? (
          <SectionSkeleton rows={4} />
        ) : query.isError ? (
          <QueryError title="Couldn't load DNS" error={query.error} />
        ) : (
          <>
            <TrafficChart
              points={s!.points}
              series={[
                { key: "queries", label: "Lookups", color: "var(--chart-3)" },
                { key: "blocked", label: "Blocked", color: "var(--chart-2)" },
              ]}
              stepSeconds={s!.stepSeconds}
              rangeMs={preset.ms}
              incompleteFrom={query.dataUpdatedAt - 15_000}
              format={(v) => `${formatNumber(Math.round(v))}/min`}
              noun="lookups"
              note={false}
              compact
            />
            {s!.topBlocked.length > 0 && (
              <BarList>
                {s!.topBlocked.slice(0, 3).map((n) => (
                  <BarRow
                    key={n.name}
                    label={<span className="truncate font-mono text-ident">{n.name}</span>}
                    total={n.blocked}
                    received={0}
                    sent={n.blocked}
                    max={s!.topBlocked[0].blocked}
                    format={(v) => `${formatNumber(v)} blocked`}
                  />
                ))}
              </BarList>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
