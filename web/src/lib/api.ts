export type Health = {
  status: "ok"
  version: string
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, { signal })
  if (!response.ok) throw new Error(`${path}: ${response.status} ${response.statusText}`)
  return (await response.json()) as T
}

export function fetchHealth(signal?: AbortSignal) {
  return getJSON<Health>("/api/health", signal)
}

export type Severity = "warning" | "info"

export type Finding = {
  severity: Severity
  code: string
  message: string
}

export type DeviationStatus = {
  kind: string
  count: number
  lastDetail: string
  lastSeen: string
}

export type FieldStatus = {
  enterprise: number
  id: number
  name: string
  /** IANA abstract data type; empty for enterprise and unknown elements. */
  type: string
  length: number
  variable: boolean
  scope: boolean
}

export type TemplateStatus = {
  observationDomain: number
  id: number
  options: boolean
  scopeFieldCount: number
  firstSeen: string
  lastSeen: string
  fields: FieldStatus[]
  findings: Finding[]
}

export type TimingStatus = {
  samples: number
  /** Median of (received − export time): positive when the Exporter's clock is behind. */
  clockOffsetSeconds: number
  /** Median of (export time − flow end). */
  exportLagSeconds: number
  flows: number
  estimatedTimeFlows: number
  findings: Finding[]
}

export type ExporterStatus = {
  address: string
  lastSource: string
  firstSeen: string
  lastSeen: string
  datagrams: number
  bytes: number
  messages: number
  records: number
  observationDomains: number[] | null
  deviations: DeviationStatus[]
  templates: TemplateStatus[]
  findings: Finding[]
  timing: TimingStatus | null
}

export type CollectorStatus = {
  listeners: string[]
  droppedOverExporterLimit: number
  maxExporters: number
  exporters: ExporterStatus[]
}

export function fetchExporters(signal?: AbortSignal) {
  return getJSON<CollectorStatus>("/api/exporters", signal)
}

export type BandwidthPoint = {
  t: string
  inboundBps: number
  outboundBps: number
  internalBps: number
}

export type BandwidthResult = {
  from: string
  to: string
  stepSeconds: number
  points: BandwidthPoint[]
}

export function fetchBandwidth(range: { from: Date; to: Date; exporter?: string }, signal?: AbortSignal) {
  const params = new URLSearchParams({ from: range.from.toISOString(), to: range.to.toISOString() })
  if (range.exporter) params.set("exporter", range.exporter)
  return getJSON<BandwidthResult>(`/api/overview/bandwidth?${params}`, signal)
}
