import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"

import { CountingFields } from "@/components/counting-fields"
import { FormActions, FormError, Section, SectionHeading } from "@/components/page"
import { Button } from "@/components/ui/button"
import { setCounting, type Counting, type ExporterStatus } from "@/lib/api"
import { formatNumber } from "@/lib/format"
import { parseSubnets } from "@/lib/subnets"

/** How an approved Exporter's Flows are counted when several Exporters see the same traffic. */
export function CountingSection({ exporter }: { exporter: ExporterStatus }) {
  const queryClient = useQueryClient()
  const saved: Counting = {
    role: exporter.role === "internal" ? "internal" : "edge",
    subnets: exporter.subnets ?? [],
    multiplePoints: exporter.multiplePoints,
  }
  const [value, setValue] = useState<Counting>(saved)
  const [subnetsText, setSubnetsText] = useState(saved.subnets.join(", "))
  const next: Counting = { ...value, subnets: value.role === "internal" ? parseSubnets(subnetsText) : [] }
  const dirty =
    next.role !== saved.role ||
    next.multiplePoints !== saved.multiplePoints ||
    next.subnets.join(",") !== (saved.role === "internal" ? saved.subnets : []).join(",")
  const save = useMutation({
    mutationFn: () => setCounting(exporter.address, next),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["exporters"] }),
  })
  const c = exporter.counting

  return (
    <Section>
      <SectionHeading
        title="Counting"
        aside={
          c && c.flows > 0
            ? [
                saved.role === "internal" && `${formatNumber(c.flows - c.uncounted)} of ${formatNumber(c.flows)} Flows counted`,
                saved.multiplePoints && `${formatNumber(c.duplicatesRemoved)} duplicates removed`,
              ]
                .filter(Boolean)
                .join(" · ") || undefined
            : undefined
        }
      />
      {saved.role === "internal" && (
        <p className="text-sm text-muted-foreground">
          Only Flows between two hosts in one of its LAN subnets are counted from this Exporter. The rest is routed traffic
          the Edge Exporter counts; it is kept as Flows marked "not counted" and left out of every total. Its
          classification settings below don't apply.
        </p>
      )}
      <CountingFields
        idPrefix={`counting-${exporter.address}`}
        value={value}
        subnetsText={subnetsText}
        onChange={setValue}
        onSubnetsText={setSubnetsText}
      />
      <p className="text-xs text-muted-foreground">
        Applies to Flows from now on, and charts mark when. To count stored Flows again, reprocess this Exporter below.
      </p>
      <FormError error={save.error} />
      <FormActions>
        {dirty && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setValue(saved)
              setSubnetsText(saved.subnets.join(", "))
            }}
          >
            Cancel
          </Button>
        )}
        <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
          Save
        </Button>
      </FormActions>
    </Section>
  )
}
