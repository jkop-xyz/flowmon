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
  /** Which protocol defined it. */
  protocol: "ipfix" | "netflow9" | "netflow5"
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
  /** Empty until the Exporter is approved. */
  classificationMode: ClassificationMode | ""
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
  /** Whether its Flows are sampled; null before any arrived. */
  sampling: SamplingStatus | null
  /** The rate set by hand, for an Exporter that samples without saying so; 0 for none. */
  samplingRate: number
  subnets: string[] | null
  multiplePoints: boolean
  /** How its Flows were counted since Flowmon started; null before any arrived. */
  counting: { flows: number; uncounted: number; duplicatesRemoved: number } | null
}

export type SamplingStatus = {
  /** What the newest Flow's Exporter announced: N for 1 in N, 1 for every packet, 0 for nothing. */
  announcedRate: number
  flows: number
  /** Flows scaled up since Flowmon started, and the rate the newest was scaled by (0: not). */
  sampledFlows: number
  lastRate: number
}

export const setSamplingRate = (address: string, samplingRate: number) =>
  putJSON<unknown>(`/api/exporters/${encodeURIComponent(address)}/sampling`, { samplingRate })

export type ListenerStatus = {
  address: string
  requestedBuffer: number
  /** What the kernel granted; 0 when unknown. */
  grantedBuffer: number
  kernelDrops: number
  /** False where the kernel can't report drops. */
  dropsCounted: boolean
  datagrams: number
}

export type PipelineStats = {
  written: number
  /** Flows that didn't fit in the write queue. */
  dropped: number
  late: number
  writeErrors: number
  noAddresses: number
  optionsRecords: number
  /** NAT session events (RouterOS sends them): not traffic, not Flows. */
  natEvents: number
  queued: number
  queueCapacity: number
  lastWrite?: string
}

export type CollectorStatus = {
  listeners: ListenerStatus[]
  started: string
  lastAccepted?: string
  lastAcceptedFrom?: string
  pipeline?: PipelineStats
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
  /** The part of the three that was scaled up from sampled Flows. */
  estimatedBps?: number
}

export type Source = "raw" | "minute" | "hour"

export type BandwidthResult = {
  from: string
  to: string
  stepSeconds: number
  source: Source
  points: BandwidthPoint[]
  estimatedBytes: number
}

/** Query results carry the time after which data may still change (the Watermark). */
export type ClassificationChange = { exporter: string; t: string; description: string }

export const fetchClassificationHistory = (address: string, signal?: AbortSignal) =>
  getJSON<{ changes: ClassificationChange[] }>(`/api/exporters/${encodeURIComponent(address)}/classification-changes`, signal)

/** Removes a recorded change and its chart marker; the Flows keep their classification. */
export async function deleteClassificationChange(address: string, t: string) {
  const path = `/api/exporters/${encodeURIComponent(address)}/classification-changes?t=${encodeURIComponent(t)}`
  const response = await fetch(path, { method: "DELETE" })
  if (!response.ok) await parse(path, response)
}

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

export type TopDimension = "hosts" | "devices" | "remotes" | "protocols" | "ports" | "countries" | "asns"

export type TopItem = {
  /** A Local Host's address, or a Host Label's prefix whose addresses count as one. */
  host?: string
  /** How many addresses a Host Label's prefix row merges. */
  addresses?: number
  /** A Device's last minute or hour with traffic in the range (the devices list). */
  lastSeen?: string
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
  /** The part of the bytes scaled up from sampled Flows: an estimate. */
  estimatedBytes?: number
  /** A Device's MAC address ("devices"); host is then the address it used last. */
  mac?: string
  /** The Device's maker, from the IEEE registry. */
  vendor?: string
  /** The MAC is randomized: it may change, and has no vendor. */
  privateMac?: boolean
}

export type TopResult = {
  from: string
  to: string
  source: Source
  items: TopItem[]
  totalBytes: number
  estimatedBytes: number
  /** Set when the list came from the Top Remote IPs lists (see Coverage). */
  approximate?: boolean
  /** How many values had traffic, listed or not; a lower bound when approximate. */
  count: number
}

