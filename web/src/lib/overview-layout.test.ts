import { describe, expect, it } from "vitest"

import { defaultOrder, resolveLayout, type SavedSection } from "./overview-layout"

const ids = (saved: SavedSection[] | null) => resolveLayout(saved).map((e) => e.id)
const shown = (...list: string[]): SavedSection[] => list.map((id) => ({ id, hidden: false }))

describe("resolveLayout", () => {
  it("gives the default order, all shown, for null", () => {
    expect(resolveLayout(null)).toEqual(defaultOrder.map((id) => ({ id, hidden: false })))
  })

  it("keeps the saved order and hidden flags", () => {
    const saved = [...shown("health", "top", "figures", "load", "dns"), { id: "internet", hidden: true }]
    expect(resolveLayout(saved)).toEqual(saved)
  })

  it("drops unknown ids", () => {
    expect(ids(shown("future", "load", "figures", "dns", "top", "internet", "health"))).toEqual([
      "load",
      "figures",
      "dns",
      "top",
      "internet",
      "health",
    ])
  })

  it("inserts a missing section after its default predecessor, shown", () => {
    const saved = [{ id: "health", hidden: true }, ...shown("top", "figures", "load", "internet")]
    const out = resolveLayout(saved)
    expect(out.map((e) => e.id)).toEqual(["health", "top", "figures", "load", "dns", "internet"])
    expect(out.find((e) => e.id === "dns")?.hidden).toBe(false)
  })

  it("inserts a missing first section at the front", () => {
    expect(ids(shown("load", "dns", "top", "internet", "health"))).toEqual([
      "figures",
      "load",
      "dns",
      "top",
      "internet",
      "health",
    ])
  })

  it("chains several missing sections off the nearest one present", () => {
    expect(ids(shown("health", "figures"))).toEqual(["health", "figures", "load", "dns", "top", "internet"])
  })

  it("keeps the first of a duplicated id", () => {
    const saved = [{ id: "top", hidden: true }, ...shown("figures", "load", "dns", "top")]
    const out = resolveLayout(saved)
    expect(out.filter((e) => e.id === "top")).toEqual([{ id: "top", hidden: true }])
    expect(out).toHaveLength(defaultOrder.length)
  })

  it("gives the default order, shown, for an empty save", () => {
    expect(resolveLayout([])).toEqual(resolveLayout(null))
  })
})
