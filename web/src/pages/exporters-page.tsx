import { useQuery } from "@tanstack/react-query"
import { Link } from "react-router"
import { AlertTriangle, Cable, ChevronRight, Info } from "lucide-react"

import { ApprovalPanel, ExporterActions } from "@/components/exporter-approval"
import { InterfacesSection } from "@/components/interfaces-section"
import { CollectorCard } from "@/components/collector-card"
import { EmptyState, Page, QueryError, Section, SectionHeading, Stat, TimeAgo } from "@/components/page"
import { SNMPSection } from "@/components/snmp-section"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  fetchExporters,
  type DeviationStatus,
  type ExporterStatus,
  type Finding,
  type TemplateStatus,
  type TimingStatus,
} from "@/lib/api"
import { useExporterFilter } from "@/lib/exporter-filter"
import { formatBytes, formatNumber, formatSeconds } from "@/lib/format"

/** An Exporter counts as receiving if it sent something in the last two minutes. */
const RECEIVING_WINDOW_MS = 2 * 60 * 1000

const deviationLabels: Record<string, string> = {
  short_message: "Message shorter than a header",
  wrong_version: "Not IPFIX or NetFlow v9",
  bad_message_length: "Invalid message length",
  truncated_message: "Message cut off",
  trailing_bytes: "Bytes after the message",
  truncated_set: "Set cut off",
  bad_set_length: "Invalid set length",
  reserved_set_id: "Reserved set ID",
  template_withdrawal_ignored: "Template withdrawal ignored (not allowed over UDP)",
  invalid_template_id: "Invalid Template ID",
  invalid_scope_count: "Invalid scope field count",
  template_too_large: "Template too large",
  empty_template: "Template with zero-length records",
  truncated_template: "Template cut off",
  too_many_templates: "Too many Templates",
  field_length_invalid: "Field length doesn't fit its type",
  unknown_template: "Data dropped: Template never arrived",
  truncated_record: "Record cut off",
  nonzero_padding: "Padding isn't zero",
  sequence_gap: "Sequence number gap",
  exporter_restarted: "Exporter restarted (its uptime started again)",
  netflow_v5_not_supported_yet: "NetFlow v5 (not supported yet)",
  unknown_version: "Unknown export version",
  decoder_panic: "Decoder error",
}

function deviationLabel(kind: string) {
  return deviationLabels[kind] ?? kind.replaceAll("_", " ")
}

export function ExportersPage() {
  const { exporter: selected } = useExporterFilter()
  const query = useQuery({
    queryKey: ["exporters"],
    queryFn: ({ signal }) => fetchExporters(signal),
    refetchInterval: 15_000,
  })

  if (query.isPending) {
    return (
      <Page>
        <Skeleton className="h-6 w-72" />
        <Skeleton className="h-48 w-full" />
      </Page>
    )
  }
  if (query.isError) {
    return (
      <Page>
        <QueryError title="Couldn't load Exporters" error={query.error} />
      </Page>
    )
  }

  const { droppedOverExporterLimit, maxExporters, droppedOverPendingLimit, maxPending } = query.data
  // Pending Exporters first: they need a decision.
  const exporters = query.data.exporters
    .filter((e) => !selected || e.address === selected)
    .sort((a, b) => Number(b.approval === "pending") - Number(a.approval === "pending"))
  // "Now" is when the data was fetched, so relative times and the receiving badge are
  // consistent with the snapshot and rendering stays pure.
  const now = query.dataUpdatedAt

  return (
    <Page>
      <CollectorCard status={query.data} now={now} />

      {droppedOverExporterLimit > 0 && (
        <Alert variant="warning">
          <AlertTriangle />
          <AlertTitle>Exporter limit reached</AlertTitle>
          <AlertDescription>
            {formatNumber(droppedOverExporterLimit)} datagrams from new sources were ignored because Flowmon already
            tracks {formatNumber(maxExporters)} Exporters (FLOWMON_MAX_EXPORTERS).
          </AlertDescription>
        </Alert>
      )}

      {droppedOverPendingLimit > 0 && (
        <Alert variant="warning">
          <AlertTriangle />
          <AlertTitle>Too many Exporters waiting for approval</AlertTitle>
          <AlertDescription>
            {formatNumber(droppedOverPendingLimit)} datagrams from further new sources were ignored: at most{" "}
            {formatNumber(maxPending)} Exporters can wait for approval at once. Approve or ignore the ones below.
          </AlertDescription>
        </Alert>
      )}

      {exporters.length === 0 ? (
        <EmptyState
          icon={Cable}
          title="No Exporters yet"
          action={
            <Button asChild size="sm" variant="outline">
              <Link to="/guide/mikrotik">Open the MikroTik setup guide</Link>
            </Button>
          }
        >
          <p>
            Point your router's flow export (IPFIX or NetFlow v9) at this host, on one of the ports above. An Exporter appears here as
            soon as its first packet arrives, with every Template it sends.
          </p>
        </EmptyState>
      ) : (
        exporters.map((exporter) => <ExporterCard key={exporter.address} exporter={exporter} now={now} />)
      )}
    </Page>
  )
}