export function fetchTop(dimension: TopDimension, range: RangeParams, signal?: AbortSignal, limit?: number) {
  const params = rangeParams(range)
  params.set("dimension", dimension)
  if (limit) params.set("limit", String(limit))
  return getJSON<WithIncomplete<TopResult>>(`/api/overview/top?${params}`, signal)
}

/** One Registrable Domain's traffic; registrable is "" for traffic no Local Host looked up. */
export type DomainItem = {
  registrable: string
  receivedBytes: number
  sentBytes: number
  totalBytes: number
  flows: number
  localHosts: number
  devices: number
}

export type DomainResult = {
  from: string
  to: string
  source: Source
  items: DomainItem[]
  totalBytes: number
  count: number
  /** How far domain totals are counted: they run a few minutes behind. */
  countedThrough?: string
  /** The first minute counted: traffic from before has no domain. */
  since?: string
}

/** The busiest Registrable Domains, for the whole network or one Local Host. */
export function fetchDomains(range: RangeParams, opts: { host?: string; limit?: number }, signal?: AbortSignal) {
  const params = rangeParams(range)
  if (opts.host) params.set("host", opts.host)
  if (opts.limit) params.set("limit", String(opts.limit))
  return getJSON<{ result: DomainResult; dnsSources: number }>(`/api/domains?${params}`, signal)
}

export type RemoteName = { name: string; lookups: number; clients: number; last: string }

/** The names a Remote IP was looked up as, in the last `days` days. */
export function fetchRemoteNames(ip: string, signal?: AbortSignal) {
  return getJSON<{ names: RemoteName[]; days: number }>(`/api/remote-ips/${encodeURIComponent(ip)}/names`, signal)
}

export type ExporterOption = { address: string; name: string }

export function fetchExporterOptions(signal?: AbortSignal) {
  return getJSON<ExporterOption[]>("/api/exporters/options", signal)
}

export type ClassificationMode = "address" | "interface"

export type ExporterRole = "edge" | "internal"

/** How an Exporter's Flows are counted across Exporters. */
export type Counting = {
  role: ExporterRole
  /** An Internal Exporter's LAN subnets: only Flows between two hosts in one of them count. */
  subnets: string[]
  /** It sees the same traffic at several points, so duplicates are removed. */
  multiplePoints: boolean
}

export type ApprovalRequest = Counting & { name: string; classificationMode: ClassificationMode }

export const setCounting = (address: string, counting: Counting) =>
  putJSON<unknown>(`/api/exporters/${encodeURIComponent(address)}/counting`, counting)

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

/** The SNMP Data Source that polls an Exporter, if any. */
export type SNMPView = {
  source: { id: string; name: string; enabled: boolean; health: SourceHealth } | null
}

export const fetchSNMP = (address: string, signal?: AbortSignal) =>
  getJSON<SNMPView>(`${exporterPath(address)}/snmp`, signal)

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
  /** Shown; the license key never is. */
  maxMindAccountId?: string
  /** Why a stored MaxMind account isn't used. */
  maxMindWarning?: string
  /** Settings set by environment variable, which can't be changed here. */
  locks: { download: boolean; maxMind: boolean }
  /** A refresh is checking for or downloading databases now. */
  downloading: boolean
  dir: string
  databases: GeoIPDatabase[]
  warnings: string[]
}

export function fetchGeoIP(signal?: AbortSignal) {
  return getJSON<GeoIPStatus>("/api/geoip", signal)
}

/** A change to the GeoIP settings. An empty license key keeps the saved one; an empty account ID removes the account. */
export type GeoIPUpdate = { download?: boolean; maxMindAccountId?: string; maxMindLicenseKey?: string }

export const saveGeoIP = (u: GeoIPUpdate) => putJSON<GeoIPStatus>("/api/geoip", u)

