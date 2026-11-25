import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Pencil, Plus, Tag, Trash2, X } from "lucide-react"
import { useState, type FormEvent, type ReactNode } from "react"
import { Link, useLocation, useParams, useSearchParams } from "react-router"

import { BarList, BarRow } from "@/components/bar-list"
import { DomainsCard } from "@/components/domains-card"
import { HostDNSCard } from "@/pages/dns-pages"
import { HostLink } from "@/components/host"
import { Field, FormActions, FormError, Page, QueryError, SectionSkeleton, Stat, TimeAgo } from "@/components/page"
import { CoverageNote, RemoteIPLink } from "@/components/remote"
import { ReverseDNS } from "@/components/reverse-dns"
import { ServicePort } from "@/components/service-port"
import { EstimateNote } from "@/components/estimate"
import { SortableHead } from "@/components/sortable-head"
import { TrafficChart, type TrafficSeries } from "@/components/traffic-chart"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  fetchDevice,
  fetchDevicesAt,
  setDeviceLabel,
  mergeDevice,
  splitDevice,
  createHostLabel,
  deleteHostLabel,
  fetchHostRemoteIPs,
  fetchHostSummary,
  fetchHostTraffic,
  fetchTop,
  updateHostLabel,
  type HostItem,
  type HostLabel,
  type Device,
  type HostName,
} from "@/lib/api"
import { countryName } from "@/lib/countries"
import { useExporterFilter } from "@/lib/exporter-filter"
import { formatBytes, formatNumber } from "@/lib/format"
import { hostPath, useHostLabels, useHostNames } from "@/lib/hosts"
import { deviceLabelsKey, useDeviceLabels } from "@/lib/devices"
import { useReverseDNS } from "@/lib/reverse-dns"
import { serviceKey, useServiceNames } from "@/lib/services"
import { sortRows, useSort, type Sort } from "@/lib/sort"
import { resolveRange, useTimeRange } from "@/lib/time-range"

const trafficSeries: TrafficSeries<"receivedBps" | "sentBps">[] = [
  { key: "receivedBps", label: "Received", color: "var(--chart-1)" },
  { key: "sentBps", label: "Sent", color: "var(--chart-2)" },
]

/** After a label changes, every Host Name and host grouping on screen may be stale. */
function useLabelInvalidation() {
  const client = useQueryClient()
  return () => {
    for (const key of ["host-labels", "host-names", "top", "host-summary", "host-traffic", "host-remote-ips"]) {
      void client.invalidateQueries({ queryKey: [key] })
    }
  }
}

/**
 * Byte columns keep one width, so the name column takes the spare room and the numbers
 * stay together instead of spreading to the card's edge (#72).
 */
const numberColumn = "w-28"

function BytesCells({ item }: { item: { receivedBytes: number; sentBytes: number; totalBytes: number } }) {
  return (
    <>
      <TableCell className={`${numberColumn} text-right tabular-nums`}>{formatBytes(item.receivedBytes)}</TableCell>
      <TableCell className={`${numberColumn} text-right tabular-nums`}>{formatBytes(item.sentBytes)}</TableCell>
      <TableCell className={`${numberColumn} text-right tabular-nums`}>{formatBytes(item.totalBytes)}</TableCell>
    </>
  )
}

type BytesColumn = "received" | "sent" | "total"

function BytesHeads<K extends string>({ sort, onSort }: { sort: Sort<K | BytesColumn>; onSort: (k: K | BytesColumn) => void }) {
  return (
    <>
      <SortableHead column="received" sort={sort} onSort={onSort} numeric className={numberColumn}>
        Received
      </SortableHead>
      <SortableHead column="sent" sort={sort} onSort={onSort} numeric className={numberColumn}>
        Sent
      </SortableHead>
      <SortableHead column="total" sort={sort} onSort={onSort} numeric className={numberColumn}>
        Total
      </SortableHead>
    </>
  )
}

/** A row's bytes for a BytesColumn. */
function bytesOf(item: { receivedBytes: number; sentBytes: number; totalBytes: number }, column: string) {
  return column === "received" ? item.receivedBytes : column === "sent" ? item.sentBytes : item.totalBytes
}

function DataTable({ head, children }: { head: ReactNode; children: ReactNode }) {
  return (
    <div className="overflow-x-auto rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow>{head}</TableRow>
        </TableHeader>
        <TableBody>{children}</TableBody>
      </Table>
    </div>
  )
}

