package flowstore

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/rollup"
)

// MaxFlowList is the most Flows one page returns; MaxFlowOffset the deepest page.
const (
	MaxFlowList   = 1000
	MaxFlowOffset = 100_000
)

// FlowSort is what a Flow list is ordered by.
type FlowSort string

// Flow list orders.
const (
	SortEnd      FlowSort = "end"
	SortStart    FlowSort = "start"
	SortBytes    FlowSort = "bytes"
	SortPackets  FlowSort = "packets"
	SortDuration FlowSort = "duration"
)

var sortColumns = map[FlowSort]string{
	SortEnd: "end_ts", SortStart: "start_ts", SortBytes: "bytes", SortPackets: "packets",
	SortDuration: "date_diff('millisecond', start_ts, end_ts)",
}

// ErrUnknownSort is returned for an unsupported FlowSort.
var ErrUnknownSort = errors.New("flowstore: unknown sort")

// FlowQuery filters, sorts and pages raw Flows that overlap [From, To). Zero filters
// match everything.
type FlowQuery struct {
	From, To time.Time
	Exporter netip.Addr
	// LocalHosts, when not nil, keeps Flows with one of these Local Hosts (addresses) at
	// either end; none matches nothing.
	LocalHosts []string
	// Remote keeps Flows with this Remote IP.
	Remote netip.Addr
	// ASN and Country keep Flows whose Remote IP had them; HasASN says ASN is set (ASN
	// 0 is "unknown").
	ASN     uint32
	HasASN  bool
	Country string
	// Protocol keeps one IP protocol when HasProtocol is set.
	Protocol    uint8
	HasProtocol bool
	// Port keeps Flows with this port at either end, before or after NAT.
	Port uint16
	// Domain keeps Flows whose domain is this name or under it ("bbc.co.uk" keeps
	// "www.bbc.co.uk"); NoDomain keeps Inbound and Outbound Flows without one.
	Domain   string
	NoDomain bool
	// Directions keeps these Directions; empty keeps all.
	Directions []flow.Direction
	Sort       FlowSort
	// Asc sorts ascending; the default is descending (newest, largest first).
	Asc    bool
	Limit  int
	Offset int
}

// FlowItem is one raw Flow in a list.
type FlowItem struct {
	// ID finds the Flow again for its detail view.
	ID                  string    `json:"id"`
	Exporter            string    `json:"exporter"`
	Start               time.Time `json:"start"`
	End                 time.Time `json:"end"`
	Direction           string    `json:"direction"`
	Src                 string    `json:"src"`
	Dst                 string    `json:"dst"`
	SrcPort             uint16    `json:"srcPort"`
	DstPort             uint16    `json:"dstPort"`
	LocalHost           string    `json:"localHost,omitempty"`
	LocalIsExporterHost bool      `json:"localIsExporterHost,omitempty"`
	LocalPort           uint16    `json:"localPort"`
	Peer                string    `json:"peer,omitempty"`
	PeerPort            uint16    `json:"peerPort"`
	Protocol            uint8     `json:"protocol"`
	// ServicePort is whichever of the two ports identifies the service.
	ServicePort uint16 `json:"servicePort"`
	Bytes       uint64 `json:"bytes"`
	Packets     uint64 `json:"packets"`
	// SamplingRate is N when the Exporter sampled 1 in N packets: Bytes and Packets are
	// then estimates, scaled up by N.
	SamplingRate uint32 `json:"samplingRate,omitempty"`
	Country      string `json:"country,omitempty"`
	ASN          uint32 `json:"asn,omitempty"`
	ASNOrg       string `json:"asnOrg,omitempty"`
	// Domain is the name the Local Host looked up for the Remote IP, if known.
	Domain string `json:"domain,omitempty"`
}

// FlowPage is one page of raw Flows.
type FlowPage struct {
	Items []FlowItem `json:"items"`
	// Total is how many Flows match.
	Total int `json:"total"`
	// RawFrom is the start of the oldest raw Flows still kept; nil when there are none.
	RawFrom *time.Time `json:"rawFrom,omitempty"`
}