/** Retries failed downloads now instead of in a few hours. */
export const retryGeoIP = () => postJSON<GeoIPStatus>("/api/geoip/refresh", {})

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
  /** The part of the bytes scaled up from sampled Flows: an estimate. */
  estimatedBytes?: number
}

export type RemoteResult = Coverage & {
  items: RemoteItem[]
  totalBytes: number
  otherBytes: number
  estimatedBytes: number
}

export type RemoteHost = {
  host: string
  localIsExporterHost?: boolean
  receivedBytes: number
  sentBytes: number
  totalBytes: number
  flows: number
  /** The part of the bytes scaled up from sampled Flows: an estimate. */
  estimatedBytes?: number
}

export type GeoInfo = { country?: string; asn?: number; asnOrg?: string }

export type RemoteDetail = Coverage &
  GeoInfo & {
    remoteIp: string
    receivedBytes: number
    sentBytes: number
    flows: number
    estimatedBytes: number
    localHosts: RemoteHost[]
    /** What today's GeoIP databases say. */
    current?: GeoInfo
  }

/** "uncounted": an Internal Exporter's routed Flow, which an Edge Exporter counts instead. */
export type Direction = "inbound" | "outbound" | "internal" | "transit" | "uncounted"

export type FlowItem = {
  /** Finds the Flow again for its detail view. */
  id: string
  exporter: string
  start: string
  end: string
  direction: Direction
  src: string
  dst: string
  srcPort: number
  dstPort: number
  /** Absent for transit Flows. */
  localHost?: string
  localIsExporterHost?: boolean
  localPort: number
  peer?: string
  peerPort: number
  protocol: number
  /** Whichever of the two ports identifies the service. */
  servicePort: number
  bytes: number
  packets: number
  /** N when the Exporter sampled 1 in N packets: bytes and packets are then estimates, scaled up by N. */
  samplingRate?: number
  country?: string
  asn?: number
  asnOrg?: string
  /** The name the Local Host looked up for the Remote IP (Domain Attribution), if known. */
  domain?: string
}

export type FlowPage = {
  items: FlowItem[]
  total: number
  rawFrom?: string
  /** What a Host Name in the Local Host filter matched: the Local Hosts with their names. */
  hostMatch?: { name: string; hosts: { host: string; name: string }[] }
}

export type FieldView = {
  enterprise?: number
  id: number
  name: string
  /** IANA defines the field, so value is decoded by its type. */
  known: boolean
  type?: string
  value: string
  raw: string
  error?: string
}

export type FlowDetail = FlowItem & {
  observationDomain: number
  received: string
  exportTime: string
  timeEstimated: boolean
  postNatSrc?: string
  postNatDst?: string
  postNatSrcPort?: number
  postNatDstPort?: number
  ingressInterface: number
  egressInterface: number
  fields: FieldView[]
  fieldsError?: string
  /** One direction of an RFC 5103 biflow record, which is stored as two Flows. */
  biflow?: "forward" | "reverse"
}

export type FlowSort = "end" | "start" | "bytes" | "packets" | "duration"

/** Flow Explorer filters, as the API and the page URL name them. */
export type FlowFilters = {
  host?: string
  remote?: string
  asn?: string
  country?: string
  protocol?: string
  port?: string
  /** A name, keeping Flows to it and its subdomains; "-" keeps Flows with no DNS lookup. */
  domain?: string
  /** A Device's MAC: Flows of its Local Host, or Internal Peer. */
  device?: string
  /** Comma-separated Directions. */
  direction?: string
}

export function fetchFlows(
  range: RangeParams,
  filters: FlowFilters,
  page: { sort: FlowSort; asc: boolean; limit: number; offset: number },
  signal?: AbortSignal,
) {
  const params = rangeParams(range)
  for (const [k, v] of Object.entries(filters)) if (v) params.set(k, v)
  params.set("sort", page.sort)
  params.set("order", page.asc ? "asc" : "desc")
  params.set("limit", String(page.limit))
  params.set("offset", String(page.offset))
  return getJSON<FlowPage>(`/api/flows?${params}`, signal)
}

