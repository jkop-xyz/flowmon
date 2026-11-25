package geoip

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// buildDB writes an MMDB with the given database type and records.
func buildDB(t *testing.T, dbType string, records map[string]mmdbtype.Map) []byte {
	t.Helper()
	w, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: dbType, RecordSize: 24, BuildEpoch: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	for cidr, rec := range records {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Insert(n, rec); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if _, err := w.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func countryDB(t *testing.T, dbType string) []byte {
	return buildDB(t, dbType, map[string]mmdbtype.Map{
		"8.8.8.0/24": {"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")}},
		// Anycast: no physical country, only the registered one.
		"1.1.1.0/24":       {"registered_country": mmdbtype.Map{"iso_code": mmdbtype.String("AU")}},
		"2001:4860::/32":   {"country": mmdbtype.Map{"iso_code": mmdbtype.String("us")}},
		"185.199.108.0/22": {"country": mmdbtype.Map{"iso_code": mmdbtype.String("XK")}},
	})
}

func asnDB(t *testing.T, dbType string) []byte {
	return buildDB(t, dbType, map[string]mmdbtype.Map{
		"8.8.8.0/24": {"autonomous_system_number": mmdbtype.Uint32(15169), "autonomous_system_organization": mmdbtype.String("Google LLC")},
	})
}

