package flow

import "testing"

// Written before ServicePort, from the glossary: the Service Port is the well-known
// port at either end, otherwise the lower of the two. The rule ignores which end is
// the source, so a reply Flow lands on the same Service Port as its request.
func TestServicePort(t *testing.T) {
	cases := []struct {
		name              string
		protocol          uint8
		local, peer, want uint16
	}{
		{"HTTPS request from an ephemeral port", 6, 50000, 443, 443},
		{"reply Flow: the source port is the service", 6, 443, 50000, 443},
		{"both ends well-known (DNS between servers)", 17, 53, 53, 53},
		{"both ends well-known, different (NTP to a privileged client port)", 17, 123, 1023, 123},
		{"both ends ephemeral (BitTorrent): the lower one", 6, 61000, 51413, 51413},
		{"WireGuard on a registered port, the client ephemeral", 17, 51820, 60123, 51820},
		{"registered service port below the ephemeral range", 6, 8006, 49999, 8006},
		{"Tunnel peers: same rule, nothing special", 17, 41641, 41641, 41641},
		{"ICMP has no ports", 1, 0, 0, 0},
		{"one end without a port", 6, 0, 443, 443},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ServicePort(c.protocol, c.local, c.peer); got != c.want {
				t.Errorf("ServicePort(%d, %d, %d) = %d, want %d", c.protocol, c.local, c.peer, got, c.want)
			}
			// The rule must not depend on which end is which.
			if got := ServicePort(c.protocol, c.peer, c.local); got != c.want {
				t.Errorf("swapped: ServicePort(%d, %d, %d) = %d, want %d", c.protocol, c.peer, c.local, got, c.want)
			}
		})
	}
}
