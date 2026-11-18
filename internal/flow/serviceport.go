package flow

// wellKnownMax is the top of the IANA well-known (system) port range.
const wellKnownMax = 1023

// ServicePort returns the port that identifies the service of a Flow between two ports:
// a well-known port at either end, otherwise the lower of the two (see GLOSSARY.md).
// Ports of 0 (no ports, e.g. ICMP) are ignored. The result doesn't depend on which
// end is the source, so request and reply Flows share a Service Port.
func ServicePort(protocol uint8, a, b uint16) uint16 {
	_ = protocol // the rule is the same for every protocol; ICMP simply has no ports
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	}
	aKnown, bKnown := a <= wellKnownMax, b <= wellKnownMax
	switch {
	case aKnown && !bKnown:
		return a
	case bKnown && !aKnown:
		return b
	}
	return min(a, b)
}
