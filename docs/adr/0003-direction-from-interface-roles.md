# Decide Direction from interface roles first and Local Networks second

A Flow's Direction and its Local Host come mainly from **which way the Flow crosses an Exporter's WAN Interfaces**. Address-based Local Networks are only the fallback, for Exporters whose interfaces have no roles. We chose this because deciding by address breaks in common home setups:
- LANs that use global IPv6 addresses from a delegated prefix that can change
- WAN addresses behind CGNAT
- overlay networks such as Tailscale in 100.64/10

Interface roles are what the router itself knows.

## Consequences

- The user marks each Exporter's WAN-facing Interfaces once. Flowmon suggests them from traffic patterns.
- 100.64/10 is not in the default Local Networks. Users with an overlay network add it themselves.
- Flowmon suggests IPv6 prefixes it sees on LAN-facing Interfaces, so a prefix change from the ISP gets noticed.
- Exporters also have a role. An **Edge Exporter** sees traffic to and from the internet; an **Internal Exporter** sits inside, for example on a hypervisor. The rule for counting traffic seen by both is under "Revisions after the second review" below; it replaces the first version, which still counted routed traffic between VLANs twice.

## Revisions after the second review

- **Interfaces are identified by name, not by number.** RouterOS can renumber PPPoE, VLAN and WireGuard interfaces after a reconnect or a configuration change, and a WAN marked by number would quietly flip every Direction. Names come from SNMP `ifName` or from options templates. Flows are mapped from number to name at decode time, and the UI warns when the mapping changes.
- Interface Roles are **WAN / LAN / Tunnel**. Peers reached through a Tunnel count as Local Hosts.
- Each Exporter has a **Classification Mode**: by interface, or by address (Local Networks). By address is chosen automatically when an Exporter sends no interface numbers.
- An Exporter's role, Classification Mode and WAN Interfaces are set when the admin approves it, so there is never a period when Direction is unknown.
- **Counting across Exporters:** an Internal Exporter only contributes Flows where both ends are in the same subnet. Everything else is counted from Edge Exporters. The full table (Exporter role × which ends are local × direction across the interface → counted or not) lives in the tests. Exporters marked "observes multiple points" have duplicates removed by 5-tuple and overlapping time.

## Revisions after the third review

- **Interface names come from SNMP `ifName` when SNMP is set up.** Otherwise the user maps numbers to names on the Exporter page. RouterOS is not expected to send names in options templates. Flowmon suggests the WAN Interface from traffic: the interface where Remote IPs come in.
- **"Same subnet" for Internal Exporters (v1.1)** uses the prefixes on each LAN Interface, never a broad Local Network such as 10.0.0.0/8, which would make every routed VLAN-to-VLAN Flow look like same-subnet traffic.
- **Duplicate removal (v1.1)** happens only within a single Exporter, uses a NAT-aware key, and runs before Rollups.

## As built (#9)

- Interface number 0 means the Exporter itself (no input or output interface). Such Flows are attributed to the Exporter Host, which uses the Exporter's own address rather than its current WAN IP, in both Classification Modes.
- A Flow crossing an interface with no role, or carrying no interface numbers, is classified by address even when the Exporter classifies by interface.
- Interface names come from the user or from Options Templates (interfaceName scoped by an interface number); SNMP follows in #10. A name the user set is never overwritten by a reported one; a different reported name is listed as a conflict.
- Only classification changes are marked on charts. Renaming an interface doesn't change classification, because roles follow names.

## As built (#25)

- An Internal Exporter's LAN subnets are set with its role, when it is approved or later on its Exporter page, rather than read from its interfaces: hypervisors exporting with softflowd rarely have SNMP or interface names. Subnets broader than /16 (IPv4) or /64 (IPv6) are refused.
- Its Flows that aren't between two hosts in the same listed subnet get the Direction **Uncounted**. They are stored raw, so the Flow Explorer shows them and reprocessing can count them again after the subnets change, but they are in no Rollup, Top Remote IPs list or total. Classification Mode and Interface Roles don't apply to an Internal Exporter.
- Duplicate removal ("observes multiple points") runs in ingest, before sampling and classification. A Flow is a duplicate when one of its keys (its pre-NAT 5-tuple, or its post-NAT one) matches a Flow from the same Exporter whose time overlaps. Flows are remembered for the longest Watermark (37 minutes). The full counting table by Exporter role is in `internal/classify/internal_test.go`.
