import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react"
import type { ReactNode } from "react"

import { TableHead } from "@/components/ui/table"
import { cn } from "@/lib/utils"
import type { Sort } from "@/lib/sort"

/** A table header that sorts by its column, drawn like the Flow Explorer's. */
export function SortableHead<K extends string>({
  column,
  sort,
  onSort,
  numeric,
  className,
  children,
}: {
  column: K
  sort: Sort<K>
  onSort: (column: K) => void
  numeric?: boolean
  className?: string
  children: ReactNode
}) {
  const active = sort.key === column
  return (
    <TableHead className={cn(numeric && "text-right", className)} aria-sort={active ? (sort.asc ? "ascending" : "descending") : "none"}>
      <button
        type="button"
        onClick={() => onSort(column)}
        className={`inline-flex items-center gap-1 hover:text-foreground ${numeric ? "flex-row-reverse" : ""}`}
      >
        {children}
        {!active ? (
          <ChevronsUpDown className="size-3.5 text-muted-foreground" />
        ) : sort.asc ? (
          <ArrowUp className="size-3.5" />
        ) : (
          <ArrowDown className="size-3.5" />
        )}
      </button>
    </TableHead>
  )
}
