import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Pencil, Plus, Tag, Trash2, X } from "lucide-react"
import { useState, type FormEvent, type ReactNode } from "react"
import { Link, useLocation, useParams } from "react-router"

import { BarList, BarRow } from "@/components/bar-list"
import { HostLink } from "@/components/host"
import { Field, FormActions, FormError, Page, QueryError, SectionSkeleton, Stat, TimeAgo } from "@/components/page"
import { CoverageNote, RemoteIPLink } from "@/components/remote"
import { ReverseDNS } from "@/components/reverse-dns"
import { TrafficChart, type TrafficSeries } from "@/components/traffic-chart"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  createHostLabel,
  deleteHostLabel,
  fetchHostRemoteIPs,
  fetchHostSummary,
  fetchHostTraffic,
  fetchTop,
  updateHostLabel,
  type HostItem,
  type HostLabel,
  type HostName,
} from "@/lib/api"
import { countryName } from "@/lib/countries"
import { useExporterFilter } from "@/lib/exporter-filter"
import { formatBytes, formatNumber, protocolName } from "@/lib/format"
import { hostPath, useHostLabels, useHostNames } from "@/lib/hosts"
import { useReverseDNS } from "@/lib/reverse-dns"
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

function BytesCells({ item }: { item: { receivedBytes: number; sentBytes: number; totalBytes: number } }) {
  return (
    <>
      <TableCell className="text-right tabular-nums">{formatBytes(item.receivedBytes)}</TableCell>
      <TableCell className="text-right tabular-nums">{formatBytes(item.sentBytes)}</TableCell>
      <TableCell className="text-right tabular-nums">{formatBytes(item.totalBytes)}</TableCell>
    </>
  )
}

function BytesHeads() {
  return (
    <>
      <TableHead className="text-right">Received</TableHead>
      <TableHead className="text-right">Sent</TableHead>
      <TableHead className="text-right">Total</TableHead>
    </>
  )
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
  const query = useQuery({
    queryKey: ["top", "hosts", preset.value, exporter, "all"],
    queryFn: ({ signal }) => fetchTop("hosts", { ...resolveRange(preset), exporter }, signal, 300),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const items = query.data?.result.items ?? []
  const names = useHostNames(items.map((i) => i.host ?? ""))
  const needle = filter.trim().toLowerCase()
  const shown = needle
    ? items.filter((i) => i.host?.toLowerCase().includes(needle) || names[i.host ?? ""]?.name?.toLowerCase().includes(needle))
    : items

  return (
    <Page>
      <Card>
        <CardHeader>
          <CardTitle>Local Hosts</CardTitle>
          <CardDescription>
            The busiest Local Hosts by bytes sent and received, {preset.label.toLowerCase()}, including Internal traffic.
          </CardDescription>
          <CardAction>
            <Input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter by name or address"
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
                  <TableHead>Local Host</TableHead>
                  <BytesHeads />
                </>
              }
            >
              {shown.map((it) => (
                <TableRow key={it.host}>
                  <TableCell className="max-w-80">
                    <HostLink host={it.host ?? ""} name={names[it.host ?? ""]} exporterHost={it.localIsExporterHost} addresses={it.addresses} />
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{formatBytes(it.receivedBytes)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatBytes(it.sentBytes)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatBytes(it.totalBytes)}</TableCell>
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
                <TableHead>Name</TableHead>
                <TableHead>Address or prefix</TableHead>
                <TableHead>Changed</TableHead>
                <TableHead className="w-0" />
              </>
            }
          >
            {labels.data.labels.map((l) => (
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
        <Link to={{ pathname: hostPath(label.prefix), search }} className="font-mono text-xs underline-offset-4 hover:underline">
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
  const names = useHostNames([host])
  const label = preset.label.toLowerCase()

  return (
    <Page>
      <IdentityCard host={host} name={names[host]} />
      <Card>
        <CardHeader>
          <CardTitle>Traffic</CardTitle>
          <CardDescription>What it received and sent, {label}, with Remote IPs and other Local Hosts.</CardDescription>
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
        </CardContent>
      </Card>
      {summary.data && host.includes("/") && <AddressesCard addresses={summary.data.result.addresses} />}
      <div className="grid items-start gap-6 lg:grid-cols-2">
        <RemotePeersCard host={host} label={label} />
        <LocalPeersCard query={summary} peers={summary.data?.result.localPeers} label={label} />
      </div>
      <div className="grid items-start gap-6 lg:grid-cols-3">
        <BarCard
          title="Service Ports"
          description="The services it used or offered."
          query={summary}
          items={summary.data?.result.ports}
          empty="No Flows in this time range."
          label={(it) => (
            <span className="truncate font-mono text-xs">
              {it.servicePort
                ? `${it.servicePort}/${protocolName(it.protocol ?? 0).toLowerCase()}`
                : `${protocolName(it.protocol ?? 0)} (no port)`}
            </span>
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
    </Page>
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
  return (
    <Card>
      <CardHeader>
        <CardTitle>Addresses</CardTitle>
        <CardDescription>The addresses in this prefix that had traffic, counted as one Local Host.</CardDescription>
      </CardHeader>
      <CardContent>
        {addresses.length === 0 ? (
          <p className="text-sm text-muted-foreground">No address in this prefix had traffic in this time range.</p>
        ) : (
          <DataTable
            head={
              <>
                <TableHead>Address</TableHead>
                <BytesHeads />
              </>
            }
          >
            {addresses.map((a) => (
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
                <TableHead>Local Host</TableHead>
                <BytesHeads />
              </>
            }
          >
            {peers.map((it) => (
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
  const query = useQuery({
    queryKey: ["host-remote-ips", host, preset.value, exporter],
    queryFn: ({ signal }) => fetchHostRemoteIPs(host, { ...resolveRange(preset), exporter }, signal),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous,
  })
  const names = useReverseDNS(query.data?.result.items.map((i) => i.remoteIp) ?? [])
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
                <TableHead>Remote IP</TableHead>
                <BytesHeads />
              </>
            }
          >
            {query.data.result.items.map((it) => (
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
