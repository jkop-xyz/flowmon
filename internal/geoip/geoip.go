// Package geoip is GeoIP enrichment (see IP Enrichment in GLOSSARY.md): the country and
// ASN Owner of a Remote IP, read from MMDB files. Records are decoded into our own
// structs, which only name the fields MaxMind GeoLite2/GeoIP2 and DB-IP Lite share, so
// either vendor's databases work.
package geoip

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// Info is what enrichment knows about an address. Empty fields mean unknown.
type Info struct {
	// Country is the ISO 3166-1 alpha-2 code, upper case (Kosovo is "XK").
	Country string
	ASN     uint32
	// ASNOrg is the ASN Owner's name, e.g. "Google LLC".
	ASNOrg string
}

// Kind is what a database answers.
type Kind string

// Database kinds.
const (
	KindCountry Kind = "country"
	KindASN     Kind = "asn"
)

// Kinds lists every kind, in display order.
var Kinds = []Kind{KindCountry, KindASN}

// countryRecord holds the fields of a Country or City record that both vendors use.
// registered_country is the fallback for networks with no physical location (anycast,
// some satellite providers).
type countryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	RegisteredCountry struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"registered_country"`
}

type asnRecord struct {
	Number uint32 `maxminddb:"autonomous_system_number"`
	Org    string `maxminddb:"autonomous_system_organization"`
}

// Database is one loaded MMDB file.
type Database struct {
	Kind   Kind
	Source Source
	Path   string
	// Type is the database_type from the file's metadata, e.g. "GeoLite2-Country".
	Type    string
	BuiltAt time.Time
	reader  *maxminddb.Reader
}

// ErrUnknownDatabase is returned for an MMDB file that has neither country nor ASN data.
var ErrUnknownDatabase = errors.New("geoip: not a country, city or ASN database")

// kindOf tells from the database type which records a file holds. ISP databases carry
// the ASN fields too; City and Enterprise databases carry country.
func kindOf(databaseType string) (Kind, bool) {
	t := strings.ToLower(databaseType)
	switch {
	case strings.Contains(t, "asn"), strings.Contains(t, "isp"):
		return KindASN, true
	case strings.Contains(t, "country"), strings.Contains(t, "city"), strings.Contains(t, "enterprise"):
		return KindCountry, true
	}
	return "", false
}

// OpenFile reads a whole MMDB file into memory. Reading rather than mapping it means a
// replaced file can't pull the data from under a running lookup.
func OpenFile(path string, source Source) (*Database, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r, err := maxminddb.OpenBytes(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	kind, ok := kindOf(r.Metadata.DatabaseType)
	if !ok {
		return nil, fmt.Errorf("%s (%q): %w", path, r.Metadata.DatabaseType, ErrUnknownDatabase)
	}
	return &Database{Kind: kind, Source: source, Path: path, Type: r.Metadata.DatabaseType,
		BuiltAt: r.Metadata.BuildTime().UTC(), reader: r}, nil
}

// enrichable reports whether an address can have GeoIP data: public unicast only.
func enrichable(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && a.IsGlobalUnicast() && !a.IsPrivate()
}

// Resolver answers lookups from the current databases. It is safe for concurrent use,
// and databases can be swapped while lookups run.
type Resolver struct {
	country atomic.Pointer[Database]
	asn     atomic.Pointer[Database]
}

// Set makes db the database for its kind. A nil db for kind k clears it.
func (r *Resolver) Set(k Kind, db *Database) {
	if k == KindASN {
		r.asn.Store(db)
	} else {
		r.country.Store(db)
	}
}

// Current returns the database in use for k, or nil.
func (r *Resolver) Current(k Kind) *Database {
	if k == KindASN {
		return r.asn.Load()
	}
	return r.country.Load()
}

// Lookup returns what the databases say about a. Private and other non-public addresses
// get an empty Info.
func (r *Resolver) Lookup(a netip.Addr) Info {
	var info Info
	if r == nil || !enrichable(a) {
		return info
	}
	a = a.Unmap()
	if db := r.country.Load(); db != nil {
		var rec countryRecord
		if err := db.reader.Lookup(a).Decode(&rec); err == nil {
			info.Country = strings.ToUpper(rec.Country.ISOCode)
			if info.Country == "" {
				info.Country = strings.ToUpper(rec.RegisteredCountry.ISOCode)
			}
		}
	}
	if db := r.asn.Load(); db != nil {
		var rec asnRecord
		if err := db.reader.Lookup(a).Decode(&rec); err == nil {
			info.ASN, info.ASNOrg = rec.Number, rec.Org
		}
	}
	return info
}

// Enrich sets the Peer's country and ASN Owner on a classified Flow. Only Flows with a
// Remote IP are enriched; Internal and transit Flows are left empty.
func (r *Resolver) Enrich(f *flow.Flow) {
	f.PeerCountry, f.PeerASN, f.PeerASNOrg = "", 0, ""
	if f.Direction != flow.DirectionInbound && f.Direction != flow.DirectionOutbound {
		return
	}
	info := r.Lookup(f.Peer)
	f.PeerCountry, f.PeerASN, f.PeerASNOrg = info.Country, info.ASN, info.ASNOrg
}
