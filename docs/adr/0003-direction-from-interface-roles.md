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
