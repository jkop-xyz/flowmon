import { AlertTriangle } from "lucide-react"

import { LocalNetworksCard, RetentionCard } from "@/components/collection-settings"
import { AppStoreCard } from "@/components/app-store-card"
import { FlowStoreCard } from "@/components/flow-store-card"
import { DBIPAttribution } from "@/components/geoip"
import { ServiceNamesCard } from "@/components/service-names-card"
import { SourcesCard } from "@/components/sources-card"
import { Page, QueryError, Section, SectionHeading, SectionSkeleton, TimeAgo } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { GeoIPDatabase, GeoIPStatus } from "@/lib/api"
import { formatDateTime } from "@/lib/format"
import { sourceLabels, useGeoIP, usesDBIP } from "@/lib/geoip"

const kindLabels: Record<GeoIPDatabase["kind"], string> = { country: "Countries", asn: "ASN Owners" }

export function SettingsPage() {
  return (
    <Page narrow>
      <LocalNetworksCard />
      <SourcesCard />
      <RetentionCard />
      <AppStoreCard />
      <FlowStoreCard />
      <GeoIPCard />
      <ServiceNamesCard />
    </Page>
  )
}

function GeoIPCard() {
  const query = useGeoIP()
  return (
    <Card>
      <CardHeader>
        <CardTitle>GeoIP databases</CardTitle>
        <CardDescription>
          Each Remote IP's country and ASN Owner are looked up when its Flow is stored, and kept with it.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {query.isPending ? (
          <SectionSkeleton rows={2} />
        ) : query.isError ? (
          <QueryError title="Couldn't load the GeoIP status" error={query.error} />
        ) : (
          <GeoIPDetails status={query.data} now={query.dataUpdatedAt} />
        )}
      </CardContent>
    </Card>
  )
}

function GeoIPDetails({ status, now }: { status: GeoIPStatus; now: number }) {
  const problems = status.databases.filter((d) => d.error)
  return (
    <>
      <div className="overflow-x-auto rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Data</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Database</TableHead>
              <TableHead>Built</TableHead>
              <TableHead>Updated</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {status.databases.map((d) => (
              <TableRow key={d.kind}>
                <TableCell className="font-medium">{kindLabels[d.kind]}</TableCell>
                {d.source ? (
                  <>
                    <TableCell>
                      <Badge variant="secondary">{sourceLabels[d.source]}</Badge>
                    </TableCell>
                    <TableCell>
                      <span className="font-mono text-ident" title={d.path}>
                        {d.type}
                      </span>
                    </TableCell>
                    <TableCell>{d.builtAt && <TimeAgo iso={d.builtAt} now={now} />}</TableCell>
                    <TableCell>{d.updatedAt && <TimeAgo iso={d.updatedAt} now={now} />}</TableCell>
                  </>
                ) : (
                  <TableCell colSpan={4}>
                    <Badge variant="warning">Not loaded</Badge>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {problems.map((d) => (
        <Alert key={d.kind} variant="warning">
          <AlertTriangle />
          <AlertTitle>{kindLabels[d.kind]}: the last update failed</AlertTitle>
          <AlertDescription>
            <p>{d.error}</p>
            {d.lastAttempt && <p>Last tried {formatDateTime(d.lastAttempt)}. Flowmon tries again every few hours.</p>}
          </AlertDescription>
        </Alert>
      ))}
      {status.warnings.length > 0 && (
        <Alert variant="warning">
          <AlertTriangle />
          <AlertTitle>Some files in {status.dir} were ignored</AlertTitle>
          <AlertDescription>
            <ul className="list-disc pl-4">
              {status.warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}

      <Section>
        <SectionHeading title="Where the databases come from" />
        <ol className="flex list-decimal flex-col gap-1 pl-5 text-sm text-muted-foreground">
          <li>
            MMDB files you put in <code className="font-mono text-ident text-foreground">{status.dir}</code> (country,
            city or ASN databases from MaxMind or DB-IP). They are picked up within the hour and never replaced.
          </li>
          <li>
            MaxMind GeoLite2, downloaded weekly with your free account (
            <code className="font-mono text-ident text-foreground">FLOWMON_MAXMIND_ACCOUNT_ID</code> and{" "}
            <code className="font-mono text-ident text-foreground">FLOWMON_MAXMIND_LICENSE_KEY</code>).{" "}
            {status.maxMindConfigured ? "Configured." : "Not configured."}
          </li>
          <li>DB-IP Lite, downloaded weekly without an account. Slightly less detailed than GeoLite2.</li>
        </ol>
        <p className="text-sm text-muted-foreground">
          {status.downloadsEnabled ? (
            <>
              Downloads are on. Set <code className="font-mono text-ident text-foreground">FLOWMON_GEOIP_DOWNLOAD=false</code>{" "}
              to use only your own files.
            </>
          ) : (
            <>
              Downloads are off (<code className="font-mono text-ident text-foreground">FLOWMON_GEOIP_DOWNLOAD=false</code>
              ), so only your own files are used.
            </>
          )}{" "}
          A new database applies to Flows from then on; stored Flows keep what was known when they arrived.
        </p>
        {usesDBIP(status) && (
          <p className="text-sm text-muted-foreground">
            DB-IP Lite data is licensed under CC BY 4.0: <DBIPAttribution />.
          </p>
        )}
      </Section>
    </>
  )
}