// flowKey identifies a raw Flow: raw Flows have no stored ID, and DuckDB row ids change
// when rows are deleted (reprocessing does). Identical Flows share a key; they are
// indistinguishable anyway.
type flowKey struct {
	exporter         string
	domain           uint32
	received         time.Time
	start, end       time.Time
	src, dst         string
	srcPort, dstPort uint16
	protocol         uint8
	bytes            uint64
}

func (k flowKey) String() string {
	s := strings.Join([]string{k.exporter, strconv.FormatUint(uint64(k.domain), 10),
		strconv.FormatInt(k.received.UnixMicro(), 10), strconv.FormatInt(k.start.UnixMicro(), 10), strconv.FormatInt(k.end.UnixMicro(), 10),
		k.src, k.dst, strconv.Itoa(int(k.srcPort)), strconv.Itoa(int(k.dstPort)), strconv.Itoa(int(k.protocol)),
		strconv.FormatUint(k.bytes, 10)}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

// ErrNotFound is returned for a Flow that doesn't exist (any more).
var ErrNotFound = errors.New("flowstore: not found")

func parseFlowKey(id string) (flowKey, error) {
	b, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return flowKey{}, ErrNotFound
	}
	f := strings.Split(string(b), "|")
	if len(f) != 11 {
		return flowKey{}, ErrNotFound
	}
	var nums [8]uint64
	for i, j := range []int{1, 2, 3, 4, 7, 8, 9, 10} {
		v, err := strconv.ParseInt(f[j], 10, 64)
		if err != nil {
			return flowKey{}, ErrNotFound
		}
		nums[i] = uint64(v)
	}
	return flowKey{exporter: f[0], domain: uint32(nums[0]), received: time.UnixMicro(int64(nums[1])).UTC(),
		start: time.UnixMicro(int64(nums[2])).UTC(), end: time.UnixMicro(int64(nums[3])).UTC(), src: f[5], dst: f[6],
		srcPort: uint16(nums[4]), dstPort: uint16(nums[5]), protocol: uint8(nums[6]), bytes: nums[7]}, nil
}

// listColumns are the columns scanFlowItem reads.
const listColumns = `exporter, observation_domain, received, start_ts, end_ts, direction, src, dst, src_port, dst_port,
	local_host, local_is_exporter_host, local_port, peer, peer_port, protocol, bytes, packets,
	coalesce(peer_country, ''), coalesce(peer_asn, 0), coalesce(sampling_rate, 0), coalesce(domain, '')`

func (s *Store) scanFlowItem(rows interface{ Scan(...any) error }) (FlowItem, error) {
	var f FlowItem
	var k flowKey
	var dir uint8
	var local, peer sql.NullString
	if err := rows.Scan(&f.Exporter, &k.domain, &k.received, &f.Start, &f.End, &dir, &f.Src, &f.Dst, &f.SrcPort, &f.DstPort,
		&local, &f.LocalIsExporterHost, &f.LocalPort, &peer, &f.PeerPort, &f.Protocol, &f.Bytes, &f.Packets,
		&f.Country, &f.ASN, &f.SamplingRate, &f.Domain); err != nil {
		return f, err
	}
	f.Start, f.End, f.LocalHost, f.Peer = f.Start.UTC(), f.End.UTC(), local.String, peer.String
	f.Direction = flow.Direction(dir).String()
	f.ServicePort = flow.ServicePort(f.Protocol, f.SrcPort, f.DstPort)
	if f.LocalHost != "" {
		f.ServicePort = flow.ServicePort(f.Protocol, f.LocalPort, f.PeerPort)
	}
	if f.ASN != 0 {
		f.ASNOrg = s.ASNName(f.ASN)
	}
	k.exporter, k.received, k.start, k.end, k.src, k.dst = f.Exporter, k.received.UTC(), f.Start, f.End, f.Src, f.Dst
	k.srcPort, k.dstPort, k.protocol, k.bytes = f.SrcPort, f.DstPort, f.Protocol, f.Bytes
	f.ID = k.String()
	return f, nil
}

// where returns the query's filters as SQL.
func (q FlowQuery) where(a *sqlArgs) string {
	w := fmt.Sprintf(`end_ts >= %s AND start_ts < %s`, a.add(q.From.UTC()), a.add(q.To.UTC()))
	if q.Exporter.IsValid() {
		w += ` AND exporter = ` + a.add(q.Exporter.String())
	}
	if q.LocalHosts != nil {
		w += fmt.Sprintf(` AND (%s OR (direction = 3 AND %s))`, inList(a, "local_host", q.LocalHosts), inList(a, "peer", q.LocalHosts))
	}
	if q.Remote.IsValid() {
		w += ` AND direction IN (1, 2) AND peer = ` + a.add(q.Remote.Unmap().String())
	}
	if q.HasASN {
		w += ` AND direction IN (1, 2) AND peer_asn = ` + a.add(q.ASN)
	}
	if q.Country != "" {
		w += ` AND peer_country = ` + a.add(q.Country)
	}
	if q.HasProtocol {
		w += ` AND protocol = ` + a.add(q.Protocol)
	}
	if q.Domain != "" {
		w += fmt.Sprintf(` AND (domain = %[1]s OR ends_with(domain, '.' || %[1]s))`, a.add(q.Domain))
	}
	if q.NoDomain {
		w += ` AND direction IN (1, 2) AND coalesce(domain, '') = ''`
	}
	if q.Port != 0 {
		p := a.add(q.Port)
		w += fmt.Sprintf(` AND (src_port = %[1]s OR dst_port = %[1]s OR post_nat_src_port = %[1]s OR post_nat_dst_port = %[1]s)`, p)
	}
	if len(q.Directions) > 0 {
		ds := make([]string, len(q.Directions))
		for i, d := range q.Directions {
			ds[i] = strconv.Itoa(int(d))
		}
		w += ` AND direction IN (` + strings.Join(ds, ", ") + `)`
	}
	return w
}

// Flows lists the raw Flows that match q, one page at a time.
func (s *Store) Flows(ctx context.Context, q FlowQuery) (FlowPage, error) {
	if !q.To.After(q.From) || q.To.Sub(q.From) > MaxRange {
		return FlowPage{}, ErrInvalidRange
	}
	if q.Sort == "" {
		q.Sort = SortEnd
	}
	order, ok := sortColumns[q.Sort]
	if !ok {
		return FlowPage{}, ErrUnknownSort
	}
	if q.Limit <= 0 || q.Limit > MaxFlowList {
		q.Limit = 100
	}
	q.Offset = min(max(q.Offset, 0), MaxFlowOffset)
	page := FlowPage{Items: []FlowItem{}}
	if days := s.RawDays(time.Time{}); len(days) > 0 {
		page.RawFrom = &days[0]
	}
	// Flows are filed by end time.
	tables := s.tablesFor(q.From, q.To.Add(rollup.MaxSpread))
	if len(tables) == 0 {
		return page, nil
	}
	a := &sqlArgs{}
	where := q.where(a)
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf(`SELECT * FROM %s WHERE %s`, t, where)
	}
	union := strings.Join(parts, "\nUNION ALL\n")

	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM (`+union+`)`, a.args...).Scan(&page.Total); err != nil {
		return FlowPage{}, fmt.Errorf("flows count: %w", err)
	}
	dir := "DESC"
	if q.Asc {
		dir = "ASC"
	}
	// Ties are broken by every key column, so pages don't overlap.
	query := fmt.Sprintf(`SELECT %s FROM (%s) ORDER BY %s %s, end_ts %[4]s, start_ts %[4]s, exporter, src, dst, src_port, dst_port, protocol, bytes, received
LIMIT %d OFFSET %d`, listColumns, union, order, dir, q.Limit, q.Offset)
	rows, err := s.db.QueryContext(ctx, query, a.args...)
	if err != nil {
		return FlowPage{}, fmt.Errorf("flows query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		f, err := s.scanFlowItem(rows)
		if err != nil {
			return FlowPage{}, err
		}
		page.Items = append(page.Items, f)
	}
	return page, rows.Err()
}

// FlowDetail is one raw Flow with everything stored about it.
type FlowDetail struct {
	FlowItem
	ObservationDomain uint32           `json:"observationDomain"`
	Received          time.Time        `json:"received"`
	ExportTime        time.Time        `json:"exportTime"`
	TimeEstimated     bool             `json:"timeEstimated"`
	PostNATSrc        string           `json:"postNatSrc,omitempty"`
	PostNATDst        string           `json:"postNatDst,omitempty"`
	PostNATSrcPort    uint16           `json:"postNatSrcPort,omitempty"`
	PostNATDstPort    uint16           `json:"postNatDstPort,omitempty"`
	IngressInterface  uint32           `json:"ingressInterface"`
	EgressInterface   uint32           `json:"egressInterface"`
	Fields            []flow.FieldView `json:"fields"`
	// FieldsError says why the stored fields couldn't be decoded.
	FieldsError string `json:"fieldsError,omitempty"`
	// Biflow is "forward" or "reverse" for one direction of an RFC 5103 biflow record,
	// which Flowmon stores as two Flows; empty otherwise.
	Biflow string `json:"biflow,omitempty"`
}

// Flow returns one raw Flow by the ID a list gave it, or ErrNotFound when it is gone
// (retention dropped its day, or reprocessing changed it).
func (s *Store) Flow(ctx context.Context, id string) (FlowDetail, error) {
	k, err := parseFlowKey(id)
	if err != nil {
		return FlowDetail{}, err
	}
	table := dayTable(k.end)
	s.mu.Lock()
	exists := s.days[table]
	s.mu.Unlock()
	if !exists {
		return FlowDetail{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+listColumns+`, export_time, time_estimated, post_nat_src, post_nat_dst,
	post_nat_src_port, post_nat_dst_port, ingress_interface, egress_interface, fields
FROM `+table+` WHERE end_ts = $1 AND start_ts = $2 AND received = $3 AND exporter = $4 AND observation_domain = $5
	AND src = $6 AND dst = $7 AND src_port = $8 AND dst_port = $9 AND protocol = $10 AND bytes = $11 LIMIT 1`,
		k.end, k.start, k.received, k.exporter, k.domain, k.src, k.dst, k.srcPort, k.dstPort, k.protocol, k.bytes)
	var d FlowDetail
	var postSrc, postDst sql.NullString
	var fields []byte
	// scanFlowItem reads the list columns; the detail columns follow them.
	scan := scanFunc(func(dest ...any) error {
		return row.Scan(append(dest, &d.ExportTime, &d.TimeEstimated, &postSrc, &postDst, &d.PostNATSrcPort, &d.PostNATDstPort,
			&d.IngressInterface, &d.EgressInterface, &fields)...)
	})
	item, err := s.scanFlowItem(scan)
	if errors.Is(err, sql.ErrNoRows) {
		return FlowDetail{}, ErrNotFound
	}
	if err != nil {
		return FlowDetail{}, err
	}
	d.FlowItem, d.ObservationDomain, d.Received = item, k.domain, k.received
	d.ExportTime = d.ExportTime.UTC()
	d.PostNATSrc, d.PostNATDst = postSrc.String, postDst.String
	if d.Fields, err = flow.DescribeFields(fields); err != nil {
		d.Fields, d.FieldsError = []flow.FieldView{}, err.Error()
	}
	d.Biflow = biflowHalf(d)
	return d, nil
}

// biflowHalf says which direction of a biflow record a stored Flow is: the record's
// source address is the forward direction's.
func biflowHalf(d FlowDetail) string {
	biflow, forward := false, false
	for _, f := range d.Fields {
		if f.Enterprise == ipfix.EnterpriseReverse {
			biflow = true
		}
		if f.Enterprise == 0 && (f.ID == 8 || f.ID == 27) && f.Value == d.Src {
			forward = true
		}
	}
	switch {
	case !biflow:
		return ""
	case forward:
		return "forward"
	}
	return "reverse"
}

// scanFunc adapts a function to the Scan method scanFlowItem needs.
type scanFunc func(dest ...any) error

func (f scanFunc) Scan(dest ...any) error { return f(dest...) }
