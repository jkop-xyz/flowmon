import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Lock, Plus, RotateCcw, X } from "lucide-react"
import { useState, type FormEvent } from "react"
import { Link } from "react-router"

import { Field, FormActions, FormError, QueryError, SectionSkeleton } from "@/components/page"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { fetchSettings, saveSettings, type Settings } from "@/lib/api"

function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: ({ signal }) => fetchSettings(signal) })
}

function useSave() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: saveSettings,
    onSuccess: (s) => client.setQueryData(["settings"], s),
  })
}

export function EnvLock({ env }: { env: string }) {
  return (
    <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
      <Lock className="size-3.5 shrink-0" />
      <span>
        Set by <code className="font-mono">{env}</code>, so it can't be changed here.
      </span>
    </span>
  )
}

/** Which address ranges are yours: they decide every Flow's Direction. */
export function LocalNetworksCard() {
  const query = useSettings()
  return (
    <Card>
      <CardHeader>
        <CardTitle>Local Networks</CardTitle>
        <CardDescription>
          Addresses in these ranges are Local Hosts; everything else is a Remote IP. They decide each Flow's Direction, for
          Exporters that classify by address and for addresses the interfaces don't settle.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {query.isPending ? (
          <SectionSkeleton rows={3} />
        ) : query.isError ? (
          <QueryError title="Couldn't load the settings" error={query.error} />
        ) : (
          <LocalNetworksForm key={query.data.localNetworks.value.join(",")} settings={query.data} />
        )}
      </CardContent>
    </Card>
  )
}

function LocalNetworksForm({ settings }: { settings: Settings }) {
  const { value, defaults, locked, env } = settings.localNetworks
  const [nets, setNets] = useState(value)
  const [adding, setAdding] = useState("")
  const [addError, setAddError] = useState("")
  const save = useSave()
  const dirty = nets.join(",") !== value.join(",")
  const isDefault = nets.join(",") === defaults.join(",")

  const add = (e: FormEvent) => {
    e.preventDefault()
    const p = adding.trim()
    // The server checks properly; this catches typos before they reach the list.
    if (!/^[0-9a-fA-F:.]+\/\d{1,3}$/.test(p)) {
      setAddError("Enter a prefix such as 192.168.88.0/24 or 2001:db8:1::/48.")
      return
    }
    if (!nets.includes(p)) setNets([...nets, p])
    setAdding("")
    setAddError("")
  }

  return (
    <div className="flex flex-col gap-4">
      <ul className="flex flex-wrap gap-2">
        {nets.map((p) => (
          <li key={p}>
            <Badge variant="outline" className="h-7 gap-1 pr-1 font-mono text-ident">
              {p}
              {defaults.includes(p) && <span className="font-sans text-muted-foreground">default</span>}
              {!locked && (
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={`Remove ${p}`}
                  disabled={nets.length === 1}
                  onClick={() => setNets(nets.filter((n) => n !== p))}
                >
                  <X />
                </Button>
              )}
            </Badge>
          </li>
        ))}
      </ul>
      {locked ? (
        <EnvLock env={env} />
      ) : (
        <>
          <form onSubmit={add} className="flex flex-col gap-1">
            <div className="flex gap-2">
              <Input
                value={adding}
                onChange={(e) => setAdding(e.target.value)}
                placeholder="Add a prefix, e.g. 100.64.0.0/10"
                aria-label="Prefix to add"
                className="h-8 w-full font-mono sm:w-72"
              />
              <Button type="submit" size="sm" variant="outline">
                <Plus />
                Add
              </Button>
            </div>
            {addError && <p className="text-sm text-destructive">{addError}</p>}
          </form>
          <p className="text-xs text-muted-foreground">
            Changes apply to Flows from now on, and charts mark when. To classify stored Flows again, reprocess an Exporter
            on the{" "}
            <Link to="/exporters" className="underline underline-offset-4">
              Exporters
            </Link>{" "}
            page.
          </p>
          <FormError error={save.error} />
          <FormActions>
            {!isDefault && (
              <Button size="sm" variant="ghost" onClick={() => setNets(defaults)}>
                <RotateCcw />
                Defaults
              </Button>
            )}
            {dirty && (
              <Button size="sm" variant="ghost" onClick={() => setNets(value)}>
                Cancel
              </Button>
            )}
            <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate({ localNetworks: nets })}>
              Save
            </Button>
          </FormActions>
        </>
      )}
    </div>
  )
}

const retentionFields = [
  { key: "raw", label: "Raw Flows", hint: "Every Flow with all its fields: the Flow Explorer and exact Top Remote IPs." },
  { key: "minute", label: "Minute Rollups", hint: "Totals per minute: charts and top lists for ranges under a day." },
  { key: "hour", label: "Hour Rollups", hint: "Totals per hour: charts and top lists for a day or more." },
  { key: "dns", label: "DNS Queries", hint: "Every lookup a DNS Data Source logged, such as AdGuard Home. Kept apart from Flows; the size cap drops them first." },
] as const

