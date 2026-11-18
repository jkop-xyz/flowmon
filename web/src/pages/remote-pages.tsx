import { useQuery } from "@tanstack/react-query"
import { AlertTriangle, RotateCw, Search } from "lucide-react"
import { useState, type FormEvent } from "react"
import { Link, useNavigate, useLocation, useParams } from "react-router"

import { DBIPAttribution } from "@/components/geoip"
import { FlowDetailSheet, FlowTable } from "@/components/flow-table"
import { HostLink } from "@/components/host"
import { Page, QueryError, SectionSkeleton, Stat, TimeAgo } from "@/components/page"
import { CoverageNote, RemoteIPLink } from "@/components/remote"
import { ReverseDNS } from "@/components/reverse-dns"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  fetchRDAP,
  fetchFlows,
  fetchRemoteIP,
  fetchRemoteIPs,
  type GeoInfo,
  type Registration,
  type RemoteDetail,
} from "@/lib/api"
import { countryName } from "@/lib/countries"
import { useExporterFilter } from "@/lib/exporter-filter"
import { formatBytes, formatDateTime, formatNumber } from "@/lib/format"
import { useGeoIP, usesDBIP } from "@/lib/geoip"
import { useHostNames } from "@/lib/hosts"
import { useReverseDNS } from "@/lib/reverse-dns"
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
  const names = useReverseDNS(query.data?.result.items.map((i) => i.remoteIp) ?? [])

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
                      <TableCell className="max-w-72">
                        <div className="flex flex-col gap-0.5">
                          <RemoteIPLink ip={it.remoteIp} />
                          <ReverseDNS name={names[it.remoteIp]} />
                        </div>
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
          <RegistrationCard ip={ip} />
          <LocalHostsCard d={detail.data.result} rangeFrom={rangeFrom} label={preset.label.toLowerCase()} />
        </>
      )}
      <FlowsCard ip={ip} />
    </Page>
  )
}

