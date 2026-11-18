import { useQuery } from "@tanstack/react-query"
import { ArrowDownLeft, ArrowUpRight, Search } from "lucide-react"
import { useState, type FormEvent } from "react"
import { useNavigate, useLocation, useParams } from "react-router"

import { DBIPAttribution } from "@/components/geoip"
import { Page, QueryError, SectionSkeleton, Stat } from "@/components/page"
import { CoverageNote, RemoteIPLink } from "@/components/remote"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { fetchRemoteFlows, fetchRemoteIP, fetchRemoteIPs, type FlowList, type GeoInfo, type RemoteDetail } from "@/lib/api"
import { countryName } from "@/lib/countries"
import { useExporterFilter } from "@/lib/exporter-filter"
import { formatBytes, formatDateTime, formatNumber, protocolName } from "@/lib/format"
import { useGeoIP, usesDBIP } from "@/lib/geoip"
import { resolveRange, useTimeRange } from "@/lib/time-range"

function countryLabel(code?: string) {
  return code ? `${countryName(code)} (${code})` : "Unknown"
}

function asnLabel(g: GeoInfo) {
  return g.asn ? `AS${g.asn}${g.asnOrg ? ` ${g.asnOrg}` : ""}` : "Unknown"
}

function DBIPNote() {
  const geoip = useGeoIP()
  if (!usesDBIP(geoip.data)) return null
  return (
    <p className="text-xs text-muted-foreground">
      <DBIPAttribution />
    </p>
  )
}

/** Accepts an address and opens its page. */
function OpenIPForm() {
  const navigate = useNavigate()
  const { search } = useLocation()
  const [value, setValue] = useState("")
  const [error, setError] = useState("")
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const ip = value.trim()
    // The server checks properly; this only catches obvious typos.
    if (!/^[0-9a-fA-F:.]+$/.test(ip) || !(ip.includes(".") || ip.includes(":"))) {
      setError("Enter an IPv4 or IPv6 address.")
      return
    }
    setError("")
    navigate({ pathname: `/remote/${ip}`, search })
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-1">
      <div className="flex gap-2">
        <Input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="Open an address, e.g. 1.1.1.1"
          aria-label="IP address"
          className="h-8 w-full sm:w-64"
        />
        <Button type="submit" size="sm" variant="outline">
          <Search />
          Open
        </Button>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
    </form>
  )
}

