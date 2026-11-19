import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useState, type FormEvent } from "react"

import { FormError, Section, SectionHeading } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { setSamplingRate, type ExporterStatus } from "@/lib/api"
import { formatNumber } from "@/lib/format"

/**
 * Whether an Exporter samples packets, and the rate to assume when it samples without
 * saying so. Sampled counts are scaled up by the rate and shown as estimates.
 */
export function SamplingSection({ exporter }: { exporter: ExporterStatus }) {
  const queryClient = useQueryClient()
  const manual = exporter.samplingRate
  const [value, setValue] = useState(manual > 1 ? String(manual) : "")
  const save = useMutation({
    mutationFn: (rate: number) => setSamplingRate(exporter.address, rate),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["exporters"] }),
  })
  const st = exporter.sampling
  const announced = st?.announcedRate ?? 0

  let status: string
  if (announced > 1) {
    status = `The Exporter says it samples 1 in ${formatNumber(announced)} packets, so its counts are scaled up by ${formatNumber(announced)} and shown as estimates.`
  } else if (announced === 1) {
    status = "The Exporter says it counts every packet."
  } else if (manual > 1) {
    status = `Set here: 1 in ${formatNumber(manual)} packets. The Exporter doesn't say, so its counts are scaled up by ${formatNumber(manual)} and shown as estimates.`
  } else {
    status =
      "Not sampled, as far as Flowmon can tell: the Exporter doesn't say it samples. If it does anyway, set its rate here."
  }

  const parsed = value.trim() === "" ? 0 : Number(value)
  const valid = Number.isInteger(parsed) && parsed >= 0 && parsed <= 1_048_576
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (valid) save.mutate(parsed)
  }

  return (
    <Section>
      <SectionHeading
        title="Sampling"
        aside={st && st.flows > 0 ? `${formatNumber(st.sampledFlows)} of ${formatNumber(st.flows)} Flows scaled up` : undefined}
      />
      <p className="text-sm text-muted-foreground">{status}</p>
      <form onSubmit={submit} className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <label htmlFor={`sampling-${exporter.address}`}>When it doesn't say, assume 1 in</label>
          <Input
            id={`sampling-${exporter.address}`}
            inputMode="numeric"
            placeholder="none"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            className="h-8 w-28 tabular-nums"
          />
          <span>packets</span>
          <Button type="submit" size="sm" variant="outline" disabled={!valid || parsed === (manual > 1 ? manual : 0) || save.isPending}>
            Save
          </Button>
        </div>
        {!valid && <p className="text-sm text-destructive">Enter a whole number up to 1,048,576, or leave it empty.</p>}
        <p className="text-xs text-muted-foreground">
          {announced > 1
            ? "Ignored while the Exporter announces its own rate. "
            : "Applies to Flows from now on, and charts mark when. "}
          Stored Flows keep the rate they arrived with.
        </p>
        <FormError error={save.error} />
      </form>
    </Section>
  )
}
