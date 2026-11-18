# Rollups record traffic as seen from the Local Host, with Direction fixed at storage time

Rollups are grouped by Local Host and peer. They hold **bytes and packets received** and **bytes and packets sent** as separate measures, always from the Local Host's point of view. A one-way Flow (MikroTik) fills one side. An RFC 5103 biflow fills both. The Service Port rule ignores which end is the source, so reply Flows land in the same row as their request. We chose this over keying Rollups on Direction because biflows (v1.1) would otherwise need a schema change, and the Rollup schema is the most expensive thing to change later.

## Consequences

- Direction is calculated **once, when a Flow is stored**, and then fixed, the same as the geo and ASN data. Changing an Interface Role applies from then on. Flowmon offers to reprocess the last 7 days from raw Flows. Older history is left as it was, and the charts mark the point where the change happened.
- **Late data:** each Exporter has a Watermark, its longest observed Flow duration (or a value the user sets) plus 2 minutes. Rollups only ever add, so late Flows are still added to their buckets. Flows more than 2 hours past the Watermark are dropped and counted.
- Day tables and day boundaries are in UTC, and the UI shows times in the browser's time zone.
- **Top Remote IPs** are kept per hour, ranked by bytes and by Flow count, with an "other" row so they add up to the totals. A daily top list is also built from raw Flows before they are dropped. Results over long time ranges are labelled approximate.

## Revisions after the third review

- **What "peer" means.** The minute and hour Rollups are grouped by: Exporter, Local Host, peer type (Remote or Local), peer ASN, peer country, protocol, Service Port, and the peer Local Host for Internal traffic. Remote IPs appear only in Top Remote IPs, which keeps the minute Rollups small over 90 days.
- **Network totals count each byte once.** Network total = bytes received from Remote IPs + bytes sent to Remote IPs + bytes sent to Local Hosts. Internal traffic is counted only on the side that sent it. Per-host views still show both sides.
- **The Watermark is capped.** It is the 99th percentile of Flow durations from that Exporter over the last 24 hours (or a value the user sets), capped at 35 minutes, plus 2 minutes, and it is shown on the Exporter page. Rollups only add, so the Watermark only decides when raw data may be dropped and when the UI shades data as "incomplete".
- **Interface mappings are stored with the time range they were valid for**, and reprocessing uses the mapping that applied when each Flow happened. The Exporter Host is tracked by its WAN Interface, not by IP, and learned addresses expire, because a DHCP WAN changes address.
- **Tests come first.** Tests for the counting table (Exporter role × which ends are local × direction across the interface) and for the Service Port rule are written before the classification code. The Service Port cases include both ends well-known, both ends ephemeral, reply Flows, and Tunnel peers. Every later decoder (NetFlow v9 and v5, biflows) must pass the same tests.
