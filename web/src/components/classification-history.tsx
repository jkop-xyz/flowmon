import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Trash2 } from "lucide-react"
import { useEffect } from "react"
import { useLocation } from "react-router"
import { Button } from "@/components/ui/button"
import { FormError, QueryError, Section, SectionHeading, SectionSkeleton } from "@/components/page"
import { classificationHistoryId } from "@/lib/anchors"
import { deleteClassificationChange, fetchClassificationHistory } from "@/lib/api"
import { formatDateTime } from "@/lib/format"

/** Every recorded classification change of an Exporter, each a dashed line on charts. */
export function ClassificationHistory({ address }: { address: string }) {
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ["classification-history", address],
    queryFn: ({ signal }) => fetchClassificationHistory(address, signal),
  })
  const remove = useMutation({
    mutationFn: (t: string) => deleteClassificationChange(address, t),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["classification-history", address] })
      void queryClient.invalidateQueries({ queryKey: ["bandwidth"] })
    },
  })
  // Opened from a chart's link: scroll here once the list is in.
  const { hash } = useLocation()
  const loaded = query.isSuccess
  useEffect(() => {
    if (loaded && hash === `#${classificationHistoryId(address)}`) {
      document.getElementById(classificationHistoryId(address))?.scrollIntoView({ block: "start" })
    }
  }, [loaded, hash, address])
  const heading = (
    <SectionHeading
      title="Classification history"
      aside="Each change is a dashed line on traffic charts. Removing one only removes its line: Flows keep the classification they got."
    />
  )
  if (query.isPending) {
    return (
      <Section>
        {heading}
        <SectionSkeleton rows={2} />
      </Section>
    )
  }
  if (query.isError) {
    return (
      <Section>
        {heading}
        <QueryError title="Couldn't load the classification history" error={query.error} />
      </Section>
    )
  }
  const changes = query.data.changes
  return (
    <div id={classificationHistoryId(address)} className="scroll-mt-4">
      <Section>
        {heading}
        {changes.length === 0 ? (
          <p className="text-sm text-muted-foreground">No classification changes recorded.</p>
        ) : (
          <ul className="flex flex-col divide-y rounded-lg border text-sm">
            {changes.map((c) => (
              <li key={c.t} className="flex items-start justify-between gap-3 px-3 py-2">
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-xs text-muted-foreground">{formatDateTime(c.t)}</span>
                  <span className="break-words">{c.description}</span>
                </span>
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={`Remove the change at ${formatDateTime(c.t)}`}
                  disabled={remove.isPending && remove.variables === c.t}
                  onClick={() => remove.mutate(c.t)}
                >
                  <Trash2 /> Remove
                </Button>
              </li>
            ))}
          </ul>
        )}
        <FormError error={remove.error} />
      </Section>
    </div>
  )
}
