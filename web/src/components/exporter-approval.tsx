import { useMutation, useQueryClient } from "@tanstack/react-query"
import { MoreHorizontal, ShieldQuestion } from "lucide-react"
import { useState } from "react"

import { ClassificationModeField } from "@/components/classification-mode-field"
import { Field, FormActions, FormError, Section, SectionHeading } from "@/components/page"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import {
  approveExporter,
  forgetExporter,
  ignoreExporter,
  type ClassificationMode,
  type ExporterStatus,
} from "@/lib/api"
import { formatNumber } from "@/lib/format"

function useInvalidate() {
  const queryClient = useQueryClient()
  return () => {
    void queryClient.invalidateQueries({ queryKey: ["exporters"] })
    void queryClient.invalidateQueries({ queryKey: ["exporter-options"] })
  }
}

/** Approval form for a Pending (or ignored) Exporter. */
export function ApprovalPanel({ exporter }: { exporter: ExporterStatus }) {
  const invalidate = useInvalidate()
  const [name, setName] = useState(exporter.name)
  // Classification by interface needs interface numbers in the Exporter's Templates.
  const sendsInterfaces =
    exporter.templates.some((t) => !t.options) &&
    exporter.templates.every((t) => t.options || !t.findings.some((f) => f.code === "no_interfaces"))
  const [mode, setMode] = useState<ClassificationMode>(sendsInterfaces ? "interface" : "address")
  const approve = useMutation({
    mutationFn: () => approveExporter(exporter.address, { name, role: "edge", classificationMode: mode }),
    onSuccess: invalidate,
  })
  const ignore = useMutation({ mutationFn: () => ignoreExporter(exporter.address), onSuccess: invalidate })
  const pending = exporter.approval === "pending"
  const error = approve.error ?? ignore.error

  return (
    <Section className="rounded-lg border border-warning/40 bg-warning/5 p-4">
      <SectionHeading
        title={
          <>
            <ShieldQuestion className="size-4 text-warning-foreground" />
            {pending ? "This Exporter is waiting for approval" : "This Exporter is ignored"}
          </>
        }
      />
      <p className="text-sm text-muted-foreground">
        {pending
          ? `Flowmon keeps its Templates but drops its Flows until you approve it (${formatNumber(exporter.pendingDroppedRecords)} dropped so far). Only approve addresses you recognise: anyone who can reach the collector port can send data.`
          : `Its datagrams are dropped without decoding (${formatNumber(exporter.ignoredDatagrams)} so far).`}
      </p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={`name-${exporter.address}`} label="Name">
          <Input
            id={`name-${exporter.address}`}
            placeholder="e.g. MikroTik"
            value={name}
            maxLength={64}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field id={`role-${exporter.address}`} label="Exporter role">
          <Select value="edge">
            <SelectTrigger id={`role-${exporter.address}`} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="edge">Edge</SelectItem>
              <SelectItem value="internal" disabled>
                Internal (v1.1)
              </SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </div>
      <Field label="Classification">
        <ClassificationModeField
          idPrefix={`approve-${exporter.address}`}
          value={mode}
          onChange={setMode}
          interfaceAvailable={sendsInterfaces}
        />
      </Field>
      <FormError error={error} />
      <FormActions>
        {pending && (
          <Button size="sm" variant="outline" onClick={() => ignore.mutate()} disabled={ignore.isPending}>
            {ignore.isPending ? "Ignoring…" : "Ignore"}
          </Button>
        )}
        <Button size="sm" onClick={() => approve.mutate()} disabled={approve.isPending}>
          {approve.isPending ? "Approving…" : "Approve"}
        </Button>
      </FormActions>
    </Section>
  )
}

/** Menu for an approved or ignored Exporter. */
export function ExporterActions({ exporter }: { exporter: ExporterStatus }) {
  const invalidate = useInvalidate()
  const ignore = useMutation({ mutationFn: () => ignoreExporter(exporter.address), onSuccess: invalidate })
  const forget = useMutation({ mutationFn: () => forgetExporter(exporter.address), onSuccess: invalidate })
  if (exporter.approval === "pending") return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" className="ml-auto" aria-label="Exporter actions">
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {exporter.approval === "approved" && (
          <DropdownMenuItem onSelect={() => ignore.mutate()}>Ignore this Exporter</DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={() => forget.mutate()}>Forget (ask for approval again)</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
