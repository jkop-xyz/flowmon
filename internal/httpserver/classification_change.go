package httpserver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jkop-xyz/flowmon/internal/exporters"
	"github.com/jkop-xyz/flowmon/internal/interfaces"
)

// maxNamesInMarker bounds how many interfaces a chart marker names before "and N more".
const maxNamesInMarker = 6

var roleLabels = map[interfaces.Role]string{
	interfaces.RoleWAN: "WAN", interfaces.RoleLAN: "LAN", interfaces.RoleTunnel: "Tunnel", "": "no role",
}

var modeLabels = map[exporters.ClassificationMode]string{
	exporters.ByAddress: "by address", exporters.ByInterface: "by interface",
}

// describeClassificationChange says what a save changed, for a chart marker. ok is false
// when the save changes no Flow's classification: nothing changed, or only roles changed
// while the Exporter classifies by address, where roles aren't used.
func describeClassificationChange(oldMode, newMode exporters.ClassificationMode, oldRoles, newRoles map[string]interfaces.Role) (desc string, ok bool) {
	if oldMode != newMode {
		desc = fmt.Sprintf("classification %s → %s", modeLabels[oldMode], modeLabels[newMode])
		if newMode == exporters.ByInterface {
			desc += "; " + summarizeRoles(newRoles)
		}
		return desc, true
	}
	if newMode != exporters.ByInterface {
		return "", false
	}
	var names []string
	for name := range unionKeys(oldRoles, newRoles) {
		if oldRoles[name] != newRoles[name] {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("%s %s → %s", name, roleLabels[oldRoles[name]], roleLabels[newRoles[name]])
	}
	return "interface roles: " + capped(parts), true
}

// summarizeRoles lists the interfaces that have a role, grouped by role:
// "WAN: ether1; LAN: bridge, vlan10".
func summarizeRoles(roles map[string]interfaces.Role) string {
	byRole := map[interfaces.Role][]string{}
	for name, role := range roles {
		if role != "" {
			byRole[role] = append(byRole[role], name)
		}
	}
	if len(byRole) == 0 {
		return "no interface roles"
	}
	var groups []string
	for _, role := range []interfaces.Role{interfaces.RoleWAN, interfaces.RoleLAN, interfaces.RoleTunnel} {
		if names := byRole[role]; len(names) > 0 {
			sort.Strings(names)
			groups = append(groups, roleLabels[role]+": "+capped(names))
		}
	}
	return strings.Join(groups, "; ")
}

func capped(items []string) string {
	if len(items) <= maxNamesInMarker {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:maxNamesInMarker], ", "), len(items)-maxNamesInMarker)
}

func unionKeys(a, b map[string]interfaces.Role) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}