export function fetchFlow(id: string, signal?: AbortSignal) {
  return getJSON<FlowDetail>(`/api/flows/${encodeURIComponent(id)}`, signal)
}

/** What the server ranks Remote IPs by: the list holds the top ones by it. */
export type RemoteSort = "total" | "received" | "sent" | "flows" | "hosts"

export function fetchRemoteIPs(range: RangeParams, sort: RemoteSort, asc: boolean, signal?: AbortSignal) {
  const params = rangeParams(range)
  params.set("sort", sort)
  if (asc) params.set("order", "asc")
  params.set("limit", "100")
  return getJSON<WithIncomplete<RemoteResult>>(`/api/remote-ips?${params}`, signal)
}

export function fetchRemoteIP(ip: string, range: RangeParams, signal?: AbortSignal) {
  return getJSON<WithIncomplete<RemoteDetail>>(`/api/remote-ips/${encodeURIComponent(ip)}?${rangeParams(range)}`, signal)
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

export type HostLabel = { id: string; prefix: string; name: string; createdAt: string; updatedAt: string }

export function fetchHostLabels(signal?: AbortSignal) {
  return getJSON<{ labels: HostLabel[] }>("/api/host-labels", signal)
}

export const createHostLabel = (label: { prefix: string; name: string }) => postJSON<HostLabel>("/api/host-labels", label)

export const updateHostLabel = (id: string, label: { prefix: string; name: string }) =>
  putJSON<HostLabel>(`/api/host-labels/${encodeURIComponent(id)}`, label)

export async function deleteHostLabel(id: string) {
  const path = `/api/host-labels/${encodeURIComponent(id)}`
  const response = await fetch(path, { method: "DELETE" })
  if (!response.ok) await parse(path, response)
}

export type HostName = {
  name?: string
  /** label wins over dns; absent when there is no name. */
  source?: "label" | "dns"
  status: ReverseDNSName["status"]
  /** The Host Label that named it, which may be on a wider prefix. */
  label?: { id: string; prefix: string; name: string }
}

export function fetchHostNames(hosts: string[], signal?: AbortSignal) {
  const params = new URLSearchParams()
  for (const h of hosts) params.append("host", h)
  return getJSON<{ names: Record<string, HostName> }>(`/api/host-names?${params}`, signal)
}

export type HostItem = {
  protocol?: number
  servicePort?: number
  host?: string
  addresses?: number
  country?: string
  asn?: number
  asnOrg?: string
  receivedBytes: number
  sentBytes: number
  totalBytes: number
  flows: number
  /** The part of the bytes scaled up from sampled Flows: an estimate. */
  estimatedBytes?: number
}

export type HostSummary = {
  from: string
  to: string
  source: Source
  addresses: { address: string; receivedBytes: number; sentBytes: number; totalBytes: number }[]
  localIsExporterHost?: boolean
  receivedBytes: number
  sentBytes: number
  remoteBytes: number
  internalBytes: number
  flows: number
  estimatedBytes: number
  ports: HostItem[]
  localPeers: HostItem[]
  countries: HostItem[]
  asns: HostItem[]
}

export type HostTraffic = {
  from: string
  to: string
  stepSeconds: number
  source: Source
  points: { t: string; receivedBps: number; sentBps: number; estimatedBps?: number }[]
  estimatedBytes: number
}

function hostParams(host: string, range: RangeParams) {
  const params = rangeParams(range)
  params.set("host", host)
  return params
}

export function fetchHostSummary(host: string, range: RangeParams, signal?: AbortSignal) {
  return getJSON<WithIncomplete<HostSummary>>(`/api/local-hosts/summary?${hostParams(host, range)}`, signal)
}

export function fetchHostTraffic(host: string, range: RangeParams, signal?: AbortSignal) {
  return getJSON<WithIncomplete<HostTraffic>>(`/api/local-hosts/traffic?${hostParams(host, range)}`, signal)
}

export function fetchHostRemoteIPs(host: string, range: RangeParams, sort: RemoteSort, asc: boolean, signal?: AbortSignal) {
  const params = hostParams(host, range)
  params.set("sort", sort)
  if (asc) params.set("order", "asc")
  params.set("limit", "20")
  return getJSON<WithIncomplete<RemoteResult>>(`/api/local-hosts/remote-ips?${params}`, signal)
}

/** A name the user gave a Service Port; protocol 0 names it for every protocol. */
export type UserServiceName = {
  protocol: number
  port: number
  name: string
  updatedAt: string
  /** What the IANA registry calls the port. */
  ianaName?: string
}

export type ServiceName = { name?: string; description?: string; source?: "user" | "iana" }

export function fetchServiceNames(signal?: AbortSignal) {
  return getJSON<{ names: UserServiceName[] }>("/api/service-names", signal)
}

/** Service Names for "protocol/port" keys. */
export function lookupServiceNames(keys: string[], signal?: AbortSignal) {
  const params = new URLSearchParams()
  for (const k of keys) params.append("port", k)
  return getJSON<{ names: Record<string, ServiceName> }>(`/api/service-names/lookup?${params}`, signal)
}

export const setServiceName = (protocol: number, port: number, name: string) =>
  putJSON<UserServiceName>(`/api/service-names/${protocol}/${port}`, { name })

export async function deleteServiceName(protocol: number, port: number) {
  const path = `/api/service-names/${protocol}/${port}`
  const response = await fetch(path, { method: "DELETE" })
  if (!response.ok) await parse(path, response)
}

/** How the App Store's database is named in the UI. */
export const appStoreNames = { sqlite: "SQLite (built in)", postgres: "PostgreSQL" } as const

export type AppStoreStatus = {
  configured: boolean
  available: boolean
  since?: string
  error?: string
  driver?: "sqlite" | "postgres"
  /** The SQLite path or the Postgres URL and schema, without the password. */
  location?: string
}

export type CollectionStatus = {
  approvedExporters: number
  lastFlow?: string
  lastFlowFrom?: string
  /** Approved Exporters exist but no Flows arrived for warnAfterSeconds. */
  quiet: boolean
  warnAfterSeconds: number
}

/** Present when FLOWMON_FLOW_STORE_MAX_GB caps the Flow Store. */
export type FlowStoreCapStatus = { usedBytes: number; maxBytes: number; overCap: boolean }

export function fetchStatus(signal?: AbortSignal) {
  return getJSON<{ appStore?: AppStoreStatus; collection?: CollectionStatus; flowStore?: FlowStoreCapStatus }>(
    "/api/status",
    signal,
  )
}

export type FlowStoreInfo = {
  /** The Flow Store's file, which a backup replaces to restore. */
  path: string
  /** What the data takes; space freed by drops is reused before the file grows. */
  usedBytes: number
  fileBytes: number
  /** 0 without a cap. */
  maxBytes: number
  cap?: {
    checked?: string
    /** Still over the cap with nothing left the cap may drop. */
    over: boolean
    lastDrop?: string
    dropped: string[]
  }
  memoryBytes: number
  memoryLimit: string
  threads: number
  rawDays: number
  oldestRawDay?: string
  /** Whether FLOWMON_BACKUP_TOKEN is set. */
  backupToken: boolean
}

export function fetchFlowStore(signal?: AbortSignal) {
  return getJSON<FlowStoreInfo>("/api/flow-store", signal)
}

export const flowStoreBackupURL = "/api/flow-store/backup"

export type RetentionSetting = { seconds: number; locked: boolean; env: string }

export type Settings = {
  /** The internet line's speed in bits per second; 0 when not set. */
  line: { downBps: number; upBps: number }
  localNetworks: { value: string[]; defaults: string[]; locked: boolean; env: string }
  retention: Record<"raw" | "minute" | "hour" | "dns", RetentionSetting>
  minRetentionDays: number
  maxRetentionDays: number
}

export function fetchSettings(signal?: AbortSignal) {
  return getJSON<Settings>("/api/settings", signal)
}

export const saveSettings = (change: {
  localNetworks?: string[]
  retentionDays?: Partial<Record<"raw" | "minute" | "hour" | "dns", number>>
  line?: { downBps: number; upBps: number }
}) => putJSON<Settings>("/api/settings", change)

/** How a Data Source setting is entered: secret ones are never sent back. */
export type SourceFieldType = "text" | "url" | "secret" | "bool" | "select"

export type SourceField = {
  key: string
  label: string
  help?: string
  type: SourceFieldType
  required?: boolean
  default?: string
  /** A select's choices; "" means none. */
  options?: string[]
  /** Where the source connects. Changing it means entering the secret fields again. */
  endpoint?: boolean
}

/** A kind of Data Source, such as AdGuard Home. */
export type SourceKind = { name: string; title: string; description: string; fields: SourceField[] }

export type SourceState = "disabled" | "starting" | "ok" | "failing"

export type SourceHealth = {
  state: SourceState
  since: string
  lastSuccess?: string
  detail?: string
  lastError?: string
  error?: string
  warnings?: string[]
}

/** A configured Data Source: settings without their secrets, which secretsSet lists. */
export type DataSource = {
  id: string
  kind: string
  name: string
  enabled: boolean
  settings: Record<string, string>
  created: string
  secretsSet: string[]
  /** Set by FLOWMON_SOURCE_* environment variables: not editable here. */
  locked: boolean
  health: SourceHealth
}

export type SourceInput = { kind: string; name: string; enabled: boolean; settings: Record<string, string> }

export function fetchSources(signal?: AbortSignal) {
  return getJSON<{ kinds: SourceKind[]; sources: DataSource[] }>("/api/sources", signal)
}

export const createSource = (s: SourceInput) => postJSON<DataSource>("/api/sources", s)

export const updateSource = (id: string, s: SourceInput) => putJSON<DataSource>(`/api/sources/${encodeURIComponent(id)}`, s)

export async function deleteSource(id: string) {
  const path = `/api/sources/${encodeURIComponent(id)}`
  const response = await fetch(path, { method: "DELETE" })
  if (!response.ok) await parse(path, response)
}

/** Connects once with these settings; with an id, empty secrets mean the stored ones. */
export const testSource = (s: SourceInput & { id?: string }) =>
  postJSON<{ ok: boolean; message: string }>("/api/sources/test", s)

export type DNSPoint = { t: string; queries: number; blocked: number }
export type DNSName = { name: string; queries: number; blocked: number; clients: number; last: string }
export type DNSClient = { client: string; clientName?: string; queries: number; blocked: number; last: string }
export type DNSUpstream = { upstream: string; queries: number; avgMs: number; p95Ms: number }
export type DNSAnswer = { address: string; queries: number; last: string }

/** Lookups in a range: the network's, one Local Host's or one domain's (then topNames are full names). */
export type DNSSummary = {
  from: string
  to: string
  stepSeconds: number
  queries: number
  blocked: number
  clients: number
  /** Lookups per minute. */
  points: DNSPoint[]
  topNames: DNSName[]
  topBlocked: DNSName[]
  topClients: DNSClient[]
  upstreams: DNSUpstream[]
  answers?: DNSAnswer[]
  /** The oldest day of DNS Queries kept. */
  since?: string
}

export function fetchDNS(range: RangeParams, opts: { host?: string; domain?: string }, signal?: AbortSignal) {
  const params = rangeParams(range)
  if (opts.host) params.set("host", opts.host)
  if (opts.domain) params.set("domain", opts.domain)
  return getJSON<{ result: DNSSummary; dnsSources: number }>(`/api/dns?${params}`, signal)
}

export type DeviceAddress = { address: string; firstSeen: string; lastSeen: string }

/** A Device: a MAC address and the addresses it used (ADR 0005). */
export type Device = {
  mac: string
  vendor?: string
  /** Randomized (locally administered): it may change, and has no vendor. */
  privateMac?: boolean
  /** A router hop's MAC, never a Device. */
  router?: boolean
  /** Most recently seen first, the merged MACs' included. */
  addresses: DeviceAddress[]
  /** The MACs counted as this Device: "auto" for Private Addresses with the same name. */
  merged?: { mac: string; vendor?: string; reason: "auto" | "manual"; mergedAt: string }[]
  /** The Device this MAC now counts as. */
  mergedInto?: string
  /** Where SNMP sources' neighbor tables saw it, most recently first. */
  neighbors?: {
    source: string
    sourceName?: string
    address: string
    ifIndex: number
    interface?: string
    firstSeen: string
    lastSeen: string
  }[]
}

/** A Device neighbor tables saw in a range that no Flow had. */
export type QuietDevice = { mac: string; vendor?: string; privateMac?: boolean; address: string; lastSeen: string }

export function fetchQuietDevices(range: RangeParams, signal?: AbortSignal) {
  return getJSON<{ devices: QuietDevice[] }>(`/api/devices/quiet?${rangeParams(range)}`, signal)
}

/** Counts mac as the Device into, with everything merged into mac. */
export const mergeDevice = (mac: string, into: string) =>
  postJSONNoContent(`/api/devices/${encodeURIComponent(mac)}/merge`, { into })

/** Undoes mac's merge: it counts as its own Device again. */
export async function splitDevice(mac: string) {
  const path = `/api/devices/${encodeURIComponent(mac)}/merge`
  const response = await fetch(path, { method: "DELETE" })
  if (!response.ok) await parse(path, response)
}

async function postJSONNoContent(path: string, body: unknown) {
  const response = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) })
  if (!response.ok) await parse(path, response)
}

