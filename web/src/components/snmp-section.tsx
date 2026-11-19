import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, CheckCircle2, ChevronRight } from "lucide-react"
import { useState } from "react"

import {
  Field,
  FormActions,
  FormError,
  QueryError,
  Section,
  SectionHeading,
  SectionSkeleton,
  TimeAgo,
} from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  fetchReconciliation,
  fetchSNMP,
  saveSNMP,
  testSNMP,
  type ReconDirection,
  type SNMPConfig,
  type SNMPView,
} from "@/lib/api"
import { formatBytes, formatHour, formatNumber } from "@/lib/format"

const overheadLabels: Record<string, string> = {
  none: "None (0 B)",
  ethernet: "Ethernet (14 B)",
  "ethernet-vlan": "Ethernet + VLAN (18 B)",
  pppoe: "PPPoE over Ethernet (22 B)",
  custom: "Custom",
}

// Protocol names as RouterOS shows them; the value is what Flowmon stores.
const authProtocols: [string, string][] = [
  ["none", "None"],
  ["SHA", "SHA1 (RouterOS)"],
  ["SHA256", "SHA-256"],
  ["SHA384", "SHA-384"],
  ["SHA512", "SHA-512"],
  ["SHA224", "SHA-224"],
  ["MD5", "MD5"],
]
const privProtocols: [string, string][] = [
  ["none", "None"],
  ["AES", "AES (RouterOS)"],
  ["AES192", "AES-192"],
  ["AES256", "AES-256"],
  ["DES", "DES"],
]

/** SNMP settings and Reconciliation for an approved Exporter. */
export function SNMPSection({ address }: { address: string }) {
  const query = useQuery({ queryKey: ["snmp", address], queryFn: ({ signal }) => fetchSNMP(address, signal) })
  if (query.isPending) {
    return (
      <Section>
        <SectionHeading title="SNMP" />
        <SectionSkeleton rows={1} />
      </Section>
    )
  }
  if (query.isError) {
    return (
      <Section>
        <SectionHeading title="SNMP" />
        <QueryError title="Couldn't load the SNMP settings" error={query.error} />
      </Section>
    )
  }
  return (
    <SNMPEditor key={JSON.stringify(query.data.config)} address={address} view={query.data} now={query.dataUpdatedAt} />
  )
}

function statusLine(view: SNMPView, now: number) {
  if (!view.config.enabled) return "Off"
  if (view.status.lastError) return `Polling fails: ${view.status.lastError}`
  if (!view.status.lastPoll) return "On, waiting for the first poll"
  return (
    <>
      Polled <TimeAgo iso={view.status.lastPoll} now={now} />, {formatNumber(view.status.interfaces)} interface names
    </>
  )
}

/**
 * The settings the form edits and sends, built field by field from the loaded view.
 * The view also says which secrets are stored (hasCommunity …); the API refuses
 * those as unknown fields, so a spread copy made every Test and Save fail (#71).
 * Secrets start empty: leaving them empty keeps the stored ones.
 */
function formConfig(c: SNMPView["config"]): SNMPConfig {
  return {
    enabled: c.enabled,
    version: c.version,
    target: c.target,
    user: c.user,
    authProtocol: c.authProtocol,
    privProtocol: c.privProtocol,
    overhead: c.overhead,
    overheadBytes: c.overheadBytes,
    community: "",
    authPassword: "",
    privPassword: "",
  }
}

