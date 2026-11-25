package snmp

import (
	"net/netip"
	"os"
	"testing"
)

// TestRealAgent runs against a net-snmp agent when FLOWMON_TEST_SNMP_TARGET is set
// (host:port), with community "flowmontest" and v3 user flowmon (SHA256/AES).
func TestRealAgent(t *testing.T) {
	target := os.Getenv("FLOWMON_TEST_SNMP_TARGET")
	if target == "" {
		t.Skip("FLOWMON_TEST_SNMP_TARGET not set")
	}
	for name, c := range map[string]Config{
		"v2c": {Version: "v2c", Target: target, Community: "flowmontest"},
		"v3":  {Version: "v3", Target: target, User: "flowmon", AuthProtocol: "SHA256", AuthPassword: "authpass123", PrivProtocol: "AES", PrivPassword: "privpass123"},
	} {
		t.Run(name, func(t *testing.T) {
			a, err := Dial(c, netip.MustParseAddr("127.0.0.1"))
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			tbl, err := a.Table(ctx)
			if err != nil || len(tbl.Rows) == 0 || tbl.Uptime == 0 {
				t.Fatalf("table %+v %v", tbl, err)
			}
			var lo IfRow
			for _, r := range tbl.Rows {
				if r.Name == "lo" {
					lo = r
				}
			}
			t.Logf("%d interfaces, up %s, lo %+v", len(tbl.Rows), tbl.Uptime, lo)
			if lo.InOctets == 0 && lo.InPackets == 0 {
				t.Error("expected 64-bit counters for lo")
			}
		})
	}
	bad := Config{Version: "v2c", Target: target, Community: "wrong"}
	if a, err := Dial(bad, netip.MustParseAddr("127.0.0.1")); err == nil {
		if _, err := a.Table(ctx); err == nil {
			t.Error("a wrong community must fail")
		}
		a.Close()
	}
}