export function fetchDevice(mac: string, signal?: AbortSignal) {
  return getJSON<Device>(`/api/devices/${encodeURIComponent(mac)}`, signal)
}

/** The Devices that used an address, most recently first. */
export function fetchDevicesAt(address: string, signal?: AbortSignal) {
  return getJSON<{ devices: Device[] }>(`/api/devices?address=${encodeURIComponent(address)}`, signal)
}

/** Names given to Devices, by MAC. Empty when the App Store is unavailable. */
export function fetchDeviceLabels(signal?: AbortSignal) {
  return getJSON<{ labels: Record<string, string> }>("/api/device-labels", signal)
}

/** Names a Device; an empty name removes its label. */
export const setDeviceLabel = (mac: string, name: string) =>
  putJSON<{ mac: string; name: string }>(`/api/device-labels/${encodeURIComponent(mac)}`, { name })

/** A DNS server a Local Host or Device sent to; "other" ones bypass the DNS Data Sources. */
export type DNSServer = {
  server: string
  port: number
  flows: number
  bytes: number
  last: string
  kind: "source" | "exporter" | "other"
}

export function fetchDNSServers(host: string, range: RangeParams, signal?: AbortSignal) {
  const params = rangeParams(range)
  params.set("host", host)
  return getJSON<{ servers: DNSServer[]; dnsSources: number }>(`/api/dns-servers?${params}`, signal)
}

