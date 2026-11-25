import { useMutation, useQueryClient } from "@tanstack/react-query"
import { CircleCheck, CircleX, Lock, Plus, Trash2 } from "lucide-react"
import { useState, type FormEvent } from "react"
import { Link, useSearchParams } from "react-router"

import { Field, FormActions, FormError, QueryError, SectionSkeleton, TimeAgo } from "@/components/page"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import {
  createSource,
  deleteSource,
  testSource,
  updateSource,
  type DataSource,
  type SourceHealth,
  type SourceInput,
  type SourceKind,
} from "@/lib/api"
import { kindPlural, sourcesKey, useSources } from "@/lib/sources"
import { cn } from "@/lib/utils"

/** A select's value for "none": Radix selects can't hold an empty string. */
const NONE = "__none__"

const stateDot: Record<SourceHealth["state"], string> = {
  disabled: "bg-muted-foreground/40",
  starting: "bg-muted-foreground",
  ok: "bg-emerald-600",
  failing: "bg-destructive",
}

/**
 * The sources of one kind, such as every AdGuard Home: a list beside the selected one's status and settings.
 * ?source=<id> in the URL picks which one is shown.
 */
export function SourceKindPanel({ kindName }: { kindName: string }) {
  const query = useSources()
  const [params, setParams] = useSearchParams()
  // A remount resets the form after Cancel.
  const [formKey, setFormKey] = useState(0)
  if (query.isPending) return <SectionSkeleton rows={3} />
  if (query.isError) return <QueryError title="Couldn't load the Data Sources" error={query.error} />
  const kind = query.data.kinds.find((k) => k.name === kindName)
  if (!kind)
    return <QueryError title="No such kind of Data Source" error={new Error(`Flowmon doesn't know "${kindName}".`)} />
  const sources = query.data.sources.filter((s) => s.kind === kindName)
  const picked = params.get("source")
  const adding = picked === "new" || sources.length === 0
  const selected = adding ? undefined : (sources.find((s) => s.id === picked) ?? sources[0])
  const pick = (id: string | null) => {
    const next = new URLSearchParams(params)
    if (id) next.set("source", id)
    else next.delete("source")
    setParams(next, { replace: true })
    setFormKey((n) => n + 1)
  }
  const now = query.dataUpdatedAt
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col items-start justify-between gap-3 sm:flex-row">
        <div className="min-w-0">
          <h2 className="text-lg font-semibold">{kindPlural(kind)}</h2>
          <p className="max-w-3xl text-sm text-muted-foreground">{kind.description}</p>
        </div>
        {!adding && (
          <Button size="sm" className="shrink-0" onClick={() => pick("new")}>
            <Plus />
            Add {kind.title}
          </Button>
        )}
      </div>
      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,18rem)_minmax(0,1fr)]">
        {sources.length > 0 && (
          <div className="flex flex-col gap-2">
            {sources.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => pick(s.id)}
                className={cn(
                  "flex items-center gap-3 rounded-lg border bg-card px-3 py-2.5 text-left transition-colors hover:bg-muted",
                  s.id === selected?.id && "border-ring ring-1 ring-ring",
                )}
              >
                <span className={cn("size-2 shrink-0 rounded-full", stateDot[s.health.state])} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1.5 truncate font-medium">
                    {s.name}
                    {s.locked && (
                      <Lock
                        className="size-3.5 shrink-0 text-muted-foreground"
                        aria-label="Set by environment variables"
                      />
                    )}
                  </span>
                  <span className="block truncate font-mono text-xs text-muted-foreground">
                    {s.settings.url || s.settings.address}
                  </span>
                </span>
                <span
                  className={cn("text-xs", s.health.state === "failing" ? "text-destructive" : "text-muted-foreground")}
                >
                  {stateLabels[s.health.state]}
                </span>
              </button>
            ))}
            {adding && (
              <div className="flex items-center gap-3 rounded-lg border border-dashed border-ring px-3 py-2.5 text-sm">
                <Plus className="size-4" />
                New {kind.title}
              </div>
            )}
          </div>
        )}
        <Card className={sources.length === 0 ? "lg:col-span-2" : undefined}>
          <CardContent className="flex flex-col gap-4">
            {selected ? (
              <>
                <SourceRow source={selected} kind={kind} now={now} />
                {!selected.locked && (
                  <SourceForm
                    key={`${selected.id}/${formKey}`}
                    kind={kind}
                    source={selected}
                    onDone={(saved) => pick(saved?.id ?? null)}
                    onCancel={() => setFormKey((n) => n + 1)}
                  />
                )}
              </>
            ) : (
              <>
                {sources.length === 0 && (
                  <p className="text-sm text-muted-foreground">None yet. Add one to start collecting from it.</p>
                )}
                <SourceForm
                  key={`new/${formKey}`}
                  kind={kind}
                  onDone={(saved) => pick(saved?.id ?? null)}
                  onCancel={sources.length > 0 ? () => pick(null) : undefined}
                />
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

const stateLabels: Record<SourceHealth["state"], string> = {
  disabled: "Off",
  starting: "Starting",
  ok: "Collecting",
  failing: "Failing",
}

function SourceRow({ source: s, kind, now }: { source: DataSource; kind?: SourceKind; now: number }) {
  const h = s.health
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{s.name}</span>
        {s.name !== (kind?.title ?? s.kind) && (
          <span className="text-sm text-muted-foreground">{kind?.title ?? s.kind}</span>
        )}
        <Badge variant={h.state === "failing" ? "destructive" : h.state === "ok" ? "default" : "secondary"}>
          {stateLabels[h.state]}
        </Badge>
        <span className="text-xs text-muted-foreground">
          since <TimeAgo iso={h.since} now={now} />
        </span>
        {s.locked && (
          <span className="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground">
            <Lock className="size-3.5" />
            Set by <code className="font-mono">FLOWMON_SOURCE_*</code>, so it can't be changed here.
          </span>
        )}
      </div>
      {(s.settings.url || s.settings.address) && (
        <p className="flex flex-wrap items-center gap-3 text-sm">
          <span className="font-mono text-ident">{s.settings.url || s.settings.address}</span>
          {s.kind === "snmp" && (
            <Link to={`/snmp/${s.id}`} className="underline underline-offset-4">
              Interfaces and health
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
function SourceForm({
  kind,
  source,
  onDone,
  onCancel,
}: {
  kind: SourceKind
  source?: DataSource
  /** After saving (with what was saved) or deleting. */
  onDone: (saved?: DataSource) => void
  onCancel?: () => void
}) {
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
    onSuccess: (saved) => {
      refresh()
      void queryClient.invalidateQueries({ queryKey: ["domains"] })
      onDone(saved)
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
    <form onSubmit={submit} className="flex flex-col gap-4">
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
                placeholder={
                  f.type === "secret" && source?.secretsSet.includes(f.key) ? "Stored; leave empty to keep" : undefined
                }
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
        <p
          className={
            test.data.ok ? "flex items-center gap-2 text-sm" : "flex items-center gap-2 text-sm text-destructive"
          }
        >
          {test.data.ok ? (
            <CircleCheck className="size-4 shrink-0 text-area" />
          ) : (
            <CircleX className="size-4 shrink-0" />
          )}
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
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button type="submit" disabled={save.isPending}>
          {source ? "Save" : "Add"}
        </Button>
      </FormActions>
    </form>
  )
}