export function RemoteIPsPage() {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const [sort, setSort] = useState<"bytes" | "flows">("bytes")
  const query = useQuery({
    queryKey: ["remote-ips", preset.value, exporter, sort],
    queryFn: ({ signal }) => fetchRemoteIPs({ ...resolveRange(preset), exporter }, sort, signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const rangeFrom = resolveRange(preset).from

  return (
    <Page>
      <Card>
        <CardHeader className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex flex-col gap-1.5">
            <CardTitle>Remote IPs</CardTitle>
            <CardDescription>The busiest addresses on the internet side, {preset.label.toLowerCase()}.</CardDescription>
          </div>
          <OpenIPForm />
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">Rank by</span>
            <div className="flex gap-1">
              {(["bytes", "flows"] as const).map((s) => (
                <Button key={s} size="sm" variant={sort === s ? "secondary" : "ghost"} onClick={() => setSort(s)}>
                  {s === "bytes" ? "Bytes" : "Flows"}
                </Button>
              ))}
            </div>
          </div>
          {query.isPending ? (
            <SectionSkeleton rows={8} />
          ) : query.isError ? (
            <QueryError title="Couldn't load Remote IPs" error={query.error} />
          ) : query.data.result.items.length === 0 ? (
            <p className="text-sm text-muted-foreground">No Flows with Remote IPs in this time range yet.</p>
          ) : (
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Remote IP</TableHead>
                    <TableHead>Country</TableHead>
                    <TableHead>ASN Owner</TableHead>
                    <TableHead className="text-right">Received</TableHead>
                    <TableHead className="text-right">Sent</TableHead>
                    <TableHead className="text-right">Flows</TableHead>
                    <TableHead className="text-right">Local Hosts</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {query.data.result.items.map((it) => (
                    <TableRow key={it.remoteIp}>
                      <TableCell>
                        <RemoteIPLink ip={it.remoteIp} />
                      </TableCell>
                      <TableCell>{countryLabel(it.country)}</TableCell>
                      <TableCell className="max-w-64 truncate" title={asnLabel(it)}>
                        {asnLabel(it)}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">{formatBytes(it.receivedBytes)}</TableCell>
                      <TableCell className="text-right tabular-nums">{formatBytes(it.sentBytes)}</TableCell>
                      <TableCell className="text-right tabular-nums">{formatNumber(Math.round(it.flows))}</TableCell>
                      <TableCell className="text-right tabular-nums">{it.localHosts}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          {query.data && (
            <>
              {query.data.result.otherBytes > 0 && (
                <p className="text-xs text-muted-foreground">
                  {formatBytes(query.data.result.otherBytes)} of {formatBytes(query.data.result.totalBytes)} was with Remote
                  IPs outside the lists.
                </p>
              )}
              <CoverageNote coverage={query.data.result} rangeFrom={rangeFrom} />
            </>
          )}
          <DBIPNote />
        </CardContent>
      </Card>
    </Page>
  )
}

export function RemoteIPPage() {
  const { ip = "" } = useParams()
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const detail = useQuery({
    queryKey: ["remote-ip", ip, preset.value, exporter],
    queryFn: ({ signal }) => fetchRemoteIP(ip, { ...resolveRange(preset), exporter }, signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const rangeFrom = resolveRange(preset).from

  return (
    <Page>
      {detail.isPending ? (
        <SectionSkeleton rows={4} />
      ) : detail.isError ? (
        <QueryError title={`Couldn't load ${ip}`} error={detail.error} />
      ) : (
        <>
          <IdentityCard d={detail.data.result} />
          <LocalHostsCard d={detail.data.result} rangeFrom={rangeFrom} label={preset.label.toLowerCase()} />
        </>
      )}
      <FlowsCard ip={ip} />
    </Page>
  )
}

function IdentityCard({ d }: { d: RemoteDetail }) {
  // Today's lookup comes first; stored Flows keep what was known when they arrived.
  const now = d.current ?? d
  const storedDiffers =
    d.current && (d.country || d.asn) && (d.country !== d.current.country || d.asn !== d.current.asn)
  return (
    <Card>
      <CardHeader>
        <CardTitle className="font-mono text-lg">{d.remoteIp}</CardTitle>
        <CardDescription className="flex flex-col gap-0.5">
          <span>
            {countryLabel(now.country)} · {asnLabel(now)}
          </span>
          {storedDiffers && (
            <span className="text-xs">
              Its Flows were stored as {countryLabel(d.country)} · {asnLabel(d)}; the GeoIP database has changed since.
            </span>
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <Stat label="Received from it" value={formatBytes(d.receivedBytes)} />
          <Stat label="Sent to it" value={formatBytes(d.sentBytes)} />
          <Stat label="Flows" value={formatNumber(Math.round(d.flows))} />
          <Stat label="Local Hosts" value={d.localHosts.length} />
        </div>
        <DBIPNote />
      </CardContent>
    </Card>
  )
}

function LocalHostsCard({ d, rangeFrom, label }: { d: RemoteDetail; rangeFrom: Date; label: string }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Local Hosts</CardTitle>
        <CardDescription>The Local Hosts that talked to {d.remoteIp}, {label}.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {d.localHosts.length === 0 ? (
          <p className="text-sm text-muted-foreground">No Local Host talked to it in this time range.</p>
        ) : (
          <div className="overflow-x-auto rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Local Host</TableHead>
                  <TableHead className="text-right">Received</TableHead>
                  <TableHead className="text-right">Sent</TableHead>
                  <TableHead className="text-right">Flows</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.localHosts.map((h) => (
                  <TableRow key={h.host}>
                    <TableCell>
                      <span className="flex items-center gap-2">
                        <span className="font-mono text-xs">{h.host}</span>
                        {h.localIsExporterHost && <Badge variant="outline">Exporter Host</Badge>}
                      </span>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{formatBytes(h.receivedBytes)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatBytes(h.sentBytes)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatNumber(Math.round(h.flows))}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        <CoverageNote coverage={d} rangeFrom={rangeFrom} />
      </CardContent>
    </Card>
  )
}

function FlowsCard({ ip }: { ip: string }) {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const query = useQuery({
    queryKey: ["remote-flows", ip, preset.value, exporter],
    queryFn: ({ signal }) => fetchRemoteFlows(ip, { ...resolveRange(preset), exporter }, signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const rangeFrom = resolveRange(preset).from
  return (
    <Card>
      <CardHeader>
        <CardTitle>Flows</CardTitle>
        <CardDescription>Its raw Flows, newest first.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {query.isPending ? (
          <SectionSkeleton rows={6} />
        ) : query.isError ? (
          <QueryError title="Couldn't load its Flows" error={query.error} />
        ) : (
          <FlowsTable list={query.data} rangeFrom={rangeFrom} />
        )}
      </CardContent>
    </Card>
  )
}

function FlowsTable({ list, rangeFrom }: { list: FlowList; rangeFrom: Date }) {
  const truncated = list.rawFrom && new Date(list.rawFrom) > rangeFrom
  return (
    <>
      {list.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {list.rawFrom ? "No raw Flows with it in this time range." : "No raw Flows are kept yet."}
        </p>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Ended</TableHead>
                <TableHead>Direction</TableHead>
                <TableHead>Local Host</TableHead>
                <TableHead>Remote port</TableHead>
                <TableHead className="text-right">Bytes</TableHead>
                <TableHead className="text-right">Packets</TableHead>
                <TableHead>Exporter</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.items.map((f, i) => (
                <TableRow key={`${f.end}/${f.localHost}/${f.localPort}/${f.peerPort}/${i}`}>
                  <TableCell className="tabular-nums" title={`Started ${formatDateTime(f.start)}`}>
                    {formatDateTime(f.end)}
                  </TableCell>
                  <TableCell>
                    <span className="flex items-center gap-1">
                      {f.direction === "inbound" ? (
                        <ArrowDownLeft className="size-3.5 text-chart-1" />
                      ) : (
                        <ArrowUpRight className="size-3.5 text-chart-2" />
                      )}
                      {f.direction === "inbound" ? "Inbound" : "Outbound"}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {f.localHost}:{f.localPort}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {f.peerPort}/{protocolName(f.protocol).toLowerCase()}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{formatBytes(f.bytes)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatNumber(f.packets)}</TableCell>
                  <TableCell className="font-mono text-xs">{f.exporter}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {(list.more || truncated) && (
        <p className="text-xs text-muted-foreground">
          {list.more && `Showing the newest ${list.items.length}. `}
          {truncated &&
            `Raw Flows are only kept from ${formatDateTime(list.rawFrom!)}, so older Flows in this time range aren't available. The totals above still include them.`}
        </p>
      )}
    </>
  )
}