func writeFile(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLookup(t *testing.T) {
	dir := t.TempDir()
	var r Resolver
	for _, f := range []struct {
		name string
		b    []byte
	}{{"country.mmdb", countryDB(t, "GeoLite2-Country")}, {"asn.mmdb", asnDB(t, "DBIP-ASN-Lite")}} {
		db, err := OpenFile(writeFile(t, dir, f.name, f.b), SourceFile)
		if err != nil {
			t.Fatal(err)
		}
		r.Set(db.Kind, db)
	}

	cases := []struct {
		addr string
		want Info
	}{
		{"8.8.8.8", Info{Country: "US", ASN: 15169, ASNOrg: "Google LLC"}},
		{"::ffff:8.8.8.8", Info{Country: "US", ASN: 15169, ASNOrg: "Google LLC"}},
		{"1.1.1.1", Info{Country: "AU"}},
		{"2001:4860:4860::8888", Info{Country: "US"}},
		{"185.199.110.1", Info{Country: "XK"}},
		{"9.9.9.9", Info{}},
		{"192.168.1.10", Info{}},
		{"fe80::1", Info{}},
		{"127.0.0.1", Info{}},
	}
	for _, c := range cases {
		if got := r.Lookup(netip.MustParseAddr(c.addr)); got != c.want {
			t.Errorf("Lookup(%s) = %+v, want %+v", c.addr, got, c.want)
		}
	}
	var nilResolver *Resolver
	if got := nilResolver.Lookup(netip.MustParseAddr("8.8.8.8")); got != (Info{}) {
		t.Errorf("nil resolver = %+v", got)
	}
}

func TestOpenFileRejectsUnknownDatabases(t *testing.T) {
	p := writeFile(t, t.TempDir(), "x.mmdb", buildDB(t, "GeoIP2-Anonymous-IP", nil))
	if _, err := OpenFile(p, SourceFile); err == nil {
		t.Fatal("expected an error for an anonymous-IP database")
	}
}

func TestMountedFilesWinAndNeverDownload(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dir, dl := t.TempDir(), t.TempDir()
	writeFile(t, dir, "GeoLite2-City.mmdb", countryDB(t, "GeoLite2-City"))
	writeFile(t, dir, "GeoLite2-ASN.mmdb", asnDB(t, "GeoLite2-ASN"))
	writeFile(t, dir, "notes.mmdb", []byte("not a database"))
	// A cached download exists too, but the mounted file wins.
	writeFile(t, dl, "dbip-country.mmdb", countryDB(t, "DBIP-Country-Lite"))

	var r Resolver
	m := NewManager(Options{Dir: dir, DownloadDir: dl, Download: true, DBIPURL: srv.URL, MaxMindURL: srv.URL}, &r)
	m.Refresh(context.Background())

	if requests.Load() != 0 {
		t.Errorf("%d downloads attempted, want none", requests.Load())
	}
	st := m.Status()
	for _, d := range st.Databases {
		if d.Source != SourceFile {
			t.Errorf("%s served by %q, want a mounted file", d.Kind, d.Source)
		}
	}
	if st.Databases[0].Type != "GeoLite2-City" {
		t.Errorf("country database = %q", st.Databases[0].Type)
	}
	if len(st.Warnings) != 1 || !strings.Contains(st.Warnings[0], "notes.mmdb") {
		t.Errorf("warnings = %v, want one about notes.mmdb", st.Warnings)
	}
	if got := r.Lookup(netip.MustParseAddr("8.8.8.8")); got.Country != "US" || got.ASN != 15169 {
		t.Errorf("lookup = %+v", got)
	}

	// Removing the mounted country file falls back to the cached download.
	os.Remove(filepath.Join(dir, "GeoLite2-City.mmdb"))
	m.Load()
	if d := r.Current(KindCountry); d == nil || d.Source != SourceDBIP {
		t.Errorf("after removing the mounted file, country = %+v", d)
	}
}

func gz(t *testing.T, b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func tarGz(t *testing.T, name string, b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	tw.WriteHeader(&tar.Header{Name: "GeoLite2-Country_20261103/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: "GeoLite2-Country_20261103/LICENSE.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3})
	tw.Write([]byte("lic"))
	tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(b))})
	tw.Write(b)
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestDownloadsDBIPFallingBackToLastMonth(t *testing.T) {
	now := time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC)
	country, asn := gz(t, countryDB(t, "DBIP-Country-Lite")), gz(t, asnDB(t, "DBIP-ASN-Lite"))
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/dbip-country-lite-2026-11.mmdb.gz":
			w.Write(country)
		case "/dbip-asn-lite-2026-11.mmdb.gz":
			w.Write(asn)
		default:
			http.NotFound(w, r) // December isn't out yet
		}
	}))
	defer srv.Close()

	var r Resolver
	clock := now
	dl := filepath.Join(t.TempDir(), "dl")
	m := NewManager(Options{DownloadDir: dl, Download: true, DBIPURL: srv.URL, Now: func() time.Time { return clock }}, &r)
	m.Refresh(context.Background())
	for _, k := range Kinds { // the files' age counts on the fake clock
		os.Chtimes(filepath.Join(dl, downloadName(SourceDBIP, k)), now, now)
	}
	want := []string{"/dbip-country-lite-2026-12.mmdb.gz", "/dbip-country-lite-2026-11.mmdb.gz", "/dbip-asn-lite-2026-12.mmdb.gz", "/dbip-asn-lite-2026-11.mmdb.gz"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Errorf("requests = %v, want %v", paths, want)
	}
	if got := r.Lookup(netip.MustParseAddr("8.8.8.8")); got != (Info{Country: "US", ASN: 15169, ASNOrg: "Google LLC"}) {
		t.Errorf("lookup = %+v", got)
	}
	for _, d := range m.Status().Databases {
		if d.Source != SourceDBIP || d.Error != "" || d.LastAttempt == nil {
			t.Errorf("status %+v", d)
		}
	}

	// Within a week nothing is downloaded again.
	paths = nil
	clock = now.Add(3 * 24 * time.Hour)
	m.Refresh(context.Background())
	if len(paths) != 0 {
		t.Errorf("refreshed too early: %v", paths)
	}
}

