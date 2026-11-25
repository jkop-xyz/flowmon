import { useQuery } from "@tanstack/react-query"
import type { SortingState } from "@tanstack/react-table"
import { ChevronLeft, ChevronRight, X } from "lucide-react"
import { useState, type FormEvent } from "react"
import { useSearchParams } from "react-router"

import { FlowDetailSheet, FlowTable } from "@/components/flow-table"
import { Field, Page, QueryError, SectionSkeleton } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { fetchExporterOptions, fetchFlows, type Direction, type FlowFilters, type FlowPage, type FlowSort } from "@/lib/api"
import { useExporterFilter } from "@/lib/exporter-filter"
import { directionInfo } from "@/lib/flows"
import { formatDateTime, formatNumber } from "@/lib/format"
import { resolveRange, useTimeRange } from "@/lib/time-range"

const filterKeys = ["host", "device", "remote", "domain", "asn", "country", "protocol", "port", "direction"] as const satisfies (keyof FlowFilters)[]
const sorts: FlowSort[] = ["end", "start", "bytes", "packets", "duration"]
const pageSizes = [50, 100, 200, 500]
const directions: Direction[] = ["inbound", "outbound", "internal", "transit", "uncounted"]
const ANY = "any"
const protocols: [string, string][] = [
  ["6", "TCP"],
  ["17", "UDP"],
  ["1", "ICMP"],
  ["58", "ICMPv6"],
  ["47", "GRE"],
  ["50", "ESP"],
  ["132", "SCTP"],
]

/**
 * The Flow Explorer's state, kept in the URL next to the time range and Exporter, so a
 * filtered view can be linked to and survives a reload.
 */
function useExplorerState() {
  const [params, setParams] = useSearchParams()
  const filters: FlowFilters = Object.fromEntries(filterKeys.map((k) => [k, params.get(k) ?? ""]))
  const sortParam = params.get("sort") as FlowSort | null
  const sort: FlowSort = sortParam && sorts.includes(sortParam) ? sortParam : "end"
  const asc = params.get("order") === "asc"
  const size = pageSizes.includes(Number(params.get("size"))) ? Number(params.get("size")) : 100
  const page = Math.max(0, Number(params.get("page")) || 0)
  const update = (changes: Record<string, string | number | undefined>) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        for (const [k, v] of Object.entries(changes)) {
          if (v === undefined || v === "" || (k === "page" && v === 0)) next.delete(k)
          else next.set(k, String(v))
        }
        return next
      },
      { replace: true },
    )
  return { filters, sort, asc, size, page, update }
}

export function FlowExplorerPage() {
  const { preset } = useTimeRange()
  const { exporter } = useExporterFilter()
  const exporterOptions = useQuery({ queryKey: ["exporter-options"], queryFn: ({ signal }) => fetchExporterOptions(signal) })
  // The Exporter column says nothing when one is picked, or when there is only one.
  const exporterColumnHidden = exporter !== "" || (exporterOptions.data?.length ?? 0) <= 1
  const { filters, sort, asc, size, page, update } = useExplorerState()
  const [params] = useSearchParams()
  const selected = params.get("flow")
  const query = useQuery({
    queryKey: ["flows", "explorer", preset.value, exporter, filters, sort, asc, size, page],
    queryFn: ({ signal }) =>
      fetchFlows({ ...resolveRange(preset), exporter }, filters, { sort, asc, limit: size, offset: page * size }, signal),
    placeholderData: (previous) => previous,
  })
  const sorting: SortingState = [{ id: sort, desc: !asc }]
  const total = query.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / size))
  const rangeFrom = resolveRange(preset).from
  const truncated = query.data?.rawFrom && new Date(query.data.rawFrom) > rangeFrom

  return (
    <Page className="max-w-none">
      <Card>
        <CardHeader>
          <CardTitle>Flow Explorer</CardTitle>
          <CardDescription>
            Raw Flows, {preset.label.toLowerCase()}. Filter, sort by a column, and click a Flow for every field the Exporter
            sent.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <FilterForm key={filterKeys.map((k) => filters[k]).join("|")} filters={filters} onApply={(f) => update({ ...f, page: 0 })} />
          <div className="flex flex-wrap items-center gap-1 text-sm">
            <span className="mr-1 text-muted-foreground">Direction</span>
            {directions.map((d) => {
              const active = (filters.direction ?? "").split(",").filter(Boolean)
              const on = active.includes(d)
              const Icon = directionInfo[d].icon
              return (
                <Button
                  key={d}
                  size="sm"
                  variant={on ? "secondary" : "ghost"}
                  aria-pressed={on}
                  onClick={() => {
                    const next = on ? active.filter((x) => x !== d) : [...active, d]
                    update({ direction: directions.filter((x) => next.includes(x)).join(","), page: 0 })
                  }}
                >
                  <Icon className={directionInfo[d].className} />
                  {directionInfo[d].label}
                </Button>
              )
            })}
          </div>
          {query.data?.hostMatch && <HostMatchNote match={query.data.hostMatch} />}
          {filters.device && (
            <p className="text-sm text-muted-foreground">
              Flows of the Device <span className="font-mono text-ident">{filters.device}</span>, at every address it used.
            </p>
          )}
          {query.isPending ? (
            <SectionSkeleton rows={10} />
          ) : query.isError ? (
            <QueryError title="Couldn't load Flows" error={query.error} />
          ) : query.data.items.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              {query.data.rawFrom ? "No raw Flows match in this time range." : "No raw Flows are kept yet."}
            </p>
          ) : (
            <FlowTable
              items={query.data.items}
              sorting={sorting}
              onSortingChange={(updater) => {
                const next = typeof updater === "function" ? updater(sorting) : updater
                const s = next[0]
                if (s) update({ sort: s.id, order: s.desc ? undefined : "asc", page: 0 })
              }}
              onSelect={(f) => update({ flow: f.id })}
              hidden={exporterColumnHidden ? ["exporter"] : []}
            />
          )}
          {query.data && query.data.total > 0 && (
            <div className="flex flex-wrap items-center justify-between gap-3 text-sm">
              <span className="text-muted-foreground tabular-nums">
                {formatNumber(page * size + 1)}–{formatNumber(Math.min(total, (page + 1) * size))} of {formatNumber(total)} Flows
              </span>
              <div className="flex items-center gap-2">
                <Select value={String(size)} onValueChange={(v) => update({ size: v === "100" ? undefined : v, page: 0 })}>
                  <SelectTrigger size="sm" aria-label="Flows per page">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {pageSizes.map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {n} per page
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button size="icon-sm" variant="outline" aria-label="Previous page" disabled={page === 0} onClick={() => update({ page: page - 1 })}>
                  <ChevronLeft />
                </Button>
                <span className="tabular-nums">
                  {formatNumber(page + 1)} / {formatNumber(pages)}
                </span>
                <Button
                  size="icon-sm"
                  variant="outline"
                  aria-label="Next page"
                  disabled={page + 1 >= pages || (page + 1) * size > 100_000}
                  onClick={() => update({ page: page + 1 })}
                >
                  <ChevronRight />
                </Button>
              </div>
            </div>
          )}
          {truncated && (
            <p className="text-xs text-muted-foreground">
              Raw Flows are only kept from {formatDateTime(query.data!.rawFrom!)}, so older Flows in this time range aren't
              available.
            </p>
          )}
        </CardContent>
      </Card>
      <FlowDetailSheet id={selected} onClose={() => update({ flow: undefined })} />
    </Page>
  )
}

