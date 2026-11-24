package datasource

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// envPrefix starts every Data Source variable:
//
//	FLOWMON_SOURCE_<ID>_KIND=adguard        required: the Kind's name
//	FLOWMON_SOURCE_<ID>_NAME=AdGuard        optional: defaults to the ID
//	FLOWMON_SOURCE_<ID>_ENABLED=false       optional: defaults to true
//	FLOWMON_SOURCE_<ID>_<FIELD>=value       a setting, by its Field key upper-cased
//
// <ID> is letters and digits. The Source's id is "env-" and the ID lower-cased.
const envPrefix = "FLOWMON_SOURCE_"

var envID = regexp.MustCompile(`^[A-Z0-9]+$`)

// FromEnviron reads the Sources set in environ (os.Environ()'s form). Settings are taken
// as given; New checks them against their Kind.
func FromEnviron(environ []string) ([]Source, error) {
	byID := map[string]*Source{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, envPrefix) {
			continue
		}
		id, key, ok := strings.Cut(strings.TrimPrefix(k, envPrefix), "_")
		if !ok || key == "" || !envID.MatchString(id) {
			return nil, fmt.Errorf("%s: expected %s<ID>_<SETTING>, with an ID of letters and digits", k, envPrefix)
		}
		src := byID[id]
		if src == nil {
			src = &Source{ID: "env-" + strings.ToLower(id), Name: strings.ToLower(id), Enabled: true, Settings: Settings{}}
			byID[id] = src
		}
		switch key {
		case "KIND":
			src.Kind = strings.ToLower(strings.TrimSpace(v))
		case "NAME":
			src.Name = strings.TrimSpace(v)
		case "ENABLED":
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "1", "yes", "on":
				src.Enabled = true
			case "false", "0", "no", "off":
				src.Enabled = false
			default:
				return nil, fmt.Errorf("%s: must be true or false", k)
			}
		default:
			src.Settings[strings.ToLower(key)] = v
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]Source, 0, len(ids))
	for _, id := range ids {
		src := byID[id]
		if src.Kind == "" {
			return nil, fmt.Errorf("%s%s_KIND is required", envPrefix, id)
		}
		out = append(out, *src)
	}
	return out, nil
}
