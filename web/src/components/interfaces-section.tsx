import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, History, RefreshCw } from "lucide-react"
import { useState } from "react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  fetchInterfaces,
  setClassification,
  setInterfaceName,
  startReprocess,
  type ClassificationMode,
  type InterfaceRole,
  type InterfacesResponse,
  type InterfaceView,
} from "@/lib/api"
import { formatDateTime, formatNumber } from "@/lib/format"

const NO_ROLE = "__none__"

const roleLabels: Record<Exclude<InterfaceRole, "">, string> = {
  wan: "WAN",
  lan: "LAN",
  tunnel: "Tunnel",
}

/** Interfaces, Interface Roles and the Classification Mode of an approved Exporter. */
export function InterfacesSection({ address }: { address: string }) {
  const query = useQuery({
    queryKey: ["interfaces", address],
    queryFn: ({ signal }) => fetchInterfaces(address, signal),
    refetchInterval: (q) => (q.state.data?.reprocess.running ? 2_000 : 15_000),
  })
  if (!query.data) return null
  // Remount the editor when the saved state changes, so drafts start from it.
  const key = `${query.data.classificationMode}|${query.data.interfaces.map((i) => `${i.index}:${i.name}:${i.role}`).join(",")}`
  return <InterfacesEditor key={key} address={address} data={query.data} />
}

