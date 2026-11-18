import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, CheckCircle2, ChevronRight } from "lucide-react"
import { useState } from "react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
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
import { formatAgo, formatBytes } from "@/lib/format"

const presetLabels: Record<string, string> = {
  none: "None (0 B)",
  ethernet: "Ethernet (14 B)",
  "ethernet-vlan": "Ethernet + VLAN (18 B)",
  pppoe: "PPPoE over Ethernet (22 B)",
  custom: "Custom",
}

/** SNMP settings and Reconciliation for an approved Exporter. */
export function SNMPSection({ address }: { address: string }) {
  const query = useQuery({ queryKey: ["snmp", address], queryFn: ({ signal }) => fetchSNMP(address, signal) })
  if (!query.data) return null
  return <SNMPEditor key={JSON.stringify(query.data.config)} address={address} view={query.data} />
}

function Field({ id, label, children }: { id: string; label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id} className="text-xs">
        {label}
      </Label>
      {children}
    </div>
  )
}

function SNMPEditor({ address, view }: { address: string; view: SNMPView }) {
  const queryClient = useQueryClient()
  const [cfg, setCfg] = useState<SNMPConfig>({ ...view.config, community: "", authPassword: "", privPassword: "" })
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
    <section className="flex flex-col gap-3">
      <Collapsible defaultOpen={!view.config.enabled}>
        <div className="flex flex-wrap items-center gap-2">
          <CollapsibleTrigger className="group flex items-center gap-1 text-sm font-medium">
            <ChevronRight className="size-4 transition-transform group-data-[state=open]:rotate-90" />
            SNMP
          </CollapsibleTrigger>
          <span className="text-xs text-muted-foreground">
            {view.config.enabled
              ? view.status.lastError
                ? `Polling fails: ${view.status.lastError}`
                : view.status.lastPoll
                  ? `Polled ${formatAgo(view.status.lastPoll, Date.parse(view.status.lastPoll))}; ${view.status.interfaces} interface names`
                  : "Enabled; waiting for the first poll"
              : "Off. Fills in interface names and checks Flow totals against the router's own counters."}
          </span>
        </div>
        <CollapsibleContent className="mt-3 flex flex-col gap-4 rounded-lg border p-3">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={cfg.enabled} onChange={(e) => set("enabled", e.target.checked)} />
            Poll this Exporter every minute
          </label>
          <div className="grid gap-3 sm:grid-cols-3">
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
                  {Object.entries(presetLabels).map(([k, label]) => (
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
            {cfg.version === "v2c" ? (
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
            ) : (
              <>
                <Field id={id("user")} label="User">
                  <Input
                    id={id("user")}
                    autoComplete="off"
                    value={cfg.user ?? ""}
                    onChange={(e) => set("user", e.target.value)}
                  />
                </Field>
                <Field id={id("auth")} label="Authentication">
                  <div className="flex gap-2">
                    <Select
                      value={cfg.authProtocol || "none"}
                      onValueChange={(v) => set("authProtocol", v === "none" ? "" : v)}
                    >
                      <SelectTrigger className="w-28">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {["none", "SHA", "SHA256", "SHA384", "SHA512", "SHA224", "MD5"].map((p) => (
                          <SelectItem key={p} value={p}>
                            {p === "none" ? "None" : p === "SHA" ? "SHA (SHA1, RouterOS)" : p}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <Input
                      id={id("auth")}
                      type="password"
                      autoComplete="off"
                      placeholder={stored(view.config.hasAuthPassword) || "Password"}
                      value={cfg.authPassword}
                      onChange={(e) => set("authPassword", e.target.value)}
                    />
                  </div>
                </Field>
                <Field id={id("priv")} label="Privacy (encryption)">
                  <div className="flex gap-2">
                    <Select
                      value={cfg.privProtocol || "none"}
                      onValueChange={(v) => set("privProtocol", v === "none" ? "" : v)}
                    >
                      <SelectTrigger className="w-28">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {["none", "AES256", "AES192", "AES", "DES"].map((p) => (
                          <SelectItem key={p} value={p}>
                            {p === "none" ? "None" : p}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <Input
                      id={id("priv")}
                      type="password"
                      autoComplete="off"
                      placeholder={stored(view.config.hasPrivPassword) || "Password"}
                      value={cfg.privPassword}
                      onChange={(e) => set("privPassword", e.target.value)}
                    />
                  </div>
                </Field>
              </>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            Credentials are stored encrypted. On RouterOS, allow SNMP only from Flowmon's address.
          </p>
          {test.isSuccess && (
            <Alert>
              <CheckCircle2 />
              <AlertDescription>
                Connected: {test.data.interfaces.length} interfaces ({test.data.interfaces.slice(0, 6).join(", ")}
                {test.data.interfaces.length > 6 ? ", …" : ""}).
              </AlertDescription>
            </Alert>
          )}
          {(test.error ?? save.error) && (
            <p className="text-sm text-destructive">{(test.error ?? save.error)?.message}</p>
          )}
          <div className="flex gap-2">
            <Button size="sm" variant="outline" disabled={test.isPending} onClick={() => test.mutate()}>
              {test.isPending ? "Testing…" : "Test connection"}
            </Button>
            <Button size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
              Save
            </Button>
          </div>
        </CollapsibleContent>
      </Collapsible>
      {view.config.enabled && <ReconciliationTable address={address} />}
    </section>
  )
}

function Cell({ d }: { d: ReconDirection }) {
  if (d.gap === null) return <span className="text-muted-foreground">too little traffic</span>
  const pct = d.gap * 100
  return (
    <span className={Math.abs(d.gap) > 0.15 ? "text-destructive" : undefined}>
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
  const data = query.data
  if (!data) return null
  if (data.interfaces.length === 0) {
    return <p className="text-sm text-muted-foreground">Mark a WAN Interface above to reconcile its counters.</p>
  }
  return (
    <div className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">Reconciliation</h3>
      <p className="text-xs text-muted-foreground">
        Bytes on each WAN Interface from SNMP (minus {data.overheadBytes} B per packet) against bytes in Flows, per
        complete hour. A warning needs a gap above {data.thresholds.gap * 100}% for {data.thresholds.sustainedHours}{" "}
        hours in a row.
      </p>
      {data.interfaces.map((i) => (
        <div key={i.name} className="flex flex-col gap-2">
          {i.warning && (
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertTitle>{i.name}: Flows and interface counters disagree</AlertTitle>
              <AlertDescription>
                Traffic is missing from the Flows or counted twice. Common causes: hardware offload (FastTrack HW, L3HW)
                that Traffic Flow can't see, a long active timeout, or the wrong overhead preset.
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
                      <TableCell className="tabular-nums">
                        {new Date(h.hour).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" })}
                      </TableCell>
                      <TableCell>
                        <Cell d={h.in} />
                      </TableCell>
                      <TableCell>
                        <Cell d={h.out} />
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
    </div>
  )
}
