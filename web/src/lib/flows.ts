import { ArrowDownLeft, ArrowLeftRight, ArrowUpRight, Shuffle, type LucideIcon } from "lucide-react"

import type { Direction } from "@/lib/api"

/** How each Direction is labelled and drawn. */
export const directionInfo: Record<Direction, { label: string; icon: LucideIcon; className: string }> = {
  inbound: { label: "Inbound", icon: ArrowDownLeft, className: "text-chart-1" },
  outbound: { label: "Outbound", icon: ArrowUpRight, className: "text-chart-2" },
  internal: { label: "Internal", icon: ArrowLeftRight, className: "text-muted-foreground" },
  transit: { label: "Transit", icon: Shuffle, className: "text-muted-foreground" },
}