function InterfacesEditor({ address, data }: { address: string; data: InterfacesResponse }) {
  const queryClient = useQueryClient()
  const [mode, setMode] = useState<ClassificationMode>(data.classificationMode)
  const [roles, setRoles] = useState<Record<string, InterfaceRole>>(() =>
    Object.fromEntries(data.interfaces.filter((i) => i.name).map((i) => [i.name, i.role])),
  )
  const [saved, setSaved] = useState(false)
  const refresh = (next: InterfacesResponse) => queryClient.setQueryData(["interfaces", address], next)

  const save = useMutation({
    mutationFn: () => setClassification(address, mode, roles),
    onSuccess: (next) => {
      setSaved(true)
      refresh(next)
    },
  })
  const reprocess = useMutation({
    mutationFn: () => startReprocess(address),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["interfaces", address] }),
  })

  const dirty =
    mode !== data.classificationMode || data.interfaces.some((i) => i.name && (roles[i.name] ?? "") !== i.role)
  const hasWAN = Object.values(roles).includes("wan")
  const rp = data.reprocess
  const thisJob = rp.exporter === address

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="text-sm font-medium">Interfaces</h3>
        <p className="text-xs text-muted-foreground">
          Flows are counted since Flowmon started. Interface 0 means the Exporter itself.
        </p>
      </div>

      {data.changes.length > 0 && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Interface mappings changed</AlertTitle>
          <AlertDescription>
            <ul className="list-disc pl-4">
              {data.changes.map((c, i) => (
                <li key={i}>
                  {c.conflict
                    ? `The Exporter reports interface ${c.index} as "${c.newName}" (${c.source}), but you named it "${c.oldName}".`
                    : `Interface ${c.index} changed from "${c.oldName}" to "${c.newName}" (${c.source}).`}{" "}
                  <span className="text-muted-foreground">{formatDateTime(c.at)}</span>
                </li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}

      {!data.hasInterfaceNumbers ? (
        <p className="text-sm text-muted-foreground">
          This Exporter's Flows carry no interface numbers, so it can only be classified by address.
        </p>
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-12">#</TableHead>
                <TableHead>Name</TableHead>
                <TableHead className="w-40">Role</TableHead>
                <TableHead className="text-right">Flows in</TableHead>
                <TableHead className="text-right">Flows out</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.interfaces.map((i) => (
                <InterfaceRow
                  key={i.index}
                  address={address}
                  iface={i}
                  role={i.name ? (roles[i.name] ?? "") : ""}
                  onRole={(role) => setRoles({ ...roles, [i.name]: role })}
                  onSaved={refresh}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <div className="flex flex-col gap-3 rounded-lg border p-3">
        <Label>Classification</Label>
        <RadioGroup
          value={mode}
          onValueChange={(v) => setMode(v as ClassificationMode)}
          className="flex flex-wrap gap-6"
        >
          <label className="flex items-center gap-2 text-sm">
            <RadioGroupItem value="address" /> By address (Local Networks)
          </label>
          <label className="flex items-center gap-2 text-sm">
            <RadioGroupItem value="interface" disabled={!data.hasInterfaceNumbers} /> By interface (WAN Interfaces)
          </label>
        </RadioGroup>
        {mode === "interface" && !hasWAN && (
          <p className="text-sm text-muted-foreground">
            Mark at least one WAN Interface. Flows crossing interfaces without a role are still classified by address.
          </p>
        )}
        {save.error && <p className="text-sm text-destructive">{save.error.message}</p>}
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
            Save classification
          </Button>
          <span className="text-xs text-muted-foreground">Applies to Flows from now on.</span>
        </div>
      </div>

      {(saved || (thisJob && (rp.running || rp.finishedAt))) && (
        <Alert>
          <History />
          <AlertTitle>
            {thisJob && rp.running
              ? `Reprocessing… ${rp.daysDone} day${rp.daysDone === 1 ? "" : "s"} done`
              : thisJob && rp.finishedAt
                ? rp.error
                  ? "Reprocessing failed"
                  : `Reprocessed ${formatNumber(rp.flows)} Flows`
                : "Reprocess stored Flows?"}
          </AlertTitle>
          <AlertDescription className="flex flex-col gap-2">
            {thisJob && rp.error ? (
              <span>{rp.error}</span>
            ) : thisJob && rp.finishedAt && !rp.running ? (
              <span>
                Flows and Rollups since {formatDateTime(rp.from ?? data.reprocessFrom)} now use the current
                classification. Older history is unchanged; charts mark the change.
              </span>
            ) : (
              <span>
                The new classification applies from now on. Flowmon can also re-classify the raw Flows it still has
                (since {formatDateTime(data.reprocessFrom)}), using the interface names that were valid at the time, and
                rebuild their Rollups. Older history stays as it was.
              </span>
            )}
            {!rp.running && (
              <div>
                <Button size="sm" variant="outline" disabled={reprocess.isPending} onClick={() => reprocess.mutate()}>
                  <RefreshCw /> Reprocess the last days
                </Button>
              </div>
            )}
            {reprocess.error && <span className="text-destructive">{reprocess.error.message}</span>}
          </AlertDescription>
        </Alert>
      )}
    </section>
  )
}

function InterfaceRow({
  address,
  iface,
  role,
  onRole,
  onSaved,
}: {
  address: string
  iface: InterfaceView
  role: InterfaceRole
  onRole: (role: InterfaceRole) => void
  onSaved: (next: InterfacesResponse) => void
}) {
  const [name, setName] = useState(iface.name)
  const rename = useMutation({ mutationFn: () => setInterfaceName(address, iface.index, name), onSuccess: onSaved })
  const commit = () => {
    if (name.trim() && name !== iface.name) rename.mutate()
  }

  return (
    <TableRow>
      <TableCell className="tabular-nums text-muted-foreground">{iface.index}</TableCell>
      <TableCell>
        <div className="flex items-center gap-2">
          <Input
            className="h-8 max-w-56 font-mono text-xs"
            placeholder="Name this interface"
            value={name}
            maxLength={64}
            aria-label={`Name of interface ${iface.index}`}
            onChange={(e) => setName(e.target.value)}
            onBlur={commit}
            onKeyDown={(e) => e.key === "Enter" && commit()}
          />
          {iface.suggestedWan && <Badge variant="outline">Suggested WAN</Badge>}
          {iface.source && iface.source !== "user" && (
            <span className="text-xs text-muted-foreground">from {iface.source}</span>
          )}
        </div>
        {rename.error && <p className="text-xs text-destructive">{rename.error.message}</p>}
      </TableCell>
      <TableCell>
        <Select
          value={role || NO_ROLE}
          disabled={!iface.name}
          onValueChange={(v) => onRole(v === NO_ROLE ? "" : (v as InterfaceRole))}
        >
          <SelectTrigger size="sm" className="w-32" aria-label={`Role of interface ${iface.index}`}>
            <SelectValue placeholder={iface.name ? "No role" : "Name it first"} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={NO_ROLE}>No role</SelectItem>
            {Object.entries(roleLabels).map(([value, label]) => (
              <SelectItem key={value} value={value}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </TableCell>
      <TableCell className="text-right tabular-nums">{formatNumber(iface.flowsIn)}</TableCell>
      <TableCell className="text-right tabular-nums">{formatNumber(iface.flowsOut)}</TableCell>
    </TableRow>
  )
}