const roleLabels: Record<string, string> = { edge: "Edge", internal: "Internal" }
const modeLabels: Record<string, string> = { interface: "By interface", address: "By address" }

function ApprovalBadge({ exporter, receiving }: { exporter: ExporterStatus; receiving: boolean }) {
  switch (exporter.approval) {
    case "pending":
      return <Badge variant="warning">Waiting for approval</Badge>
    case "ignored":
      return <Badge variant="secondary">Ignored</Badge>
  }
  if (!exporter.seen) return <Badge variant="secondary">Not seen yet</Badge>
  return <Badge variant={receiving ? "default" : "secondary"}>{receiving ? "Receiving" : "Silent"}</Badge>
}

function ExporterCard({ exporter, now }: { exporter: ExporterStatus; now: number }) {
  const receiving = now - new Date(exporter.lastSeen).getTime() < RECEIVING_WINDOW_MS
  const domains = exporter.observationDomains ?? []
  const approved = exporter.approval === "approved"

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="min-w-0 break-all">
            {exporter.name && <span className="mr-2">{exporter.name}</span>}
            <span className={exporter.name ? "font-mono text-muted-foreground" : "font-mono"}>{exporter.address}</span>
          </CardTitle>
          <ApprovalBadge exporter={exporter} receiving={receiving} />
          {approved && (
            <Badge variant="outline">
              {roleLabels[exporter.role] ?? exporter.role} ·{" "}
              {modeLabels[exporter.classificationMode] ?? exporter.classificationMode}
            </Badge>
          )}
          <ExporterActions exporter={exporter} />
        </div>
        <CardDescription>
          {exporter.seen ? (
            <>
              Last seen <TimeAgo iso={exporter.lastSeen} now={now} />
              {" · "}from <span className="font-mono">{exporter.lastSource}</span>
              {domains.length > 0 && <> · Observation Domain {domains.join(", ")}</>}
            </>
          ) : (
            "Not heard from since Flowmon started"
          )}
        </CardDescription>
      </CardHeader>
      {/* The same sections in the same order for every Exporter. */}
      <CardContent className="flex flex-col gap-6">
        {!approved && <ApprovalPanel exporter={exporter} />}
        {exporter.findings.length > 0 && <Findings findings={exporter.findings} />}

        <Section>
          <SectionHeading title="Traffic" aside="Since Flowmon started" />
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Stat label="Datagrams" value={formatNumber(exporter.datagrams)} />
            <Stat label="Messages decoded" value={formatNumber(exporter.messages)} />
            <Stat label="Flows decoded" value={formatNumber(exporter.records)} />
            <Stat label="Export data received" value={formatBytes(exporter.bytes)} />
          </div>
        </Section>

        {exporter.timing && <Timing timing={exporter.timing} />}
        {exporter.deviations.length > 0 && <Deviations deviations={exporter.deviations} now={now} />}
        {approved && <InterfacesSection address={exporter.address} />}
        {approved && <SNMPSection address={exporter.address} />}

        <Section>
          <SectionHeading title="Templates" />
          {exporter.templates.length === 0 ? (
            <p className="text-sm text-muted-foreground">No Templates received yet.</p>
          ) : (
            exporter.templates.map((t) => <TemplateRow key={`${t.observationDomain}/${t.id}`} template={t} now={now} />)
          )}
        </Section>
      </CardContent>
    </Card>
  )
}