export type SNMPInterface = {
  source: string
  ifIndex: number
  name: string
  alias?: string
  /** Bits per second; 0 when unknown. */
  speed: number
  up: boolean
  lastSeen: string
  /** The latest minute's rates and the last hour's errors; absent without recent counters. */
  now?: { at: string; inBps: number; outBps: number; hourErrors: number; hourDiscards: number }
}

/**
 * Device Health readings by name: uptime (s), cpu (%), memory_used and memory_total (bytes), wifi_clients, and
 * sensors as "<kind>:<name>" with kind temperature (°C), voltage (V), current (A), power (W) or fan (rpm).
 */
export type HealthMetrics = Record<string, number>

export type WifiClient = {
  mac: string
  ifIndex: number
  interface?: string
  /** dBm. */
  signal: number
  txRate: number
  rxRate: number
  connectedSeconds: number
  seen: string
}

export type SNMPDevice = {
  source: { id: string; name: string; enabled: boolean; health: SourceHealth }
  address: string
  exporter?: string
  interfaces: SNMPInterface[]
  /** The latest minute of Device Health; absent before the first poll. */
  health?: { at: string; metrics: HealthMetrics; boot?: string }
  wifi: WifiClient[]
}

export function fetchSNMPDevice(id: string, signal?: AbortSignal) {
  return getJSON<SNMPDevice>(`/api/snmp-devices/${encodeURIComponent(id)}`, signal)
}

