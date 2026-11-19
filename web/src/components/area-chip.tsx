import type { LucideIcon } from "lucide-react"

import type { Area } from "@/lib/navigation"
import { cn } from "@/lib/utils"

/** An icon on a tile tinted in an area's colour: in the sidebar and on cards that lead there. */
export function AreaChip({ area, icon: Icon, className }: { area: Area; icon: LucideIcon; className?: string }) {
  return (
    <span
      data-area={area}
      className={cn("flex size-7 shrink-0 items-center justify-center rounded-md bg-area-tint text-area-ink", className)}
    >
      <Icon className="size-4" />
    </span>
  )
}
