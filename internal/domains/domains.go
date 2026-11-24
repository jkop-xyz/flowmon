// Package domains works with DNS names: the Registrable Domain a name belongs to, from
// the Public Suffix List (https://publicsuffix.org) that golang.org/x/net/publicsuffix
// embeds. Updating that module updates the list.
package domains

import (
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// Normalize returns name as Flowmon keeps it: lower case, ASCII (an internationalized
// name in its xn-- form, as DNS carries it), without a trailing dot.
func Normalize(name string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	for i := 0; i < len(name); i++ {
		if name[i] >= 0x80 {
			if a, err := idna.Lookup.ToASCII(name); err == nil {
				return a
			}
			break
		}
	}
	return strings.ToLower(name)
}

// Registrable returns the Registrable Domain (eTLD+1) of name: the public suffix plus one
// label, such as "bbc.co.uk" for "www.news.bbc.co.uk" and "jakob.github.io" for
// "jakob.github.io". A name with no label left of its public suffix ("co.uk", "lan") is
// its own Registrable Domain. name is normalized first.
func Registrable(name string) string {
	name = Normalize(name)
	if name == "" {
		return ""
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(name); err == nil {
		return d
	}
	return name
}
