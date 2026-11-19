import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { useState, type FormEvent } from "react"

import { Field, FormActions, FormError, QueryError, SectionSkeleton, TimeAgo } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { deleteServiceName, fetchServiceNames, setServiceName, type UserServiceName } from "@/lib/api"
import { serviceKey } from "@/lib/services"

/** Protocols a Service Name can be set for; 0 is every protocol. */
const protocols: Record<string, string> = { "6": "TCP", "17": "UDP", "0": "Any protocol" }

const portLabel = (n: { protocol: number; port: number }) =>
  `${n.port}/${n.protocol === 0 ? "any" : protocols[String(n.protocol)]?.toLowerCase() ?? n.protocol}`

function useInvalidate() {
  const client = useQueryClient()
  return () => {
    void client.invalidateQueries({ queryKey: ["service-names"] })
    void client.invalidateQueries({ queryKey: ["user-service-names"] })
  }
}

/** The user's own Service Names, which win over the IANA registry's. */
export function ServiceNamesCard() {
  const query = useQuery({ queryKey: ["user-service-names"], queryFn: ({ signal }) => fetchServiceNames(signal) })
  const [editing, setEditing] = useState<UserServiceName | "new" | null>(null)
  const names = query.data?.names ?? []
  const invalidate = useInvalidate()
  const remove = useMutation({
    mutationFn: (n: UserServiceName) => deleteServiceName(n.protocol, n.port),
    onSuccess: invalidate,
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>Service Names</CardTitle>
        <CardDescription>
          Service Ports are named from the IANA registry, such as 443/tcp https. Name your own services here, such as 8006
          proxmox; your names win.
        </CardDescription>
        {editing === null && (
          <CardAction>
            <Button size="sm" variant="outline" onClick={() => setEditing("new")}>
              <Plus />
              Add name
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {editing !== null && (
          <ServiceNameForm
            key={editing === "new" ? "new" : serviceKey(editing.protocol, editing.port)}
            existing={editing === "new" ? undefined : editing}
            onDone={() => setEditing(null)}
          />
        )}
        {query.isPending ? (
          <SectionSkeleton rows={2} />
        ) : query.isError ? (
          <QueryError title="Couldn't load your Service Names" error={query.error} />
        ) : names.length === 0 ? (
          editing === null && <p className="text-sm text-muted-foreground">You haven't named any ports yet.</p>
        ) : (
          <div className="overflow-x-auto rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Port</TableHead>
                  <TableHead>Your name</TableHead>
                  <TableHead>IANA name</TableHead>
                  <TableHead>Changed</TableHead>
                  <TableHead className="w-0" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {names.map((n) => (
                    <TableRow key={serviceKey(n.protocol, n.port)}>
                      <TableCell className="font-mono text-ident">{portLabel(n)}</TableCell>
                      <TableCell className="max-w-48 truncate" title={n.name}>
                        {n.name}
                      </TableCell>
                      <TableCell className="text-muted-foreground">{n.ianaName ?? "—"}</TableCell>
                      <TableCell className="text-muted-foreground">
                        <TimeAgo iso={n.updatedAt} now={query.dataUpdatedAt} />
                      </TableCell>
                      <TableCell>
                        <div className="flex justify-end gap-1">
                          <Button size="icon-sm" variant="ghost" aria-label={`Rename ${portLabel(n)}`} onClick={() => setEditing(n)}>
                            <Pencil />
                          </Button>
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            aria-label={`Remove the name of ${portLabel(n)}`}
                            disabled={remove.isPending}
                            onClick={() => remove.mutate(n)}
                          >
                            <Trash2 />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        <FormError error={remove.error} />
      </CardContent>
    </Card>
  )
}

function ServiceNameForm({ existing, onDone }: { existing?: UserServiceName; onDone: () => void }) {
  const [port, setPort] = useState(existing ? String(existing.port) : "")
  const [protocol, setProtocol] = useState(existing ? String(existing.protocol) : "6")
  const [name, setName] = useState(existing?.name ?? "")
  const invalidate = useInvalidate()
  const portNumber = Number(port)
  const validPort = Number.isInteger(portNumber) && portNumber >= 1 && portNumber <= 65535
  const save = useMutation({
    mutationFn: () => setServiceName(Number(protocol), portNumber, name.trim()),
    onSuccess: () => {
      invalidate()
      onDone()
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate()
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-4 rounded-lg border p-4">
      <div className="grid gap-4 sm:grid-cols-[8rem_10rem_1fr]">
        <Field id="service-port" label="Port">
          <Input
            id="service-port"
            inputMode="numeric"
            value={port}
            onChange={(e) => setPort(e.target.value.replace(/\D/g, ""))}
            placeholder="8006"
            disabled={!!existing}
            className="font-mono"
            autoFocus={!existing}
          />
        </Field>
        <Field id="service-protocol" label="Protocol">
          <Select value={protocol} onValueChange={setProtocol} disabled={!!existing}>
            <SelectTrigger id="service-protocol" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {Object.entries(protocols).map(([value, label]) => (
                <SelectItem key={value} value={value}>
                  {label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field id="service-name" label="Name">
          <Input
            id="service-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={40}
            placeholder="e.g. proxmox"
            autoFocus={!!existing}
          />
        </Field>
      </div>
      <FormError error={save.error} />
      <FormActions>
        <Button type="button" size="sm" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" size="sm" disabled={save.isPending || !validPort || !name.trim()}>
          Save
        </Button>
      </FormActions>
    </form>
  )
}
