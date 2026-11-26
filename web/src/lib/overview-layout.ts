import type { SavedSection } from "./api"

export type OverviewSectionId = "figures" | "load" | "dns" | "top" | "internet" | "health"

export type { SavedSection }
export type LayoutEntry = { id: OverviewSectionId; hidden: boolean }

/** The sections in their default order: the Overview's order today. */
export const defaultOrder: readonly OverviewSectionId[] = ["figures", "load", "dns", "top", "internet", "health"]

const known = new Set<string>(defaultOrder)
const isKnown = (id: string): id is OverviewSectionId => known.has(id)

/**
 * The layout to draw: saved entries with known ids, in saved order, the first of any duplicate.
 * A section missing from the save goes in after the nearest section that comes before it in
 * defaultOrder (or first), shown. null gives the default order.
 */
export function resolveLayout(saved: SavedSection[] | null): LayoutEntry[] {
  const layout: LayoutEntry[] = []
  for (const { id, hidden } of saved ?? []) {
    if (isKnown(id) && !layout.some((e) => e.id === id)) layout.push({ id, hidden })
  }
  for (const [i, id] of defaultOrder.entries()) {
    if (layout.some((e) => e.id === id)) continue
    // Walk back through defaultOrder to the nearest section already placed.
    let at = 0
    for (let j = i - 1; j >= 0; j--) {
      const placed = layout.findIndex((e) => e.id === defaultOrder[j])
      if (placed >= 0) {
        at = placed + 1
        break
      }
    }
    layout.splice(at, 0, { id, hidden: false })
  }
  return layout
}
