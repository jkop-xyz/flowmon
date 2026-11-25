import { useQuery } from "@tanstack/react-query"
import type { ReactNode } from "react"
import { Link, useLocation, useParams } from "react-router"

import { HostLink } from "@/components/host"
import { Page, QueryError, SectionSkeleton, Stat, TimeAgo } from "@/components/page"
import { RemoteIPLink } from "@/components/remote"
import { TrafficChart, type TrafficSeries } from "@/components/traffic-chart"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { fetchDNS, type DNSClient, type DNSName, type DNSSummary } from "@/lib/api"
import { formatDateTime, formatNumber } from "@/lib/format"
import { useHostNames } from "@/lib/hosts"
import { resolveRange, useTimeRange } from "@/lib/time-range"

const series: TrafficSeries<"queries" | "blocked">[] = [
  { key: "queries", label: "Lookups", color: "var(--chart-1)" },
  { key: "blocked", label: "Blocked", color: "var(--chart-2)" },
]

const perMinute = (v: number) => `${v < 10 ? v.toFixed(1) : formatNumber(Math.round(v))}/min`

function useDNS(opts: { host?: string; domain?: string }) {
  const { preset } = useTimeRange()
  return useQuery({
    queryKey: ["dns", preset.value, opts.host, opts.domain],
    queryFn: ({ signal }) => fetchDNS(resolveRange(preset), opts, signal),
    refetchInterval: 30_000,
    placeholderData: (previous) => previous,
  })
}

/** An upstream's host: "https://dns10.quad9.net:443/dns-query" reads "dns10.quad9.net". */
function upstreamHost(u: string) {
  try {
    return new URL(u.includes("://") ? u : `dns://${u}`).hostname || u
  } catch {
    return u
  }
}

/** The share of lookups blocked, as a percentage. */
function blockRate(s: DNSSummary) {
  return s.queries === 0 ? "–" : `${((100 * s.blocked) / s.queries).toFixed(1)} %`
}

/** What a DNS view says when there is nothing to show: no source, or nothing yet. */
function DNSOff() {
  return (
    <p className="text-sm text-muted-foreground">
      DNS collection is off. Add AdGuard Home under{" "}
      <Link to="/settings" className="underline underline-offset-4">
        Settings → Data Sources
      </Link>{" "}
      to see which names are looked up and which are blocked.
    </p>
  )
}

/** The totals; oneHost leaves out how many Local Hosts asked, always one on a host's page. */
function Totals({ s, oneHost = false }: { s: DNSSummary; oneHost?: boolean }) {
  return (
    <div className={oneHost ? "grid grid-cols-3 gap-3" : "grid grid-cols-2 gap-3 md:grid-cols-4"}>
      <Stat label="Lookups" value={formatNumber(s.queries)} />
      <Stat label="Blocked" value={formatNumber(s.blocked)} />
      <Stat label="Block rate" value={blockRate(s)} />
      {!oneHost && <Stat label="Local Hosts asking" value={formatNumber(s.clients)} />}
    </div>
  )
}

/** The chart; the last 15 s before fetched may still change, as sources are polled every 15 s. */
function Chart({ s, fetched }: { s: DNSSummary; fetched: number }) {
  const { preset } = useTimeRange()
  return (
    <TrafficChart
      points={s.points}
      series={series}
      stepSeconds={s.stepSeconds}
      rangeMs={preset.ms}
      incompleteFrom={fetched - 15_000}
      format={perMinute}
      noun="lookups"
    />
  )
}