/** The text filters, applied together when submitted. */
function FilterForm({ filters, onApply }: { filters: FlowFilters; onApply: (f: FlowFilters) => void }) {
  const [draft, setDraft] = useState<FlowFilters>(filters)
  const set = (k: keyof FlowFilters) => (e: React.ChangeEvent<HTMLInputElement>) => setDraft({ ...draft, [k]: e.target.value })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    onApply({ ...draft, country: draft.country?.trim().toUpperCase() })
  }
  const active = filterKeys.some((k) => k !== "direction" && filters[k])
  return (
    <form onSubmit={submit} className="grid grid-cols-2 gap-3 lg:grid-cols-[repeat(7,minmax(0,1fr))_auto]">
      <Field id="f-host" label="Local Host">
        <Input
          id="f-host"
          value={draft.host}
          onChange={set("host")}
          placeholder="name, address or prefix"
          title="A Host Name (a Host Label, or part of a reverse DNS name), an address, or a prefix"
          className="font-mono"
        />
      </Field>
      <Field id="f-remote" label="Remote IP">
        <Input id="f-remote" value={draft.remote} onChange={set("remote")} placeholder="1.1.1.1" className="font-mono" />
      </Field>
      <Field id="f-domain" label="Domain">
        <Input
          id="f-domain"
          value={draft.domain}
          onChange={set("domain")}
          placeholder="bbc.co.uk"
          title="A name looked up in DNS: Flows to it and its subdomains. - for Flows nobody looked up."
          className="font-mono"
        />
      </Field>
      <Field id="f-asn" label="ASN">
        <Input id="f-asn" inputMode="numeric" value={draft.asn} onChange={set("asn")} placeholder="15169" />
      </Field>
      <Field id="f-country" label="Country">
        <Input id="f-country" value={draft.country} onChange={set("country")} placeholder="DE" maxLength={2} />
      </Field>
      <Field id="f-protocol" label="Protocol">
        <Select value={draft.protocol || ANY} onValueChange={(v) => setDraft({ ...draft, protocol: v === ANY ? "" : v })}>
          <SelectTrigger id="f-protocol" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ANY}>Any</SelectItem>
            {protocols.map(([value, label]) => (
              <SelectItem key={value} value={value}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field id="f-port" label="Port">
        <Input id="f-port" inputMode="numeric" value={draft.port} onChange={set("port")} placeholder="443" />
      </Field>
      <div className="flex items-end gap-2">
        <Button type="submit" size="sm">
          Filter
        </Button>
        {active && (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => onApply({ host: "", device: "", remote: "", domain: "", asn: "", country: "", protocol: "", port: "" })}
          >
            <X />
            Clear
          </Button>
        )}
      </div>
    </form>
  )
}

/** What a Host Name in the Local Host filter stood for. */
function HostMatchNote({ match }: { match: NonNullable<FlowPage["hostMatch"]> }) {
  if (match.hosts.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No Local Host in this time range has a Host Name like “{match.name}”. Names are Host Labels and reverse DNS names;
        an address or prefix works too.
      </p>
    )
  }
  const shown = match.hosts.slice(0, 5)
  return (
    <p className="text-xs text-muted-foreground">
      “{match.name}” matches {match.hosts.length === 1 ? "one Local Host" : `${match.hosts.length} Local Hosts`}:{" "}
      {shown.map((h) => `${h.name} (${h.host})`).join(", ")}
      {match.hosts.length > shown.length && `, and ${match.hosts.length - shown.length} more`}.
    </p>
  )
}
