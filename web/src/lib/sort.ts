import { useState } from "react"

/** Which column a table is sorted by, and which way. */
export type Sort<K extends string> = { key: K; asc: boolean }

/**
 * Sort state for a table. Clicking the sorted column flips its direction; clicking
 * another sorts by it, numbers largest first and text from A.
 */
export function useSort<K extends string>(initial: Sort<K>, textColumns: readonly K[] = []) {
  const [sort, setSort] = useState<Sort<K>>(initial)
  const toggle = (key: K) =>
    setSort((s) => (s.key === key ? { key, asc: !s.asc } : { key, asc: textColumns.includes(key) }))
  return [sort, toggle] as const
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" })

/** rows sorted by value, which gives each row's number or text; ties keep their order. */
export function sortRows<T>(rows: readonly T[], value: (row: T) => number | string | undefined, asc: boolean): T[] {
  const dir = asc ? 1 : -1
  return [...rows].sort((a, b) => {
    const x = value(a) ?? ""
    const y = value(b) ?? ""
    const c = typeof x === "number" && typeof y === "number" ? x - y : collator.compare(String(x), String(y))
    return c * dir
  })
}
