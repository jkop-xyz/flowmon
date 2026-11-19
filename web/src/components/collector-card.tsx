import { AlertTriangle, Radio } from "lucide-react"
import { Link } from "react-router"

import { TimeAgo } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { CollectorStatus } from "@/lib/api"
import { formatBytes, formatNumber } from "@/lib/format"

/**
 * Whether collection is losing data: what each socket received and the kernel dropped,
 * next to what Flowmon itself dropped on the way to the Flow Store.
 */
export function CollectorCard({ status, now }: { status: CollectorStatus; now: number }) {
  const { listeners, pipeline } = status
  const kernelDrops = listeners.reduce((n, l) => n + l.kernelDrops, 0)
  const capped = listeners.filter((l) => l.grantedBuffer > 0 && l.grantedBuffer < l.requestedBuffer)
  const flowmonDrops = pipeline ? pipeline.dropped + pipeline.writeErrors : 0

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Radio className="size-4" />
          Collector
        </CardTitle>
        <CardDescription>
          {status.lastAccepted ? (
            <>
              Last Flows <TimeAgo iso={status.lastAccepted} now={now} />
              {status.lastAcceptedFrom && <> from {status.lastAcceptedFrom}</>}.{" "}
            </>
          ) : (
            "No Flows from an approved Exporter since Flowmon started. "
          )}
          Running since <TimeAgo iso={status.started} now={now} />.
        </CardDescription>
        <CardAction>
          <Link to="/guide/mikrotik" className="text-sm underline-offset-4 hover:underline">
            MikroTik setup guide
          </Link>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {kernelDrops > 0 && (
          <Alert variant="warning">
            <AlertTriangle />
            <AlertTitle>The kernel dropped {formatNumber(kernelDrops)} datagrams</AlertTitle>
            <AlertDescription>
              They arrived faster than Flowmon read them and the socket's receive buffer was full, so their Flows are
              missing. Raise <code className="font-mono">net.core.rmem_max</code> on the host (see the deploy guide), or
              give the container <code className="font-mono">CAP_NET_ADMIN</code> so Flowmon can force a larger buffer.
            </AlertDescription>
          </Alert>
        )}
        {listeners.length === 0 ? (
          <p className="text-sm text-muted-foreground">Not listening: no collector addresses are configured.</p>
        ) : (
          <div className="overflow-x-auto rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Listening on</TableHead>
                  <TableHead className="text-right">Datagrams</TableHead>
                  <TableHead className="text-right">Dropped by the kernel</TableHead>
                  <TableHead className="text-right">Receive buffer</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {listeners.map((l) => (
                  <TableRow key={l.address}>
                    <TableCell className="font-mono text-ident">{l.address}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatNumber(l.datagrams)}</TableCell>
                    <TableCell className={`text-right tabular-nums ${l.kernelDrops > 0 ? "text-destructive" : ""}`}>
                      {l.dropsCounted ? formatNumber(l.kernelDrops) : "not reported"}
                    </TableCell>
                    <TableCell className="text-right tabular-nums" title={`Asked for ${formatBytes(l.requestedBuffer)}`}>
                      {l.grantedBuffer ? formatBytes(l.grantedBuffer) : "unknown"}
                      {l.grantedBuffer > 0 && l.grantedBuffer < l.requestedBuffer && (
                        <span className="text-muted-foreground"> of {formatBytes(l.requestedBuffer)}</span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        {capped.length > 0 && kernelDrops === 0 && (
          <p className="text-xs text-muted-foreground">
            The kernel granted a smaller receive buffer than Flowmon asked for (it caps it at{" "}
            <code className="font-mono">net.core.rmem_max</code>). That's fine while nothing is dropped; bursts from a busy
            router may need more.
          </p>
        )}
        {pipeline && (
          <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-4">
            <Counter label="Flows stored" value={formatNumber(pipeline.written)} />
            <Counter
              label="Dropped: write queue full"
              value={formatNumber(pipeline.dropped)}
              bad={pipeline.dropped > 0}
              title="Flows arrived faster than the Flow Store could write them."
            />
            <Counter
              label="Dropped: too late"
              value={formatNumber(pipeline.late)}
              bad={pipeline.late > 0}
              title="Flows that arrived more than 2 hours past their Watermark. Check the Exporter's clock."
            />
            <Counter
              label="Failed writes"
              value={formatNumber(pipeline.writeErrors)}
              bad={pipeline.writeErrors > 0}
              title="Batches the Flow Store refused; the log says why."
            />
            <Counter
              label="Waiting to be written"
              value={`${formatNumber(pipeline.queued)} of ${formatNumber(pipeline.queueCapacity)}`}
            />
            <Counter
              label="Without addresses"
              value={formatNumber(pipeline.noAddresses)}
              title="Records with no source or destination address, which can't be stored as Flows."
            />
            <Counter label="Options records" value={formatNumber(pipeline.optionsRecords)} title="Metadata, such as interface names." />
            <Counter
              label="NAT events"
              value={formatNumber(pipeline.natEvents)}
              title="NAT sessions being created and deleted. Not traffic, so not Flows; they tell Flowmon the router's WAN address."
            />
            <Counter
              label="Last written"
              value={pipeline.lastWrite ? <TimeAgo iso={pipeline.lastWrite} now={now} /> : "never"}
            />
          </dl>
        )}
        {flowmonDrops === 0 && kernelDrops === 0 && pipeline && pipeline.late === 0 && (
          <p className="text-xs text-muted-foreground">Nothing has been lost since Flowmon started.</p>
        )}
      </CardContent>
    </Card>
  )
}

function Counter({ label, value, bad, title }: { label: string; value: React.ReactNode; bad?: boolean; title?: string }) {
  return (
    <div className="flex min-w-0 flex-col" title={title}>
      <dt className="truncate text-xs text-muted-foreground">{label}</dt>
      <dd className={`tabular-nums ${bad ? "text-destructive" : ""}`}>{value}</dd>
    </div>
  )
}