export type InterfacePoint = { t: string; inBps: number; outBps: number; errors: number; discards: number; coverage: number }

export function fetchInterfaceSeries(id: string, ifIndex: number, range: RangeParams, signal?: AbortSignal) {
  return getJSON<{ points: InterfacePoint[]; stepSeconds: number; restarts: string[] }>(
    `/api/snmp-devices/${encodeURIComponent(id)}/interfaces/${ifIndex}?${rangeParams(range)}`,
    signal,
  )
}

export type HealthPoint = { t: string; metrics: HealthMetrics }

/** A device's Device Health over a range, each reading's mean per bucket, and when it restarted. */
export function fetchDeviceHealth(id: string, range: RangeParams, signal?: AbortSignal) {
  return getJSON<{ points: HealthPoint[]; stepSeconds: number; restarts: string[] }>(
    `/api/snmp-devices/${encodeURIComponent(id)}/health?${rangeParams(range)}`,
    signal,
  )
}

/** The WAN's load from Interface Counters: every Exporter's WAN Interfaces that an SNMP source polls, summed. */
export type WANLoad = {
  interfaces: { source: string; sourceName: string; ifIndex: number; name: string; speed: number }[]
  /** inBps is download, outBps upload. */
  points: { t: string; inBps: number; outBps: number; inBytes: number; outBytes: number }[]
  stepSeconds: number
  /** The latest minute, when recent. */
  now?: { t: string; inBps: number; outBps: number }
  /** What load is measured against: the line set in Settings, else the interfaces' speed. */
  line: { downBps: number; upBps: number; fromSetting: boolean }
  /** What crossed the WAN over the range, and the polled seconds it covers (for an average rate). */
  total: { inBytes: number; outBytes: number; seconds: number }
}