function Findings({ findings }: { findings: Finding[] }) {
  return (
    <div className="flex flex-col gap-2">
      {findings.map((f) => (
        <Alert key={f.code} variant={f.severity === "warning" ? "warning" : "default"}>
          {f.severity === "warning" ? <AlertTriangle /> : <Info />}
          <AlertDescription>{f.message}</AlertDescription>
        </Alert>
      ))}
    </div>
  )
}

function Timing({ timing }: { timing: TimingStatus }) {
  const offset = timing.clockOffsetSeconds
  const clock =
    Math.abs(offset) < 2
      ? "In sync"
      : offset > 0
        ? `${formatSeconds(offset)} behind`
        : `${formatSeconds(-offset)} ahead`
  return (
    <Section>
      <SectionHeading title="Timing" />
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Exporter clock vs Flowmon" value={clock} />
        <Stat label="Flow end to export (median)" value={formatSeconds(timing.exportLagSeconds)} />
        <Stat label="Watermark" value={formatSeconds(timing.watermarkSeconds)} />
        <Stat
          label="Flows with estimated times"
          value={`${formatNumber(timing.estimatedTimeFlows)} of ${formatNumber(timing.flows)}`}
        />
      </div>
      {timing.findings.length > 0 && <Findings findings={timing.findings} />}
    </Section>
  )
}

function Deviations({ deviations, now }: { deviations: DeviationStatus[]; now: number }) {
  return (
    <Section>
      <SectionHeading title="Deviations and drops" />
      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Kind</TableHead>
              <TableHead className="text-right">Count</TableHead>
              <TableHead>Last detail</TableHead>
              <TableHead>Last seen</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {deviations.map((d) => (
              <TableRow key={d.kind}>
                <TableCell className="font-medium">{deviationLabel(d.kind)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatNumber(d.count)}</TableCell>
                <TableCell className="max-w-md truncate text-muted-foreground" title={d.lastDetail}>
                  {d.lastDetail}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  <TimeAgo iso={d.lastSeen} now={now} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </Section>
  )
}

function TemplateRow({ template, now }: { template: TemplateStatus; now: number }) {
  const warnings = template.findings.filter((f) => f.severity === "warning").length

  return (
    <Collapsible className="rounded-lg border">
      <CollapsibleTrigger className="group flex w-full flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2 text-left text-sm hover:bg-muted/50">
        <ChevronRight className="size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
        <span className="font-medium whitespace-nowrap">
          {template.options ? "Options Template" : "Template"} {template.id}
        </span>
        <span className="text-muted-foreground">
          · {template.protocol === "netflow9" ? "Source ID" : "Observation Domain"} {template.observationDomain} ·{" "}
          {formatNumber(template.fields.length)} fields
        </span>
        {template.protocol === "netflow9" && <Badge variant="secondary">NetFlow v9</Badge>}
        <span className="ml-auto flex items-center gap-2">
          {warnings > 0 && (
            <Badge variant="warning">
              {warnings} warning{warnings === 1 ? "" : "s"}
            </Badge>
          )}
          <span className="text-xs whitespace-nowrap text-muted-foreground">
            Refreshed <TimeAgo iso={template.lastSeen} now={now} />
          </span>
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent className="flex flex-col gap-3 border-t p-3">
        {template.findings.length > 0 && <Findings findings={template.findings} />}
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">#</TableHead>
                <TableHead>Field</TableHead>
                <TableHead>Element</TableHead>
                <TableHead>Type</TableHead>
                <TableHead className="text-right">Length</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {template.fields.map((f, i) => (
                <TableRow key={i}>
                  <TableCell className="text-muted-foreground tabular-nums">{i + 1}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {f.name}
                    {f.scope && (
                      <Badge variant="outline" className="ml-2">
                        Scope
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground tabular-nums">
                    {f.enterprise ? `${f.enterprise}/${f.id}` : f.id}
                  </TableCell>
                  <TableCell className="text-muted-foreground">{f.type || "unknown"}</TableCell>
                  <TableCell className="text-right tabular-nums">{f.variable ? "variable" : `${f.length} B`}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}
