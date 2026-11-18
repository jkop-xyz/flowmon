package flowstore

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

func geoFlow(dir flow.Direction, peer, country string, asn uint32, org string, bytes uint64) flow.Flow {
	f := mkFlow(dir, t0, t0, bytes)
	f.Peer = netip.MustParseAddr(peer)
	f.Dst = f.Peer
	f.PeerCountry, f.PeerASN, f.PeerASNOrg = country, asn, org
	return f
}

func TestTopCountriesAndASNOwners(t *testing.T) {
	s := open(t)
	if err := s.Write(ctx, []flow.Flow{
		geoFlow(flow.DirectionOutbound, "8.8.8.8", "US", 15169, "Google LLC", 1000),
		geoFlow(flow.DirectionInbound, "8.8.4.4", "US", 15169, "Google LLC", 3000),
		geoFlow(flow.DirectionInbound, "1.1.1.1", "AU", 13335, "Cloudflare, Inc.", 2500),
		geoFlow(flow.DirectionOutbound, "203.0.113.9", "", 0, "", 100), // no GeoIP data
		internalFlow(t0, t0, 99999), // never counts
	}); err != nil {
		t.Fatal(err)
	}
	q := TopQuery{Dimension: TopCountries, From: t0.Add(-time.Hour), To: t0.Add(time.Hour)}
	countries, err := s.Top(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(countries.Items) != 3 || countries.Items[0].Country != "US" || countries.Items[0].ReceivedBytes != 3000 ||
		countries.Items[0].SentBytes != 1000 || countries.Items[2].Country != "" || countries.TotalBytes != 6600 {
		t.Errorf("countries %+v", countries)
	}

	q.Dimension = TopASNs
	asns, err := s.Top(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(asns.Items) != 3 || asns.Items[0].ASN != 15169 || asns.Items[0].ASNOrg != "Google LLC" ||
		asns.Items[1].ASNOrg != "Cloudflare, Inc." || asns.Items[2].ASN != 0 || asns.Items[2].ASNOrg != "" {
		t.Errorf("asns %+v", asns.Items)
	}

	// A renamed ASN Owner takes the latest name, and names survive a restart.
	if err := s.Write(ctx, []flow.Flow{geoFlow(flow.DirectionOutbound, "1.1.1.1", "AU", 13335, "Cloudflare", 1)}); err != nil {
		t.Fatal(err)
	}
	path := s.path
	s.Close()
	s2, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.ASNName(13335); got != "Cloudflare" {
		t.Errorf("after restart: %q", got)
	}
}

func TestGeoColumnsAddedToExistingDayTables(t *testing.T) {
	// Build a store as schema 8 left it: a day table without the GeoIP columns.
	s := open(t)
	if err := s.Write(ctx, []flow.Flow{mkFlow(flow.DirectionOutbound, t0, t0, 100)}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE flows_20261118 DROP COLUMN peer_country`,
		`ALTER TABLE flows_20261118 DROP COLUMN peer_asn`,
		`DROP TABLE asn_names`,
		`DROP TABLE top_remote`,
		`DROP TABLE top_remote_built`,
		`DELETE FROM flowmon_migrations WHERE version >= 9`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	path := s.path
	s.Close()

	s2, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Write(ctx, []flow.Flow{geoFlow(flow.DirectionOutbound, "8.8.8.8", "US", 15169, "Google LLC", 50)}); err != nil {
		t.Fatal(err)
	}
	var unknown, us int
	if err := s2.db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE peer_country = '' AND peer_asn = 0),
		count(*) FILTER (WHERE peer_country = 'US' AND peer_asn = 15169) FROM flows_20261118`).Scan(&unknown, &us); err != nil {
		t.Fatal(err)
	}
	if unknown != 1 || us != 1 {
		t.Errorf("old rows unknown %d, new rows %d", unknown, us)
	}
	// Reprocessing keeps what each Flow was enriched with.
	n, _, err := s2.Reprocess(ctx, exporter, t0, func(*flow.Flow) {}, nil)
	if err != nil || n != 2 {
		t.Fatalf("reprocess %d %v", n, err)
	}
	res, err := s2.Top(ctx, TopQuery{Dimension: TopCountries, From: t0.Add(-time.Hour), To: t0.Add(time.Hour)})
	if err != nil || len(res.Items) != 2 || res.Items[0].Country != "" || res.Items[1].Country != "US" {
		t.Errorf("after reprocessing %+v %v", res.Items, err)
	}
}
