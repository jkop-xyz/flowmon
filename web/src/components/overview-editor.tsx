import { useMutation, useQueryClient } from "@tanstack/react-query"
import { ArrowDown, ArrowUp, GripVertical } from "lucide-react"
import { useEffect, useLayoutEffect, useRef, useState } from "react"

import { FormError } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { resetOverviewLayout, saveOverviewLayout } from "@/lib/api"
import {
  defaultLayout,
  isDefault,
  moveSection,
  overviewLayoutKey,
  setHidden,
  type LayoutEntry,
  type OverviewSectionId,
} from "@/lib/overview-layout"
import { cn } from "@/lib/utils"

/** What the editor shows about one section. */
export type EditorSection = {
  name: string
  cards: string
  /** Column proportions of the section's cards, for the grey sketch. */
  sketch: number[]
  /** Set when the section has nothing to show now: why, and that it comes back. */
  unavailable?: string
}

/**
 * Edits a draft of the Overview layout: reorder by drag or by the arrows, show or hide with a
 * switch. Done saves it (or deletes the saved one when it equals the default) and calls onClose;
 * Escape discards.
 */
export function OverviewEditor({
  layout,
  sections,
  onClose,
}: {
  layout: LayoutEntry[]
  sections: Record<OverviewSectionId, EditorSection>
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState(layout)
  const [dragging, setDragging] = useState<number | null>(null)
  const [over, setOver] = useState<number | null>(null)
  // After an arrow moves a row the browser drops focus; this puts it back on the same button.
  const refocus = useRef<{ id: OverviewSectionId; dir: "up" | "down" } | null>(null)
  const buttons = useRef(new Map<string, HTMLButtonElement>())

  const done = useMutation({
    mutationFn: async () => {
      if (isDefault(draft)) {
        await resetOverviewLayout()
        return null
      }
      return saveOverviewLayout(draft)
    },
    onSuccess: (saved) => {
      queryClient.setQueryData(overviewLayoutKey, saved)
      onClose()
    },
  })

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])

  useLayoutEffect(() => {
    if (!refocus.current) return
    buttons.current.get(`${refocus.current.id}:${refocus.current.dir}`)?.focus()
    refocus.current = null
  }, [draft])

  const arrow = (index: number, dir: "up" | "down") => {
    refocus.current = { id: draft[index].id, dir }
    setDraft(moveSection(draft, index, index + (dir === "up" ? -1 : 1)))
  }

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-secondary px-3 py-2.5">
        <p className="text-sm text-muted-foreground">
          Drag sections, or move them with the arrows. Your layout is saved to your account.
        </p>
        <div className="flex items-center gap-2">
          <FormError error={done.error} />
          <Button variant="ghost" size="sm" disabled={done.isPending} onClick={() => setDraft(defaultLayout())}>
            Reset to default
          </Button>
          <Button size="sm" disabled={done.isPending} onClick={() => done.mutate()}>
            Done
          </Button>
        </div>
      </div>
      <ul className="flex flex-col gap-2">
        {draft.map((entry, i) => {
          const section = sections[entry.id]
          return (
            <li
              key={entry.id}
              draggable
              onDragStart={() => setDragging(i)}
              onDragEnd={() => {
                setDragging(null)
                setOver(null)
              }}
              onDragOver={(e) => {
                if (dragging === null) return
                e.preventDefault()
                setOver(i)
              }}
              onDrop={(e) => {
                e.preventDefault()
                if (dragging !== null) setDraft(moveSection(draft, dragging, i))
                setDragging(null)
                setOver(null)
              }}
              className={cn(
                "grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-center gap-3 rounded-lg border bg-card px-3 py-2.5 sm:grid-cols-[1.25rem_4rem_minmax(0,1fr)_auto]",
                dragging === i && "opacity-40",
                over === i && dragging !== i && "border-primary",
              )}
            >
              <GripVertical className="size-4 cursor-grab text-muted-foreground" aria-hidden />
              <div
                className={cn("hidden h-9 gap-[3px] sm:grid", entry.hidden && "opacity-45")}
                style={{ gridTemplateColumns: section.sketch.map((f) => `${f}fr`).join(" ") }}
                aria-hidden
              >
                {section.sketch.map((_, k) => (
                  <i key={k} className="block rounded-[3px] bg-muted-foreground/35" />
                ))}
              </div>
              <div className={cn("min-w-0", entry.hidden && "[&>*]:opacity-45")}>
                <div className="text-sm font-semibold">{section.name}</div>
                <div className="text-xs text-muted-foreground">{section.cards}</div>
                {section.unavailable && (
                  <div className="mt-0.5 text-xs text-warning-foreground">{section.unavailable}</div>
                )}
              </div>
              <div className="flex items-center gap-1">
                {(["up", "down"] as const).map((dir) => {
                  const edge = dir === "up" ? i === 0 : i === draft.length - 1
                  const Icon = dir === "up" ? ArrowUp : ArrowDown
                  return (
                    <Button
                      key={dir}
                      ref={(el) => {
                        if (el) buttons.current.set(`${entry.id}:${dir}`, el)
                        else buttons.current.delete(`${entry.id}:${dir}`)
                      }}
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Move ${section.name} ${dir}`}
                      // aria-disabled, not disabled, so focus stays on a button that reaches the end.
                      aria-disabled={edge}
                      className={cn(edge && "opacity-30")}
                      onClick={() => !edge && arrow(i, dir)}
                    >
                      <Icon />
                    </Button>
                  )
                })}
                <Switch
                  className="ml-1.5"
                  checked={!entry.hidden}
                  onCheckedChange={(shown) => setDraft(setHidden(draft, entry.id, !shown))}
                  aria-label={`Show ${section.name}`}
                />
              </div>
            </li>
          )
        })}
      </ul>
    </>
  )
}