function IdentityCard({ d }: { d: RemoteDetail }) {
  const names = useReverseDNS([d.remoteIp])
  const name = names[d.remoteIp]
  // Today's lookup comes first; stored Flows keep what was known when they arrived.
  const now = d.current ?? d
  const storedDiffers =
    d.current && (d.country || d.asn) && (d.country !== d.current.country || d.asn !== d.current.asn)
  return (
    <Card>
      <CardHeader>
        <CardTitle className="font-mono text-lg">{d.remoteIp}</CardTitle>
        <CardDescription className="flex flex-col gap-0.5">
          {name?.status === "resolved" ? (
            <span className="font-mono text-sm text-foreground">{name.name}</span>
          ) : name?.status === "pending" || !name ? (
            <ReverseDNS name={name} className="h-4 w-40" />
          ) : (
            <span className="text-xs">{name.status === "failed" ? "Reverse DNS didn't answer; trying again later." : "No reverse DNS name."}</span>
          )}
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
  const names = useHostNames(d.localHosts.map((h) => h.host))
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
                    <TableCell className="max-w-72">
                      <HostLink host={h.host} name={names[h.host]} exporterHost={h.localIsExporterHost} />
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
  const { search } = useLocation()
  const [selected, setSelected] = useState<string | null>(null)
  const query = useQuery({
    queryKey: ["flows", "remote", ip, preset.value, exporter],
    queryFn: ({ signal }) =>
      fetchFlows({ ...resolveRange(preset), exporter }, { remote: ip }, { sort: "end", asc: false, limit: 100, offset: 0 }, signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const rangeFrom = resolveRange(preset).from
  const explorer = new URLSearchParams(search)
  explorer.set("remote", ip)
  const truncated = query.data?.rawFrom && new Date(query.data.rawFrom) > rangeFrom
  return (
    <Card>
      <CardHeader>
        <CardTitle>Flows</CardTitle>
        <CardDescription>Its raw Flows, newest first. Click one for every field.</CardDescription>
        <CardAction>
          <Link to={{ pathname: "/flows", search: explorer.toString() }} className="text-sm underline-offset-4 hover:underline">
            Open in Flow Explorer
          </Link>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {query.isPending ? (
          <SectionSkeleton rows={6} />
        ) : query.isError ? (
          <QueryError title="Couldn't load its Flows" error={query.error} />
        ) : query.data.items.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {query.data.rawFrom ? "No raw Flows with it in this time range." : "No raw Flows are kept yet."}
          </p>
        ) : (
          <FlowTable items={query.data.items} hidden={["peer", "network"]} onSelect={(f) => setSelected(f.id)} />
        )}
        {query.data && (query.data.total > query.data.items.length || truncated) && (
          <p className="text-xs text-muted-foreground">
            {query.data.total > query.data.items.length &&
              `Showing the newest ${query.data.items.length} of ${formatNumber(query.data.total)}. `}
            {truncated &&
              `Raw Flows are only kept from ${formatDateTime(query.data.rawFrom!)}, so older Flows in this time range aren't available. The totals above still include them.`}
          </p>
        )}
      </CardContent>
      <FlowDetailSheet id={selected} onClose={() => setSelected(null)} />
    </Card>
  )
}

/** Registration details from the address's registry (RDAP), fetched when the page opens. */
function RegistrationCard({ ip }: { ip: string }) {
  const query = useQuery({
    queryKey: ["rdap", ip],
    queryFn: ({ signal }) => fetchRDAP(ip, signal),
    staleTime: 60 * 60_000,
    retry: false,
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>Registration</CardTitle>
        <CardDescription>Who holds the network {ip} belongs to, according to its regional registry (RDAP).</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {query.isPending ? (
          <SectionSkeleton rows={3} />
        ) : query.isError ? (
          <Alert variant="warning">
            <AlertTriangle />
            <AlertTitle>Couldn't get the registration</AlertTitle>
            <AlertDescription>
              <p>{query.error.message}</p>
              <Button size="sm" variant="outline" className="mt-2" onClick={() => void query.refetch()}>
                <RotateCw />
                Try again
              </Button>
            </AlertDescription>
          </Alert>
        ) : query.data.status === "not-found" || !query.data.registration ? (
          <p className="text-sm text-muted-foreground">The registry has no record of this address.</p>
        ) : (
          <RegistrationDetails r={query.data.registration} fetchedAt={query.data.fetchedAt} now={query.dataUpdatedAt} />
        )}
      </CardContent>
    </Card>
  )
}

function RegistrationDetails({ r, fetchedAt, now }: { r: Registration; fetchedAt: string; now: number }) {
  const rows: [string, React.ReactNode][] = []
  if (r.organisation) rows.push(["Holder", r.organisation])
  if (r.name || r.handle) rows.push(["Network", [r.name, r.handle && r.handle !== r.name ? `(${r.handle})` : ""].filter(Boolean).join(" ")])
  if (r.cidrs.length > 0) rows.push(["CIDR", <span key="cidr" className="font-mono text-xs">{r.cidrs.join(", ")}</span>])
  if (r.start && r.end) rows.push(["Range", <span key="range" className="font-mono text-xs">{`${r.start} – ${r.end}`}</span>])
  if (r.type) rows.push(["Type", r.type])
  if (r.country) rows.push(["Country", countryLabel(r.country)])
  if (r.abuseEmail)
    rows.push([
      "Abuse contact",
      <a key="abuse" href={`mailto:${r.abuseEmail}`} className="underline underline-offset-4">
        {r.abuseEmail}
      </a>,
    ])
  if (r.registered) rows.push(["Registered", formatDateTime(r.registered)])
  if (r.lastChanged) rows.push(["Last changed", formatDateTime(r.lastChanged)])
  return (
    <>
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
        {rows.map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="min-w-0 break-words">{value}</dd>
          </div>
        ))}
      </dl>
      <p className="text-xs text-muted-foreground">
        From {r.server ?? "the registry"}, fetched <TimeAgo iso={fetchedAt} now={now} />. Kept for a week.
      </p>
    </>
  )
}
