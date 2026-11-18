import { useMutation, useQueryClient } from "@tanstack/react-query"
import { MoreHorizontal, ShieldQuestion } from "lucide-react"
import { useState } from "react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { approveExporter, forgetExporter, ignoreExporter, type ExporterStatus } from "@/lib/api"
import { formatNumber } from "@/lib/format"

function useInvalidate() {
  const queryClient = useQueryClient()
  return () => {
    void queryClient.invalidateQueries({ queryKey: ["exporters"] })
    void queryClient.invalidateQueries({ queryKey: ["exporter-options"] })
  }
}

/** Approval form for a Pending (or ignored) Exporter. */
export function ApprovalPanel({ exporter }: { exporter: ExporterStatus }) {
  const invalidate = useInvalidate()
  const [name, setName] = useState(exporter.name)
  const approve = useMutation({
    mutationFn: () => approveExporter(exporter.address, { name, role: "edge", classificationMode: "address" }),
    onSuccess: invalidate,
  })
  const ignore = useMutation({ mutationFn: () => ignoreExporter(exporter.address), onSuccess: invalidate })
  const pending = exporter.approval === "pending"
  const error = approve.error ?? ignore.error

  return (
    <Alert className="border-primary/40">
      <ShieldQuestion />
      <AlertTitle>{pending ? "This Exporter is waiting for approval" : "This Exporter is ignored"}</AlertTitle>
      <AlertDescription className="flex flex-col gap-4">
        <p>
          {pending
            ? `Flowmon keeps its Templates but drops its Flows until you approve it (${formatNumber(exporter.pendingDroppedRecords)} dropped so far). Only approve addresses you recognise: anyone who can reach the collector port can send data.`
            : `Its datagrams are dropped without decoding (${formatNumber(exporter.ignoredDatagrams)} so far).`}
        </p>
        <div className="grid w-full gap-4 text-foreground sm:grid-cols-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor={`name-${exporter.address}`}>Name</Label>
            <Input
              id={`name-${exporter.address}`}
              placeholder="e.g. MikroTik"
              value={name}
              maxLength={64}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label>Role</Label>
            <Select value="edge">
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="edge">Edge</SelectItem>
                <SelectItem value="internal" disabled>
                  Internal (v1.1)
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label>Classification</Label>
            <Select value="address">
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="address">By address</SelectItem>
                <SelectItem value="interface" disabled>
                  By interface (later)
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
        {error && <p className="text-destructive">{error.message}</p>}
        <div className="flex gap-2">
          <Button onClick={() => approve.mutate()} disabled={approve.isPending}>
            Approve
          </Button>
          {pending && (
            <Button variant="outline" onClick={() => ignore.mutate()} disabled={ignore.isPending}>
              Ignore
            </Button>
          )}
        </div>
      </AlertDescription>
    </Alert>
  )
}

/** Menu for an approved or ignored Exporter. */
export function ExporterActions({ exporter }: { exporter: ExporterStatus }) {
  const invalidate = useInvalidate()
  const ignore = useMutation({ mutationFn: () => ignoreExporter(exporter.address), onSuccess: invalidate })
  const forget = useMutation({ mutationFn: () => forgetExporter(exporter.address), onSuccess: invalidate })
  if (exporter.approval === "pending") return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" className="ml-auto" aria-label="Exporter actions">
          <MoreHorizontal className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {exporter.approval === "approved" && (
          <DropdownMenuItem onSelect={() => ignore.mutate()}>Ignore this Exporter</DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={() => forget.mutate()}>Forget (ask for approval again)</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
