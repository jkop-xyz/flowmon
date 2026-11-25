import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { CircleCheck, CircleX, Lock, Pencil, Plus, Trash2 } from "lucide-react"
import { useState, type FormEvent } from "react"
import { Link } from "react-router"

import { Field, FormActions, FormError, QueryError, SectionSkeleton, TimeAgo } from "@/components/page"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import {
  createSource,
  deleteSource,
  fetchSources,
  testSource,
  updateSource,
  type DataSource,
  type SourceHealth,
  type SourceInput,
  type SourceKind,
} from "@/lib/api"

const sourcesKey = ["sources"]

/** A select's value for "none": Radix selects can't hold an empty string. */
const NONE = "__none__"

/** Integrations other than Exporters, such as AdGuard Home: add, edit, test and watch them. */
export function SourcesCard() {
  const query = useQuery({ queryKey: sourcesKey, queryFn: ({ signal }) => fetchSources(signal), refetchInterval: 15_000 })
  // "new:<kind>" while adding one, a source id while editing it.
  const [editing, setEditing] = useState<string | null>(null)
  const kinds = query.data?.kinds ?? []
  return (
    <Card>
      <CardHeader>
        <CardTitle>Data Sources</CardTitle>
        <CardDescription>
          What Flowmon collects besides Flows from Exporters, such as DNS from AdGuard Home. Passwords are stored encrypted
          with <code className="font-mono">secret.key</code>.
        </CardDescription>
        {kinds.length > 0 && editing === null && (
          <CardAction className="flex gap-2">
            {kinds.map((k) => (
              <Button key={k.name} size="sm" variant="outline" onClick={() => setEditing(`new:${k.name}`)}>
                <Plus />
                {k.title}
              </Button>
            ))}
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {query.isPending ? (
          <SectionSkeleton rows={2} />
        ) : query.isError ? (
          <QueryError title="Couldn't load the Data Sources" error={query.error} />
        ) : (
          <>
            {editing?.startsWith("new:") && (
              <SourceForm kind={kinds.find((k) => `new:${k.name}` === editing)!} onDone={() => setEditing(null)} />
            )}
            {query.data.sources.length === 0 && !editing && (
              <p className="text-sm text-muted-foreground">None yet. Nothing besides Flows is collected until you add one.</p>
            )}
            {query.data.sources.map((s) => {
              const kind = kinds.find((k) => k.name === s.kind)
              return editing === s.id && kind ? (
                <SourceForm key={s.id} kind={kind} source={s} onDone={() => setEditing(null)} />
              ) : (
                <SourceRow
                  key={s.id}
                  source={s}
                  kind={kind}
                  now={query.dataUpdatedAt}
                  onEdit={editing === null ? () => setEditing(s.id) : undefined}
                />
              )
            })}
          </>
        )}
      </CardContent>
    </Card>
  )
}

const stateLabels: Record<SourceHealth["state"], string> = {
  disabled: "Off",
  starting: "Starting",
  ok: "Collecting",
  failing: "Failing",
}

function SourceRow({ source: s, kind, now, onEdit }: { source: DataSource; kind?: SourceKind; now: number; onEdit?: () => void }) {
  const h = s.health
  return (
    <div className="flex flex-col gap-2 rounded-lg border p-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{s.name}</span>
        {s.name !== (kind?.title ?? s.kind) && <span className="text-sm text-muted-foreground">{kind?.title ?? s.kind}</span>}
        <Badge variant={h.state === "failing" ? "destructive" : h.state === "ok" ? "default" : "secondary"}>
          {stateLabels[h.state]}
        </Badge>
        <span className="text-xs text-muted-foreground">
          since <TimeAgo iso={h.since} now={now} />
        </span>
        {s.locked ? (
          <span className="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground">
            <Lock className="size-3.5" />
            Set by <code className="font-mono">FLOWMON_SOURCE_*</code>, so it can't be changed here.
          </span>
        ) : (
          onEdit && (
            <Button size="sm" variant="ghost" className="ml-auto" onClick={onEdit}>
              <Pencil />
              Edit
            </Button>
          )
        )}
      </div>
      {(s.settings.url || s.settings.address) && (
        <p className="flex flex-wrap items-center gap-3 text-sm">
          <span className="font-mono text-ident">{s.settings.url || s.settings.address}</span>
          {s.kind === "snmp" && (
            <Link to={`/snmp/${s.id}`} className="underline underline-offset-4">
              Its interfaces
            </Link>
          )}
        </p>
      )}
      <dl className="grid gap-x-4 gap-y-1 text-sm sm:grid-cols-[auto_1fr]">
        {h.lastSuccess && (
          <>
            <dt className="text-muted-foreground">Last success</dt>
            <dd>
              <TimeAgo iso={h.lastSuccess} now={now} />
              {h.detail && <span className="text-muted-foreground"> · {h.detail}</span>}
            </dd>
          </>
        )}
        {h.lastError && h.error && (
          <>
            <dt className="text-muted-foreground">Last error</dt>
            <dd>
              <TimeAgo iso={h.lastError} now={now} /> · <span className="text-destructive">{h.error}</span>
            </dd>
          </>
        )}
      </dl>
      {h.warnings?.map((w) => (
        <Alert key={w} variant="warning">
          <AlertDescription>{w}</AlertDescription>
        </Alert>
      ))}
    </div>
  )
}

/** Adds a source of kind, or edits source. Secret fields left empty keep what is stored. */
function SourceForm({ kind, source, onDone }: { kind: SourceKind; source?: DataSource; onDone: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(source?.name ?? kind.title)
  const [enabled, setEnabled] = useState(source?.enabled ?? true)
  const [settings, setSettings] = useState<Record<string, string>>(() => {
    const out: Record<string, string> = {}
    for (const f of kind.fields) out[f.key] = source?.settings[f.key] ?? (f.type === "secret" ? "" : (f.default ?? ""))
    return out
  })
  const input = (): SourceInput => ({ kind: kind.name, name, enabled, settings })
  const refresh = () => void queryClient.invalidateQueries({ queryKey: sourcesKey })
  const save = useMutation({
    mutationFn: () => (source ? updateSource(source.id, input()) : createSource(input())),
    onSuccess: () => {
      refresh()
      void queryClient.invalidateQueries({ queryKey: ["domains"] })
      onDone()
    },
  })
  const remove = useMutation({
    mutationFn: () => deleteSource(source!.id),
    onSuccess: () => {
      refresh()
      onDone()
    },
  })
  const test = useMutation({ mutationFn: () => testSource({ ...input(), id: source?.id }) })
  const id = (k: string) => `source-${source?.id ?? "new"}-${k}`
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate()
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4 rounded-lg border p-4">
      <p className="text-sm text-muted-foreground">{kind.description}</p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={id("name")} label="Name">
          <Input id={id("name")} value={name} onChange={(e) => setName(e.target.value)} required />
        </Field>
        {kind.fields.map((f) =>
          f.type === "select" ? (
            <Field key={f.key} id={id(f.key)} label={f.label} hint={f.help}>
              <Select
                value={settings[f.key] || NONE}
                onValueChange={(v) => setSettings({ ...settings, [f.key]: v === NONE ? "" : v })}
              >
                <SelectTrigger id={id(f.key)} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(f.options ?? []).map((o) => (
                    <SelectItem key={o || NONE} value={o || NONE}>
                      {o || "None"}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          ) : f.type === "bool" ? (
            <label key={f.key} htmlFor={id(f.key)} className="flex items-center gap-3 self-end text-sm">
              <Switch
                id={id(f.key)}
                checked={settings[f.key] === "true"}
                onCheckedChange={(v) => setSettings({ ...settings, [f.key]: v ? "true" : "false" })}
              />
              <span>
                {f.label}
                {f.help && <span className="block text-xs text-muted-foreground">{f.help}</span>}
              </span>
            </label>
          ) : (
            <Field key={f.key} id={id(f.key)} label={f.label} hint={f.help}>
              <Input
                id={id(f.key)}
                type={f.type === "secret" ? "password" : f.type === "url" ? "url" : "text"}
                autoComplete={f.type === "secret" ? "new-password" : "off"}
                className={f.type === "url" ? "font-mono" : undefined}
                required={f.required && !(f.type === "secret" && source?.secretsSet.includes(f.key))}
                placeholder={f.type === "secret" && source?.secretsSet.includes(f.key) ? "Stored; leave empty to keep" : undefined}
                value={settings[f.key]}
                onChange={(e) => setSettings({ ...settings, [f.key]: e.target.value })}
              />
            </Field>
          ),
        )}
      </div>
      <label htmlFor={id("enabled")} className="flex items-center gap-3 text-sm">
        <Switch id={id("enabled")} checked={enabled} onCheckedChange={setEnabled} />
        Collect from it
      </label>
      {test.data && (
        <p className={test.data.ok ? "flex items-center gap-2 text-sm" : "flex items-center gap-2 text-sm text-destructive"}>
          {test.data.ok ? <CircleCheck className="size-4 shrink-0 text-area" /> : <CircleX className="size-4 shrink-0" />}
          {test.data.message}
        </p>
      )}
      <FormError error={test.error ?? save.error ?? remove.error} />
      <FormActions>
        {source && (
          <Button
            type="button"
            variant="ghost"
            className="mr-auto text-destructive"
            disabled={remove.isPending}
            onClick={() => {
              if (confirm(`Delete ${source.name}? What it collected stays until its retention ends.`)) remove.mutate()
            }}
          >
            <Trash2 />
            Delete
          </Button>
        )}
        <Button type="button" variant="outline" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? "Testing…" : "Test"}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {source ? "Save" : "Add"}
        </Button>
      </FormActions>
    </form>
  )
}
