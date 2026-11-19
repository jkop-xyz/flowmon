package collector

import (
	"net/netip"
	"sort"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

type sessionKey struct {
	remote netip.AddrPort
	local  netip.AddrPort
}

type session struct {
	s        *ipfix.Session
	lastSeen time.Time
}

type deviationCount struct {
	count      uint64
	lastDetail string
	lastSeen   time.Time
}

// exporter is everything the collector knows about one Exporter, identified by its
// source address. Guarded by Collector.mu.
type exporter struct {
	addr                  netip.Addr
	firstSeen             time.Time
	lastSeen              time.Time
	lastSource            netip.AddrPort
	datagrams             uint64
	bytes                 uint64
	messages              uint64
	records               uint64
	templateRedefinitions uint64
	admission             Admission
	pendingDropped        uint64 // records decoded while pending, then dropped
	ignoredDatagrams      uint64
	observationDomains    map[uint32]struct{}
	deviations            map[ipfix.Deviation]*deviationCount
	sessions              map[sessionKey]*session
}

func newExporter(addr netip.Addr, now time.Time) *exporter {
	return &exporter{
		addr:               addr,
		firstSeen:          now,
		lastSeen:           now,
		observationDomains: map[uint32]struct{}{},
		deviations:         map[ipfix.Deviation]*deviationCount{},
		sessions:           map[sessionKey]*session{},
	}
}

func (e *exporter) deviation(d ipfix.Deviation, detail string, now time.Time) {
	dc := e.deviations[d]
	if dc == nil {
		dc = &deviationCount{}
		e.deviations[d] = dc
	}
	dc.count++
	dc.lastDetail = detail
	dc.lastSeen = now
}

// session returns the transport session for key, creating it if needed. When the
// Exporter has too many sessions (for example after restarts with new source ports),
// the least recently used one is dropped.
func (e *exporter) session(key sessionKey, now time.Time, limits ipfix.Limits, maxSessions int) *session {
	if s, ok := e.sessions[key]; ok {
		s.lastSeen = now
		return s
	}
	for k, s := range e.sessions {
		if now.Sub(s.lastSeen) > limits.TemplateTimeout {
			delete(e.sessions, k)
		}
	}
	if len(e.sessions) >= maxSessions {
		var oldest sessionKey
		var oldestSeen time.Time
		for k, s := range e.sessions {
			if oldestSeen.IsZero() || s.lastSeen.Before(oldestSeen) {
				oldest, oldestSeen = k, s.lastSeen
			}
		}
		delete(e.sessions, oldest)
	}
	s := &session{s: ipfix.NewSession(ipfix.TransportUDP, limits), lastSeen: now}
	e.sessions[key] = s
	return s
}

// ExporterStatus is one Exporter on the Exporters page.
type ExporterStatus struct {
	Address string `json:"address"`
	// Approval is "pending", "approved" or "ignored".
	Approval string `json:"approval"`
	// Seen is false for an approved Exporter that hasn't sent anything since Flowmon started.
	Seen bool `json:"seen"`
	// Name, Role and ClassificationMode come from the approval.
	Name                  string            `json:"name"`
	Role                  string            `json:"role"`
	ClassificationMode    string            `json:"classificationMode"`
	PendingDroppedRecords uint64            `json:"pendingDroppedRecords"`
	IgnoredDatagrams      uint64            `json:"ignoredDatagrams"`
	LastSource            string            `json:"lastSource"`
	FirstSeen             time.Time         `json:"firstSeen"`
	LastSeen              time.Time         `json:"lastSeen"`
	Datagrams             uint64            `json:"datagrams"`
	Bytes                 uint64            `json:"bytes"`
	Messages              uint64            `json:"messages"`
	Records               uint64            `json:"records"`
	ObservationDomains    []uint32          `json:"observationDomains"`
	Deviations            []DeviationStatus `json:"deviations"`
	Templates             []TemplateStatus  `json:"templates"`
	Findings              []Finding         `json:"findings"`
	// Timing is filled in by the ingest pipeline once Flows have been normalised.
	Timing *TimingStatus `json:"timing"`
	// Sampling is filled in by the ingest pipeline: whether this Exporter's Flows are
	// sampled. Nil until Flows have arrived.
	Sampling *SamplingStatus `json:"sampling"`
	// SamplingRate is the rate set by hand, for an Exporter that doesn't announce one.
	SamplingRate uint32 `json:"samplingRate"`
}

// SamplingStatus says whether an Exporter's Flows are sampled, and how Flowmon knows.
type SamplingStatus struct {
	// AnnouncedRate is what the newest Flow's record or the Exporter's Options records
	// said: N for 1 in N packets, 1 for "every packet", 0 for nothing.
	AnnouncedRate uint32 `json:"announcedRate"`
	// SampledFlows counts Flows whose counts were scaled up since Flowmon started, and
	// LastRate is the rate the newest one was scaled by (0 if the newest wasn't).
	SampledFlows uint64 `json:"sampledFlows"`
	Flows        uint64 `json:"flows"`
	LastRate     uint32 `json:"lastRate"`
}

// TimingStatus compares an Exporter's times with Flowmon's clock.
type TimingStatus struct {
	Samples int `json:"samples"`
	// ClockOffsetSeconds is the median of (received - export time): positive when the
	// Exporter's clock is behind, negative when it is ahead.
	ClockOffsetSeconds float64 `json:"clockOffsetSeconds"`
	// ExportLagSeconds is the median of (export time - flow end).
	ExportLagSeconds float64 `json:"exportLagSeconds"`
	// WatermarkSeconds is how long after a bucket ends this Exporter's data may still change.
	WatermarkSeconds float64 `json:"watermarkSeconds"`
	// LateFlows were dropped for arriving more than 2 h past the Watermark.
	LateFlows          uint64    `json:"lateFlows"`
	Flows              uint64    `json:"flows"`
	EstimatedTimeFlows uint64    `json:"estimatedTimeFlows"`
	Findings           []Finding `json:"findings"`
}

// DeviationStatus counts one kind of deviation.
type DeviationStatus struct {
	Kind       string    `json:"kind"`
	Count      uint64    `json:"count"`
	LastDetail string    `json:"lastDetail"`
	LastSeen   time.Time `json:"lastSeen"`
}

// TemplateStatus is one Template an Exporter has sent.
type TemplateStatus struct {
	// Protocol is "ipfix", "netflow9" or "netflow5".
	Protocol          string        `json:"protocol"`
	ObservationDomain uint32        `json:"observationDomain"`
	ID                uint16        `json:"id"`
	Options           bool          `json:"options"`
	ScopeFieldCount   uint16        `json:"scopeFieldCount"`
	FirstSeen         time.Time     `json:"firstSeen"`
	LastSeen          time.Time     `json:"lastSeen"`
	Fields            []FieldStatus `json:"fields"`
	Findings          []Finding     `json:"findings"`
}

// FieldStatus is one field of a Template.
type FieldStatus struct {
	Enterprise uint32 `json:"enterprise"`
	ID         uint16 `json:"id"`
	Name       string `json:"name"`
	// Type is the IANA abstract data type, empty for enterprise and unknown elements.
	Type     string `json:"type"`
	Length   uint16 `json:"length"`
	Variable bool   `json:"variable"`
	Scope    bool   `json:"scope"`
}

// noneDecodedGrace is how long an Exporter may send without any decoded records before
// the "0% decoded" finding appears. MikroTik resends Templates every 20 packets, so a
// few seconds of undecodable data after a restart is normal.
const noneDecodedGrace = 60 * time.Second

func (e *exporter) status(now time.Time) ExporterStatus {
	st := ExporterStatus{
		Address:               e.addr.String(),
		Approval:              e.admission.String(),
		Seen:                  true,
		PendingDroppedRecords: e.pendingDropped,
		IgnoredDatagrams:      e.ignoredDatagrams,
		LastSource:            e.lastSource.String(),
		FirstSeen:             e.firstSeen,
		LastSeen:              e.lastSeen,
		Datagrams:             e.datagrams,
		Bytes:                 e.bytes,
		Messages:              e.messages,
		Records:               e.records,
		Deviations:            []DeviationStatus{},
		Templates:             []TemplateStatus{},
		Findings:              []Finding{},
	}
	for od := range e.observationDomains {
		st.ObservationDomains = append(st.ObservationDomains, od)
	}
	sort.Slice(st.ObservationDomains, func(i, j int) bool { return st.ObservationDomains[i] < st.ObservationDomains[j] })

	for kind, dc := range e.deviations {
		st.Deviations = append(st.Deviations, DeviationStatus{Kind: string(kind), Count: dc.count, LastDetail: dc.lastDetail, LastSeen: dc.lastSeen})
	}
	sort.Slice(st.Deviations, func(i, j int) bool { return st.Deviations[i].Count > st.Deviations[j].Count })

	// The same Template can live in several sessions (e.g. after a source-port change);
	// show the most recently refreshed copy.
	latest := map[[2]uint32]ipfix.TemplateInfo{}
	for _, s := range e.sessions {
		for _, ti := range s.s.Templates() {
			k := [2]uint32{ti.ObservationDomain, uint32(ti.Template.ID)}
			if cur, ok := latest[k]; !ok || ti.LastSeen.After(cur.LastSeen) {
				latest[k] = ti
			}
		}
	}
	for _, ti := range latest {
		st.Templates = append(st.Templates, templateStatus(ti))
	}
	sort.Slice(st.Templates, func(i, j int) bool {
		a, b := st.Templates[i], st.Templates[j]
		if a.ObservationDomain != b.ObservationDomain {
			return a.ObservationDomain < b.ObservationDomain
		}
		return a.ID < b.ID
	})

	switch {
	case e.admission != AdmitAccept:
		// Pending and ignored Exporters get their own banner in the UI.
	case e.records == 0 && e.datagrams > 0 && now.Sub(e.firstSeen) >= noneDecodedGrace:
		st.Findings = append(st.Findings, Finding{SeverityWarning, "none_decoded",
			"0% decoded: this Exporter is sending data, but no Flows could be decoded. Check the deviations and Templates below, or turn on FLOWMON_DEBUG_CAPTURE to record what it sends."})
	case e.messages == 0 && e.datagrams > 0:
		st.Findings = append(st.Findings, Finding{SeverityWarning, "not_ipfix",
			"Nothing this Exporter sent could be read as IPFIX or NetFlow. Check that it exports IPFIX (version 10), NetFlow v9 or v5."})
	}
	return st
}

func templateStatus(ti ipfix.TemplateInfo) TemplateStatus {
	t := ti.Template
	ts := TemplateStatus{
		Protocol:          "ipfix",
		ObservationDomain: ti.ObservationDomain,
		ID:                t.ID,
		Options:           t.Options(),
		ScopeFieldCount:   t.ScopeFieldCount,
		FirstSeen:         ti.FirstSeen,
		LastSeen:          ti.LastSeen,
		Fields:            make([]FieldStatus, len(t.Fields)),
		Findings:          AssessTemplate(t),
	}
	if ts.Findings == nil {
		ts.Findings = []Finding{}
	}
	switch t.Version {
	case ipfix.VersionNetFlowV9:
		ts.Protocol = "netflow9"
	case ipfix.VersionNetFlowV5:
		ts.Protocol = "netflow5"
	}
	for i, f := range t.Fields {
		fs := FieldStatus{Enterprise: f.Enterprise, ID: f.ID, Name: f.Name(), Length: f.Length, Variable: f.Variable(), Scope: i < int(t.ScopeFieldCount)}
		if e, ok := f.Element(); ok {
			fs.Type = e.Type.String()
		}
		ts.Fields[i] = fs
	}
	return ts
}
