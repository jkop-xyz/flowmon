export type Health = {
  status: "ok"
  version: string
}

/** An API error carries the HTTP status and the server's message. */
export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function parse<T>(path: string, response: Response): Promise<T> {
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`
    try {
      const body = (await response.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // not JSON: keep the status text
    }
    throw new ApiError(response.status, message || path)
  }
  return (await response.json()) as T
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  return parse<T>(path, await fetch(path, { signal }))
}

export async function postJSON<T>(path: string, body: unknown): Promise<T> {
  return parse<T>(
    path,
    await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }),
  )
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
  /** How long after a bucket ends this Exporter's data may still change. */
  watermarkSeconds: number
  lateFlows: number
  flows: number
  estimatedTimeFlows: number
  findings: Finding[]
}

export type Approval = "pending" | "approved" | "ignored"

export type ExporterStatus = {
  address: string
  approval: Approval
  /** False for an approved Exporter that hasn't sent anything since Flowmon started. */
  seen: boolean
  name: string
  role: string
  classificationMode: string
  pendingDroppedRecords: number
  ignoredDatagrams: number
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
  droppedOverPendingLimit: number
  maxPending: number
  droppedNewExporterRate: number
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

export type Source = "raw" | "minute" | "hour"

export type BandwidthResult = {
  from: string
  to: string
  stepSeconds: number
  source: Source
  points: BandwidthPoint[]
}

/** Query results carry the time after which data may still change (the Watermark). */
export type ClassificationChange = { exporter: string; t: string; description: string }

export type WithIncomplete<T> = {
  result: T
  incompleteFrom: string
  /** When an Exporter's classification changed (bandwidth only). */
  changes?: ClassificationChange[]
}

type RangeParams = { from: Date; to: Date; exporter?: string }

function rangeParams(range: RangeParams) {
  const params = new URLSearchParams({ from: range.from.toISOString(), to: range.to.toISOString() })
  if (range.exporter) params.set("exporter", range.exporter)
  return params
}

export function fetchBandwidth(range: RangeParams, signal?: AbortSignal) {
  return getJSON<WithIncomplete<BandwidthResult>>(`/api/overview/bandwidth?${rangeParams(range)}`, signal)
}

export type TopDimension = "hosts" | "protocols" | "ports"

export type TopItem = {
  host?: string
  localIsExporterHost?: boolean
  protocol: number
  servicePort: number
  receivedBytes: number
  sentBytes: number
  internalBytes: number
  totalBytes: number
}

export type TopResult = {
  from: string
  to: string
  source: Source
  items: TopItem[]
  totalBytes: number
}

export function fetchTop(dimension: TopDimension, range: RangeParams, signal?: AbortSignal) {
  const params = rangeParams(range)
  params.set("dimension", dimension)
  return getJSON<WithIncomplete<TopResult>>(`/api/overview/top?${params}`, signal)
}

export type ExporterOption = { address: string; name: string }

export function fetchExporterOptions(signal?: AbortSignal) {
  return getJSON<ExporterOption[]>("/api/exporters/options", signal)
}

export type ClassificationMode = "address" | "interface"

export type ApprovalRequest = { name: string; role: "edge"; classificationMode: ClassificationMode }

export const approveExporter = (address: string, req: ApprovalRequest) =>
  postJSON<unknown>(`/api/exporters/${encodeURIComponent(address)}/approve`, req)

export const ignoreExporter = (address: string) =>
  postJSON<unknown>(`/api/exporters/${encodeURIComponent(address)}/ignore`, {})

export async function forgetExporter(address: string) {
  const response = await fetch(`/api/exporters/${encodeURIComponent(address)}`, { method: "DELETE" })
  if (!response.ok) throw new ApiError(response.status, "couldn't forget the Exporter")
}

export type InterfaceRole = "" | "wan" | "lan" | "tunnel"

export type InterfaceView = {
  index: number
  name: string
  source: string
  role: InterfaceRole
  suggestedWan: boolean
  flowsIn: number
  flowsOut: number
  bytesIn: number
  bytesOut: number
  remoteIn: number
}

export type InterfaceChange = {
  index: number
  oldName: string
  newName: string
  source: string
  at: string
  conflict: boolean
}

export type ReprocessStatus = {
  running: boolean
  exporter?: string
  from?: string
  startedAt?: string
  finishedAt?: string
  daysDone: number
  flows: number
  error?: string
}

export type InterfacesResponse = {
  classificationMode: ClassificationMode
  hasInterfaceNumbers: boolean
  interfaces: InterfaceView[]
  changes: InterfaceChange[]
  reprocess: ReprocessStatus
  reprocessFrom: string
}

const exporterPath = (address: string) => `/api/exporters/${encodeURIComponent(address)}`

export function fetchInterfaces(address: string, signal?: AbortSignal) {
  return getJSON<InterfacesResponse>(`${exporterPath(address)}/interfaces`, signal)
}

async function putJSON<T>(path: string, body: unknown): Promise<T> {
  return parse<T>(
    path,
    await fetch(path, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }),
  )
}

export const setInterfaceName = (address: string, index: number, name: string) =>
  putJSON<InterfacesResponse>(`${exporterPath(address)}/interfaces/${index}`, { name })

export const setClassification = (
  address: string,
  classificationMode: ClassificationMode,
  roles: Record<string, InterfaceRole>,
) => putJSON<InterfacesResponse>(`${exporterPath(address)}/classification`, { classificationMode, roles })

export const startReprocess = (address: string) => postJSON<ReprocessStatus>(`${exporterPath(address)}/reprocess`, {})
