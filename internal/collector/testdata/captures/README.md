# Exporter captures

Each `.pcap` here is replayed by `TestCaptures` in `../../captures_test.go`: every record must decode into a Flow with the times the exporter gave, without deviations beyond those a capture that starts mid-stream shows.

Record a capture with tcpdump on the collector's port (`tcpdump -i any -w exporter.pcap udp port 2055`) or with `FLOWMON_DEBUG_CAPTURE`. Save Wireshark captures as pcap, not pcapng. Captures show who talked to whom: use traffic from a test network, or anonymise addresses before adding one.

Note each file below: the exporter, its version and settings, and how the traffic was made.

| File | Exporter | How it was made |
|---|---|---|
| `sendipfix-netflow9.pcap` | `internal/devtools/sendipfix -version 9` (synthetic, not a real exporter) | `FLOWMON_DEBUG_CAPTURE` on a local Flowmon; checks the capture and replay path end to end |
| `sendipfix-netflow5.pcap` | `internal/devtools/sendipfix -version 5` (synthetic, not a real exporter) | `FLOWMON_DEBUG_CAPTURE` on a local Flowmon |
| `sendipfix-biflow.pcap` | `internal/devtools/sendipfix -biflow` (synthetic, not a real exporter) | `FLOWMON_DEBUG_CAPTURE` on a local Flowmon; RFC 5103 biflow records |
| `softflowd-netflow9.pcap` | softflowd 1.1.1 (Debian), `-v 9` | HTTP, ICMP and UDP between two containers on a test network (192.168.88.0/24, 2001:db8:88::/64), recorded with tcpdump, then exported with `softflowd -r` and captured on the collector port. IPv4 and IPv6 Templates. |
| `softflowd-netflow5.pcap` | softflowd 1.1.1 (Debian), `-v 5` | The same traffic as `softflowd-netflow9.pcap`, IPv4 only (v5 has no IPv6). |
