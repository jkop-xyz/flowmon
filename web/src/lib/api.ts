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

export type TopDimension = "hosts" | "remotes" | "protocols" | "ports" | "countries" | "asns"

export type TopItem = {
  host?: string
  remoteIp?: string
  localIsExporterHost?: boolean
  protocol: number
  servicePort: number
  /** ISO alpha-2; empty for traffic whose Remote IP had no GeoIP data. */
  country?: string
  /** 0 or absent for traffic whose Remote IP had no GeoIP data. */
  asn?: number
  asnOrg?: string
  receivedBytes: number
  sentBytes: number
  internalBytes: number
  totalBytes: number
  flows?: number
}

export type TopResult = {
  from: string
  to: string
  source: Source
  items: TopItem[]
  totalBytes: number
  /** Set when the list came from the Top Remote IPs lists (see Coverage). */
  approximate?: boolean
}

export function fetchTop(dimension: TopDimension, range: RangeParams, signal?: AbortSignal, limit?: number) {
  const params = rangeParams(range)
  params.set("dimension", dimension)
  if (limit) params.set("limit", String(limit))
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

export type SNMPConfig = {
  enabled: boolean
  version: "v2c" | "v3"
  target: string
  community?: string
  user?: string
  authProtocol?: string
  authPassword?: string
  privProtocol?: string
  privPassword?: string
  overhead: string
  overheadBytes: number
}

export type SNMPView = {
  config: SNMPConfig & { hasCommunity: boolean; hasAuthPassword: boolean; hasPrivPassword: boolean }
  status: { lastPoll?: string; lastError?: string; interfaces: number }
  overheadPresets: Record<string, number>
}

export const fetchSNMP = (address: string, signal?: AbortSignal) =>
  getJSON<SNMPView>(`${exporterPath(address)}/snmp`, signal)

export const saveSNMP = (address: string, config: SNMPConfig) =>
  putJSON<SNMPView>(`${exporterPath(address)}/snmp`, { config })

export const testSNMP = (address: string, config: SNMPConfig) =>
  postJSON<{ interfaces: string[] }>(`${exporterPath(address)}/snmp/test`, { config })

export type ReconDirection = { interfaceBytes: number; flowBytes: number; gap: number | null }

export type ReconInterface = {
  name: string
  index: number
  warning: boolean
  hours: { hour: string; complete: boolean; in: ReconDirection; out: ReconDirection }[]
}

export type Reconciliation = {
  overheadBytes: number
  enabled: boolean
  interfaces: ReconInterface[]
  thresholds: { gap: number; sustainedHours: number; minBytesPerHour: number }
}

export const fetchReconciliation = (address: string, signal?: AbortSignal) =>
  getJSON<Reconciliation>(`${exporterPath(address)}/reconciliation`, signal)

export type GeoIPSource = "file" | "maxmind" | "dbip"

export type GeoIPDatabase = {
  kind: "country" | "asn"
  /** Absent when no database is loaded for this kind. */
  source?: GeoIPSource
  type?: string
  path?: string
  builtAt?: string
  updatedAt?: string
  lastAttempt?: string
  error?: string
}

export type GeoIPStatus = {
  downloadsEnabled: boolean
  maxMindConfigured: boolean
  dir: string
  databases: GeoIPDatabase[]
  warnings: string[]
}

export function fetchGeoIP(signal?: AbortSignal) {
  return getJSON<GeoIPStatus>("/api/geoip", signal)
}

/** Where an answer about Remote IPs came from, and what it can't include. */
export type Coverage = {
  from: string
  to: string
  /** raw is exact; hour and day are the Top Remote IPs lists. */
  source: "raw" | "hour" | "day"
  approximate: boolean
  /** Start of the oldest raw Flows still kept. */
  rawFrom?: string
  /** Start of the oldest list of the granularity used. */
  listsFrom?: string
}

export type RemoteItem = {
  remoteIp: string
  country?: string
  asn?: number
  asnOrg?: string
  receivedBytes: number
  sentBytes: number
  totalBytes: number
  flows: number
  localHosts: number
}

export type RemoteResult = Coverage & {
  items: RemoteItem[]
  totalBytes: number
  otherBytes: number
}

export type RemoteHost = {
  host: string
  localIsExporterHost?: boolean
  receivedBytes: number
  sentBytes: number
  totalBytes: number
  flows: number
}

export type GeoInfo = { country?: string; asn?: number; asnOrg?: string }

export type RemoteDetail = Coverage &
  GeoInfo & {
    remoteIp: string
    receivedBytes: number
    sentBytes: number
    flows: number
    localHosts: RemoteHost[]
    /** What today's GeoIP databases say. */
    current?: GeoInfo
  }

export type FlowItem = {
  exporter: string
  start: string
  end: string
  direction: "inbound" | "outbound"
  localHost: string
  localIsExporterHost?: boolean
  localPort: number
  peer: string
  peerPort: number
  protocol: number
  bytes: number
  packets: number
}

export type FlowList = { items: FlowItem[]; more: boolean; rawFrom?: string }

export function fetchRemoteIPs(range: RangeParams, sort: "bytes" | "flows", signal?: AbortSignal) {
  const params = rangeParams(range)
  params.set("sort", sort)
  params.set("limit", "100")
  return getJSON<WithIncomplete<RemoteResult>>(`/api/remote-ips?${params}`, signal)
}

export function fetchRemoteIP(ip: string, range: RangeParams, signal?: AbortSignal) {
  return getJSON<WithIncomplete<RemoteDetail>>(`/api/remote-ips/${encodeURIComponent(ip)}?${rangeParams(range)}`, signal)
}

export function fetchRemoteFlows(ip: string, range: RangeParams, signal?: AbortSignal) {
  const params = rangeParams(range)
  params.set("limit", "100")
  return getJSON<FlowList>(`/api/remote-ips/${encodeURIComponent(ip)}/flows?${params}`, signal)
}

export type ReverseDNSName = {
  name?: string
  /** pending until the background queue gets to it; failed is retried later. */
  status: "resolved" | "none" | "pending" | "failed"
}

export function fetchReverseDNS(ips: string[], signal?: AbortSignal) {
  const params = new URLSearchParams()
  for (const ip of ips) params.append("ip", ip)
  return getJSON<{ names: Record<string, ReverseDNSName> }>(`/api/reverse-dns?${params}`, signal)
}

export type Registration = {
  handle?: string
  name?: string
  type?: string
  country?: string
  cidrs: string[]
  start?: string
  end?: string
  organisation?: string
  abuseEmail?: string
  registered?: string
  lastChanged?: string
  server?: string
}

export type RDAPResult = {
  status: "found" | "not-found"
  registration?: Registration
  fetchedAt: string
}

export function fetchRDAP(ip: string, signal?: AbortSignal) {
  return getJSON<RDAPResult>(`/api/remote-ips/${encodeURIComponent(ip)}/rdap`, signal)
}
