import { useMutation, useQueryClient } from "@tanstack/react-query"
import { ExternalLink, RefreshCw } from "lucide-react"
import { useState, type FormEvent } from "react"

import { EnvLock } from "@/components/collection-settings"
import { Field, FormActions, FormError } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { retryGeoIP, saveGeoIP, type GeoIPStatus } from "@/lib/api"

function useGeoIPMutation<A>(fn: (a: A) => Promise<GeoIPStatus>) {
  const client = useQueryClient()
  return useMutation({ mutationFn: fn, onSuccess: (s) => client.setQueryData(["geoip"], s) })
}

/** Whether Flowmon downloads databases itself, and the MaxMind account it downloads GeoLite2 with. */
export function GeoIPSettings({ status }: { status: GeoIPStatus }) {
  const toggle = useGeoIPMutation(saveGeoIP)
  const retry = useGeoIPMutation(retryGeoIP)
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <div className="flex items-center gap-3">
          <Switch
            id="geoip-download"
            checked={status.downloadsEnabled}
            disabled={status.locks.download || toggle.isPending}
            onCheckedChange={(on: boolean) => toggle.mutate({ download: on })}
          />
          <Label htmlFor="geoip-download">Download databases</Label>
          {status.downloadsEnabled && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="ml-auto"
              disabled={status.downloading || retry.isPending}
              onClick={() => retry.mutate(undefined)}
            >
              <RefreshCw className={status.downloading ? "animate-spin" : undefined} />
              {status.downloading ? "Checking…" : "Check now"}
            </Button>
          )}
        </div>
        <p className="text-xs text-muted-foreground">
          {status.locks.download ? (
            <EnvLock env="FLOWMON_GEOIP_DOWNLOAD" />
          ) : status.downloadsEnabled ? (
            "Flowmon downloads and updates the databases itself each week: MaxMind GeoLite2 with an account below, DB-IP Lite without one. Check now retries a failed download at once."
          ) : (
            "Off: only MMDB files you put in the GeoIP folder are used."
          )}
        </p>
        <FormError error={toggle.error ?? retry.error} />
      </div>
      <MaxMindForm key={status.maxMindAccountId ?? ""} status={status} />
    </div>
  )
}

function MaxMindForm({ status }: { status: GeoIPStatus }) {
  const [accountId, setAccountId] = useState(status.maxMindAccountId ?? "")
  const [licenseKey, setLicenseKey] = useState("")
  const save = useGeoIPMutation(saveGeoIP)
  const locked = status.locks.maxMind
  const saved = status.maxMindAccountId ?? ""
  const changed = accountId.trim() !== saved || licenseKey.trim() !== ""
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(
      { maxMindAccountId: accountId.trim(), maxMindLicenseKey: licenseKey.trim() },
      { onSuccess: () => setLicenseKey("") },
    )
  }
  const remove = () => save.mutate({ maxMindAccountId: "" }, { onSuccess: () => setAccountId("") })
  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div>
        <h3 className="text-sm font-medium">MaxMind GeoLite2 account</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          GeoLite2 is a little more detailed than DB-IP Lite, and free with an account.{" "}
          <a
            href="https://www.maxmind.com/en/geolite2/signup"
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 underline underline-offset-4"
          >
            Sign up at MaxMind
            <ExternalLink className="size-3" />
          </a>
          , then create a license key under Manage License Keys. The key is stored encrypted.
        </p>
      </div>
      {locked ? (
        <EnvLock env="FLOWMON_MAXMIND_ACCOUNT_ID" />
      ) : (
        <>
          <div className="grid gap-4 md:grid-cols-2">
            <Field id="maxmind-account" label="Account ID">
              <Input
                id="maxmind-account"
                inputMode="numeric"
                autoComplete="off"
                placeholder="123456"
                value={accountId}
                onChange={(e) => setAccountId(e.target.value)}
              />
            </Field>
            <Field
              id="maxmind-key"
              label="License key"
              hint={status.maxMindConfigured ? "Saved. Leave empty to keep it." : undefined}
            >
              <Input
                id="maxmind-key"
                type="password"
                autoComplete="new-password"
                placeholder={status.maxMindConfigured ? "••••••••••••" : ""}
                value={licenseKey}
                onChange={(e) => setLicenseKey(e.target.value)}
              />
            </Field>
          </div>
          {status.maxMindWarning && <p className="text-sm text-warning-foreground">{status.maxMindWarning}</p>}
          <FormError error={save.error} />
          <FormActions>
            {saved && (
              <Button type="button" size="sm" variant="ghost" onClick={remove} disabled={save.isPending}>
                Remove account
              </Button>
            )}
            <Button type="submit" size="sm" disabled={!changed || save.isPending || accountId.trim() === ""}>
              {saved ? "Save" : "Save and download"}
            </Button>
          </FormActions>
        </>
      )}
    </form>
  )
}
