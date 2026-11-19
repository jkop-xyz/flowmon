package httpserver

import (
	"testing"

	"github.com/jkop-xyz/flowmon/internal/exporters"
	"github.com/jkop-xyz/flowmon/internal/interfaces"
)

func TestDescribeClassificationChange(t *testing.T) {
	const (
		addr  = exporters.ByAddress
		iface = exporters.ByInterface
		wan   = interfaces.RoleWAN
		lan   = interfaces.RoleLAN
	)
	type roles = map[string]interfaces.Role
	for _, tc := range []struct {
		name               string
		oldMode, newMode   exporters.ClassificationMode
		oldRoles, newRoles roles
		want               string
		marked             bool
	}{
		{"roles while by address change nothing", addr, addr, roles{}, roles{"ether1": wan}, "", false},
		{"nothing changed", iface, iface, roles{"ether1": wan}, roles{"ether1": wan}, "", false},
		{"switching to by interface names the roles", addr, iface, roles{"ether1": wan, "bridge": lan, "lo": ""},
			roles{"ether1": wan, "bridge": lan, "lo": ""}, "classification by address → by interface; WAN: ether1; LAN: bridge", true},
		{"switching back to by address", iface, addr, roles{"ether1": wan}, roles{"ether1": wan}, "classification by interface → by address", true},
		{"role changes by interface name each change", iface, iface, roles{"ether1": wan, "bridge": lan},
			roles{"ether1": wan, "bridge": "", "tun6": wan}, "interface roles: bridge LAN → no role, tun6 no role → WAN", true},
		{"long lists are cut", addr, iface, nil,
			roles{"a": lan, "b": lan, "c": lan, "d": lan, "e": lan, "f": lan, "g": lan, "h": lan},
			"classification by address → by interface; LAN: a, b, c, d, e, f and 2 more", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, marked := describeClassificationChange(tc.oldMode, tc.newMode, tc.oldRoles, tc.newRoles)
			if got != tc.want || marked != tc.marked {
				t.Errorf("got %q, %v; want %q, %v", got, marked, tc.want, tc.marked)
			}
		})
	}
}
