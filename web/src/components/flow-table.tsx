import { useQuery } from "@tanstack/react-query"
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
  type OnChangeFn,
  type SortingState,
} from "@tanstack/react-table"
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react"
import type { ReactNode } from "react"

import { HostLink } from "@/components/host"
import { QueryError, SectionSkeleton } from "@/components/page"
import { RemoteIPLink } from "@/components/remote"
import { ServicePort } from "@/components/service-port"
import { Badge } from "@/components/ui/badge"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { EstimateMark } from "@/components/estimate"
import { fetchFlow, type Direction, type FlowDetail, type FlowItem, type HostName, type ServiceName } from "@/lib/api"
import { countryName } from "@/lib/countries"
import { directionInfo } from "@/lib/flows"
import { formatBytes, formatDateTime, formatNumber, protocolName } from "@/lib/format"
import { useHostNames } from "@/lib/hosts"
import { serviceKey, useServiceNames } from "@/lib/services"

function DirectionLabel({ direction }: { direction: Direction }) {
  const d = directionInfo[direction]
  return (
    <span className="flex items-center gap-1">
      <d.icon className={`size-3.5 ${d.className}`} />
      {d.label}
    </span>
  )
}

function formatDuration(start: string, end: string) {
  const ms = new Date(end).getTime() - new Date(start).getTime()
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`
  return `${Math.floor(ms / 60_000)} m ${Math.round((ms % 60_000) / 1000)} s`
}

function Endpoint({ address, port, children }: { address?: string; port: number; children?: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col">
      {children ?? <span className="truncate font-mono text-xs">{address}</span>}
      {port > 0 && <span className="font-mono text-xs text-muted-foreground">port {port}</span>}
    </div>
  )
}

type Lookups = { hosts: Record<string, HostName>; services: Record<string, ServiceName> }

/** The table's columns; ids double as the API's sort names where they sort. */
function columns({ hosts, services }: Lookups): ColumnDef<FlowItem>[] {
  return [
    {
      id: "end",
      accessorFn: (f) => f.end,
      header: "Ended",
      cell: ({ row }) => <span className="tabular-nums whitespace-nowrap">{formatDateTime(row.original.end)}</span>,
    },
    { id: "direction", header: "Direction", enableSorting: false, cell: ({ row }) => <DirectionLabel direction={row.original.direction} /> },
    {
      id: "local",
      header: "Local Host",
      enableSorting: false,
      cell: ({ row }) => {
        const f = row.original
        if (!f.localHost) return <Endpoint address={f.src} port={f.srcPort} />
        return (
          <Endpoint port={f.localPort}>
            <HostLink host={f.localHost} name={hosts[f.localHost]} exporterHost={f.localIsExporterHost} />
          </Endpoint>
        )
      },
    },
    {
      id: "peer",
      header: "Peer",
      enableSorting: false,
      cell: ({ row }) => {
        const f = row.original
        if (!f.peer) return <Endpoint address={f.dst} port={f.dstPort} />
        return (
          <Endpoint port={f.peerPort}>
            {f.direction === "internal" ? <HostLink host={f.peer} name={hosts[f.peer]} /> : <RemoteIPLink ip={f.peer} />}
          </Endpoint>
        )
      },
    },
    {
      id: "service",
      header: "Service",
      enableSorting: false,
      cell: ({ row }) => {
        const f = row.original
        return <ServicePort protocol={f.protocol} port={f.servicePort} name={services[serviceKey(f.protocol, f.servicePort)]} />
      },
    },
    {
      id: "bytes",
      accessorFn: (f) => f.bytes,
      header: "Bytes",
      meta: { numeric: true },
      cell: ({ row }) => <Sampled rate={row.original.samplingRate}>{formatBytes(row.original.bytes)}</Sampled>,
    },
    {
      id: "packets",
      accessorFn: (f) => f.packets,
      header: "Packets",
      meta: { numeric: true },
      cell: ({ row }) => <Sampled rate={row.original.samplingRate}>{formatNumber(row.original.packets)}</Sampled>,
    },
    {
      id: "duration",
      accessorFn: (f) => new Date(f.end).getTime() - new Date(f.start).getTime(),
      header: "Duration",
      meta: { numeric: true },
      cell: ({ row }) => formatDuration(row.original.start, row.original.end),
    },
    {
      id: "network",
      header: "Remote network",
      enableSorting: false,
      cell: ({ row }) => {
        const f = row.original
        if (f.direction !== "inbound" && f.direction !== "outbound") return null
        const label = [f.country, f.asn ? `AS${f.asn}${f.asnOrg ? ` ${f.asnOrg}` : ""}` : ""].filter(Boolean).join(" · ")
        return (
          <span className="block max-w-48 truncate" title={f.country ? `${countryName(f.country)}${label ? ` · ${label}` : ""}` : label}>
            {label || "Unknown"}
          </span>
        )
      },
    },
    {
      id: "exporter",
      header: "Exporter",
      enableSorting: false,
      cell: ({ row }) => <span className="font-mono text-xs">{row.original.exporter}</span>,
    },
  ]
}

/**
 * Raw Flows as a table. With sorting and onSortingChange the sortable headers sort on the
 * server; clicking a row calls onSelect.
 */
export function FlowTable({
  items,
  sorting,
  onSortingChange,
  onSelect,
  hidden = [],
}: {
  items: FlowItem[]
  sorting?: SortingState
  onSortingChange?: OnChangeFn<SortingState>
  onSelect?: (item: FlowItem) => void
  /** Column ids to leave out, e.g. "peer" on a Remote IP's page. */
  hidden?: string[]
}) {
  const hosts = useHostNames(items.flatMap((f) => [f.localHost ?? "", f.direction === "internal" ? (f.peer ?? "") : ""]))
  const services = useServiceNames(items.map((f) => ({ protocol: f.protocol, port: f.servicePort })))
  // TanStack Table's API can't be memoized by the React Compiler, which therefore leaves
  // this component alone; that is fine for one table per page.
  // oxlint-disable-next-line react/incompatible-library
  const table = useReactTable({
    data: items,
    columns: columns({ hosts, services }),
    getCoreRowModel: getCoreRowModel(),
    getRowId: (f, i) => `${f.id}/${i}`,
    manualSorting: true,
    enableSorting: !!onSortingChange,
    enableSortingRemoval: false,
    sortDescFirst: true,
    state: { sorting: sorting ?? [], columnVisibility: Object.fromEntries(hidden.map((id) => [id, false])) },
    onSortingChange,
  })

  return (
    <div className="overflow-x-auto rounded-lg border">
      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((group) => (
            <TableRow key={group.id}>
              {group.headers.map((header) => {
                const numeric = (header.column.columnDef.meta as { numeric?: boolean } | undefined)?.numeric
                const sorted = header.column.getIsSorted()
                const label = flexRender(header.column.columnDef.header, header.getContext())
                return (
                  <TableHead key={header.id} className={numeric ? "text-right" : undefined}>
                    {header.column.getCanSort() ? (
                      <button
                        type="button"
                        onClick={header.column.getToggleSortingHandler()}
                        className={`inline-flex items-center gap-1 hover:text-foreground ${numeric ? "flex-row-reverse" : ""}`}
                        aria-label={`Sort by ${String(header.column.columnDef.header)}`}
                      >
                        {label}
                        {sorted === "asc" ? (
                          <ArrowUp className="size-3.5" />
                        ) : sorted === "desc" ? (
                          <ArrowDown className="size-3.5" />
                        ) : (
                          <ChevronsUpDown className="size-3.5 text-muted-foreground" />
                        )}
                      </button>
                    ) : (
                      label
                    )}
                  </TableHead>
                )
              })}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {table.getRowModel().rows.map((row) => (
            <TableRow
              key={row.id}
              className={onSelect ? "cursor-pointer" : undefined}
              onClick={(e) => {
                // Links inside a row open their own page.
                if (!(e.target as HTMLElement).closest("a")) onSelect?.(row.original)
              }}
            >
              {row.getVisibleCells().map((cell) => {
                const numeric = (cell.column.columnDef.meta as { numeric?: boolean } | undefined)?.numeric
                return (
                  <TableCell key={cell.id} className={numeric ? "text-right tabular-nums" : undefined}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </TableCell>
                )
              })}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

/** Everything stored about one Flow, in a side panel. */
export function FlowDetailSheet({ id, onClose }: { id: string | null; onClose: () => void }) {
  const query = useQuery({
    queryKey: ["flow", id],
    queryFn: ({ signal }) => fetchFlow(id!, signal),
    enabled: !!id,
    staleTime: Infinity,
    retry: false,
  })
  return (
    <Sheet open={!!id} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-2xl data-[side=right]:sm:max-w-2xl">
        <SheetHeader>
          <SheetTitle>Flow</SheetTitle>
          <SheetDescription>Everything stored about it, and every field as the Exporter sent it.</SheetDescription>
        </SheetHeader>
        <div className="flex flex-col gap-6 px-4 pb-6">
          {query.isPending ? (
            <SectionSkeleton rows={6} />
          ) : query.isError ? (
            <QueryError title="Couldn't load the Flow" error={query.error} />
          ) : (
            <FlowDetailView d={query.data} />
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

/** A count that is an estimate when the Flow was sampled. */
function Sampled({ rate, children }: { rate?: number; children: ReactNode }) {
  if (!rate) return children
  return (
    <span title={`Sampled 1 in ${formatNumber(rate)} packets: an estimate`}>
      <EstimateMark title={`Sampled 1 in ${formatNumber(rate)} packets: an estimate`} /> {children}
    </span>
  )
}

function FlowDetailView({ d }: { d: FlowDetail }) {
  const rows: [string, ReactNode][] = [
    ["Direction", <DirectionLabel key="d" direction={d.direction} />],
    ["Start", formatDateTime(d.start) + (d.timeEstimated ? " (estimated)" : "")],
    ["End", formatDateTime(d.end)],
    ["Received", formatDateTime(d.received)],
    ["Exported", formatDateTime(d.exportTime)],
    ["Source", <span key="s" className="font-mono text-xs">{`${d.src}:${d.srcPort}`}</span>],
    ["Destination", <span key="t" className="font-mono text-xs">{`${d.dst}:${d.dstPort}`}</span>],
  ]
  if (d.postNatSrc || d.postNatDst) {
    rows.push([
      "After NAT",
      <span key="n" className="font-mono text-xs">{`${d.postNatSrc ?? d.src}:${d.postNatSrcPort ?? d.srcPort} → ${d.postNatDst ?? d.dst}:${d.postNatDstPort ?? d.dstPort}`}</span>,
    ])
  }
  rows.push(
    ["Protocol", protocolName(d.protocol)],
    ["Bytes", `${d.samplingRate ? "≈ " : ""}${formatBytes(d.bytes)} (${formatNumber(d.bytes)})`],
    ["Packets", `${d.samplingRate ? "≈ " : ""}${formatNumber(d.packets)}`],
    ...(d.samplingRate
      ? ([
          [
            "Sampling",
            `1 in ${formatNumber(d.samplingRate)} packets. The Exporter counted ${formatNumber(Math.round(d.bytes / d.samplingRate))} bytes in ${formatNumber(Math.round(d.packets / d.samplingRate))} packets; the counts above are scaled up to estimate the real traffic.`,
          ],
        ] as [string, ReactNode][])
      : []),
    ...(d.biflow
      ? ([
          [
            "Biflow",
            `The ${d.biflow} direction of a biflow record (RFC 5103), which Flowmon stores as two Flows. The fields below are the whole record's.`,
          ],
        ] as [string, ReactNode][])
      : []),
    ["Interfaces", `in ${d.ingressInterface || "–"}, out ${d.egressInterface || "–"}`],
    ["Exporter", <span key="e" className="font-mono text-xs">{`${d.exporter} (domain ${d.observationDomain})`}</span>],
  )
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
      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-medium">Fields</h3>
        {d.fieldsError ? (
          <p className="text-sm text-destructive">The stored fields can't be read: {d.fieldsError}</p>
        ) : (
          <div className="overflow-x-auto rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Element</TableHead>
                  <TableHead>Value</TableHead>
                  <TableHead>Raw</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.fields.map((f, i) => (
                  <TableRow key={`${f.enterprise ?? 0}/${f.id}/${i}`}>
                    <TableCell>
                      <div className="flex flex-col">
                        <span className="font-mono text-xs">{f.name}</span>
                        <span className="text-xs text-muted-foreground">
                          {f.enterprise ? `enterprise ${f.enterprise}, ` : ""}id {f.id}
                          {f.type ? `, ${f.type}` : ""}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="max-w-56 font-mono text-xs break-all whitespace-normal">
                      {f.value}
                      {!f.known && (
                        <Badge variant="outline" className="ml-2">
                          raw
                        </Badge>
                      )}
                      {f.error && <p className="font-sans text-destructive">{f.error}</p>}
                    </TableCell>
                    <TableCell className="max-w-40 font-mono text-xs break-all whitespace-normal text-muted-foreground">{f.raw}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </>
  )
}