export function LocalHostsPage() {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const [filter, setFilter] = useState("")
  // By Device unless the page asks for addresses (?by=address).
  const [params, setParams] = useSearchParams()
  const dimension = params.get("by") === "address" ? "hosts" : "devices"
  const setBy = (by: "device" | "address") =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (by === "address") next.set("by", "address")
        else next.delete("by")
        return next
      },
      { replace: true },
    )
  const query = useQuery({
    queryKey: ["top", dimension, preset.value, exporter, "all"],
    queryFn: ({ signal }) => fetchTop(dimension, { ...resolveRange(preset), exporter }, signal, 300),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const items = query.data?.result.items ?? []
  const names = useHostNames(items.map((i) => i.host ?? ""))
  const deviceLabels = useDeviceLabels()
  const [sort, onSort] = useSort<"host" | BytesColumn>({ key: "total", asc: false }, ["host"])
  const needle = filter.trim().toLowerCase()
  const filtered = needle
    ? items.filter(
        (i) =>
          i.host?.toLowerCase().includes(needle) ||
          i.mac?.includes(needle) ||
          (i.mac && deviceLabels[i.mac]?.toLowerCase().includes(needle)) ||
          i.vendor?.toLowerCase().includes(needle) ||
          names[i.host ?? ""]?.name?.toLowerCase().includes(needle),
      )
    : items
  const shown = sortRows(
    filtered,
    (i) => (sort.key === "host" ? ((i.mac && deviceLabels[i.mac]) || names[i.host ?? ""]?.name || i.host) : bytesOf(i, sort.key)),
    sort.asc,
  )

  return (
    <Page>
      <Card>
        <CardHeader>
          <CardTitle>{dimension === "devices" ? "Devices" : "Local Hosts"}</CardTitle>
          <CardDescription>
            The busiest Local Hosts by bytes sent and received, {preset.label.toLowerCase()}, including Internal traffic.{" "}
            {dimension === "devices"
              ? "By Device: the addresses one MAC used count as one, named by the address it used last. Traffic without a known MAC counts by address."
              : "By address: each address on its own."}
          </CardDescription>
          <CardAction className="flex flex-wrap items-center justify-end gap-2">
            <div className="flex rounded-md border p-0.5" role="group" aria-label="List by">
              {(["device", "address"] as const).map((by) => (
                <Button
                  key={by}
                  size="sm"
                  variant={(by === "device") === (dimension === "devices") ? "secondary" : "ghost"}
                  className="h-7"
                  aria-pressed={(by === "device") === (dimension === "devices")}
                  onClick={() => setBy(by)}
                >
                  {by === "device" ? "By Device" : "By address"}
                </Button>
              ))}
            </div>
            <Input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter by name, address or MAC"
              aria-label="Filter Local Hosts"
              className="h-8 w-48 sm:w-64"
            />
          </CardAction>
        </CardHeader>
        <CardContent>
          {query.isPending ? (
            <SectionSkeleton rows={8} />
          ) : query.isError ? (
            <QueryError title="Couldn't load Local Hosts" error={query.error} />
          ) : items.length === 0 ? (
            <p className="text-sm text-muted-foreground">No Flows in this time range yet.</p>
          ) : shown.length === 0 ? (
            <p className="text-sm text-muted-foreground">No Local Host matches “{filter.trim()}”.</p>
          ) : (
            <DataTable
              head={
                <>
                  <SortableHead column="host" sort={sort} onSort={onSort}>
                    Local Host
                  </SortableHead>
                  <BytesHeads sort={sort} onSort={onSort} />
                </>
              }
            >
              {shown.map((it) => (
                <TableRow key={`${it.mac}/${it.host}`}>
                  <TableCell className="max-w-80">
                    <HostLink
                      host={it.host ?? ""}
                      name={names[it.host ?? ""]}
                      exporterHost={it.localIsExporterHost}
                      addresses={it.addresses}
                      device={
                        it.mac ? { mac: it.mac, vendor: it.vendor, privateMac: it.privateMac, label: deviceLabels[it.mac] } : undefined
                      }
                    />
                  </TableCell>
                  <BytesCells item={it} />
                </TableRow>
              ))}
            </DataTable>
          )}
        </CardContent>
      </Card>
      <HostLabelsCard />
    </Page>
  )
}

/** Every Host Label, with a form to add one. */
function HostLabelsCard() {
  const labels = useHostLabels()
  const [adding, setAdding] = useState(false)
  const [sort, onSort] = useSort<"name" | "prefix" | "changed">({ key: "name", asc: true }, ["name", "prefix"])
  const rows = sortRows(
    labels.data?.labels ?? [],
    (l) => (sort.key === "name" ? l.name : sort.key === "prefix" ? l.prefix : new Date(l.updatedAt).getTime()),
    sort.asc,
  )
  return (
    <Card>
      <CardHeader>
        <CardTitle>Host Labels</CardTitle>
        <CardDescription>
          Name a Local Host, or a prefix so that a host's rotating addresses count as one Local Host. A Host Label wins over
          reverse DNS; the longest matching prefix wins over a shorter one.
        </CardDescription>
        {!adding && (
          <CardAction>
            <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
              <Plus />
              Add label
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {adding && <LabelForm onDone={() => setAdding(false)} />}
        {labels.isPending ? (
          <SectionSkeleton rows={3} />
        ) : labels.isError ? (
          <QueryError title="Couldn't load Host Labels" error={labels.error} />
        ) : labels.data.labels.length === 0 ? (
          !adding && <p className="text-sm text-muted-foreground">No Host Labels yet.</p>
        ) : (
          <DataTable
            head={
              <>
                <SortableHead column="name" sort={sort} onSort={onSort}>
                  Name
                </SortableHead>
                <SortableHead column="prefix" sort={sort} onSort={onSort}>
                  Address or prefix
                </SortableHead>
                <SortableHead column="changed" sort={sort} onSort={onSort}>
                  Changed
                </SortableHead>
                <TableHead className="w-0" />
              </>
            }
          >
            {rows.map((l) => (
              <LabelRow key={l.id} label={l} now={labels.dataUpdatedAt} />
            ))}
          </DataTable>
        )}
      </CardContent>
    </Card>
  )
}

function LabelRow({ label, now }: { label: HostLabel; now: number }) {
  const { search } = useLocation()
  const [editing, setEditing] = useState(false)
  const invalidate = useLabelInvalidation()
  const remove = useMutation({ mutationFn: () => deleteHostLabel(label.id), onSuccess: invalidate })
  if (editing) {
    return (
      <TableRow>
        <TableCell colSpan={4}>
          <LabelForm label={label} onDone={() => setEditing(false)} />
        </TableCell>
      </TableRow>
    )
  }
  return (
    <TableRow>
      <TableCell className="max-w-64 truncate" title={label.name}>
        {label.name}
      </TableCell>
      <TableCell>
        <Link to={{ pathname: hostPath(label.prefix), search }} className="font-mono text-ident underline-offset-4 hover:underline">
          {label.prefix}
        </Link>
      </TableCell>
      <TableCell className="text-muted-foreground">
        <TimeAgo iso={label.updatedAt} now={now} />
      </TableCell>
      <TableCell>
        <div className="flex justify-end gap-1">
          <Button size="icon-sm" variant="ghost" aria-label={`Edit ${label.name}`} onClick={() => setEditing(true)}>
            <Pencil />
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Remove ${label.name}`}
            disabled={remove.isPending}
            onClick={() => remove.mutate()}
          >
            <Trash2 />
          </Button>
        </div>
        <FormError error={remove.error} />
      </TableCell>
    </TableRow>
  )
}

/** Adds a Host Label, or changes one. */
function LabelForm({ label, prefix, onDone }: { label?: HostLabel; prefix?: string; onDone: () => void }) {
  const [name, setName] = useState(label?.name ?? "")
  const [target, setTarget] = useState(label?.prefix ?? prefix ?? "")
  const invalidate = useLabelInvalidation()
  const save = useMutation({
    mutationFn: () => {
      const body = { prefix: target.trim(), name: name.trim() }
      return label ? updateHostLabel(label.id, body) : createHostLabel(body)
    },
    onSuccess: () => {
      invalidate()
      onDone()
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate()
  }
  const id = label?.id ?? "new"
  return (
    <form onSubmit={submit} className="flex flex-col gap-4 rounded-lg border p-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={`label-name-${id}`} label="Name">
          <Input
            id={`label-name-${id}`}
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={64}
            placeholder="e.g. Living room TV"
            autoFocus
          />
        </Field>
        <Field
          id={`label-prefix-${id}`}
          label="Address or prefix"
          hint="An address names one host; a prefix such as 2001:db8:1:2::/64 makes its addresses one Local Host."
        >
          <Input
            id={`label-prefix-${id}`}
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            placeholder="192.168.88.20 or fd00::/64"
            className="font-mono"
          />
        </Field>
      </div>
      <FormError error={save.error} />
      <FormActions>
        <Button type="button" size="sm" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" size="sm" disabled={save.isPending || !name.trim() || !target.trim()}>
          {label ? "Save" : "Add label"}
        </Button>
      </FormActions>
    </form>
  )
}

export function LocalHostPage() {
  const host = useParams()["*"] ?? ""
  const names = useHostNames([host])
  return (
    <Page>
      <IdentityCard host={host} name={names[host]} />
      <HostBody host={host} />
    </Page>
  )
}

/** A Device: everything its addresses did, together. */
export function DevicePage() {
  const { mac = "" } = useParams()
  return (
    <Page>
      <DeviceIdentityCard mac={mac.toLowerCase()} />
      <HostBody host={`device:${mac.toLowerCase()}`} />
    </Page>
  )
}

/**
 * What a Local Host, or a Device ("device:<mac>"), did: its traffic, Peers, DNS, domains,
 * ports and where its Remote IPs are.
 */
function HostBody({ host }: { host: string }) {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const range = () => ({ ...resolveRange(preset), exporter })
  const summary = useQuery({
    queryKey: ["host-summary", host, preset.value, exporter],
    queryFn: ({ signal }) => fetchHostSummary(host, range(), signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const traffic = useQuery({
    queryKey: ["host-traffic", host, preset.value, exporter],
    queryFn: ({ signal }) => fetchHostTraffic(host, range(), signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const serviceNames = useServiceNames(
    (summary.data?.result.ports ?? []).map((p) => ({ protocol: p.protocol ?? 0, port: p.servicePort ?? 0 })),
  )
  const label = preset.label.toLowerCase()

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Traffic</CardTitle>
          <CardDescription>What it received and sent, {label}, with Remote IPs and other Local Hosts.</CardDescription>
          <CardAction>
            <ExplorerLink host={host} />
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {summary.isPending ? (
            <SectionSkeleton rows={1} />
          ) : summary.isError ? (
            <QueryError title={`Couldn't load ${host}`} error={summary.error} />
          ) : (
            <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
              <Stat label="Received" value={formatBytes(summary.data.result.receivedBytes)} />
              <Stat label="Sent" value={formatBytes(summary.data.result.sentBytes)} />
              <Stat label="With Remote IPs" value={formatBytes(summary.data.result.remoteBytes)} />
              <Stat label="Internal" value={formatBytes(summary.data.result.internalBytes)} />
              <Stat label="Flows" value={formatNumber(Math.round(summary.data.result.flows))} />
            </div>
          )}
          {traffic.isPending ? (
            <Skeleton className="h-72 w-full" />
          ) : traffic.isError ? (
            <QueryError title="Couldn't load its traffic" error={traffic.error} />
          ) : (
            <TrafficChart
              points={traffic.data.result.points}
              series={trafficSeries}
              stepSeconds={traffic.data.result.stepSeconds}
              rangeMs={preset.ms}
              incompleteFrom={new Date(traffic.data.incompleteFrom).getTime()}
            />
          )}
          {summary.data && (
            <EstimateNote
              estimated={summary.data.result.estimatedBytes}
              total={summary.data.result.receivedBytes + summary.data.result.sentBytes}
            />
          )}
        </CardContent>
      </Card>
      {summary.data && (host.includes("/") || host.startsWith("device:")) && (
        <AddressesCard addresses={summary.data.result.addresses} />
      )}
      {/* Side by side only where a table of addresses, names and three byte columns fits in half. */}
      <div className="grid items-start gap-6 2xl:grid-cols-2">
        <RemotePeersCard host={host} label={label} />
        <LocalPeersCard query={summary} peers={summary.data?.result.localPeers} label={label} />
      </div>
      <HostDNSCard host={host} />
      <div className="grid items-start gap-6 lg:grid-cols-2 3xl:grid-cols-4">
        <DomainsCard host={host} />
        <BarCard
          title="Service Ports"
          description="The services it used or offered."
          query={summary}
          items={summary.data?.result.ports}
          empty="No Flows in this time range."
          label={(it) => (
            <ServicePort
              protocol={it.protocol ?? 0}
              port={it.servicePort ?? 0}
              name={serviceNames[serviceKey(it.protocol ?? 0, it.servicePort ?? 0)]}
            />
          )}
          rowKey={(it) => `${it.protocol}/${it.servicePort}`}
        />
        <BarCard
          title="ASN Owners"
          description="Who announces the Remote IPs it talked to."
          query={summary}
          items={summary.data?.result.asns}
          empty="No traffic with Remote IPs in this time range."
          label={(it) => {
            const text = it.asn ? `AS${it.asn}${it.asnOrg ? ` ${it.asnOrg}` : ""}` : "Unknown"
            return (
              <span className="truncate" title={text}>
                {text}
              </span>
            )
          }}
          rowKey={(it) => String(it.asn ?? 0)}
        />
        <BarCard
          title="Countries"
          description="Where the Remote IPs it talked to are."
          query={summary}
          items={summary.data?.result.countries}
          empty="No traffic with Remote IPs in this time range."
          label={(it) => <span className="truncate">{it.country ? `${countryName(it.country)} (${it.country})` : "Unknown"}</span>}
          rowKey={(it) => it.country ?? ""}
        />
      </div>
    </>
  )
}

/** Opens the Flow Explorer filtered to this Local Host or Device, keeping the time range. */
function ExplorerLink({ host }: { host: string }) {
  const { search } = useLocation()
  const params = new URLSearchParams(search)
  if (host.startsWith("device:")) params.set("device", host.slice("device:".length))
  else params.set("host", host)
  return (
    <Link to={{ pathname: "/flows", search: params.toString() }} className="text-sm underline-offset-4 hover:underline">
      Open in Flow Explorer
    </Link>
  )
}

/** The Local Host's Host Name, where it came from, and its Host Label. */
function IdentityCard({ host, name }: { host: string; name?: HostName }) {
  const { search } = useLocation()
  const [editing, setEditing] = useState(false)
  const invalidate = useLabelInvalidation()
  const own = name?.label && name.label.prefix === host ? name.label : undefined
  const labels = useHostLabels()
  const ownLabel = own && labels.data?.labels.find((l) => l.id === own.id)
  const remove = useMutation({ mutationFn: () => deleteHostLabel(own!.id), onSuccess: invalidate })
  const isPrefix = host.includes("/")

  let origin: ReactNode
  if (!name || name.status === "pending") {
    origin = <ReverseDNS name={name} className="h-4 w-40" />
  } else if (name.source === "label" && name.label && !own) {
    origin = (
      <span>
        Named by the Host Label on{" "}
        <Link to={{ pathname: hostPath(name.label.prefix), search }} className="font-mono underline underline-offset-4">
          {name.label.prefix}
        </Link>
        , which this address is in.
      </span>
    )
  } else if (name.source === "label") {
    origin = <span>Named by a Host Label.</span>
  } else if (name.source === "dns") {
    origin = <span>Named by reverse DNS. A Host Label would win over it.</span>
  } else if (isPrefix) {
    origin = <span>A prefix with no Host Label.</span>
  } else {
    origin = (
      <span>{name.status === "failed" ? "Reverse DNS didn't answer; trying again later." : "No reverse DNS name."}</span>
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex min-w-0 flex-wrap items-center gap-2 text-lg break-all">
          {name?.name ? name.name : <span className="font-mono">{host}</span>}
          {name?.source === "label" && (
            <Badge variant="secondary">
              <Tag />
              Host Label
            </Badge>
          )}
        </CardTitle>
        <CardDescription className="flex flex-col gap-0.5">
          {name?.name && <span className="font-mono text-sm text-foreground">{host}</span>}
          {origin}
          {!host.includes("/") && <DeviceOfAddress address={host} />}
        </CardDescription>
        {!editing && (
          <CardAction className="flex gap-1">
            {/* Icons alone on a phone, so the Host Name keeps the width. */}
            <Button
              size="sm"
              variant="outline"
              onClick={() => setEditing(true)}
              disabled={!!own && !ownLabel}
              aria-label={own ? "Edit label" : "Add label"}
            >
              {own ? <Pencil /> : <Plus />}
              <span className="hidden sm:inline">{own ? "Edit label" : "Add label"}</span>
            </Button>
            {own && (
              <Button size="sm" variant="ghost" onClick={() => remove.mutate()} disabled={remove.isPending} aria-label="Remove label">
                <X />
                <span className="hidden sm:inline">Remove label</span>
              </Button>
            )}
          </CardAction>
        )}
      </CardHeader>
      {(editing || remove.error) && (
        <CardContent>
          {editing && <LabelForm label={ownLabel} prefix={host} onDone={() => setEditing(false)} />}
          <FormError error={remove.error} />
        </CardContent>
      )}
    </Card>
  )
}

function AddressesCard({ addresses }: { addresses: { address: string; receivedBytes: number; sentBytes: number; totalBytes: number }[] }) {
  const names = useHostNames(addresses.map((a) => a.address))
  const [sort, onSort] = useSort<"address" | BytesColumn>({ key: "total", asc: false }, ["address"])
  const rows = sortRows(addresses, (a) => (sort.key === "address" ? a.address : bytesOf(a, sort.key)), sort.asc)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Addresses</CardTitle>
        <CardDescription>The addresses that had traffic in this time range, counted together.</CardDescription>
      </CardHeader>
      <CardContent>
        {addresses.length === 0 ? (
          <p className="text-sm text-muted-foreground">No address had traffic in this time range.</p>
        ) : (
          <DataTable
            head={
              <>
                <SortableHead column="address" sort={sort} onSort={onSort}>
                  Address
                </SortableHead>
                <BytesHeads sort={sort} onSort={onSort} />
              </>
            }
          >
            {rows.map((a) => (
              <TableRow key={a.address}>
                <TableCell>
                  {/* The prefix's label names every address in it; reverse DNS may know more. */}
                  <HostLink host={a.address} name={names[a.address]?.source === "dns" ? names[a.address] : undefined} />
                </TableCell>
                <BytesCells item={a} />
              </TableRow>
            ))}
          </DataTable>
        )}
      </CardContent>
    </Card>
  )
}

type SummaryQuery = { isPending: boolean; isError: boolean; error: Error | null }

function LocalPeersCard({ query, peers = [], label }: { query: SummaryQuery; peers?: HostItem[]; label: string }) {
  const names = useHostNames(peers.map((p) => p.host ?? ""))
  const [sort, onSort] = useSort<"host" | BytesColumn>({ key: "total", asc: false }, ["host"])
  const rows = sortRows(peers, (p) => (sort.key === "host" ? (names[p.host ?? ""]?.name ?? p.host) : bytesOf(p, sort.key)), sort.asc)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Local Peers</CardTitle>
        <CardDescription>Other Local Hosts it talked to, {label}.</CardDescription>
      </CardHeader>
      <CardContent>
        {query.isPending ? (
          <SectionSkeleton rows={4} />
        ) : query.isError ? (
          <QueryError title="Couldn't load its Local Peers" error={query.error!} />
        ) : peers.length === 0 ? (
          <p className="text-sm text-muted-foreground">No Internal traffic in this time range.</p>
        ) : (
          <DataTable
            head={
              <>
                <SortableHead column="host" sort={sort} onSort={onSort}>
                  Local Host
                </SortableHead>
                <BytesHeads sort={sort} onSort={onSort} />
              </>
            }
          >
            {rows.map((it) => (
              <TableRow key={it.host}>
                <TableCell className="max-w-64">
                  <HostLink host={it.host ?? ""} name={names[it.host ?? ""]} addresses={it.addresses} />
                </TableCell>
                <BytesCells item={it} />
              </TableRow>
            ))}
          </DataTable>
        )}
      </CardContent>
    </Card>
  )
}

