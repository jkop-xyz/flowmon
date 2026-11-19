import { estimateShare } from "@/lib/format"

/**
 * Estimates from sampling Exporters: an Exporter that counts 1 in N packets has its
 * counts scaled up by N, so the bytes shown are estimates of the real traffic.
 */

/** A note under a chart or list whose bytes include estimates; nothing when none do. */
export function EstimateNote({ estimated, total }: { estimated?: number; total: number }) {
  if (!estimated || estimated <= 0) return null
  return (
    <p className="text-xs text-muted-foreground">
      <EstimateMark /> Estimated: {estimateShare(estimated, total)} of these bytes come from sampled packets. An Exporter
      that counts 1 in N packets has its counts scaled up by N, to estimate the real traffic.
    </p>
  )
}

/** "≈", marking a number that is (partly) an estimate. */
export function EstimateMark({ title }: { title?: string }) {
  return (
    <span aria-label="estimated" title={title ?? "Estimated from sampled packets"} className="text-muted-foreground">
      ≈
    </span>
  )
}