export type DeviceHealthSummary = {
  source: string
  name: string
  state: SourceState
  at?: string
  metrics: HealthMetrics
  /** In the last day, newest first. */
  restarts: string[]
}

export type InterfaceProblem = {
  source: string
  sourceName: string
  ifIndex: number
  name: string
  alias?: string
  /** A named interface (one with a description) that is down. */
  down: boolean
  /** In the last hour, both directions. */
  errors: number
  discards: number
}

export type NetworkOverview = { wan: WANLoad | null; devices: DeviceHealthSummary[]; problems: InterfaceProblem[] }

export function fetchNetworkOverview(range: RangeParams, signal?: AbortSignal) {
  return getJSON<NetworkOverview>(`/api/overview/network?${rangeParams(range)}`, signal)
}

/** Who is using the internet now: the last few minutes' traffic with Remote IPs, by Device and by domain. */
export type UsageNow = {
  from: string
  to: string
  devices: { mac?: string; vendor?: string; privateMac?: boolean; host: string; inBps: number; outBps: number }[]
  domains: { domain: string; inBps: number; outBps: number; topMac?: string; topHost: string }[]
}

export function fetchUsageNow(signal?: AbortSignal) {
  return getJSON<UsageNow>("/api/overview/now", signal)
}

/** One Overview section in a user's saved layout; the id may be one this build doesn't know. */
export type SavedSection = { id: string; hidden: boolean }

/** The signed-in user's saved Overview layout; null when they have never saved one. */
export async function fetchOverviewLayout(signal?: AbortSignal): Promise<SavedSection[] | null> {
  return (await getJSON<{ sections: SavedSection[] | null }>("/api/me/overview-layout", signal)).sections
}
