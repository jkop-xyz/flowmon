# v2: Data Sources, Devices identified by MAC, and domains on traffic

Status: accepted (design round, 2026-11-19); not built yet. Milestones v2.0 "DNS and Devices" and v2.1 "SNMP everywhere".

v1 answers questions about Flows: who sent what to which address. v2 makes Flowmon a home-network dashboard that also answers "what is this device doing", "where does my traffic go, by domain", "why is the internet slow" and "what is on my network". In that order of priority, by combining Flows with two new sources: AdGuard Home (DNS) and SNMP polled from any device.

## Data Sources

Everything collected besides Exporters comes from a **Data Source**: one configured integration of one kind, such as an AdGuard Home instance or an SNMP device. Every kind has settings in the Flow Store (secrets encrypted with `secret.key`, as SNMP's are now), a collection loop, and a health status ("last poll", "last error") on one Sources page. Each kind writes its own tables. v2 has two kinds; new ones (Pi-hole, the RouterOS API, UniFi, syslog or log shipping) are one package each behind the same interface. There can be any number of sources of a kind: two AdGuard instances for redundancy merge into one DNS log that remembers which one answered.

Settings can be given by environment variables, which lock them in the UI, as Local Networks already work.

## One binary, with a seam for a time-series database

v2 stays one binary and the two stores of ADR 0001. DNS Queries, Domain Rollups, Interface Counters and Device Health go into the Flow Store, under the existing retention, size cap and backups. Time series are written through an interface, so a ClickHouse, InfluxDB or Prometheus backend can be added later without touching collectors. v2 ships only DuckDB. SNMP values also appear on `/metrics`, so Prometheus users get them anyway.

## Devices are MAC addresses

A **Device** is a MAC address and everything seen from it. A Local Host stays the address-level unit (an IP, or a Host Label's prefix); a Device groups the addresses it used across DHCP and IPv4/IPv6. MACs come from two places, combined:

- the MAC fields of Flow records (RouterOS already sends `sourceMacAddress`/`destinationMacAddress`), and
- ARP and IPv6 neighbor tables polled over SNMP (v2.1), which also find quiet devices.

A MAC seen only through a router hop (the router's own) is never taken for the Device behind it.

Each Flow records its Local Host's Device when it is stored, fixed like its GeoIP data, and the Rollups gain a Device column, so a Device is counted consistently even as its address changes. Lists default to "by Device", with Flows without a known MAC listed by address. Reprocessing fills in Devices from the stored fields of raw Flows. Older Rollups stay address-only.

Names: a Host Label (which can now target a MAC), else reverse DNS of its current address, else the vendor from an embedded IEEE OUI table.

**Private Addresses** (randomized MACs, locally administered bit set) have no vendor and may rotate. They are Devices too, marked as such. When a new Private Address appears with the same reverse DNS name as a Device whose Private Address stopped being seen, the two are merged automatically (shown, and undoable); otherwise they can be merged by hand. Matching on vendor was considered and doesn't work: a randomized MAC carries no vendor.

## DNS from AdGuard Home, and domains on traffic

DNS collection is **off until turned on**, because DNS logs reveal far more than Flows, and has its own retention (raw DNS Queries 7 days). AdGuard Home can't push, so Flowmon polls `/control/querylog` every 10–15 seconds for entries newer than the last one seen (base URL, user and password, optional "skip TLS verification", Test connection). If Flowmon is down longer than AdGuard keeps its log, that gap stays empty; log shipping could close it later.

**Domain Attribution:** a Flow gets the name from the most recent DNS Query by the same Local Host, within 24 hours before the Flow, whose answer contained the Flow's Remote IP. 24 hours rather than the TTL, because devices keep using cached addresses. It is set at ingest and fixed. A short pass afterwards fills in Flows that arrived before their DNS Query was polled. Domains can't be filled in from before collection started.

Raw Flows keep the full name; charts and lists group by **Registrable Domain** (eTLD+1, from an embedded Public Suffix List), with drill-down to full names. Domain totals live in separate **Domain Rollups** (minute and hour, by Local Host or Device and Registrable Domain): adding the domain to the Rollups' key would multiply their size by domains × hosts × ports.

Flowmon shows the share of each Device's traffic that no DNS Query explains (DNS over HTTPS, hard-coded addresses, own resolvers), flags Devices that send DNS (ports 53, 853) to servers other than AdGuard, and warns when AdGuard sees nearly all queries from one address that is an Exporter: then clients reach AdGuard through the router, and per-Device DNS and Domain Attribution don't work.

## SNMP everywhere (v2.1)

Any device can be an SNMP Data Source, not only Exporters; an Exporter's SNMP section becomes a link to its source. Every interface is polled every 60 seconds into **Interface Counters** (bytes, packets, errors, discards, up/down), and **Device Health** (uptime, CPU, memory, temperature, voltage) is sampled the same way, both kept as minute and hour data under the Rollup retentions. Standard MIBs work everywhere (IF-MIB, HOST-RESOURCES, IP-MIB for ARP/ND, LLDP for switch ports); vendor MIBs are added when real devices need them, starting with MIKROTIK-MIB (ADR 0002's stance). Reconciliation moves onto Interface Counters.

## Storage

One size cap (`FLOWMON_FLOW_STORE_MAX_GB`) covers everything and drops raw DNS days first, then raw Flow days. Snapshots include everything.

## Out of scope for v2.0

Alerts and notifications (a v2.x update may add them), user roles, and anything that writes to devices: v2.0 is read-only collection and dashboards.
