import { geoEqualEarth, geoPath } from "d3-geo"
import type { Feature, FeatureCollection, Geometry } from "geojson"
import { useMemo, useState } from "react"
import { feature } from "topojson-client"
import type { GeometryCollection, Topology } from "topojson-specification"
import world from "world-atlas/countries-110m.json"

import type { TopItem } from "@/lib/api"
import { countryName, shapeCountry } from "@/lib/countries"
import { formatBytes } from "@/lib/format"

const width = 960
const height = 440
// A sequential ramp in one hue: each step mixes more of --chart-1 into the empty-map colour.
const steps = [22, 40, 58, 78, 100]
const stepColor = (i: number) => `color-mix(in oklab, var(--chart-1) ${steps[i]}%, var(--muted))`

type Shape = { key: string; country?: string; name: string; d: string }

/** Country shapes, projected once. Antarctica is left out: nobody's traffic lives there. */
function useShapes(): Shape[] {
  return useMemo(() => {
    const topology = world as unknown as Topology<{ countries: GeometryCollection<{ name: string }> }>
    const fc = feature(topology, topology.objects.countries) as FeatureCollection<Geometry, { name: string }>
    const features = fc.features.filter((f) => f.id !== "010")
    const projection = geoEqualEarth().fitExtent(
      [
        [4, 4],
        [width - 4, height - 4],
      ],
      { type: "FeatureCollection", features } as FeatureCollection,
    )
    const path = geoPath(projection)
    return features.map((f: Feature<Geometry, { name: string }>, i) => ({
      key: String(f.id ?? `n${i}`),
      country: shapeCountry(f.id, f.properties?.name),
      name: f.properties?.name ?? "",
      d: path(f) ?? "",
    }))
  }, [])
}

/** Bins totals on a log scale: traffic per country spans many orders of magnitude. */
function binner(values: number[]) {
  const positive = values.filter((v) => v > 0)
  if (positive.length === 0) return () => -1
  const lo = Math.log(Math.min(...positive))
  const hi = Math.log(Math.max(...positive))
  return (v: number) => {
    if (v <= 0) return -1
    if (hi === lo) return steps.length - 1
    return Math.min(steps.length - 1, Math.floor(((Math.log(v) - lo) / (hi - lo)) * steps.length))
  }
}

export default function WorldMap({
  items,
  total,
  onSelect,
}: {
  items: TopItem[]
  total: number
  /** Called with a country's ISO code when it is clicked (countries with traffic only). */
  onSelect?: (country: string) => void
}) {
  const shapes = useShapes()
  const [hover, setHover] = useState<{ shape: Shape; x: number; y: number; flip: boolean } | null>(null)

  const byCountry = useMemo(() => new Map(items.filter((i) => i.country).map((i) => [i.country!, i])), [items])
  const bin = useMemo(() => binner(items.filter((i) => i.country).map((i) => i.totalBytes)), [items])

  const hovered = hover?.shape.country ? byCountry.get(hover.shape.country) : undefined
  return (
    <div className="flex flex-col gap-2">
      <div className="relative" onMouseLeave={() => setHover(null)}>
        <svg viewBox={`0 0 ${width} ${height}`} className="h-auto w-full" role="img" aria-label="Traffic by country">
          {shapes.map((s) => {
            const item = s.country ? byCountry.get(s.country) : undefined
            const step = item ? bin(item.totalBytes) : -1
            const active = hover?.shape.key === s.key
            return (
              <path
                key={s.key}
                d={s.d}
                fill={step < 0 ? "var(--muted)" : stepColor(step)}
                stroke={active ? "var(--foreground)" : "var(--background)"}
                strokeWidth={active ? 1.5 : 0.6}
                className={`transition-[stroke] ${item && onSelect ? "cursor-pointer" : ""}`}
                onClick={item && onSelect && s.country ? () => onSelect(s.country!) : undefined}
                onMouseMove={(e) => {
                  const box = e.currentTarget.ownerSVGElement!.parentElement!.getBoundingClientRect()
                  const x = e.clientX - box.left
                  setHover({ shape: s, x, y: e.clientY - box.top, flip: x > box.width / 2 })
                }}
              />
            )
          })}
        </svg>
        {hover && (
          <div
            className="pointer-events-none absolute z-10 min-w-40 rounded-lg border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md"
            style={{
              left: hover.x,
              top: hover.y,
              transform: `translate(${hover.flip ? "calc(-100% - 12px)" : "12px"}, 12px)`,
            }}
          >
            <div className="font-medium">{hover.shape.country ? countryName(hover.shape.country) : hover.shape.name}</div>
            {hovered ? (
              <div className="mt-1 grid grid-cols-[auto_auto] gap-x-3 gap-y-0.5 tabular-nums">
                <span className="text-muted-foreground">Total</span>
                <span className="text-right">
                  {formatBytes(hovered.totalBytes)}
                  {total > 0 && ` (${((hovered.totalBytes / total) * 100).toFixed(1)}%)`}
                </span>
                <span className="text-muted-foreground">Received</span>
                <span className="text-right">{formatBytes(hovered.receivedBytes)}</span>
                <span className="text-muted-foreground">Sent</span>
                <span className="text-right">{formatBytes(hovered.sentBytes)}</span>
              </div>
            ) : (
              <div className="mt-1 text-muted-foreground">No traffic</div>
            )}
            {hovered && onSelect && <div className="mt-1 text-muted-foreground">Click for its Flows</div>}
          </div>
        )}
      </div>
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground" aria-hidden>
        <span className="mr-1 inline-block size-2.5 rounded-sm bg-muted" />
        <span className="mr-3">None</span>
        <span>Less</span>
        {steps.map((_, i) => (
          <span key={i} className="inline-block h-2.5 w-5 rounded-sm" style={{ background: stepColor(i) }} />
        ))}
        <span>More</span>
      </div>
    </div>
  )
}