function SNMPEditor({ address, view, now }: { address: string; view: SNMPView; now: number }) {
  const queryClient = useQueryClient()
  const [cfg, setCfg] = useState<SNMPConfig>(() => formConfig(view.config))
  const set = <K extends keyof SNMPConfig>(key: K, value: SNMPConfig[K]) => setCfg({ ...cfg, [key]: value })
  const test = useMutation({ mutationFn: () => testSNMP(address, cfg) })
  const save = useMutation({
    mutationFn: () => saveSNMP(address, cfg),
    onSuccess: (next) => {
      queryClient.setQueryData(["snmp", address], next)
      void queryClient.invalidateQueries({ queryKey: ["reconciliation", address] })
    },
  })
  const stored = (has: boolean) => (has ? "Stored; leave empty to keep" : "")
  const id = (k: string) => `snmp-${address}-${k}`

  return (
    <Section>
      <Collapsible defaultOpen={!view.config.enabled} className="flex flex-col gap-3">
        <SectionHeading
          title={
            <CollapsibleTrigger className="group flex items-center gap-1">
              <ChevronRight className="size-4 transition-transform group-data-[state=open]:rotate-90" />
              SNMP
            </CollapsibleTrigger>
          }
          aside={statusLine(view, now)}
        />
        <CollapsibleContent className="flex flex-col gap-4 rounded-lg border p-4">
          <p className="text-sm text-muted-foreground">
            Fills in interface names and checks Flow totals against the router's own interface counters. Credentials are
            stored encrypted; on RouterOS, allow SNMP only from Flowmon's address.
          </p>
          <label htmlFor={id("enabled")} className="flex items-center gap-3 text-sm">
            <Switch id={id("enabled")} checked={cfg.enabled} onCheckedChange={(v) => set("enabled", v)} />
            Poll this Exporter every minute
          </label>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field id={id("version")} label="Version">
              <Select value={cfg.version} onValueChange={(v) => set("version", v as SNMPConfig["version"])}>
                <SelectTrigger id={id("version")} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="v3">SNMPv3 (recommended)</SelectItem>
                  <SelectItem value="v2c">SNMPv2c</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field id={id("target")} label="Target">
              <Input
                id={id("target")}
                className="font-mono"
                placeholder={`${address}:161`}
                value={cfg.target}
                onChange={(e) => set("target", e.target.value)}
              />
            </Field>
            <Field id={id("overhead")} label="Layer-2 overhead per packet">
              <Select value={cfg.overhead || "ethernet"} onValueChange={(v) => set("overhead", v)}>
                <SelectTrigger id={id("overhead")} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {Object.entries(overheadLabels).map(([k, label]) => (
                    <SelectItem key={k} value={k}>
                      {label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {cfg.overhead === "custom" && (
              <Field id={id("overheadBytes")} label="Custom bytes per packet">
                <Input
                  id={id("overheadBytes")}
                  type="number"
                  min={0}
                  max={64}
                  value={cfg.overheadBytes}
                  onChange={(e) => set("overheadBytes", Number(e.target.value))}
                />
              </Field>
            )}
          </div>
          {cfg.version === "v2c" ? (
            <div className="grid gap-4 sm:grid-cols-3">
              <Field id={id("community")} label="Community">
                <Input
                  id={id("community")}
                  type="password"
                  autoComplete="off"
                  placeholder={stored(view.config.hasCommunity)}
                  value={cfg.community}
                  onChange={(e) => set("community", e.target.value)}
                />
              </Field>
            </div>
          ) : (
            <div className="grid gap-4 sm:grid-cols-3">
              <Field id={id("user")} label="User (community name on RouterOS)">
                <Input
                  id={id("user")}
                  autoComplete="off"
                  value={cfg.user ?? ""}
                  onChange={(e) => set("user", e.target.value)}
                />
              </Field>
              <Field id={id("authProtocol")} label="Authentication protocol">
                <Select
                  value={cfg.authProtocol || "none"}
                  onValueChange={(v) => set("authProtocol", v === "none" ? "" : v)}
                >
                  <SelectTrigger id={id("authProtocol")} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {authProtocols.map(([v, label]) => (
                      <SelectItem key={v} value={v}>
                        {label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field id={id("authPassword")} label="Authentication password">
                <Input
                  id={id("authPassword")}
                  type="password"
                  autoComplete="off"
                  placeholder={stored(view.config.hasAuthPassword)}
                  value={cfg.authPassword}
                  onChange={(e) => set("authPassword", e.target.value)}
                />
              </Field>
              <div className="hidden sm:block" />
              <Field id={id("privProtocol")} label="Privacy (encryption) protocol">
                <Select
                  value={cfg.privProtocol || "none"}
                  onValueChange={(v) => set("privProtocol", v === "none" ? "" : v)}
                >
                  <SelectTrigger id={id("privProtocol")} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {privProtocols.map(([v, label]) => (
                      <SelectItem key={v} value={v}>
                        {label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field id={id("privPassword")} label="Privacy password">
                <Input
                  id={id("privPassword")}
                  type="password"
                  autoComplete="off"
                  placeholder={stored(view.config.hasPrivPassword)}
                  value={cfg.privPassword}
                  onChange={(e) => set("privPassword", e.target.value)}
                />
              </Field>
            </div>
          )}
          {test.isSuccess && (
            <Alert>
              <CheckCircle2 />
              <AlertDescription>
                Connected: {formatNumber(test.data.interfaces.length)} interfaces (
                {test.data.interfaces.slice(0, 6).join(", ")}
                {test.data.interfaces.length > 6 ? ", …" : ""}).
              </AlertDescription>
            </Alert>
          )}
          <FormError error={test.error ?? save.error} />
          <FormActions>
            <Button size="sm" variant="outline" disabled={test.isPending} onClick={() => test.mutate()}>
              {test.isPending ? "Testing…" : "Test connection"}
            </Button>
            <Button size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
              {save.isPending ? "Saving…" : "Save SNMP settings"}
            </Button>
          </FormActions>
        </CollapsibleContent>
      </Collapsible>
      {view.config.enabled && <ReconciliationTable address={address} />}
    </Section>
  )
}

function Cell({ d, threshold }: { d: ReconDirection; threshold: number }) {
  if (d.gap === null) return <span className="text-muted-foreground">Too little traffic</span>
  const pct = d.gap * 100
  return (
    <span className={Math.abs(d.gap) > threshold ? "text-warning-foreground" : undefined}>
      {formatBytes(d.interfaceBytes)} vs {formatBytes(d.flowBytes)}{" "}
      <span className="tabular-nums">
        ({pct > 0 ? "−" : "+"}
        {Math.abs(pct).toFixed(1)}%)
      </span>
    </span>
  )
}

function ReconciliationTable({ address }: { address: string }) {
  const query = useQuery({
    queryKey: ["reconciliation", address],
    queryFn: ({ signal }) => fetchReconciliation(address, signal),
    refetchInterval: 5 * 60_000,
  })
  const heading = <SectionHeading title="Reconciliation" />
  if (query.isPending) {
    return (
      <Section>
        {heading}
        <SectionSkeleton rows={2} />
      </Section>
    )
  }
  if (query.isError) {
    return (
      <Section>
        {heading}
        <QueryError title="Couldn't load the Reconciliation" error={query.error} />
      </Section>
    )
  }
  const data = query.data
  const gapPct = Math.round(data.thresholds.gap * 100)
  return (
    <Section>
      {heading}
      {data.interfaces.length === 0 ? (
        <p className="text-sm text-muted-foreground">Mark a WAN Interface above to reconcile its counters.</p>
      ) : (
        <>
          <p className="text-xs text-muted-foreground">
            Bytes on each WAN Interface from SNMP (minus {data.overheadBytes} B per packet) against bytes in Flows, per
            complete hour. A warning needs a gap above {gapPct}% for {data.thresholds.sustainedHours} hours in a row.
          </p>
          {data.interfaces.map((i) => (
            <div key={i.name} className="flex flex-col gap-2">
              {i.warning && (
                <Alert variant="warning">
                  <AlertTriangle />
                  <AlertTitle>{i.name}: Flows and interface counters disagree</AlertTitle>
                  <AlertDescription>
                    Traffic is missing from the Flows or counted twice. Common causes: hardware offload (FastTrack HW,
                    L3HW) that Traffic Flow can't see, a long active timeout, or the wrong overhead preset.
                  </AlertDescription>
                </Alert>
              )}
              <div className="rounded-lg border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{i.name} · hour</TableHead>
                      <TableHead>In: interface vs Flows</TableHead>
                      <TableHead>Out: interface vs Flows</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {i.hours
                      .filter((h) => h.complete)
                      .slice(-6)
                      .reverse()
                      .map((h) => (
                        <TableRow key={h.hour}>
                          <TableCell className="tabular-nums">{formatHour(h.hour)}</TableCell>
                          <TableCell>
                            <Cell d={h.in} threshold={data.thresholds.gap} />
                          </TableCell>
                          <TableCell>
                            <Cell d={h.out} threshold={data.thresholds.gap} />
                          </TableCell>
                        </TableRow>
                      ))}
                    {i.hours.every((h) => !h.complete) && (
                      <TableRow>
                        <TableCell colSpan={3} className="text-muted-foreground">
                          The first complete hour appears after an hour of polling.
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              </div>
            </div>
          ))}
        </>
      )}
    </Section>
  )
}