const days = (seconds: number) => Math.round((seconds / 86400) * 100) / 100

/** How long each kind of data is kept. */
export function RetentionCard() {
  const query = useSettings()
  return (
    <Card>
      <CardHeader>
        <CardTitle>Retention</CardTitle>
        <CardDescription>
          How long each kind of data is kept. Older data is dropped a whole day (or month, for hour Rollups) at a time.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {query.isPending ? (
          <SectionSkeleton rows={3} />
        ) : query.isError ? (
          <QueryError title="Couldn't load the settings" error={query.error} />
        ) : (
          <RetentionForm
            key={retentionFields.map((f) => query.data.retention[f.key].seconds).join(",")}
            settings={query.data}
          />
        )}
      </CardContent>
    </Card>
  )
}

function RetentionForm({ settings }: { settings: Settings }) {
  const initial = Object.fromEntries(retentionFields.map((f) => [f.key, String(days(settings.retention[f.key].seconds))]))
  const [values, setValues] = useState<Record<string, string>>(initial)
  const save = useSave()
  const changed = retentionFields.filter((f) => !settings.retention[f.key].locked && values[f.key] !== initial[f.key])
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate({ retentionDays: Object.fromEntries(changed.map((f) => [f.key, Number(values[f.key])])) })
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div className="grid gap-4 md:grid-cols-3">
        {retentionFields.map((f) => {
          const s = settings.retention[f.key]
          return (
            <Field key={f.key} id={`retention-${f.key}`} label={`${f.label} (days)`} hint={s.locked ? <EnvLock env={s.env} /> : f.hint}>
              <Input
                id={`retention-${f.key}`}
                type="number"
                min={settings.minRetentionDays}
                max={settings.maxRetentionDays}
                step="any"
                value={values[f.key]}
                disabled={s.locked}
                onChange={(e) => setValues({ ...values, [f.key]: e.target.value })}
              />
            </Field>
          )
        })}
      </div>
      <p className="text-xs text-muted-foreground">
        Shortening retention drops older data at the next cleanup, within ten minutes. Raw Flows take by far the most disk.
      </p>
      <FormError error={save.error} />
      <FormActions>
        {changed.length > 0 && (
          <Button type="button" size="sm" variant="ghost" onClick={() => setValues(initial)}>
            Cancel
          </Button>
        )}
        <Button type="submit" size="sm" disabled={changed.length === 0 || save.isPending}>
          Save
        </Button>
      </FormActions>
    </form>
  )
}

/** The internet line's speed, which the Overview measures WAN load against. */
export function InternetLineCard() {
  const query = useSettings()
  return (
    <Card>
      <CardHeader>
        <CardTitle>Internet line</CardTitle>
        <CardDescription>
          Your line's download and upload speed, as your provider sells it. The Overview shows how full the line is
          against these, from the router's WAN Interface counters. Empty, it uses the interface's own speed.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {query.isPending ? (
          <SectionSkeleton rows={1} />
        ) : query.isError ? (
          <QueryError title="Couldn't load the settings" error={query.error} />
        ) : (
          <InternetLineForm key={`${query.data.line.downBps}/${query.data.line.upBps}`} settings={query.data} />
        )}
      </CardContent>
    </Card>
  )
}

const mbps = (bps: number) => (bps > 0 ? String(bps / 1_000_000) : "")

function InternetLineForm({ settings }: { settings: Settings }) {
  const initial = { down: mbps(settings.line.downBps), up: mbps(settings.line.upBps) }
  const [values, setValues] = useState(initial)
  const save = useSave()
  const changed = values.down !== initial.down || values.up !== initial.up
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const bps = (v: string) => Math.round((Number(v) || 0) * 1_000_000)
    save.mutate({ line: { downBps: bps(values.down), upBps: bps(values.up) } })
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div className="grid gap-4 md:grid-cols-2">
        <Field id="line-down" label="Download (Mbit/s)">
          <Input
            id="line-down"
            type="number"
            min={0}
            step="any"
            placeholder="500"
            value={values.down}
            onChange={(e) => setValues({ ...values, down: e.target.value })}
          />
        </Field>
        <Field id="line-up" label="Upload (Mbit/s)">
          <Input
            id="line-up"
            type="number"
            min={0}
            step="any"
            placeholder="100"
            value={values.up}
            onChange={(e) => setValues({ ...values, up: e.target.value })}
          />
        </Field>
      </div>
      <FormError error={save.error} />
      <FormActions>
        {changed && (
          <Button type="button" size="sm" variant="ghost" onClick={() => setValues(initial)}>
            Cancel
          </Button>
        )}
        <Button type="submit" size="sm" disabled={!changed || save.isPending}>
          Save
        </Button>
      </FormActions>
    </form>
  )
}