// downloadServer serves MaxMind downloads for account 12345 with key "good" under
// /maxmind, and DB-IP Lite for November 2026 under /dbip.
func downloadServer(t *testing.T) *httptest.Server {
	country := tarGz(t, "GeoLite2-Country_20261103/GeoLite2-Country.mmdb", countryDB(t, "GeoLite2-Country"))
	asn := tarGz(t, "GeoLite2-ASN_20261103/GeoLite2-ASN.mmdb", asnDB(t, "GeoLite2-ASN"))
	dbipCountry, dbipASN := gz(t, countryDB(t, "DBIP-Country-Lite")), gz(t, asnDB(t, "DBIP-ASN-Lite"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/maxmind/") {
			if u, p, ok := r.BasicAuth(); !ok || u != "12345" || p != "good" {
				http.Error(w, "invalid", http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("suffix") != "tar.gz" {
				http.Error(w, "bad suffix", http.StatusBadRequest)
				return
			}
		}
		switch r.URL.Path {
		case "/maxmind/GeoLite2-Country/download":
			w.Write(country)
		case "/maxmind/GeoLite2-ASN/download":
			w.Write(asn)
		case "/dbip/dbip-country-lite-2026-11.mmdb.gz":
			w.Write(dbipCountry)
		case "/dbip/dbip-asn-lite-2026-11.mmdb.gz":
			w.Write(dbipASN)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMaxMindPreferredAndBadKeyFallsBackToDBIP(t *testing.T) {
	srv := downloadServer(t)
	now := func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) }

	t.Run("good key", func(t *testing.T) {
		var r Resolver
		m := NewManager(Options{DownloadDir: t.TempDir(), Download: true, MaxMindAccountID: "12345", MaxMindLicenseKey: "good",
			MaxMindURL: srv.URL + "/maxmind", DBIPURL: srv.URL + "/dbip", Now: now}, &r)
		m.Refresh(context.Background())
		for _, d := range m.Status().Databases {
			if d.Source != SourceMaxMind || d.Error != "" {
				t.Errorf("status %+v", d)
			}
		}
	})

	t.Run("bad key", func(t *testing.T) {
		var r Resolver
		m := NewManager(Options{DownloadDir: t.TempDir(), Download: true, MaxMindAccountID: "12345", MaxMindLicenseKey: "wrong",
			MaxMindURL: srv.URL + "/maxmind", DBIPURL: srv.URL + "/dbip", Now: now}, &r)
		m.Refresh(context.Background())
		for _, d := range m.Status().Databases {
			if d.Source != SourceDBIP || !strings.Contains(d.Error, "MaxMind: refused") {
				t.Errorf("status %+v", d)
			}
		}
		if got := r.Lookup(netip.MustParseAddr("8.8.8.8")); got.Country != "US" {
			t.Errorf("lookup = %+v", got)
		}
	})

	t.Run("downloads off", func(t *testing.T) {
		var r Resolver
		m := NewManager(Options{DownloadDir: t.TempDir(), MaxMindAccountID: "12345", MaxMindLicenseKey: "good",
			MaxMindURL: srv.URL + "/maxmind", DBIPURL: srv.URL + "/dbip", Now: now}, &r)
		m.Refresh(context.Background())
		for _, d := range m.Status().Databases {
			if d.Source != "" {
				t.Errorf("status %+v, want nothing loaded", d)
			}
		}
	})
}

func TestBrokenDownloadKeepsTheCurrentFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(gz(t, []byte("garbage")))
	}))
	defer srv.Close()
	dl := t.TempDir()
	p := writeFile(t, dl, "dbip-country.mmdb", countryDB(t, "DBIP-Country-Lite"))
	old := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(p, old, old)

	var r Resolver
	m := NewManager(Options{DownloadDir: dl, Download: true, DBIPURL: srv.URL}, &r)
	m.Refresh(context.Background())
	if d := r.Current(KindCountry); d == nil || d.Source != SourceDBIP {
		t.Fatalf("country = %+v, want the old file", d)
	}
	if st := m.Status(); st.Databases[0].Error == "" {
		t.Error("expected the failed download in the status")
	}
	entries, _ := os.ReadDir(dl)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".download-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestEnrichOnlyRemotePeers(t *testing.T) {
	var r Resolver
	db, err := OpenFile(writeFile(t, t.TempDir(), "c.mmdb", countryDB(t, "GeoLite2-Country")), SourceFile)
	if err != nil {
		t.Fatal(err)
	}
	r.Set(KindCountry, db)
	for _, c := range []struct {
		dir  flow.Direction
		want string
	}{{flow.DirectionOutbound, "US"}, {flow.DirectionInbound, "US"}, {flow.DirectionInternal, ""}, {flow.DirectionTransit, ""}} {
		f := flow.Flow{Direction: c.dir, Peer: netip.MustParseAddr("8.8.8.8"), PeerCountry: "stale"}
		r.Enrich(&f)
		if f.PeerCountry != c.want {
			t.Errorf("%s: country %q, want %q", c.dir, f.PeerCountry, c.want)
		}
	}
}