/** A ranked list from the Local Host's summary, as bars split into received and sent. */
function BarCard({
  title,
  description,
  query,
  items,
  empty,
  label,
  rowKey,
}: {
  title: string
  description: string
  query: SummaryQuery
  items?: HostItem[]
  empty: string
  label: (item: HostItem) => ReactNode
  rowKey: (item: HostItem) => string
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        {query.isPending ? (
          <SectionSkeleton rows={4} />
        ) : query.isError ? (
          <QueryError title={`Couldn't load ${title}`} error={query.error!} />
        ) : !items || items.length === 0 ? (
          <p className="text-sm text-muted-foreground">{empty}</p>
        ) : (
          <BarList>
            {items.map((it) => (
              <BarRow
                key={rowKey(it)}
                label={label(it)}
                total={it.totalBytes}
                received={it.receivedBytes}
                sent={it.sentBytes}
                max={items[0].totalBytes}
                estimated={it.estimatedBytes}
              />
            ))}
          </BarList>
        )}
      </CardContent>
    </Card>
  )
}

function RemotePeersCard({ host, label }: { host: string; label: string }) {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  // Byte columns rank on the server (the top 20 by that column); Remote IP sorts those.
  const [sort, onSort] = useSort<"ip" | BytesColumn>({ key: "total", asc: false }, ["ip"])
  const server = sort.key === "ip" ? { by: "total" as BytesColumn, asc: false } : { by: sort.key, asc: sort.asc }
  const query = useQuery({
    queryKey: ["host-remote-ips", host, preset.value, exporter, server.by, server.asc],
    queryFn: ({ signal }) => fetchHostRemoteIPs(host, { ...resolveRange(preset), exporter }, server.by, server.asc, signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const names = useReverseDNS(query.data?.result.items.map((i) => i.remoteIp) ?? [])
  const fetched = query.data?.result.items ?? []
  const rows = sort.key === "ip" ? sortRows(fetched, (i) => i.remoteIp, sort.asc) : fetched
  return (
    <Card>
      <CardHeader>
        <CardTitle>Remote IPs</CardTitle>
        <CardDescription>The busiest Remote IPs it talked to, {label}.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {query.isPending ? (
          <SectionSkeleton rows={4} />
        ) : query.isError ? (
          <QueryError title="Couldn't load its Remote IPs" error={query.error} />
        ) : query.data.result.items.length === 0 ? (
          <p className="text-sm text-muted-foreground">No traffic with Remote IPs in this time range.</p>
        ) : (
          <DataTable
            head={
              <>
                <SortableHead column="ip" sort={sort} onSort={onSort}>
                  Remote IP
                </SortableHead>
                <BytesHeads sort={sort} onSort={onSort} />
              </>
            }
          >
            {rows.map((it) => (
              <TableRow key={it.remoteIp}>
                <TableCell className="max-w-64">
                  <div className="flex flex-col gap-0.5">
                    <RemoteIPLink ip={it.remoteIp} />
                    <ReverseDNS name={names[it.remoteIp]} />
                  </div>
                </TableCell>
                <BytesCells item={it} />
              </TableRow>
            ))}
          </DataTable>
        )}
        {query.data && <CoverageNote coverage={query.data.result} rangeFrom={resolveRange(preset).from} />}
      </CardContent>
    </Card>
  )
}

/** A Device's identity: its name, MAC, maker, and every address it used. */
function DeviceIdentityCard({ mac }: { mac: string }) {
  const { search } = useLocation()
  const device = useQuery({ queryKey: ["device", mac], queryFn: ({ signal }) => fetchDevice(mac, signal), refetchInterval: 60_000 })
  const addresses = device.data?.addresses ?? []
  const names = useHostNames(addresses.map((a) => a.address))
  const current = addresses[0]?.address
  const label = useDeviceLabels()[mac]
  const name = current ? names[current]?.name : undefined
  const title = label ?? name ?? (device.data?.vendor ? `${device.data.vendor} device` : mac)
  const [editing, setEditing] = useState(false)
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2">
          {title}
          {device.data?.privateMac && (
            <Badge variant="outline" title="A randomized MAC address: it may change, so this Device may show up again under another one">
              Private address
            </Badge>
          )}
        </CardTitle>
        <CardDescription className="flex flex-wrap gap-x-3">
          <span className="font-mono text-ident">{mac}</span>
          {device.data?.vendor && <span>{device.data.vendor}</span>}
          {device.data?.privateMac && <span>No vendor: randomized MAC addresses carry none.</span>}
        </CardDescription>
        {!editing && !device.data?.router && (
          <CardAction>
            <Button size="sm" variant="outline" onClick={() => setEditing(true)} aria-label={label ? "Rename" : "Name it"}>
              {label ? <Pencil /> : <Plus />}
              <span className="hidden sm:inline">{label ? "Rename" : "Name it"}</span>
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {editing && <DeviceLabelForm mac={mac} label={label} onDone={() => setEditing(false)} />}
        {device.isPending ? (
          <SectionSkeleton rows={2} />
        ) : device.isError ? (
          <QueryError title={`Couldn't load ${mac}`} error={device.error} />
        ) : device.data.router ? (
          <p className="text-sm text-muted-foreground">
            This MAC address is a router's: Flows are never counted for it, but for the hosts behind it.
          </p>
        ) : addresses.length === 0 ? (
          <p className="text-sm text-muted-foreground">No Flows from this Device have been stored.</p>
        ) : (
          <DataTable
            head={
              <>
                <TableHead>Address</TableHead>
                <TableHead>First seen</TableHead>
                <TableHead>Last seen</TableHead>
              </>
            }
          >
            {addresses.map((a) => (
              <TableRow key={a.address}>
                <TableCell className="max-w-72">
                  <HostLink host={a.address} name={names[a.address]} />
                </TableCell>
                <TableCell>
                  <TimeAgo iso={a.firstSeen} now={device.dataUpdatedAt} />
                </TableCell>
                <TableCell>
                  <TimeAgo iso={a.lastSeen} now={device.dataUpdatedAt} />
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        )}
        {device.data?.mergedInto && (
          <p className="text-sm">
            This MAC address now counts as the Device{" "}
            <Link to={{ pathname: `/devices/${device.data.mergedInto}`, search }} className="font-mono underline underline-offset-4">
              {device.data.mergedInto}
            </Link>
            .
          </p>
        )}
        {device.data && !device.data.router && !device.data.mergedInto && (
          <MergedMACs mac={mac} merged={device.data.merged ?? []} now={device.dataUpdatedAt} />
        )}
        {label ? (
          <p className="text-xs text-muted-foreground">Named by you; the name follows the Device to every address it uses.</p>
        ) : name && (
          <p className="text-xs text-muted-foreground">
            Named after its current address,{" "}
            <Link to={{ pathname: hostPath(current!), search }} className="font-mono underline underline-offset-4">
              {current}
            </Link>
            .
          </p>
        )}
      </CardContent>
    </Card>
  )
}

/** On a single address's page: the Device that uses it, if Flowmon knows one. */
function DeviceOfAddress({ address }: { address: string }) {
  const { search } = useLocation()
  const query = useQuery({ queryKey: ["devices-at", address], queryFn: ({ signal }) => fetchDevicesAt(address, signal) })
  const d = query.data?.devices[0]
  if (!d) return null
  return (
    <span className="text-sm">
      Device:{" "}
      <Link to={{ pathname: `/devices/${d.mac}`, search }} className="font-mono text-ident underline underline-offset-4">
        {d.mac}
      </Link>
      {d.vendor && <span className="text-muted-foreground"> · {d.vendor}</span>}
      {query.data && query.data.devices.length > 1 && (
        <span className="text-muted-foreground"> · {query.data.devices.length - 1} more used it before</span>
      )}
    </span>
  )
}

/** Names a Device, or removes its name. The name is kept in the App Store, by MAC. */
function DeviceLabelForm({ mac, label, onDone }: { mac: string; label?: string; onDone: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(label ?? "")
  const save = useMutation({
    mutationFn: (next: string) => setDeviceLabel(mac, next),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: deviceLabelsKey })
      onDone()
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(name.trim())
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-3 rounded-lg border p-4">
      <Field id={`device-name-${mac}`} label="Name" hint="Shown instead of its address's name or vendor, at every address it uses.">
        <Input
          id={`device-name-${mac}`}
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={64}
          placeholder="e.g. Living room TV"
          autoFocus
        />
      </Field>
      <FormError error={save.error} />
      <FormActions>
        {label && (
          <Button type="button" variant="ghost" className="mr-auto" disabled={save.isPending} onClick={() => save.mutate("")}>
            <X />
            Remove name
          </Button>
        )}
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={save.isPending || !name.trim()}>
          Save
        </Button>
      </FormActions>
    </form>
  )
}

/**
 * The MAC addresses counted as this Device, with Undo, and a form to merge another one in:
 * Private Addresses rotate, so one device can show up under several.
 */
function MergedMACs({ mac, merged, now }: { mac: string; merged: NonNullable<Device["merged"]>; now: number }) {
  const queryClient = useQueryClient()
  const [other, setOther] = useState("")
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["device"] })
    void queryClient.invalidateQueries({ queryKey: ["top"] })
  }
  const merge = useMutation({
    mutationFn: () => mergeDevice(other.trim().toLowerCase(), mac),
    onSuccess: () => {
      setOther("")
      refresh()
    },
  })
  const split = useMutation({ mutationFn: (m: string) => splitDevice(m), onSuccess: refresh })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    merge.mutate()
  }
  return (
    <div className="flex flex-col gap-3">
      {merged.length > 0 && (
        <DataTable
          head={
            <>
              <TableHead>Also counted as this Device</TableHead>
              <TableHead>Why</TableHead>
              <TableHead>Since</TableHead>
              <TableHead />
            </>
          }
        >
          {merged.map((m) => (
            <TableRow key={m.mac}>
              <TableCell className="font-mono text-ident">{m.mac}</TableCell>
              <TableCell className="text-sm">
                {m.reason === "auto" ? "A new Private Address with the same name, after this one went quiet" : "Merged by hand"}
              </TableCell>
              <TableCell>
                <TimeAgo iso={m.mergedAt} now={now} />
              </TableCell>
              <TableCell className="text-right">
                <Button size="sm" variant="ghost" disabled={split.isPending} onClick={() => split.mutate(m.mac)}>
                  Undo
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </DataTable>
      )}
      <form onSubmit={submit} className="flex flex-wrap items-end gap-2">
        <Field id={`merge-${mac}`} label="Merge another MAC address into this Device">
          <Input
            id={`merge-${mac}`}
            value={other}
            onChange={(e) => setOther(e.target.value)}
            placeholder="aa:bb:cc:dd:ee:ff"
            className="w-56 font-mono"
          />
        </Field>
        <Button type="submit" size="sm" variant="outline" disabled={merge.isPending || !other.trim()}>
          Merge
        </Button>
      </form>
      <p className="-mt-1 text-xs text-muted-foreground">For one device that shows up under several MACs: Private Addresses rotate.</p>
      <FormError error={merge.error ?? split.error} />
    </div>
  )
}