/** Names with their lookups; each opens its domain page. */
function NameTable({
  names,
  empty,
  blockedColumn = true,
  hostsColumn = true,
}: {
  names: DNSName[]
  empty: string
  blockedColumn?: boolean
  hostsColumn?: boolean
}) {
  const { search } = useLocation()
  if (names.length === 0) return <p className="text-sm text-muted-foreground">{empty}</p>
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead className="text-right">Lookups</TableHead>
            {blockedColumn && <TableHead className="text-right">Blocked</TableHead>}
            {hostsColumn && <TableHead className="text-right">Local Hosts</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {names.map((n) => (
            <TableRow key={n.name}>
              <TableCell className="max-w-72">
                <Link
                  to={{
                    pathname: `/dns/${encodeURIComponent(n.name)}`,
                    search,
                  }}
                  className="block truncate font-mono text-ident underline-offset-4 hover:underline"
                  title={`${n.name}, last looked up ${formatDateTime(n.last)}`}
                >
                  {n.name}
                </Link>
              </TableCell>
              <TableCell className="text-right tabular-nums">{formatNumber(n.queries)}</TableCell>
              {blockedColumn && <TableCell className="text-right tabular-nums">{formatNumber(n.blocked)}</TableCell>}
              {hostsColumn && <TableCell className="text-right tabular-nums">{formatNumber(n.clients)}</TableCell>}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function ClientTable({ clients }: { clients: DNSClient[] }) {
  const names = useHostNames(clients.map((c) => c.client))
  if (clients.length === 0) return <p className="text-sm text-muted-foreground">No lookups in this time range.</p>
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Local Host</TableHead>
            <TableHead className="text-right">Lookups</TableHead>
            <TableHead className="text-right">Blocked</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {clients.map((c) => (
            <TableRow key={c.client}>
              <TableCell className="max-w-64">
                <HostLink host={c.client} name={names[c.client]} />
                {c.clientName && !names[c.client]?.name && (
                  <span className="block truncate text-xs text-muted-foreground">AdGuard Home: {c.clientName}</span>
                )}
              </TableCell>
              <TableCell className="text-right tabular-nums">{formatNumber(c.queries)}</TableCell>
              <TableCell className="text-right tabular-nums">{formatNumber(c.blocked)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function Section({
  title,
  description,
  children,
  action,
}: {
  title: string
  description: string
  children: ReactNode
  action?: ReactNode
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
        {action && <CardAction>{action}</CardAction>}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  )
}

/** Retention note: DNS Queries are kept for days, so long ranges start where they do. */
function SinceNote({ s }: { s: DNSSummary }) {
  if (!s.since || new Date(s.since) <= new Date(s.from)) return null
  return (
    <p className="text-xs text-muted-foreground">
      DNS Queries are kept from {formatDateTime(s.since)}; the range starts earlier.
    </p>
  )
}

/** The whole network's lookups. */
export function DNSPage() {
  const query = useDNS({})
  const { preset } = useTimeRange()
  if (query.isPending) {
    return (
      <Page>
        <Skeleton className="h-96 w-full" />
      </Page>
    )
  }
  if (query.isError) {
    return (
      <Page>
        <QueryError title="Couldn't load DNS" error={query.error} />
      </Page>
    )
  }
  const s = query.data.result
  if (query.data.dnsSources === 0 && s.queries === 0) {
    return (
      <Page>
        <Card>
          <CardContent>
            <DNSOff />
          </CardContent>
        </Card>
      </Page>
    )
  }
  return (
    <Page>
      <Card>
        <CardHeader>
          <CardTitle>Lookups</CardTitle>
          <CardDescription>
            Every DNS lookup your DNS sources logged, {preset.label.toLowerCase()}, and how many were blocked.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <Totals s={s} />
          <Chart s={s} fetched={query.dataUpdatedAt} />
          <SinceNote s={s} />
        </CardContent>
      </Card>
      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Section title="Top domains" description="The most looked-up Registrable Domains.">
          <NameTable names={s.topNames} empty="No lookups in this time range." />
        </Section>
        <Section title="Top blocked" description="The Registrable Domains blocked most often.">
          <NameTable names={s.topBlocked} empty="Nothing was blocked in this time range." blockedColumn={false} />
        </Section>
        <Section title="Who asks" description="The Local Hosts making the most lookups.">
          <ClientTable clients={s.topClients} />
        </Section>
        <Section
          title="Upstreams"
          description="How fast the DNS server's upstreams answered the lookups it didn't answer from its cache."
        >
          {s.upstreams.length === 0 ? (
            <p className="text-sm text-muted-foreground">No lookups went upstream in this time range.</p>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Upstream</TableHead>
                    <TableHead className="text-right">Lookups</TableHead>
                    <TableHead className="text-right">Average</TableHead>
                    <TableHead className="text-right">95th percentile</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {s.upstreams.map((u) => (
                    <TableRow key={u.upstream}>
                      <TableCell className="max-w-56 truncate font-mono text-ident" title={u.upstream}>
                        {upstreamHost(u.upstream)}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">{formatNumber(u.queries)}</TableCell>
                      <TableCell className="text-right tabular-nums">{u.avgMs.toFixed(1)} ms</TableCell>
                      <TableCell className="text-right tabular-nums">{u.p95Ms.toFixed(1)} ms</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </Section>
      </div>
    </Page>
  )
}

/** One name and the names under it: who looked it up, what it answered, and its traffic. */
export function DNSDomainPage() {
  const { domain = "" } = useParams()
  const { search } = useLocation()
  const query = useDNS({ domain })
  const { preset } = useTimeRange()
  const explorer = new URLSearchParams(search)
  explorer.set("domain", domain)
  return (
    <Page>
      {query.isPending ? (
        <SectionSkeleton rows={4} />
      ) : query.isError ? (
        <QueryError title={`Couldn't load ${domain}`} error={query.error} />
      ) : (
        <>
          <Card>
            <CardHeader>
              <CardTitle className="font-mono text-ident">{domain}</CardTitle>
              <CardDescription>
                Lookups of {domain} and the names under it, {preset.label.toLowerCase()}.
              </CardDescription>
              <CardAction>
                <Link
                  to={{ pathname: "/flows", search: `?${explorer}` }}
                  className="text-sm underline-offset-4 hover:underline"
                >
                  Its traffic in the Flow Explorer
                </Link>
              </CardAction>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              <Totals s={query.data.result} />
              <Chart s={query.data.result} fetched={query.dataUpdatedAt} />
              <SinceNote s={query.data.result} />
            </CardContent>
          </Card>
          <div className="grid items-start gap-6 lg:grid-cols-2">
            <Section title="Names" description="The full names looked up.">
              <NameTable names={query.data.result.topNames} empty="Not looked up in this time range." />
            </Section>
            <Section title="Who looked it up" description="The Local Hosts that asked.">
              <ClientTable clients={query.data.result.topClients} />
            </Section>
            <Section title="Answers" description="The addresses it was answered with, when not blocked.">
              {(query.data.result.answers ?? []).length === 0 ? (
                <p className="text-sm text-muted-foreground">No addresses in this time range.</p>
              ) : (
                <div className="overflow-x-auto">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Address</TableHead>
                        <TableHead className="text-right">Lookups</TableHead>
                        <TableHead>Last</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {query.data.result.answers!.map((a) => (
                        <TableRow key={a.address}>
                          <TableCell>
                            <RemoteIPLink
                              ip={a.address}
                              className="font-mono text-ident underline-offset-4 hover:underline"
                            />
                          </TableCell>
                          <TableCell className="text-right tabular-nums">{formatNumber(a.queries)}</TableCell>
                          <TableCell>
                            <TimeAgo iso={a.last} now={query.dataUpdatedAt} />
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              )}
            </Section>
          </div>
        </>
      )}
    </Page>
  )
}

/** A Local Host's lookups, on its page. Hidden when DNS collection is off and it has none. */
export function HostDNSCard({ host }: { host: string }) {
  const query = useDNS({ host })
  const { search } = useLocation()
  if (!query.data || (query.data.dnsSources === 0 && query.data.result.queries === 0)) return null
  const s = query.data.result
  return (
    <Card>
      <CardHeader>
        <CardTitle>DNS</CardTitle>
        <CardDescription>What it looked up, and what was blocked.</CardDescription>
        <CardAction>
          <Link to={{ pathname: "/dns", search }} className="text-sm underline-offset-4 hover:underline">
            All DNS
          </Link>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <Totals s={s} oneHost />
        <div className="grid items-start gap-6 lg:grid-cols-2">
          <div className="flex flex-col gap-2">
            <h3 className="text-sm font-medium">Top domains</h3>
            <NameTable names={s.topNames.slice(0, 8)} empty="No lookups in this time range." hostsColumn={false} />
          </div>
          <div className="flex flex-col gap-2">
            <h3 className="text-sm font-medium">Top blocked</h3>
            <NameTable
              names={s.topBlocked.slice(0, 8)}
              empty="Nothing was blocked."
              blockedColumn={false}
              hostsColumn={false}
            />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
