import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, History, RefreshCw } from "lucide-react"
import { Fragment, useState } from "react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { ClassificationModeField } from "@/components/classification-mode-field"
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
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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
import { roleHelp, snmpAndRoles } from "@/lib/classification-help"
import { formatDateTime, formatNumber } from "@/lib/format"

const NO_ROLE = "__none__"

/** Interfaces, Interface Roles and the Classification Mode of an approved Exporter. */
export function InterfacesSection({ address }: { address: string }) {
  const query = useQuery({
    queryKey: ["interfaces", address],
    queryFn: ({ signal }) => fetchInterfaces(address, signal),
    refetchInterval: (q) => (q.state.data?.reprocess.running ? 2_000 : 15_000),
  })
  if (query.isPending) {
    return (
      <Section>
        <SectionHeading title="Interfaces" />
        <SectionSkeleton />
      </Section>
    )
  }
  if (query.isError) {
    return (
      <Section>
        <SectionHeading title="Interfaces" />
        <QueryError title="Couldn't load Interfaces" error={query.error} />
      </Section>
    )
  }
  // Remount the editor when the saved state changes, so drafts start from it.
  const key = `${query.data.classificationMode}|${query.data.interfaces.map((i) => `${i.index}:${i.name}:${i.role}`).join(",")}`
  return <InterfacesEditor key={key} address={address} data={query.data} now={query.dataUpdatedAt} />
}

function InterfacesEditor({ address, data, now }: { address: string; data: InterfacesResponse; now: number }) {
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
    <Section>
      <SectionHeading
        title="Interfaces"
        aside="Flows counted since Flowmon started. Interface 0 is the Exporter itself."
      />

      {data.changes.length > 0 && (
        <Alert variant="warning">
          <AlertTriangle />
          <AlertTitle>Interface mappings changed</AlertTitle>
          <AlertDescription>
            <ul className="list-disc pl-4">
              {data.changes.map((c, i) => (
                <li key={i}>
                  {c.conflict
                    ? `The Exporter reports interface ${c.index} as "${c.newName}" (${c.source}), but you named it "${c.oldName}".`
                    : `Interface ${c.index} changed from "${c.oldName}" to "${c.newName}" (${c.source}).`}{" "}
                  <span className="text-muted-foreground">
                    (<TimeAgo iso={c.at} now={now} />)
                  </span>
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
                <TableHead className="w-36">Interface Role</TableHead>
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
      {data.hasInterfaceNumbers && <RoleKey />}

      <Field label="Classification" hint="Applies to Flows from now on.">
        <ClassificationModeField
          idPrefix={`interfaces-${address}`}
          value={mode}
          onChange={setMode}
          interfaceAvailable={data.hasInterfaceNumbers}
        />
      </Field>
      {mode === "interface" && !hasWAN && (
        <p className="text-sm text-muted-foreground">
          Mark at least one WAN Interface. Flows crossing interfaces without a role are still classified by address.
        </p>
      )}
      <FormError error={save.error} />
      <FormActions>
        <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? "Saving…" : "Save classification"}
        </Button>
      </FormActions>

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
                  <RefreshCw /> {reprocess.isPending ? "Starting…" : "Reprocess the last days"}
                </Button>
              </div>
            )}
            <FormError error={reprocess.error} />
          </AlertDescription>
        </Alert>
      )}
    </Section>
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
            className="h-8 w-full max-w-56 min-w-32 font-mono text-ident"
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
        <FormError error={rename.error} />
      </TableCell>
      <TableCell>
        <Select
          value={role || NO_ROLE}
          disabled={!iface.name}
          onValueChange={(v) => onRole(v === NO_ROLE ? "" : (v as InterfaceRole))}
        >
          <SelectTrigger size="sm" className="w-full" aria-label={`Role of interface ${iface.index}`}>
            <SelectValue placeholder={iface.name ? "No role" : "Name it first"} />
          </SelectTrigger>
          <SelectContent>
            {roleHelp.map((r) => (
              <SelectItem key={r.role || NO_ROLE} value={r.role || NO_ROLE}>
                {r.label}
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

/** What each Interface Role means, under the table where roles are picked. */
function RoleKey() {
  return (
    <div className="flex flex-col gap-2 text-xs text-muted-foreground">
      <dl className="grid gap-x-3 gap-y-1 sm:grid-cols-[auto_1fr]">
        {roleHelp.map((r) => (
          <Fragment key={r.label}>
            <dt className="font-medium text-foreground">{r.label}</dt>
            <dd>{r.meaning}</dd>
          </Fragment>
        ))}
      </dl>
      <p>{snmpAndRoles}</p>
    </div>
  )
}
