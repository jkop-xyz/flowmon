package flowstore

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

func explorerFlows() []flow.Flow {
	at := t0.Add(-30 * time.Minute)
	f := func(dir flow.Direction, local, peer string, port uint16, at time.Time, bytes uint64, country string, asn uint32) flow.Flow {
		fl := hostFlow(dir, local, peer, port, at, bytes)
		fl.Start = at.Add(-time.Duration(bytes) * time.Millisecond)
		fl.PeerCountry, fl.PeerASN = country, asn
		return fl
	}
	transit := mkFlow(flow.DirectionTransit, at, at, 7)
	transit.Local, transit.Peer, transit.LocalPort, transit.PeerPort = netip.Addr{}, netip.Addr{}, 0, 0
	transit.Src, transit.Dst = netip.MustParseAddr("100.64.0.1"), netip.MustParseAddr("100.64.0.2")
	transit.SrcPort, transit.DstPort = 5000, 6000
	return []flow.Flow{
		f(flow.DirectionOutbound, "192.168.1.5", "1.1.1.1", 443, at, 100, "AU", 13335),
		f(flow.DirectionInbound, "192.168.1.5", "8.8.8.8", 53, at.Add(time.Minute), 300, "US", 15169),
		f(flow.DirectionOutbound, "192.168.1.6", "8.8.8.8", 53, at.Add(2*time.Minute), 200, "US", 15169),
		f(flow.DirectionInternal, "192.168.1.6", "192.168.1.5", 445, at.Add(3*time.Minute), 50, "", 0),
		transit,
	}
}

func TestFlowsFilterSortAndPage(t *testing.T) {
	s := open(t)
	if err := s.Write(ctx, explorerFlows()); err != nil {
		t.Fatal(err)
	}
	base := FlowQuery{From: t0.Add(-time.Hour), To: t0}
	count := func(q FlowQuery) (int, []FlowItem) {
		t.Helper()
		p, err := s.Flows(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return p.Total, p.Items
	}
	with := func(f func(q *FlowQuery)) FlowQuery {
		q := base
		f(&q)
		return q
	}

	n, items := count(base)
	if n != 5 || items[0].Direction != "internal" || items[4].Direction != "outbound" {
		t.Fatalf("all, newest first: %d %+v", n, items)
	}
	for name, c := range map[string]struct {
		q    FlowQuery
		want int
	}{
		// .5 is the Local Host of two Flows and the Peer of the Internal one.
		"local host":     {with(func(q *FlowQuery) { q.LocalHosts = []string{"192.168.1.5"} }), 3},
		"no local hosts": {with(func(q *FlowQuery) { q.LocalHosts = []string{} }), 0},
		"remote":         {with(func(q *FlowQuery) { q.Remote = netip.MustParseAddr("8.8.8.8") }), 2},
		"asn":            {with(func(q *FlowQuery) { q.ASN, q.HasASN = 15169, true }), 2},
		"unknown asn":    {with(func(q *FlowQuery) { q.ASN, q.HasASN = 0, true }), 0},
		"country":        {with(func(q *FlowQuery) { q.Country = "AU" }), 1},
		"protocol":       {with(func(q *FlowQuery) { q.Protocol, q.HasProtocol = 17, true }), 0},
		"port":           {with(func(q *FlowQuery) { q.Port = 53 }), 2},
		"transit port":   {with(func(q *FlowQuery) { q.Port = 6000 }), 1},
		"directions":     {with(func(q *FlowQuery) { q.Directions = []flow.Direction{flow.DirectionInternal, flow.DirectionTransit} }), 2},
	} {
		if n, _ := count(c.q); n != c.want {
			t.Errorf("%s: %d, want %d", name, n, c.want)
		}
	}

	_, items = count(with(func(q *FlowQuery) { q.Sort = SortBytes }))
	if items[0].Bytes != 300 || items[4].Bytes != 7 {
		t.Errorf("by bytes: %+v", items)
	}
	_, items = count(with(func(q *FlowQuery) { q.Sort, q.Asc = SortDuration, true }))
	if items[0].Bytes != 7 || items[4].Bytes != 300 {
		t.Errorf("by duration: %+v", items)
	}
	// Pages don't overlap.
	_, p1 := count(with(func(q *FlowQuery) { q.Limit = 2 }))
	_, p2 := count(with(func(q *FlowQuery) { q.Limit, q.Offset = 2, 2 }))
	if len(p1) != 2 || len(p2) != 2 || p1[1].ID == p2[0].ID {
		t.Errorf("pages %+v %+v", p1, p2)
	}
	if _, err := s.Flows(ctx, with(func(q *FlowQuery) { q.Sort = "colour" })); err != ErrUnknownSort {
		t.Errorf("unknown sort: %v", err)
	}
	if got := items[4]; got.Country != "US" || got.ASN != 15169 || got.ServicePort != 53 || got.LocalHost != "192.168.1.5" {
		t.Errorf("item %+v", got)
	}
}

func TestFlowDetail(t *testing.T) {
	s := open(t)
	flows := explorerFlows()
	flows[0].Fields = flow.EncodeFields([]ipfix.Field{{Spec: ipfix.FieldSpec{ID: 8, Length: 4}, Raw: []byte{192, 168, 1, 5}}})
	if err := s.Write(ctx, flows); err != nil {
		t.Fatal(err)
	}
	p, err := s.Flows(ctx, FlowQuery{From: t0.Add(-time.Hour), To: t0, Country: "AU"})
	if err != nil || len(p.Items) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	d, err := s.Flow(ctx, p.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Bytes != 100 || d.Peer != "1.1.1.1" || !d.Received.Equal(flows[0].Received) || len(d.Fields) != 1 ||
		d.Fields[0].Name != "sourceIPv4Address" || d.Fields[0].Value != "192.168.1.5" {
		t.Errorf("detail %+v", d)
	}
	for _, id := range []string{"", "not-base64!", "YWJj", p.Items[0].ID[:len(p.Items[0].ID)-4]} {
		if _, err := s.Flow(ctx, id); err != ErrNotFound {
			t.Errorf("%q: %v", id, err)
		}
	}
}

func TestFlowDetailSaysWhichBiflowHalf(t *testing.T) {
	s := open(t)
	flows := explorerFlows()[:2]
	// One biflow record, source 192.168.1.5: its forward half went out to 1.1.1.1; say the
	// second Flow was its reverse half, from the other end back to the source.
	fields := flow.EncodeFields([]ipfix.Field{
		{Spec: ipfix.FieldSpec{ID: 8, Length: 4}, Raw: []byte{192, 168, 1, 5}},
		{Spec: ipfix.FieldSpec{Enterprise: ipfix.EnterpriseReverse, ID: 1, Length: 4}, Raw: []byte{0, 0, 1, 44}},
	})
	flows[0].Fields, flows[1].Fields = fields, fields
	if err := s.Write(ctx, flows); err != nil {
		t.Fatal(err)
	}
	p, err := s.Flows(ctx, FlowQuery{From: t0.Add(-time.Hour), To: t0, Asc: true, Sort: "start"})
	if err != nil || len(p.Items) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	got := map[string]string{}
	for _, it := range p.Items {
		d, err := s.Flow(ctx, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		got[d.Src] = d.Biflow
	}
	if got["192.168.1.5"] != "forward" || got["8.8.8.8"] != "reverse" {
		t.Errorf("%v", got)
	}
	if d, _ := s.Flow(ctx, p.Items[0].ID); d.Fields[1].Name != "reverseOctetDeltaCount" || d.Fields[1].Value != "300" {
		t.Errorf("reverse field %+v", d.Fields[1])
	}
}
