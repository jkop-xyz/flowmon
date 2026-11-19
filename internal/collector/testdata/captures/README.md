# Exporter captures

Each `.pcap` here is replayed by `TestCaptures` in `../../captures_test.go`: every record must decode into a Flow with the times the exporter gave, without deviations beyond those a capture that starts mid-stream shows.

Record a capture with tcpdump on the collector's port (`tcpdump -i any -w exporter.pcap udp port 2055`) or with `FLOWMON_DEBUG_CAPTURE`. Save Wireshark captures as pcap, not pcapng. Captures show who talked to whom: use traffic from a test network, or anonymise addresses before adding one.

Note each file below: the exporter, its version and settings, and how the traffic was made.

| File | Exporter | How it was made |
|---|---|---|
| `sendipfix-netflow9.pcap` | `internal/devtools/sendipfix -version 9` (synthetic, not a real exporter) | `FLOWMON_DEBUG_CAPTURE` on a local Flowmon; checks the capture and replay path end to end |
