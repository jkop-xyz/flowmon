import { useQuery } from "@tanstack/react-query"
import { AlertTriangle, Cable, ChevronRight, Info, Radio } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
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
import { formatAgo, formatBytes, formatDateTime, formatNumber, formatSeconds } from "@/lib/format"

/** An Exporter counts as receiving if it sent something in the last two minutes. */
const RECEIVING_WINDOW_MS = 2 * 60 * 1000

const deviationLabels: Record<string, string> = {
  short_message: "Message shorter than a header",
  wrong_version: "Not IPFIX",
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
  netflow_v9_not_supported_yet: "NetFlow v9 (not supported yet)",
  netflow_v5_not_supported_yet: "NetFlow v5 (not supported yet)",
  unknown_version: "Unknown export version",
  decoder_panic: "Decoder error",
}

function deviationLabel(kind: string) {
  return deviationLabels[kind] ?? kind.replaceAll("_", " ")
}

export function ExportersPage() {
  const query = useQuery({
    queryKey: ["exporters"],
    queryFn: ({ signal }) => fetchExporters(signal),
    refetchInterval: 15_000,
  })

  if (query.isPending) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-6 w-72" />
        <Skeleton className="h-48 w-full" />
      </div>
    )
  }
  if (query.isError) {
    return (
      <Alert variant="destructive" className="max-w-2xl">
        <AlertTriangle />
        <AlertTitle>Couldn't load Exporters</AlertTitle>
        <AlertDescription>{query.error.message}</AlertDescription>
      </Alert>
    )
  }

  const { listeners, exporters, droppedOverExporterLimit, maxExporters } = query.data
  // "Now" is when the data was fetched, so relative times and the receiving badge are
  // consistent with the snapshot and rendering stays pure.
  const now = query.dataUpdatedAt

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
        <Radio className="size-4" />
        <span>Listening on</span>
        {listeners.length === 0 ? (
          <span>nothing: no collector addresses are configured</span>
        ) : (
          listeners.map((l) => (
            <Badge key={l} variant="outline" className="font-mono">
              {l}
            </Badge>
          ))
        )}
      </div>

      {droppedOverExporterLimit > 0 && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Exporter limit reached</AlertTitle>
          <AlertDescription>
            {formatNumber(droppedOverExporterLimit)} datagrams from new sources were ignored because Flowmon already
            tracks {maxExporters} Exporters (FLOWMON_MAX_EXPORTERS).
          </AlertDescription>
        </Alert>
      )}

      {exporters.length === 0 ? (
        <EmptyState />
      ) : (
        exporters.map((exporter) => <ExporterCard key={exporter.address} exporter={exporter} now={now} />)
      )}
    </div>
  )
}

function EmptyState() {
  return (
    <Card className="max-w-2xl">
      <CardHeader>
        <div className="flex items-center gap-2">
          <Cable className="size-5 text-muted-foreground" />
          <CardTitle>No Exporters yet</CardTitle>
        </div>
        <CardDescription>
          Point your router's flow export (IPFIX) at this host, on one of the ports above. An Exporter appears here as
          soon as its first packet arrives, with every Template it sends.
        </CardDescription>
      </CardHeader>
    </Card>
  )
}

function ExporterCard({ exporter, now }: { exporter: ExporterStatus; now: number }) {
  const receiving = now - new Date(exporter.lastSeen).getTime() < RECEIVING_WINDOW_MS
  const domains = exporter.observationDomains ?? []

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="font-mono text-base">{exporter.address}</CardTitle>
          <Badge variant={receiving ? "default" : "secondary"}>{receiving ? "Receiving" : "Silent"}</Badge>
        </div>
        <CardDescription>
          <span title={formatDateTime(exporter.lastSeen)}>Last seen {formatAgo(exporter.lastSeen, now)}</span>
          {" · "}from <span className="font-mono">{exporter.lastSource}</span>
          {domains.length > 0 && <> · Observation Domain {domains.join(", ")}</>}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Stat label="Datagrams" value={formatNumber(exporter.datagrams)} />
          <Stat label="IPFIX messages" value={formatNumber(exporter.messages)} />
          <Stat label="Flows decoded" value={formatNumber(exporter.records)} />
          <Stat label="Received" value={formatBytes(exporter.bytes)} />
        </div>

        {exporter.findings.length > 0 && <Findings findings={exporter.findings} />}

        {exporter.timing && <Timing timing={exporter.timing} />}

        {exporter.deviations.length > 0 && <Deviations deviations={exporter.deviations} now={now} />}

        <section className="flex flex-col gap-2">
          <h3 className="text-sm font-medium">Templates</h3>
          {exporter.templates.length === 0 ? (
            <p className="text-sm text-muted-foreground">No Templates received yet.</p>
          ) : (
            exporter.templates.map((t) => (
              <TemplateRow key={`${t.observationDomain}/${t.id}`} template={t} now={now} />
            ))
          )}
        </section>
      </CardContent>
    </Card>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border p-3">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-lg font-semibold tabular-nums">{value}</span>
    </div>
  )
}

function Findings({ findings }: { findings: Finding[] }) {
  return (
    <div className="flex flex-col gap-2">
      {findings.map((f) => (
        <Alert key={f.code} variant={f.severity === "warning" ? "destructive" : "default"}>
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
    Math.abs(offset) < 2 ? "in sync" : offset > 0 ? `${formatSeconds(offset)} behind` : `${formatSeconds(-offset)} ahead`
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">Timing</h3>
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
    </section>
  )
}

function Deviations({ deviations, now }: { deviations: DeviationStatus[]; now: number }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">Deviations and drops</h3>
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
                <TableCell className="text-muted-foreground" title={formatDateTime(d.lastSeen)}>
                  {formatAgo(d.lastSeen, now)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  )
}

function TemplateRow({ template, now }: { template: TemplateStatus; now: number }) {
  const warnings = template.findings.filter((f) => f.severity === "warning").length

  return (
    <Collapsible className="rounded-lg border">
      <CollapsibleTrigger className="group flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-muted/50">
        <ChevronRight className="size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
        <span className="font-medium">
          {template.options ? "Options Template" : "Template"} {template.id}
        </span>
        <span className="text-muted-foreground">
          · domain {template.observationDomain} · {template.fields.length} fields
        </span>
        <span className="ml-auto flex items-center gap-2">
          {warnings > 0 && (
            <Badge variant="destructive">
              {warnings} warning{warnings === 1 ? "" : "s"}
            </Badge>
          )}
          <span className="text-xs text-muted-foreground" title={formatDateTime(template.lastSeen)}>
            refreshed {formatAgo(template.lastSeen, now)}
          </span>
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent className="flex flex-col gap-3 border-t p-3">
        {template.findings.length > 0 && <Findings findings={template.findings} />}
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
                      scope
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
      </CollapsibleContent>
    </Collapsible>
  )
}
